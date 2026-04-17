package retriever

import (
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

func TestFuseReciprocalRankPrefersChunksSeenByBothBackends(t *testing.T) {
	semantic := []postgres.ResourceChunk{
		{ID: "chunk-semantic-only", Content: "纯语义命中"},
		{ID: "chunk-both", Content: "词法语义双命中"},
	}
	lexical := []postgres.ResourceChunk{
		{ID: "chunk-both", Content: "词法语义双命中"},
		{ID: "chunk-lexical-only", Content: "纯词法命中"},
	}

	fused := FuseReciprocalRank(semantic, lexical)

	if len(fused) != 3 {
		t.Fatalf("expected 3 fused chunks, got %d", len(fused))
	}
	if fused[0].ID != "chunk-both" {
		t.Fatalf("expected chunk seen by both backends first, got %q", fused[0].ID)
	}
}

func TestFuseReciprocalRankKeepsSingleSourceCandidates(t *testing.T) {
	semanticOnly := FuseReciprocalRank([]postgres.ResourceChunk{
		{ID: "chunk-1", Content: "纯语义命中"},
	}, nil)
	if len(semanticOnly) != 1 || semanticOnly[0].ID != "chunk-1" {
		t.Fatalf("expected semantic-only candidate to survive, got %#v", semanticOnly)
	}

	lexicalOnly := FuseReciprocalRank(nil, []postgres.ResourceChunk{
		{ID: "chunk-2", Content: "纯词法命中"},
	})
	if len(lexicalOnly) != 1 || lexicalOnly[0].ID != "chunk-2" {
		t.Fatalf("expected lexical-only candidate to survive, got %#v", lexicalOnly)
	}
}
