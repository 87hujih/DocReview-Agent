package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agent_project/apps/server/internal/storage/postgres"
	taskservice "agent_project/apps/server/internal/task/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// TaskHandler 暴露任务创建、查询和产物查看接口。
type TaskHandler struct {
	taskService *taskservice.Service
	taskRepo    *postgres.TaskRepo
	eventRepo   *postgres.TaskEventRepo
}

type createTaskRequest struct {
	ResourceID  string `json:"resource_id"`
	Instruction string `json:"instruction"`
}

type taskSummaryResponse struct {
	ID           string    `json:"id"`
	ResourceID   string    `json:"resource_id"`
	Instruction  string    `json:"instruction"`
	Status       string    `json:"status"`
	ErrorMessage *string   `json:"error_message"`
	CreatedAt    time.Time `json:"created_at"`
}

type taskDetailResponse struct {
	ID           string    `json:"id"`
	ResourceID   string    `json:"resource_id"`
	Instruction  string    `json:"instruction"`
	Status       string    `json:"status"`
	ErrorMessage *string   `json:"error_message"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type taskStepResponse struct {
	ID           string     `json:"id"`
	StepName     string     `json:"step_name"`
	Status       string     `json:"status"`
	ErrorMessage *string    `json:"error_message"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
}

type taskArtifactResponse struct {
	ID           string          `json:"id"`
	ArtifactType string          `json:"artifact_type"`
	Content      json.RawMessage `json:"content"`
	CreatedAt    time.Time       `json:"created_at"`
}

