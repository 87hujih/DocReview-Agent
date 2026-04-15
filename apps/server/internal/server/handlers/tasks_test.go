package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	servermiddleware "agent_project/apps/server/internal/server/middleware"
	"agent_project/apps/server/internal/storage/postgres"
	taskservice "agent_project/apps/server/internal/task/service"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestCreateTaskHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.POST("/api/tasks", handler.Create)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "任务创建测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "当前版本内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/tasks",
		&ut.Body{
			Body: bytes.NewBufferString(`{"resource_id":"` + resource.ID + `","instruction":"优化第三章数据分类条款"}`),
			Len:  len(`{"resource_id":"` + resource.ID + `","instruction":"优化第三章数据分类条款"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusCreated {
		t.Fatalf("expected status %d, got %d", consts.StatusCreated, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"status":"pending"`) {
		t.Fatalf("expected pending status, got %q", body)
	}
}

func TestNewTaskHandlerRequiresEventRepo(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when event repo is nil")
		}
	}()

	_ = NewTaskHandler(nil, nil, nil)
}

func TestCreateTaskHandlerMissingVersion(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.POST("/api/tasks", handler.Create)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "缺少版本测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/tasks",
		&ut.Body{
			Body: bytes.NewBufferString(`{"resource_id":"` + resource.ID + `","instruction":"优化第三章数据分类条款"}`),
			Len:  len(`{"resource_id":"` + resource.ID + `","instruction":"优化第三章数据分类条款"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestListTasksHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks", handler.List)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "任务列表测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	if _, err := taskRepo.Create(ctx, resource.ID, "请整理列表接口"); err != nil {
		t.Fatalf("create task: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
}

func TestGetTaskByIDHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id", handler.GetByID)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "任务详情测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请整理详情接口")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/"+task.ID, nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"steps":[]`) {
		t.Fatalf("expected empty steps array, got %q", body)
	}
}

func TestGetTaskByIDHandlerIncludesErrorMessages(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id", handler.GetByID)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "任务错误字段测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请触发失败态")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskError := "任务执行失败"
	if err := taskRepo.UpdateStatus(ctx, task.ID, "failed", &taskError); err != nil {
		t.Fatalf("update task status: %v", err)
	}

	step, err := taskRepo.AddStep(ctx, task.ID, "reviewer")
	if err != nil {
		t.Fatalf("add step: %v", err)
	}
	stepError := "审阅阶段失败"
	if err := taskRepo.UpdateStep(ctx, step.ID, "failed", &stepError); err != nil {
		t.Fatalf("update step: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/"+task.ID, nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"error_message":"任务执行失败"`) {
		t.Fatalf("expected task error message in response, got %q", body)
	}
	if !strings.Contains(body, `"error_message":"审阅阶段失败"`) {
		t.Fatalf("expected step error message in response, got %q", body)
	}
}

func TestGetTaskArtifactsHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id/artifacts", handler.GetArtifacts)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "任务产物测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请整理产物接口")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := taskRepo.AddArtifact(ctx, task.ID, "review_summary", []byte(`{"summary":"需要补充分级定义"}`)); err != nil {
		t.Fatalf("add artifact: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/"+task.ID+"/artifacts", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	var payload struct {
		Artifacts []struct {
			ArtifactType string          `json:"artifact_type"`
			Content      json.RawMessage `json:"content"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(payload.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(payload.Artifacts))
	}
	if payload.Artifacts[0].ArtifactType != "review_summary" {
		t.Fatalf("expected artifact type %q, got %q", "review_summary", payload.Artifacts[0].ArtifactType)
	}
	if !json.Valid(payload.Artifacts[0].Content) {
		t.Fatalf("expected artifact content to stay as json, got %s", payload.Artifacts[0].Content)
	}
}

func TestGetTaskEventsHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id/events", handler.GetEvents)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "任务事件接口测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请记录事件")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := eventRepo.Add(ctx, postgres.TaskEventCreateParams{
		TaskID:    task.ID,
		StepName:  "planner",
		Source:    "orchestrator",
		Level:     "info",
		EventType: "step.started",
		Message:   "规划步骤开始",
		Payload:   []byte(`{"step_name":"planner"}`),
	}); err != nil {
		t.Fatalf("add event: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/"+task.ID+"/events", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	var payload struct {
		Events []struct {
			EventType string          `json:"event_type"`
			Message   string          `json:"message"`
			Payload   json.RawMessage `json:"payload"`
			StepName  string          `json:"step_name"`
		} `json:"events"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(payload.Events))
	}
	if payload.Events[0].EventType != "step.started" {
		t.Fatalf("expected event type %q, got %q", "step.started", payload.Events[0].EventType)
	}
	if payload.Events[0].StepName != "planner" {
		t.Fatalf("expected step name %q, got %q", "planner", payload.Events[0].StepName)
	}
	if !json.Valid(payload.Events[0].Payload) {
		t.Fatalf("expected event payload to stay as json, got %s", payload.Events[0].Payload)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id", handler.GetByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/00000000-0000-0000-0000-000000000000", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestGetTaskByInvalidUUID(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id", handler.GetByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/not-a-uuid", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestGetTaskArtifactsByInvalidUUID(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id/artifacts", handler.GetArtifacts)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/not-a-uuid/artifacts", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestGetTaskEventsByInvalidUUID(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.GET("/api/tasks/:id/events", handler.GetEvents)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/tasks/not-a-uuid/events", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestCreateTaskInvalidResourceIDFormat(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	taskSvc := taskservice.New(taskRepo, resourceRepo, nil, nil)
	handler := NewTaskHandler(taskSvc, taskRepo, eventRepo)
	engine := server.New()
	engine.POST("/api/tasks", handler.Create)

	body := `{"resource_id":"not-a-uuid","instruction":"整理内容"}`
	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/tasks",
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid resource_id format, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestHealthHandlerSetsRequestIDHeader(t *testing.T) {	engine := server.New()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	engine.Use(servermiddleware.RequestContext("server", logger))
	engine.GET("/healthz", Health)

	response := ut.PerformRequest(engine.Engine, "GET", "/healthz", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	requestID := string(response.Header.Peek("X-Request-ID"))
	if strings.TrimSpace(requestID) == "" {
		t.Fatalf("expected X-Request-ID header, got empty value")
	}
}
