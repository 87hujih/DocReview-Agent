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
	handler := NewApprovalHandler(approval.NewService(approvalRepo, jobRepo, taskRepo, make(chan postgres.ExecutionJob, 2)))
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
	if _, err := approvalRepo.Create(ctx, task.ID); err != nil {
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

func TestApproveApprovalHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(approvalRepo, jobRepo, taskRepo, make(chan postgres.ExecutionJob, 2)))
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

	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
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
	handler := NewApprovalHandler(approval.NewService(approvalRepo, jobRepo, taskRepo, make(chan postgres.ExecutionJob, 2)))
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

	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
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

func TestApproveApprovalNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	handler := NewApprovalHandler(approval.NewService(approvalRepo, jobRepo, taskRepo, make(chan postgres.ExecutionJob, 2)))
	engine := server.New()
	engine.POST("/api/approvals/:id/approve", handler.Approve)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/approvals/00000000-0000-0000-0000-000000000000/approve", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}