type taskEventResponse struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	RunID     *string         `json:"run_id"`
	StepName  string          `json:"step_name"`
	Source    string          `json:"source"`
	Level     string          `json:"level"`
	EventType string          `json:"event_type"`
	Message   string          `json:"message"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type createTaskResponse struct {
	Task taskSummaryResponse `json:"task"`
}

type listTasksResponse struct {
	Tasks []taskSummaryResponse `json:"tasks"`
}

type getTaskResponse struct {
	Task  taskDetailResponse `json:"task"`
	Steps []taskStepResponse `json:"steps"`
}

type getTaskArtifactsResponse struct {
	Artifacts []taskArtifactResponse `json:"artifacts"`
}

type getTaskEventsResponse struct {
	Events []taskEventResponse `json:"events"`
}

// NewTaskHandler 创建任务 handler。
func NewTaskHandler(svc *taskservice.Service, repo *postgres.TaskRepo, eventRepo *postgres.TaskEventRepo) *TaskHandler {
	return &TaskHandler{
		taskService: svc,
		taskRepo:    repo,
		eventRepo:   eventRepo,
	}
}

// Create 创建任务，并立即返回 pending 记录。
func (h *TaskHandler) Create(requestCtx context.Context, ctx *app.RequestContext) {
	var request createTaskRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "resource_id 和 instruction 不能为空"})
		return
	}

	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.Instruction = strings.TrimSpace(request.Instruction)
	if request.ResourceID == "" || request.Instruction == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "resource_id 和 instruction 不能为空"})
		return
	}

	if !isValidUUID(request.ResourceID) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "resource_id 格式无效"})
		return
	}

	task, err := h.taskService.CreateTask(requestCtx, request.ResourceID, request.Instruction)
	if err != nil {
		switch {
		case errors.Is(err, taskservice.ErrInstructionRequired):
			ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "resource_id 和 instruction 不能为空"})
		case errors.Is(err, taskservice.ErrResourceNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "资源不存在"})
		case errors.Is(err, taskservice.ErrResourceCurrentVersionNotFound):
			ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "资源当前版本不存在"})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "创建任务失败"})
		}
		return
	}

	ctx.JSON(consts.StatusCreated, createTaskResponse{
		Task: taskToSummaryResponse(*task),
	})
}

// List 返回任务列表。
func (h *TaskHandler) List(requestCtx context.Context, ctx *app.RequestContext) {
	tasks, err := h.taskService.ListTasks(requestCtx)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询任务列表失败"})
		return
	}

	response := listTasksResponse{
		Tasks: make([]taskSummaryResponse, 0, len(tasks)),
	}
	for _, task := range tasks {
		response.Tasks = append(response.Tasks, taskToSummaryResponse(task))
	}

	ctx.JSON(consts.StatusOK, response)
}

// GetByID 返回单个任务及其步骤。
func (h *TaskHandler) GetByID(requestCtx context.Context, ctx *app.RequestContext) {
	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的任务 ID"})
		return
	}
	task, steps, err := h.taskService.GetTask(requestCtx, id)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询任务失败"})
		return
	}
	if task == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}

	response := getTaskResponse{
		Task:  taskToDetailResponse(*task),
		Steps: make([]taskStepResponse, 0, len(steps)),
	}
	for _, step := range steps {
		response.Steps = append(response.Steps, taskStepToResponse(step))
	}

	ctx.JSON(consts.StatusOK, response)
}

// GetArtifacts 返回任务产物。
func (h *TaskHandler) GetArtifacts(requestCtx context.Context, ctx *app.RequestContext) {
	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的任务 ID"})
		return
	}
	task, err := h.taskRepo.GetByID(requestCtx, id)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询任务失败"})
		return
	}
	if task == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}

	artifacts, err := h.taskRepo.GetArtifacts(requestCtx, task.ID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询任务产物失败"})
		return
	}

	response := getTaskArtifactsResponse{
		Artifacts: make([]taskArtifactResponse, 0, len(artifacts)),
	}
	for _, artifact := range artifacts {
		response.Artifacts = append(response.Artifacts, taskArtifactToResponse(artifact))
	}

	ctx.JSON(consts.StatusOK, response)
}

// GetEvents 返回任务执行过程中的结构化事件流。
func (h *TaskHandler) GetEvents(requestCtx context.Context, ctx *app.RequestContext) {
	if h.eventRepo == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "任务事件服务未配置"})
		return
	}

	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的任务 ID"})
		return
	}
	task, err := h.taskRepo.GetByID(requestCtx, id)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询任务失败"})
		return
	}
	if task == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}

	events, err := h.eventRepo.ListByTask(requestCtx, task.ID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询任务事件失败"})
		return
	}

	response := getTaskEventsResponse{
		Events: make([]taskEventResponse, 0, len(events)),
	}
	for _, event := range events {
		response.Events = append(response.Events, taskEventToResponse(event))
	}

	ctx.JSON(consts.StatusOK, response)
}

func taskToSummaryResponse(task postgres.Task) taskSummaryResponse {
	return taskSummaryResponse{
		ID:           task.ID,
		ResourceID:   task.ResourceID,
		Instruction:  task.Instruction,
		Status:       task.Status,
		ErrorMessage: task.ErrorMessage,
		CreatedAt:    task.CreatedAt,
	}
}

func taskToDetailResponse(task postgres.Task) taskDetailResponse {
	return taskDetailResponse{
		ID:           task.ID,
		ResourceID:   task.ResourceID,
		Instruction:  task.Instruction,
		Status:       task.Status,
		ErrorMessage: task.ErrorMessage,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
}

func taskStepToResponse(step postgres.TaskStep) taskStepResponse {
	return taskStepResponse{
		ID:           step.ID,
		StepName:     step.StepName,
		Status:       step.Status,
		ErrorMessage: step.ErrorMessage,
		StartedAt:    step.StartedAt,
		CompletedAt:  step.CompletedAt,
	}
}

func taskArtifactToResponse(artifact postgres.TaskArtifact) taskArtifactResponse {
	return taskArtifactResponse{
		ID:           artifact.ID,
		ArtifactType: artifact.ArtifactType,
		Content:      json.RawMessage(artifact.Content),
		CreatedAt:    artifact.CreatedAt,
	}
}

func taskEventToResponse(event postgres.TaskEvent) taskEventResponse {
	return taskEventResponse{
		ID:        event.ID,
		TaskID:    event.TaskID,
		RunID:     event.RunID,
		StepName:  event.StepName,
		Source:    event.Source,
		Level:     event.Level,
		EventType: event.EventType,
		Message:   event.Message,
		Payload:   json.RawMessage(event.Payload),
		CreatedAt: event.CreatedAt,
	}
}
