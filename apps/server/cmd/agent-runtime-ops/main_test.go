package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/operations"
)

// TestRunCommandExposesDiagnosticsMetricsAndAuditedActions 验证对应场景下的正常路径与失败路径。
func TestRunCommandExposesDiagnosticsMetricsAndAuditedActions(t *testing.T) {
	service := &fakeOperationsService{
		diagnostic:  operations.Diagnostic{Run: operations.RunView{ID: "run-1", WorkspaceID: "workspace-1", Status: "failed"}},
		metrics:     operations.MetricsSnapshot{SchemaVersion: "1.1", WorkspaceID: "workspace-1"},
		comparisons: operations.ComparisonList{SchemaVersion: "1.1", WorkspaceID: "workspace-1", ResourceID: "resource-1"},
		action:      operations.ActionResult{ActionID: "action-1", Status: "completed"},
	}
	now := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	for _, args := range [][]string{
		{"-action", "diagnose", "-workspace-id", "workspace-1", "-run-id", "run-1"},
		{"-action", "metrics", "-workspace-id", "workspace-1", "-resource-id", "resource-1", "-window", "2h"},
		{"-action", "comparisons", "-workspace-id", "workspace-1", "-resource-id", "resource-1", "-window", "2h", "-limit", "100"},
		{"-action", "cancel", "-workspace-id", "workspace-1", "-run-id", "run-1", "-request-id", "request-1", "-operator-id", "operator-1", "-reason", "incident recovery"},
		{"-action", "retry", "-workspace-id", "workspace-1", "-run-id", "run-1", "-request-id", "request-2", "-operator-id", "operator-1", "-reason", "retry transient failure"},
		{"-action", "replay-dead-letter", "-workspace-id", "workspace-1", "-event-id", "event-1", "-request-id", "request-3", "-operator-id", "operator-1", "-reason", "replay fixed projection"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCommand(context.Background(), args, service, now, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%s", args, code, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Fatalf("args=%v produced no JSON", args)
		}
	}
	if service.calls != 6 || service.lastAction.RequestedAt != now {
		t.Fatalf("unexpected service calls: count=%d action=%#v", service.calls, service.lastAction)
	}
	if service.lastMetrics.ResourceID != "resource-1" || service.lastMetrics.Window != 2*time.Hour {
		t.Fatalf("metrics did not preserve the exact cohort: %#v", service.lastMetrics)
	}
	if service.lastComparisons.ResourceID != "resource-1" || service.lastComparisons.Window != 2*time.Hour || service.lastComparisons.Limit != 100 {
		t.Fatalf("comparison list did not preserve the review cohort: %#v", service.lastComparisons)
	}
}

// TestRunCommandRejectsUnauditedMutation 验证对应场景下的正常路径与失败路径。
func TestRunCommandRejectsUnauditedMutation(t *testing.T) {
	service := &fakeOperationsService{}
	var stdout, stderr bytes.Buffer
	code := runCommand(context.Background(), []string{"-action", "retry", "-workspace-id", "workspace-1", "-run-id", "run-1"}, service, time.Now(), &stdout, &stderr)
	if code != 2 || service.calls != 0 {
		t.Fatalf("unaudited mutation exit=%d calls=%d stderr=%s", code, service.calls, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = runCommand(context.Background(), []string{"-action", "comparisons", "-workspace-id", "workspace-1"}, service, time.Now(), &stdout, &stderr)
	if code != 2 || service.calls != 0 {
		t.Fatalf("unscoped comparison review exit=%d calls=%d stderr=%s", code, service.calls, stderr.String())
	}
}

type fakeOperationsService struct {
	calls           int
	diagnostic      operations.Diagnostic
	metrics         operations.MetricsSnapshot
	comparisons     operations.ComparisonList
	action          operations.ActionResult
	lastAction      operations.ActionRequest
	lastMetrics     operations.MetricsRequest
	lastComparisons operations.ComparisonListRequest
}

// Diagnose 执行该函数负责的核心处理逻辑。
func (service *fakeOperationsService) Diagnose(context.Context, operations.DiagnosticRequest) (operations.Diagnostic, error) {
	service.calls++
	return service.diagnostic, nil
}

// 指标执行该函数负责的核心处理逻辑。
func (service *fakeOperationsService) Metrics(_ context.Context, request operations.MetricsRequest) (operations.MetricsSnapshot, error) {
	service.calls++
	service.lastMetrics = request
	return service.metrics, nil
}

func (service *fakeOperationsService) Comparisons(_ context.Context, request operations.ComparisonListRequest) (operations.ComparisonList, error) {
	service.calls++
	service.lastComparisons = request
	return service.comparisons, nil
}

// Cancel 执行该函数负责的核心处理逻辑。
func (service *fakeOperationsService) Cancel(_ context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	service.calls++
	service.lastAction = request
	return service.action, nil
}

// Retry 执行该函数负责的核心处理逻辑。
func (service *fakeOperationsService) Retry(_ context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	service.calls++
	service.lastAction = request
	return service.action, nil
}

// ReplayDeadLetter 执行该函数负责的核心处理逻辑。
func (service *fakeOperationsService) ReplayDeadLetter(_ context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	service.calls++
	service.lastAction = request
	return service.action, nil
}
