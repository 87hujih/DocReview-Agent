package service

import (
	"context"
	"errors"
	"log"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/workflow"
)

var (
	// ErrInstructionRequired 表示任务指令为空。
	ErrInstructionRequired = errors.New("任务指令不能为空")
	// ErrResourceNotFound 表示资源不存在。
	ErrResourceNotFound = errors.New("资源不存在")
	// ErrResourceCurrentVersionNotFound 表示资源缺少当前版本。
	ErrResourceCurrentVersionNotFound = errors.New("资源当前版本不存在")
)

// Service 协调任务创建、查询和后台编排启动。
type Service struct {
	taskRepo     *postgres.TaskRepo
	resourceRepo *postgres.ResourceRepo
	runner       *workflow.WorkflowRunner
	eventService *taskevents.Service
}

// New 创建任务服务。
func New(
	taskRepo *postgres.TaskRepo,
	resourceRepo *postgres.ResourceRepo,
	runner *workflow.WorkflowRunner,
	eventService *taskevents.Service,
) *Service {
	return &Service{
		taskRepo:     taskRepo,
		resourceRepo: resourceRepo,
		runner:       runner,
		eventService: eventService,
	}
}

// CreateTask 创建任务，并在生产接线中异步启动编排器。
func (s *Service) CreateTask(ctx context.Context, resourceID string, instruction string) (*postgres.Task, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil, ErrInstructionRequired
	}

	resource, err := s.resourceRepo.GetByID(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, ErrResourceNotFound
	}

	version, err := s.resourceRepo.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, ErrResourceCurrentVersionNotFound
	}

	task, err := s.taskRepo.Create(ctx, resourceID, instruction)
	if err != nil {
		return nil, err
	}

	s.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "task_service",
		Level:     "info",
		EventType: "task.created",
		Message:   "任务已创建，等待编排执行",
		Payload: map[string]any{
			"resource_id": task.ResourceID,
			"status":      task.Status,
		},
	})

	if s.runner != nil {
		if err := s.runner.Enqueue(task.ID); err != nil {
			if errors.Is(err, workflow.ErrQueueFull) {
				errorMsg := "任务队列已满，无法调度执行"
				_ = s.taskRepo.UpdateStatus(ctx, task.ID, "failed", &errorMsg)
				s.recordEvent(ctx, taskevents.RecordInput{
					TaskID:    task.ID,
					Source:    "task_service",
					Level:     "error",
					EventType: "task.failed",
					Message:   errorMsg,
					Payload:   map[string]any{"reason": "queue_full"},
				})
			}
			return nil, err
		}
	}

	return task, nil
}

// GetTask 返回任务和步骤列表。
func (s *Service) GetTask(ctx context.Context, id string) (*postgres.Task, []postgres.TaskStep, error) {
	task, err := s.taskRepo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, nil
	}

	steps, err := s.taskRepo.GetSteps(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	return task, steps, nil
}

// ListTasks 返回任务列表。
func (s *Service) ListTasks(ctx context.Context) ([]postgres.Task, error) {
	return s.taskRepo.List(ctx)
}

func (s *Service) recordEvent(ctx context.Context, input taskevents.RecordInput) {
	if s.eventService == nil {
		return
	}

	if _, err := s.eventService.Record(ctx, input); err != nil {
		log.Printf("警告：记录任务事件失败：task=%s event=%s err=%v", input.TaskID, input.EventType, err)
	}
}
