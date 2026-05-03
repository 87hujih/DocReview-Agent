package approval

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
)

var (
	// ErrApprovalNotFound 表示审批记录不存在。
	ErrApprovalNotFound = errors.New("审批不存在")
	// ErrApprovalAlreadyDecided 表示审批已做出决策，不能重复操作。
	ErrApprovalAlreadyDecided = errors.New("审批已处理")
	// ErrApprovalMissingBaseVersion 表示待审批记录缺少审批时看到的版本快照。
	ErrApprovalMissingBaseVersion = errors.New("legacy approval 缺少 base_version_id")
	// ErrReasonRequired 表示拒绝审批时必须提供原因。
	ErrReasonRequired = errors.New("必须提供原因")
	// ErrTaskNotFound 表示审批对应的任务不存在。
	ErrTaskNotFound = errors.New("任务不存在")
	// ErrJobNotFound 表示执行作业不存在。
	ErrJobNotFound = errors.New("执行作业不存在")
)

const snapshotProjectionTimeout = 5 * time.Second

type taskStatusProjector interface {
	ProjectTaskStatusChanged(ctx context.Context, sourceMessageID *string, taskID string, status string) error
}

type taskStatusNotifier interface {
	Notify(ctx context.Context, task *postgres.Task, status string) error
}

// Service 负责审批通过/拒绝时的业务协调。
type Service struct {
	pool         *pgxpool.Pool
	approvalRepo *postgres.ApprovalRepo
	jobRepo      *postgres.JobRepo
	taskRepo     *postgres.TaskRepo
	jobCh        chan<- struct{}
	eventService *taskevents.Service
	projector    taskStatusProjector
	notifier     taskStatusNotifier
}

// NewService 构造审批服务。
func NewService(
	pool *pgxpool.Pool,
	approvalRepo *postgres.ApprovalRepo,
	jobRepo *postgres.JobRepo,
	taskRepo *postgres.TaskRepo,
	jobCh chan<- struct{},
	eventService *taskevents.Service,
	projector taskStatusProjector,
	notifier taskStatusNotifier,
) *Service {
	return &Service{
		pool:         pool,
		approvalRepo: approvalRepo,
		jobRepo:      jobRepo,
		taskRepo:     taskRepo,
		jobCh:        jobCh,
		eventService: eventService,
		projector:    projector,
		notifier:     notifier,
	}
}

// List 返回审批列表，可按状态过滤。
func (s *Service) List(ctx context.Context, statusFilter string) ([]postgres.Approval, error) {
	return s.approvalRepo.List(ctx, strings.TrimSpace(statusFilter))
}

// GetApproval 返回单条审批记录。
func (s *Service) GetApproval(ctx context.Context, approvalID string) (*postgres.Approval, error) {
	approvalRecord, err := s.approvalRepo.GetByID(ctx, strings.TrimSpace(approvalID))
	if err != nil {
		return nil, err
	}
	if approvalRecord == nil {
		return nil, ErrApprovalNotFound
	}

	return approvalRecord, nil
}

// GetJob 返回单条执行作业记录。
func (s *Service) GetJob(ctx context.Context, jobID string) (*postgres.ExecutionJob, error) {
	job, err := s.jobRepo.GetByID(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrJobNotFound
	}

	return job, nil
}

