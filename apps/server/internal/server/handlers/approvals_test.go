package handlers

import (
	"bytes"
	"strings"
	"testing"

	"agent_project/apps/server/internal/approval"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/task/models"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestListApprovalsHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.GET("/api/approvals", handler.List)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "审批列表测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "列出待审批任务")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := approvalRepo.Create(ctx, task.ID, version.ID); err != nil {
		t.Fatalf("create approval: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/approvals?status=pending", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"status":"pending"`) {
		t.Fatalf("expected body to contain pending approval, got %q", body)
	}
}

func TestGetApprovalHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.GET("/api/approvals/:id", handler.GetByID)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "审批详情接口测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "读取审批详情")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/approvals/"+approvalRecord.ID, nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"id":"`+approvalRecord.ID+`"`) {
		t.Fatalf("expected body to contain approval id, got %q", body)
	}
	if !strings.Contains(body, `"task_id":"`+task.ID+`"`) {
		t.Fatalf("expected body to contain task id, got %q", body)
	}
}

func TestGetApprovalHandlerNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.GET("/api/approvals/:id", handler.GetByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/approvals/00000000-0000-0000-0000-000000000000", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestGetJobHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.GET("/api/jobs/:id", handler.GetJobByID)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "执行作业详情接口测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "读取执行作业详情")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/jobs/"+jobRecord.ID, nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"id":"`+jobRecord.ID+`"`) {
		t.Fatalf("expected body to contain job id, got %q", body)
	}
	if !strings.Contains(body, `"approval_id":"`+approvalRecord.ID+`"`) {
		t.Fatalf("expected body to contain approval id, got %q", body)
	}
}

func TestGetJobHandlerNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.GET("/api/jobs/:id", handler.GetJobByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/jobs/00000000-0000-0000-0000-000000000000", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestApproveApprovalHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.POST("/api/approvals/:id/approve", handler.Approve)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "审批通过测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "通过当前审批")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "POST", "/api/approvals/"+approvalRecord.ID+"/approve", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"status":"approved"`) {
		t.Fatalf("expected body to contain approved status, got %q", body)
	}
}

func TestRejectApprovalHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.POST("/api/approvals/:id/reject", handler.Reject)

	ctx := testContext(t)
	resource, err := resourceRepo.Create(ctx, "审批拒绝接口测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "拒绝当前审批")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/approvals/"+approvalRecord.ID+"/reject",
		&ut.Body{
			Body: bytes.NewBufferString(`{"reason":"需要补充依据"}`),
			Len:  len(`{"reason":"需要补充依据"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, `"status":"rejected"`) {
		t.Fatalf("expected body to contain rejected status, got %q", body)
	}
	if !strings.Contains(body, `需要补充依据`) {
		t.Fatalf("expected body to contain reject reason, got %q", body)
	}
}

func TestRejectApprovalHandlerMissingReason(t *testing.T) {
	handler := NewApprovalHandler(nil)
	engine := server.New()
	engine.POST("/api/approvals/:id/reject", handler.Reject)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/approvals/00000000-0000-0000-0000-000000000000/reject",
		&ut.Body{
			Body: bytes.NewBufferString(`{}`),
			Len:  len(`{}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestGetApprovalByInvalidUUID(t *testing.T) {
	handler := NewApprovalHandler(nil)
	engine := server.New()
	engine.GET("/api/approvals/:id", handler.GetByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/approvals/not-a-uuid", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestApproveByInvalidUUID(t *testing.T) {
	handler := NewApprovalHandler(nil)
	engine := server.New()
	engine.POST("/api/approvals/:id/approve", handler.Approve)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/approvals/not-a-uuid/approve", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestRejectByInvalidUUID(t *testing.T) {
	handler := NewApprovalHandler(nil)
	engine := server.New()
	engine.POST("/api/approvals/:id/reject", handler.Reject)

	body := `{"reason":"测试原因"}`
	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/approvals/not-a-uuid/reject",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestGetJobByInvalidUUID(t *testing.T) {
	handler := NewApprovalHandler(nil)
	engine := server.New()
	engine.GET("/api/jobs/:id", handler.GetJobByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/jobs/not-a-uuid", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for invalid UUID, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestApproveApprovalNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil))
	engine := server.New()
	engine.POST("/api/approvals/:id/approve", handler.Approve)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/approvals/00000000-0000-0000-0000-000000000000/approve", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}
