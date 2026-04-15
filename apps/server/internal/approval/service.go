package approval

import (
	"context"
	"errors"
	"log"
	"strings"

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
	// ErrReasonRequired 表示拒绝审批时必须提供原因。
	ErrReasonRequired = errors.New("必须提供原因")
	// ErrTaskNotFound 表示审批对应的任务不存在。
	ErrTaskNotFound = errors.New("任务不存在")
	// ErrJobNotFound 表示执行作业不存在。
	ErrJobNotFound = errors.New("执行作业不存在")
)

// Service 负责审批通过/拒绝时的业务协调。
type Service struct {
	pool         *pgxpool.Pool
	approvalRepo *postgres.ApprovalRepo
	jobRepo      *postgres.JobRepo
	taskRepo     *postgres.TaskRepo
	jobCh        chan<- struct{}
	eventService *taskevents.Service
}

// NewService 构造审批服务。
func NewService(
	pool *pgxpool.Pool,
	approvalRepo *postgres.ApprovalRepo,
	jobRepo *postgres.JobRepo,
	taskRepo *postgres.TaskRepo,
	jobCh chan<- struct{},
	eventService *taskevents.Service,
) *Service {
	return &Service{
		pool:         pool,
		approvalRepo: approvalRepo,
		jobRepo:      jobRepo,
		taskRepo:     taskRepo,
		jobCh:        jobCh,
		eventService: eventService,
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

	if err := postgres.UpdateApprovalStatusTx(ctx, tx, approvalID, "approved", nil); err != nil {
		return nil, err
	}

	job, err := postgres.CreateJobTx(ctx, tx, approvalRecord.TaskID, approvalID)
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

	s.enqueueJob()

	return s.approvalRepo.GetByID(ctx, approvalID)
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

	if err := postgres.UpdateApprovalStatusTx(ctx, tx, approvalID, "rejected", &reason); err != nil {
		return nil, err
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

	return s.approvalRepo.GetByID(ctx, approvalRecord.ID)
}

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

func (s *Service) recordEvent(ctx context.Context, input taskevents.RecordInput) {
	if s.eventService == nil {
		return
	}

	if _, err := s.eventService.Record(ctx, input); err != nil {
		log.Printf("警告：记录任务事件失败：task=%s event=%s err=%v", input.TaskID, input.EventType, err)
	}
}

func (s *Service) recordEventTx(ctx context.Context, tx pgx.Tx, input taskevents.RecordInput) error {
	if s.eventService == nil {
		return nil
	}

	_, err := s.eventService.RecordTx(ctx, tx, input)
	return err
}

func stringPointer(value string) *string {
	return &value
}