// Approve 将审批切换为 approved，并创建待执行 job。
func (s *Service) Approve(ctx context.Context, approvalID string) (*postgres.Approval, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	approvalRecord, task, err := s.loadPendingApprovalTaskTx(ctx, tx, approvalID, models.StatusExecuting)
	if err != nil {
		return nil, err
	}
	if approvalRecord.BaseVersionID == nil || strings.TrimSpace(*approvalRecord.BaseVersionID) == "" {
		return nil, ErrApprovalMissingBaseVersion
	}

	updatedApproval, err := postgres.UpdateApprovalStatusTxReturning(ctx, tx, approvalID, "approved", nil)
	if err != nil {
		return nil, err
	}
	if updatedApproval == nil {
		return nil, ErrApprovalNotFound
	}

	job, err := postgres.CreateJobTx(ctx, tx, approvalRecord.TaskID, approvalID, *approvalRecord.BaseVersionID)
	if err != nil {
		return nil, err
	}

	if err := postgres.UpdateTaskStatusTx(ctx, tx, task.ID, models.StatusExecuting, nil); err != nil {
		return nil, err
	}

	from := task.Status
	task.Status = models.StatusExecuting
	if err := s.recordEventTx(ctx, tx, taskevents.RecordInput{
		TaskID:    task.ID,
		RunID:     stringPointer(job.ID),
		Source:    "approval_service",
		Level:     "info",
		EventType: "task.status_changed",
		Message:   "任务状态已更新",
		Payload: map[string]any{
			"from_status": from,
			"to_status":   models.StatusExecuting,
		},
	}); err != nil {
		return nil, err
	}
	if err := s.recordEventTx(ctx, tx, taskevents.RecordInput{
		TaskID:    task.ID,
		RunID:     stringPointer(job.ID),
		Source:    "approval_service",
		Level:     "info",
		EventType: "approval.approved",
		Message:   "审批已通过，任务进入执行阶段",
		Payload: map[string]any{
			"approval_id": approvalRecord.ID,
			"job_id":      job.ID,
			"status":      models.StatusExecuting,
		},
	}); err != nil {
		return nil, err
	}
	if err := s.recordEventTx(ctx, tx, taskevents.RecordInput{
		TaskID:    task.ID,
		RunID:     stringPointer(job.ID),
		Source:    "approval_service",
		Level:     "info",
		EventType: "job.queued",
		Message:   "执行作业已创建，等待 worker 领取",
		Payload: map[string]any{
			"approval_id": approvalRecord.ID,
			"job_id":      job.ID,
			"job_status":  job.Status,
		},
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.syncTaskStatusSideEffects(ctx, task, models.StatusExecuting)

	s.enqueueJob()

	return updatedApproval, nil
}

// Reject 将审批切换为 rejected，并把任务标记为 failed。
func (s *Service) Reject(ctx context.Context, approvalID string, reason string) (*postgres.Approval, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, ErrReasonRequired
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	approvalRecord, task, err := s.loadPendingApprovalTaskTx(ctx, tx, approvalID, models.StatusFailed)
	if err != nil {
		return nil, err
	}

	updatedApproval, err := postgres.UpdateApprovalStatusTxReturning(ctx, tx, approvalID, "rejected", &reason)
	if err != nil {
		return nil, err
	}
	if updatedApproval == nil {
		return nil, ErrApprovalNotFound
	}

	if err := postgres.UpdateTaskStatusTx(ctx, tx, task.ID, models.StatusFailed, &reason); err != nil {
		return nil, err
	}

	from := task.Status
	task.Status = models.StatusFailed
	task.ErrorMessage = &reason
	if err := s.recordEventTx(ctx, tx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "approval_service",
		Level:     "warn",
		EventType: "task.status_changed",
		Message:   "任务状态已更新",
		Payload: map[string]any{
			"from_status":   from,
			"to_status":     models.StatusFailed,
			"error_message": reason,
		},
	}); err != nil {
		return nil, err
	}
	if err := s.recordEventTx(ctx, tx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "approval_service",
		Level:     "warn",
		EventType: "approval.rejected",
		Message:   "审批已拒绝，任务已标记为失败",
		Payload: map[string]any{
			"approval_id": approvalRecord.ID,
			"reason":      reason,
			"status":      models.StatusFailed,
		},
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.syncTaskStatusSideEffects(ctx, task, models.StatusFailed)

	return updatedApproval, nil
}

// projectTaskStatus 把 `任务状态` 投影回审批状态，保持后续读取口径一致。
func (s *Service) projectTaskStatus(ctx context.Context, task *postgres.Task, status string) {
	if s.projector == nil || task == nil {
		return
	}

	projectionCtx, cancel := projectionContext(ctx)
	defer cancel()

	if err := s.projector.ProjectTaskStatusChanged(projectionCtx, task.SourceMessageID, task.ID, status); err != nil {
		log.Printf("警告：审批链回写助手上下文快照失败：task=%s status=%s err=%v", task.ID, status, err)
	}
}

// syncTaskStatusSideEffects 同步 `任务状态SideEffects`，避免状态联动逻辑散落在多个调用方。
func (s *Service) syncTaskStatusSideEffects(ctx context.Context, task *postgres.Task, status string) {
	s.projectTaskStatus(ctx, task, status)
	s.notifyTaskTerminalStatus(ctx, task, status)
}

// notifyTaskTerminalStatus 通知 `任务终态状态`，把消息发送时机和格式收口在接收者内部。
func (s *Service) notifyTaskTerminalStatus(ctx context.Context, task *postgres.Task, status string) {
	if s.notifier == nil || task == nil || !isTerminalTaskStatus(status) {
		return
	}

	notificationCtx, cancel := projectionContext(ctx)
	defer cancel()

	if err := s.notifier.Notify(notificationCtx, task, status); err != nil {
		log.Printf("警告：审批链回写助手终态消息失败：task=%s status=%s err=%v", task.ID, status, err)
	}
}

// isTerminalTaskStatus 判断任务状态是否已经进入终态，供通知和投影逻辑复用。
func isTerminalTaskStatus(status string) bool {
	return status == models.StatusCompleted || status == models.StatusFailed
}

// projectionContext 归拢状态投影所需的任务、步骤和产物读取能力，避免不同调用方各自拼装依赖。
func projectionContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), snapshotProjectionTimeout)
	}

	return context.WithTimeout(context.WithoutCancel(parent), snapshotProjectionTimeout)
}

