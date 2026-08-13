package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/identity"
	"agent_project/apps/server/internal/agent/operations"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type fakeAgentRuntimeQueryReader struct {
	runRequest      operations.RunListRequest
	approvalRequest operations.ApprovalListRequest
	diagnostic      operations.Diagnostic
	approval        operations.ApprovalSummary
	runs            []operations.RunSummary
	approvals       []operations.ApprovalSummary
	err             error
}

func (reader *fakeAgentRuntimeQueryReader) ListRuns(_ context.Context, request operations.RunListRequest) ([]operations.RunSummary, error) {
	reader.runRequest = request
	return reader.runs, reader.err
}

func (reader *fakeAgentRuntimeQueryReader) Diagnose(context.Context, operations.DiagnosticRequest) (operations.Diagnostic, error) {
	return reader.diagnostic, reader.err
}

func (reader *fakeAgentRuntimeQueryReader) ListApprovals(_ context.Context, request operations.ApprovalListRequest) ([]operations.ApprovalSummary, error) {
	reader.approvalRequest = request
	return reader.approvals, reader.err
}

func (reader *fakeAgentRuntimeQueryReader) GetApproval(context.Context, string, string) (operations.ApprovalSummary, error) {
	return reader.approval, reader.err
}

func TestAgentRuntimeQueryListUsesTrustedWorkspaceAndBoundedFilters(t *testing.T) {
	reader := &fakeAgentRuntimeQueryReader{runs: []operations.RunSummary{{ID: "run-1", Status: "running"}}}
	handler := NewAgentRuntimeQueryHandler(reader, fakeApprovalIdentity{scope: queryTrustedScope()})
	h := server.New()
	h.GET("/api/agent/runs", handler.ListRuns)
	response := ut.PerformRequest(h.Engine, "GET", "/api/agent/runs?status=running&limit=25", nil,
		ut.Header{Key: "X-Request-ID", Value: "request-1"},
		ut.Header{Key: identity.HeaderWorkspaceID, Value: queryTrustedScope().WorkspaceID},
		ut.Header{Key: identity.HeaderSignature, Value: strings.Repeat("0", 64)},
	).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected run list success, got %d: %s", response.StatusCode(), response.Body())
	}
	if reader.runRequest.WorkspaceID != queryTrustedScope().WorkspaceID || reader.runRequest.Status != "running" || reader.runRequest.Limit != 25 {
		t.Fatalf("query did not use trusted bounded scope: %+v", reader.runRequest)
	}
	var payload struct {
		Runs []operations.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil || len(payload.Runs) != 1 {
		t.Fatalf("unexpected run list payload: %s err=%v", response.Body(), err)
	}
}

func TestAgentRuntimeQueryRejectsUnsignedRequestBeforeReader(t *testing.T) {
	reader := &fakeAgentRuntimeQueryReader{}
	handler := NewAgentRuntimeQueryHandler(reader, fakeApprovalIdentity{scope: queryTrustedScope()})
	h := server.New()
	h.GET("/api/agent/approvals", handler.ListApprovals)
	response := ut.PerformRequest(h.Engine, "GET", "/api/agent/approvals", nil,
		ut.Header{Key: identity.HeaderWorkspaceID, Value: queryTrustedScope().WorkspaceID},
	).Result()
	if response.StatusCode() != consts.StatusUnauthorized || reader.approvalRequest.WorkspaceID != "" {
		t.Fatalf("unsigned query reached reader: status=%d request=%+v", response.StatusCode(), reader.approvalRequest)
	}
}

func TestAgentRuntimeQueryDetailOmitsInternalRuntimePayloads(t *testing.T) {
	runID := "00000000-0000-4000-8000-000000000020"
	reader := &fakeAgentRuntimeQueryReader{diagnostic: operations.Diagnostic{
		Run:              operations.RunView{ID: runID, Status: "running", Objective: "review", StateJSON: json.RawMessage(`{"secret":"state"}`), CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Steps:            []operations.StepView{{ID: "step-1", StepKey: "review", StepType: "graph", Status: "running", InputJSON: json.RawMessage(`{"secret":"input"}`)}},
		ToolCalls:        []operations.ToolCallView{{ID: "call-1", StepID: "step-1", ToolName: "documents.read", ToolVersion: "v1", Status: "succeeded", OutputJSON: json.RawMessage(`{"secret":"output"}`)}},
		ContextManifests: []operations.ContextManifestView{{ID: "manifest-1", ItemsJSON: json.RawMessage(`[{"secret":"context"}]`)}},
	}}
	handler := NewAgentRuntimeQueryHandler(reader, fakeApprovalIdentity{scope: queryTrustedScope()})
	h := server.New()
	h.GET("/api/agent/runs/:id", handler.GetRun)
	response := ut.PerformRequest(h.Engine, "GET", "/api/agent/runs/"+runID, nil,
		ut.Header{Key: "X-Request-ID", Value: "request-1"},
		ut.Header{Key: identity.HeaderWorkspaceID, Value: queryTrustedScope().WorkspaceID},
		ut.Header{Key: identity.HeaderSignature, Value: strings.Repeat("0", 64)},
	).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected run detail success, got %d: %s", response.StatusCode(), response.Body())
	}
	body := string(response.Body())
	for _, forbidden := range []string{"state_json", "input_json", "output_json", "context_manifests", "secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public run detail leaked %q: %s", forbidden, body)
		}
	}
}

func queryTrustedScope() identity.WorkspaceScope {
	return identity.WorkspaceScope{
		WorkspaceID: "00000000-0000-4000-8000-000000000010", Trusted: true, TrustSource: "edge-hmac-v1",
		Principal: identity.Principal{Type: "user", ID: "00000000-0000-4000-8000-000000000011"},
	}
}
