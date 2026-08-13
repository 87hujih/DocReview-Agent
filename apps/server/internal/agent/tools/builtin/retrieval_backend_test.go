package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
	agenttools "agent_project/apps/server/internal/agent/tools"
)

func TestEvidenceRetrievalBackendClassifiesProfileMismatchWithStableReasonCode(t *testing.T) {
	backend, err := NewEvidenceRetrievalBackend(errorEvidenceSearcher{err: agentevidence.ErrEmbeddingProfileMismatch})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Search(context.Background(), agenttools.SecurityContext{
		WorkspaceID: "workspace-1", PrincipalType: "user", PrincipalID: "principal-1",
	}, RetrievalInput{ResourceID: "resource-1", Query: "query"})
	var failure *agenttools.ToolError
	if !errors.As(err, &failure) {
		t.Fatalf("expected classified tool error, got %v", err)
	}
	if failure.Category != agenttools.ErrorTerminalUpstream {
		t.Fatalf("unexpected category: %s", failure.Category)
	}
	var details struct {
		ReasonCode string `json:"reason_code"`
	}
	if json.Unmarshal(failure.Details, &details) != nil || details.ReasonCode != "embedding_profile_mismatch" {
		t.Fatalf("profile mismatch must carry a stable reason code: %s", failure.Details)
	}
}

type errorEvidenceSearcher struct{ err error }

func (searcher errorEvidenceSearcher) Search(context.Context, agentevidence.SearchRequest) (agentevidence.EvidenceSet, error) {
	return agentevidence.EvidenceSet{}, searcher.err
}