// loadPendingApprovalTask 加载 `待处理审批任务`，为后续审批流程准备输入。
func (s *Service) loadPendingApprovalTask(ctx context.Context, approvalID string, targetStatus string) (*postgres.Approval, *postgres.Task, error) {
	approvalRecord, err := s.approvalRepo.GetByID(ctx, approvalID)
	if err != nil {
		return nil, nil, err
	}
	if approvalRecord == nil {
		return nil, nil, ErrApprovalNotFound
	}
	if approvalRecord.Status != "pending" {
		return nil, nil, ErrApprovalAlreadyDecided
	}

	task, err := s.taskRepo.GetByID(ctx, approvalRecord.TaskID)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, ErrTaskNotFound
	}

	if err := models.Transition(task.Status, targetStatus); err != nil {
		return nil, nil, err
	}

	return approvalRecord, task, nil
}

// loadPendingApprovalTaskTx 加载 `待处理审批任务事务`，为后续审批流程准备输入。
func (s *Service) loadPendingApprovalTaskTx(ctx context.Context, tx pgx.Tx, approvalID string, targetStatus string) (*postgres.Approval, *postgres.Task, error) {
	approvalRecord, err := postgres.GetApprovalByIDForUpdateTx(ctx, tx, approvalID)
	if err != nil {
		return nil, nil, err
	}
	if approvalRecord == nil {
		return nil, nil, ErrApprovalNotFound
	}
	if approvalRecord.Status != "pending" {
		return nil, nil, ErrApprovalAlreadyDecided
	}

	task, err := postgres.GetTaskByIDForUpdateTx(ctx, tx, approvalRecord.TaskID)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, ErrTaskNotFound
	}

	if err := models.Transition(task.Status, targetStatus); err != nil {
		return nil, nil, err
	}

	return approvalRecord, task, nil
}

// enqueueJob 把 `作业` 放入后续处理队列，统一入队前的边界检查。
func (s *Service) enqueueJob() {
	if s.jobCh == nil {
		return
	}

	select {
	case s.jobCh <- struct{}{}:
	default:
		log.Printf("警告：执行任务通道已满，信号已丢弃，pending job 将在下次触发时被领取")
	}
}

// recordEvent 记录 `事件`，统一事件和审计信息的写入位置。
func (s *Service) recordEvent(ctx context.Context, input taskevents.RecordInput) {
	if s.eventService == nil {
		return
	}

	if _, err := s.eventService.Record(ctx, input); err != nil {
		log.Printf("警告：记录任务事件失败：task=%s event=%s err=%v", input.TaskID, input.EventType, err)
	}
}

// recordEventTx 记录 `事件事务`，统一事件和审计信息的写入位置。
func (s *Service) recordEventTx(ctx context.Context, tx pgx.Tx, input taskevents.RecordInput) error {
	if s.eventService == nil {
		return nil
	}

	_, err := s.eventService.RecordTx(ctx, tx, input)
	return err
}

// stringPointer 返回字符串指针，简化构造可选文本字段时的样板代码。
func stringPointer(value string) *string {
	return &value
}
