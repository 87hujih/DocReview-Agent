package service

import (
	"context"
	"errors"
	"log"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
	"agent_project/apps/server/internal/task/workflow"
)

var (
	// ErrInstructionRequired 表示任务指令为空。
	ErrInstructionRequired = errors.New("任务指令不能为空")
	// ErrResourceNotFound 表示资源不存在。
	ErrResourceNotFound = errors.New("资源不存在")
	// ErrResourceCurrentVersionNotFound 表示资源缺少当前版本。
	ErrResourceCurrentVersionNotFound = errors.New("资源当前版本不存在")
	// ErrSuggestionMessageIDRequired 表示助手建议消息 ID 为空。
	ErrSuggestionMessageIDRequired = errors.New("任务建议消息 ID 不能为空")
)

// Service 协调任务创建、查询和后台编排启动。
type Service struct {
	taskRepo      *postgres.TaskRepo
	resourceRepo  *postgres.ResourceRepo
	runner        *workflow.WorkflowRunner
	eventService  *taskevents.Service
	runtimeShadow RuntimeShadowRecorder
}

type RuntimeShadowRecorder interface {
	RecordLegacyTask(context.Context, string, string, string) (runID string, recorded bool, err error)
}

type Option func(*Service)

// WithRuntimeShadowRecorder 执行该函数负责的核心处理逻辑。
func WithRuntimeShadowRecorder(recorder RuntimeShadowRecorder) Option {
	return func(service *Service) {
		service.runtimeShadow = recorder
	}
}

// New 创建任务服务。
func New(
	taskRepo *postgres.TaskRepo,
	resourceRepo *postgres.ResourceRepo,
	runner *workflow.WorkflowRunner,
	eventService *taskevents.Service,
	options ...Option,
) *Service {
	service := &Service{
		taskRepo:     taskRepo,
		resourceRepo: resourceRepo,
		runner:       runner,
		eventService: eventService,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// CreateTask 创建任务，并在生产接线中异步启动编排器。
func (s *Service) CreateTask(ctx context.Context, resourceID string, instruction string) (*postgres.Task, error) {
	resourceID, instruction, err := s.validateCreateTaskInput(ctx, resourceID, instruction)
	if err != nil {
		return nil, err
	}

	task, err := s.taskRepo.Create(ctx, resourceID, instruction)
	if err != nil {
		return nil, err
	}

	return s.afterTaskCreated(ctx, task)
}

// CreateTaskFromAssistantSuggestion 根据建议消息创建任务，并用 suggestion message id 做幂等保护。
func (s *Service) CreateTaskFromAssistantSuggestion(
	ctx context.Context,
	resourceID string,
	instruction string,
	sourceMessageID string,
) (*postgres.Task, bool, error) {
	resourceID, instruction, err := s.validateCreateTaskInput(ctx, resourceID, instruction)
	if err != nil {
		return nil, false, err
	}

	sourceMessageID = strings.TrimSpace(sourceMessageID)
	if sourceMessageID == "" {
		return nil, false, ErrSuggestionMessageIDRequired
	}

	task, created, err := s.taskRepo.CreateFromAssistantSuggestion(ctx, resourceID, instruction, sourceMessageID)
	if err != nil {
		return nil, false, err
	}
	if !created {
		return task, false, nil
	}

	task, err = s.afterTaskCreated(ctx, task)
	if err != nil {
		return nil, false, err
	}

	return task, true, nil
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

// recordEvent 记录 `事件`，统一事件和审计信息的写入位置。
func (s *Service) recordEvent(ctx context.Context, input taskevents.RecordInput) {
	if s.eventService == nil {
		return
	}

	if _, err := s.eventService.Record(ctx, input); err != nil {
		log.Printf("警告：记录任务事件失败：task=%s event=%s err=%v", input.TaskID, input.EventType, err)
	}
}

// validateCreateTaskInput 校验 `创建任务输入`，避免非法输入继续穿透后续流程。
func (s *Service) validateCreateTaskInput(ctx context.Context, resourceID string, instruction string) (string, string, error) {
	resourceID = strings.TrimSpace(resourceID)
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return "", "", ErrInstructionRequired
	}

	resource, err := s.resourceRepo.GetByID(ctx, resourceID)
	if err != nil {
		return "", "", err
	}
	if resource == nil {
		return "", "", ErrResourceNotFound
	}

	version, err := s.resourceRepo.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return "", "", err
	}
	if version == nil {
		return "", "", ErrResourceCurrentVersionNotFound
	}

	return resourceID, instruction, nil
}

// afterTaskCreated 在前置步骤完成后处理 `任务Created`，把补充副作用集中在同一处。
func (s *Service) afterTaskCreated(ctx context.Context, task *postgres.Task) (*postgres.Task, error) {
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
			return s.failCreatedTask(ctx, task, err)
		}
	}
	if s.runtimeShadow != nil {
		runID, recorded, err := s.runtimeShadow.RecordLegacyTask(ctx, task.ID, task.ResourceID, task.Instruction)
		if err != nil {
			log.Printf("警告：记录 Agent Runtime shadow 失败：task=%s err=%v", task.ID, err)
		} else if recorded {
			s.recordEvent(ctx, taskevents.RecordInput{
				TaskID: task.ID, Source: "agent_runtime_shadow", Level: "info",
				EventType: "agent_runtime.shadow_recorded", Message: "任务已写入 Agent Runtime shadow",
				Payload: map[string]any{"run_id": runID, "mode": "shadow"},
			})
		}
	}

	return task, nil
}

// failCreatedTask 把 `Created任务` 标记为失败，并补齐对应清理或通知逻辑。
func (s *Service) failCreatedTask(ctx context.Context, task *postgres.Task, enqueueErr error) (*postgres.Task, error) {
	errorMsg, reason := taskEnqueueFailureDetail(enqueueErr)
	from := task.Status
	if err := models.Transition(task.Status, models.StatusFailed); err != nil {
		return nil, err
	}
	if err := s.taskRepo.UpdateStatus(ctx, task.ID, models.StatusFailed, &errorMsg); err != nil {
		return nil, err
	}

	task.Status = models.StatusFailed
	task.ErrorMessage = &errorMsg
	s.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "task_service",
		Level:     "error",
		EventType: "task.status_changed",
		Message:   "任务状态已更新为失败",
		Payload: map[string]any{
			"from_status":   from,
			"to_status":     models.StatusFailed,
			"error_message": errorMsg,
			"reason":        reason,
		},
	})
	s.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "task_service",
		Level:     "error",
		EventType: "task.failed",
		Message:   errorMsg,
		Payload: map[string]any{
			"reason":        reason,
			"error_message": errorMsg,
			"status":        models.StatusFailed,
		},
	})

	return task, nil
}

// taskEnqueueFailureDetail 整理 `任务入队失败` 的细节描述，便于上层统一返回错误或状态说明。
func taskEnqueueFailureDetail(err error) (string, string) {
	// 根据当前状态或类型选择对应的处理分支。
	switch {
	case errors.Is(err, workflow.ErrQueueFull):
		return "任务队列已满，无法调度执行", "queue_full"
	case errors.Is(err, workflow.ErrRunnerStopped):
		return "工作流执行器已停止，无法调度执行", "runner_stopped"
	default:
		return "任务调度失败，无法启动执行", "enqueue_failed"
	}
}
