package service

import (
	"context"
	"errors"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
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
	orchestrator *workflow.Orchestrator
}

// New 创建任务服务。
func New(taskRepo *postgres.TaskRepo, resourceRepo *postgres.ResourceRepo, orch *workflow.Orchestrator) *Service {
	return &Service{
		taskRepo:     taskRepo,
		resourceRepo: resourceRepo,
		orchestrator: orch,
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

	if s.orchestrator != nil {
		go s.orchestrator.Orchestrate(context.Background(), task)
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
