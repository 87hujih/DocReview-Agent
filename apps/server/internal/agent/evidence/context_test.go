package evidence_test

import (
	"context"
	"testing"
	"time"

	agentcontext "agent_project/apps/server/internal/agent/context"
	agentevidence "agent_project/apps/server/internal/agent/evidence"
)

// TestContextItemsUseFusedRelevanceAndAssemblerBudget 验证对应场景下的正常路径与失败路径。
func TestContextItemsUseFusedRelevanceAndAssemblerBudget(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 14, 0, 0, 0, time.UTC)
	set := validContextEvidenceSet(createdAt)
	items, err := agentevidence.ContextItems(set)
	if err != nil {
		t.Fatalf("map EvidenceSet context: %v", err)
	}
	if len(items) != 2 || items[0].SourceID != "evidence-low" || items[0].RelevanceScore != 0.2 ||
		items[1].NodeID != "node-high" || items[1].TrustLevel != agentcontext.TrustUntrusted {
		t.Fatalf("evidence context items=%#v", items)
	}
	assembler, err := agentcontext.NewAssembler(agentcontext.Config{
		Tokenizer: contextWordTokenizer{}, TokenBudget: 7, ReservedOutputTokens: 1,
		LayerBudgets: map[agentcontext.Layer]int{agentcontext.LayerEvidence: 3},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := assembler.Assemble(context.Background(), agentcontext.Request{Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Manifest.Items) != 1 || result.Manifest.Items[0].SourceID != "evidence-high" {
		t.Fatalf("budgeted evidence=%#v", result.Manifest.Items)
	}
}

type contextWordTokenizer struct{}

// Name 执行该函数负责的核心处理逻辑。
func (contextWordTokenizer) Name() string { return "context-word-v1" }

// 数量执行该函数负责的核心处理逻辑。
func (contextWordTokenizer) Count(text string) int {
	count := 0
	inWord := false
	for _, value := range text {
		if value == ' ' || value == '\n' || value == '\t' {
			inWord = false
			continue
		}
		if !inWord {
			count++
			inWord = true
		}
	}
	return count
}

// validContextEvidenceSet 执行该函数负责的核心处理逻辑。
func validContextEvidenceSet(createdAt time.Time) agentevidence.EvidenceSet {
	set := agentevidence.EvidenceSet{
		SchemaVersion: agentevidence.SchemaVersion, SetID: "evset-context", WorkspaceID: "workspace-1",
		ResourceID: "resource-1", VersionID: "version-1", Query: "policy",
		QueryHash:      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProfileVersion: "retrieval-v1", CreatedAt: createdAt,
		Process: []agentevidence.ProcessRecord{{Stage: agentevidence.StageRecall, Status: agentevidence.ProcessSucceeded, Channel: agentevidence.ChannelLexical}},
	}
	makeEvidence := func(id, node, content string, score float64) agentevidence.Evidence {
		return agentevidence.Evidence{
			EvidenceID: id, ResourceID: set.ResourceID, VersionID: set.VersionID, NodeID: node,
			SourceType: "document_node", Content: content,
			ContentHash:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			LexicalScore: score, FusedScore: score, TrustLevel: agentevidence.TrustUntrusted, CreatedAt: createdAt,
			Provenance: agentevidence.EvidenceProvenance{
				Retrieval: []agentevidence.RetrievalRecord{{Channel: agentevidence.ChannelLexical, Rank: 1, Score: score, IndexVersion: "lexical-v1"}},
				Filtering: []agentevidence.FilterRecord{{Stage: "version_scope", Decision: agentevidence.FilterIncluded, Reason: "current_version"}},
				Fusion:    agentevidence.FusionRecord{Algorithm: agentevidence.FusionWeightedSum, ProfileVersion: "retrieval-v1", PreRerankRank: 1, Threshold: 0.1},
				Rerank:    agentevidence.RerankRecord{ProfileVersion: "rerank-disabled-v1", BeforeRank: 1, AfterRank: 1},
			},
		}
	}
	set.Evidence = []agentevidence.Evidence{
		makeEvidence("evidence-low", "node-low", "low evidence words", 0.2),
		makeEvidence("evidence-high", "node-high", "high evidence words", 0.9),
	}
	return set
}
