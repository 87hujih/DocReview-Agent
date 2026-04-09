package approval

import (
	"context"
	"errors"
	"log"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/task/models"
)

var (
	// ErrApprovalNotFound 表示审批记录不存在。
	ErrApprovalNotFound = errors.New("approval not found")
	// ErrApprovalAlreadyDecided 表示审批已做出决策，不能重复操作。
	ErrApprovalAlreadyDecided = errors.New("approval already decided")
	// ErrReasonRequired 表示拒绝审批时必须提供原因。
	ErrReasonRequired = errors.New("reason is required")
	// ErrTaskNotFound 表示审批对应的任务不存在。
	ErrTaskNotFound = errors.New("task not found")
)

// Service 负责审批通过/拒绝时的业务协调。
type Service struct {
	approvalRepo *postgres.ApprovalRepo
	jobRepo      *postgres.JobRepo
	taskRepo     *postgres.TaskRepo
	jobCh        chan<- postgres.ExecutionJob
}

// NewService 构造审批服务。
func NewService(
	approvalRepo *postgres.ApprovalRepo,
	jobRepo *postgres.JobRepo,
	taskRepo *postgres.TaskRepo,
	jobCh chan<- postgres.ExecutionJob,
) *Service {
	return &Service{
		approvalRepo: approvalRepo,
		jobRepo:      jobRepo,
		taskRepo:     taskRepo,
		jobCh:        jobCh,
	}
}

// List 返回审批列表，可按状态过滤。
func (s *Service) List(ctx context.Context, statusFilter string) ([]postgres.Approval, error) {
	return s.approvalRepo.List(ctx, strings.TrimSpace(statusFilter))
}

// Approve 将审批切换为 approved，并创建待执行 job。
func (s *Service) Approve(ctx context.Context, approvalID string) (*postgres.Approval, error) {
	approvalRecord, task, err := s.loadPendingApprovalTask(ctx, approvalID, models.StatusExecuting)
	if err != nil {
		return nil, err
	}

	if err := s.approvalRepo.UpdateStatus(ctx, approvalID, "approved", nil); err != nil {
		return nil, err
	}

	job, err := s.jobRepo.Create(ctx, approvalRecord.TaskID, approvalID)
	if err != nil {
		return nil, err
	}

	if err := s.taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		return nil, err
	}

	s.enqueueJob(*job)

	return s.approvalRepo.GetByID(ctx, approvalID)
}

// Reject 将审批切换为 rejected，并把任务标记为 failed。
func (s *Service) Reject(ctx context.Context, approvalID string, reason string) (*postgres.Approval, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, ErrReasonRequired
	}

	approvalRecord, task, err := s.loadPendingApprovalTask(ctx, approvalID, models.StatusFailed)
	if err != nil {
		return nil, err
	}

	if err := s.approvalRepo.UpdateStatus(ctx, approvalID, "rejected", &reason); err != nil {
		return nil, err
	}

	if err := s.taskRepo.UpdateStatus(ctx, task.ID, models.StatusFailed, &reason); err != nil {
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

func (s *Service) enqueueJob(job postgres.ExecutionJob) {
	if s.jobCh == nil {
		return
	}

	select {
	case s.jobCh <- job:
	default:
		log.Printf("WARN: execution job channel is full, job=%s", job.ID)
	}
}
