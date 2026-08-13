package cutover

import (
	"context"
	"strings"
	"testing"
	"time"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
	"agent_project/apps/server/internal/agent/identity"
)

type fakeShadowEvidence struct {
	request agentevidence.SearchRequest
	set     agentevidence.EvidenceSet
}

// 搜索执行该函数负责的核心处理逻辑。
func (searcher *fakeShadowEvidence) Search(_ context.Context, request agentevidence.SearchRequest) (agentevidence.EvidenceSet, error) {
	searcher.request = request
	return searcher.set, nil
}

// TestEvidenceShadowEvaluatorIsReadOnlyAndProducesTypedFacts 验证对应场景下的正常路径与失败路径。
func TestEvidenceShadowEvaluatorIsReadOnlyAndProducesTypedFacts(t *testing.T) {
	searcher := &fakeShadowEvidence{set: agentevidence.EvidenceSet{
		SchemaVersion: agentevidence.SchemaVersion, SetID: "set-1", WorkspaceID: "workspace-1", ResourceID: "resource-1",
		VersionID: "version-1", Query: "review", QueryHash: "sha256:" + strings.Repeat("a", 64), ProfileVersion: "profile-1", CreatedAt: time.Now().UTC(),
		Evidence: []agentevidence.Evidence{}, Process: []agentevidence.ProcessRecord{{Stage: agentevidence.StageRecall, Status: agentevidence.ProcessSucceeded, Channel: agentevidence.ChannelLexical}},
	}}
	evaluator, err := NewEvidenceShadowEvaluator(searcher, 8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), ShadowRequest{Request: Request{
		WorkspaceID: "workspace-1", ResourceID: "resource-1", Message: "review",
		Scope: identity.WorkspaceScope{WorkspaceID: "workspace-1", Trusted: true, TrustSource: "edge", Principal: identity.Principal{Type: "user", ID: "user-1"}},
	}, AllowWrites: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != ModeShadow || len(result.Events) != 1 || searcher.request.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected typed shadow projection: %+v / %+v", result, searcher.request)
	}
}

// TestEvidenceShadowEvaluatorFailsClosedIfWritesAreEnabled 验证对应场景下的正常路径与失败路径。
func TestEvidenceShadowEvaluatorFailsClosedIfWritesAreEnabled(t *testing.T) {
	evaluator, _ := NewEvidenceShadowEvaluator(&fakeShadowEvidence{}, 8)
	_, err := evaluator.Evaluate(context.Background(), ShadowRequest{AllowWrites: true})
	if err == nil || !strings.Contains(err.Error(), "仅允许读取") {
		t.Fatalf("应触发只读屏障，实际错误：%v", err)
	}
}
