package operations_test

import (
	"context"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/operations"
)

// TestServiceRequiresWorkspaceScopeForDiagnosticsAndMetrics 验证对应场景下的正常路径与失败路径。
func TestServiceRequiresWorkspaceScopeForDiagnosticsAndMetrics(t *testing.T) {
	store := &fakeStore{}
	service, err := operations.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Diagnose(context.Background(), operations.DiagnosticRequest{RunID: "run-1"}); err == nil {
		t.Fatal("diagnostics without workspace scope must fail")
	}
	if _, err := service.Metrics(context.Background(), operations.MetricsRequest{}); err == nil {
		t.Fatal("metrics without workspace scope must fail")
	}
	if _, err := service.Comparisons(context.Background(), operations.ComparisonListRequest{WorkspaceID: "workspace-1"}); err == nil {
		t.Fatal("comparison review without exact resource scope must fail")
	}
	if store.calls != 0 {
		t.Fatalf("invalid requests reached store: %d", store.calls)
	}
}

// TestServiceForwardsAuditedSafeActions 验证对应场景下的正常路径与失败路径。
func TestServiceForwardsAuditedSafeActions(t *testing.T) {
	store := &fakeStore{
		diagnostic: operations.Diagnostic{Run: operations.RunView{ID: "run-1", WorkspaceID: "workspace-1", Status: "failed"}},
		metrics:    operations.MetricsSnapshot{CollectedAt: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)},
		action:     operations.ActionResult{ActionID: "action-1", Status: "completed"},
	}
	service, err := operations.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Diagnose(context.Background(), operations.DiagnosticRequest{WorkspaceID: "workspace-1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Metrics(context.Background(), operations.MetricsRequest{WorkspaceID: "workspace-1", ResourceID: " resource-1 "}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Comparisons(context.Background(), operations.ComparisonListRequest{WorkspaceID: " workspace-1 ", ResourceID: " resource-1 "}); err != nil {
		t.Fatal(err)
	}
	base := operations.ActionRequest{
		WorkspaceID: "workspace-1", RequestID: "operator-request-1", OperatorID: "operator-1",
		Reason: "incident INC-42 recovery", RequestedAt: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
	}
	base.RunID = "run-1"
	if _, err := service.Cancel(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.RequestID = "operator-request-2"
	if _, err := service.Retry(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.RequestID = "operator-request-3"
	base.RunID = ""
	base.EventID = "event-1"
	if _, err := service.ReplayDeadLetter(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if store.calls != 6 || store.lastAction.OperatorID != "operator-1" || store.lastAction.Reason == "" {
		t.Fatalf("operation audit fields were not preserved: calls=%d action=%#v", store.calls, store.lastAction)
	}
	if store.lastMetrics.WorkspaceID != "workspace-1" || store.lastMetrics.ResourceID != "resource-1" || store.lastMetrics.Window != time.Hour {
		t.Fatalf("metrics cohort scope was not normalized: %#v", store.lastMetrics)
	}
	if store.lastComparisons.WorkspaceID != "workspace-1" || store.lastComparisons.ResourceID != "resource-1" ||
		store.lastComparisons.Window != time.Hour || store.lastComparisons.Limit != 200 {
		t.Fatalf("comparison review scope was not normalized: %#v", store.lastComparisons)
	}
}

// TestServiceRejectsUnauditedMutationBeforeStore 验证对应场景下的正常路径与失败路径。
func TestServiceRejectsUnauditedMutationBeforeStore(t *testing.T) {
	store := &fakeStore{}
	service, _ := operations.NewService(store)
	_, err := service.Retry(context.Background(), operations.ActionRequest{WorkspaceID: "workspace-1", RunID: "run-1"})
	if err == nil {
		t.Fatal("unaudited retry must fail")
	}
	if store.calls != 0 {
		t.Fatal("invalid retry reached store")
	}
}

type fakeStore struct {
	calls           int
	diagnostic      operations.Diagnostic
	metrics         operations.MetricsSnapshot
	action          operations.ActionResult
	lastAction      operations.ActionRequest
	lastMetrics     operations.MetricsRequest
	lastComparisons operations.ComparisonListRequest
}

// Diagnose 执行该函数负责的核心处理逻辑。
func (store *fakeStore) Diagnose(context.Context, operations.DiagnosticRequest) (operations.Diagnostic, error) {
	store.calls++
	return store.diagnostic, nil
}

// 指标执行该函数负责的核心处理逻辑。
func (store *fakeStore) Metrics(_ context.Context, request operations.MetricsRequest) (operations.MetricsSnapshot, error) {
	store.calls++
	store.lastMetrics = request
	return store.metrics, nil
}

func (store *fakeStore) Comparisons(_ context.Context, request operations.ComparisonListRequest) (operations.ComparisonList, error) {
	store.calls++
	store.lastComparisons = request
	return operations.ComparisonList{}, nil
}

// Cancel 执行该函数负责的核心处理逻辑。
func (store *fakeStore) Cancel(_ context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	store.calls++
	store.lastAction = request
	return store.action, nil
}

// Retry 执行该函数负责的核心处理逻辑。
func (store *fakeStore) Retry(_ context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	store.calls++
	store.lastAction = request
	return store.action, nil
}

// ReplayDeadLetter 执行该函数负责的核心处理逻辑。
func (store *fakeStore) ReplayDeadLetter(_ context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	store.calls++
	store.lastAction = request
	return store.action, nil
}
