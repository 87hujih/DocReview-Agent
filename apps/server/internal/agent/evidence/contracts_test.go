package evidence_test

import (
	"testing"
	"time"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
)

// TestEvidenceSetValidateAcceptsCompleteVersionedEvidence 验证对应场景下的正常路径与失败路径。
func TestEvidenceSetValidateAcceptsCompleteVersionedEvidence(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	set := agentevidence.EvidenceSet{
		SchemaVersion:  "1.0",
		SetID:          "evset-1",
		WorkspaceID:    "workspace-1",
		ResourceID:     "resource-1",
		VersionID:      "version-2",
		Query:          "runtime policy",
		QueryHash:      "sha256:08f453537c0a0146dbef49ea9f2f341b103a34f033d0af8b375c2255274e8875",
		ProfileVersion: "retrieval-v1",
		CreatedAt:      createdAt,
		Evidence: []agentevidence.Evidence{{
			EvidenceID: "evidence-1", ResourceID: "resource-1", VersionID: "version-2", NodeID: "node-7",
			SourceType: "document_node", Content: "Policy is deterministic.",
			ContentHash:  "sha256:4ee2f53bb34ec03745f0720b46122e94d586d72800bb27c59d256cf922f418d9",
			LexicalScore: 0.8, VectorScore: 0.7, FusedScore: 0.76,
			TrustLevel: agentevidence.TrustUntrusted, CreatedAt: createdAt,
			Provenance: agentevidence.EvidenceProvenance{
				Retrieval: []agentevidence.RetrievalRecord{{Channel: agentevidence.ChannelLexical, Rank: 1, Score: 0.8, IndexVersion: "lexical-v1"}},
				Filtering: []agentevidence.FilterRecord{{Stage: "version_scope", Decision: agentevidence.FilterIncluded, Reason: "current_version"}},
				Fusion:    agentevidence.FusionRecord{Algorithm: agentevidence.FusionWeightedSum, ProfileVersion: "retrieval-v1", PreRerankRank: 1, Threshold: 0.2},
				Rerank:    agentevidence.RerankRecord{Enabled: true, Applied: true, ProfileVersion: "rerank-v1", Model: "reranker-1", BeforeRank: 1, AfterRank: 1, Score: 0.9},
			},
		}},
		Process: []agentevidence.ProcessRecord{{Stage: agentevidence.StageRecall, Status: agentevidence.ProcessSucceeded, Channel: agentevidence.ChannelLexical, OutputCount: 1}},
	}

	if err := set.Validate(); err != nil {
		t.Fatalf("validate complete EvidenceSet: %v", err)
	}
}
