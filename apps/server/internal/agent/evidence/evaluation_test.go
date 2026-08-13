package evidence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
)

// TestVersionedRetrievalEvaluationMeetsRecallAndCitationGate 验证对应场景下的正常路径与失败路径。
func TestVersionedRetrievalEvaluationMeetsRecallAndCitationGate(t *testing.T) {
	data, err := os.ReadFile("testdata/retrieval_eval_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var dataset retrievalDataset
	if err := json.Unmarshal(data, &dataset); err != nil {
		t.Fatal(err)
	}
	if dataset.SchemaVersion != "1.0" || dataset.DatasetVersion != "retrieval-eval-v1" || len(dataset.Cases) < 3 {
		t.Fatalf("invalid retrieval dataset header: %#v", dataset)
	}
	createdAt := time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)
	for _, evalCase := range dataset.Cases {
		t.Run(evalCase.ID, func(t *testing.T) {
			repo := &fakeRepository{
				scope:      agentevidence.Scope{WorkspaceID: "workspace-eval", ResourceID: "resource-eval", VersionID: "version-current", SourceType: "upload", EmbeddingProfile: "embedding-eval-v1"},
				vectorType: "vector(3)",
				lexical:    evaluationCandidates(evalCase.Lexical, createdAt), semantic: evaluationCandidates(evalCase.Semantic, createdAt),
			}
			for index := 0; index < evalCase.SyntheticDistractors; index++ {
				repo.semantic = append(repo.semantic, agentevidence.ScoredCandidate{Candidate: makeEvalCandidate(
					fmt.Sprintf("node-long-%02d", index), "unrelated long document paragraph", createdAt,
				), Score: 0.5 - float64(index)/100})
			}
			if evalCase.DegradeChannel == "lexical" {
				repo.lexicalErr = errors.New("注入的词法失败")
			}
			service, err := agentevidence.NewService(agentevidence.Config{
				ProfileVersion: "retrieval-eval-profile-v1", LexicalEnabled: true, SemanticEnabled: true,
				LexicalIndexVersion: "lexical-eval-v1", SemanticIndexVersion: "hnsw-eval-v1", CandidateLimit: 50,
				FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 0.4, VectorWeight: 0.6,
				MinimumFusedScore:    0.05,
				Embedding:            agentevidence.EmbeddingProfile{Version: "embedding-eval-v1", Model: "embed-eval", Dimensions: 3, VectorType: "vector(3)"},
				RerankProfileVersion: "rerank-disabled-eval-v1", Now: func() time.Time { return createdAt },
			}, repo, fixedEmbedder{vector: []float32{0.1, 0.2, 0.3}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			set, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-eval", ResourceID: "resource-eval", Query: evalCase.Query, Limit: evalCase.Limit})
			if err != nil {
				t.Fatal(err)
			}
			recall := agentevidence.RecallAtK(set, evalCase.RelevantNodeIDs, evalCase.Limit)
			if recall < evalCase.MinimumRecall {
				t.Fatalf("recall@%d=%f want >=%f evidence=%#v", evalCase.Limit, recall, evalCase.MinimumRecall, set.Evidence)
			}
			for _, evidence := range set.Evidence {
				if evidence.EvidenceID == "" || evidence.NodeID == "" || evidence.VersionID != "version-current" {
					t.Fatalf("citation/node location is incomplete: %#v", evidence)
				}
			}
		})
	}
}

type retrievalDataset struct {
	SchemaVersion  string              `json:"schema_version"`
	DatasetVersion string              `json:"dataset_version"`
	Cases          []retrievalEvalCase `json:"cases"`
}

type retrievalEvalCase struct {
	ID                   string          `json:"id"`
	Query                string          `json:"query"`
	Limit                int             `json:"limit"`
	RelevantNodeIDs      []string        `json:"relevant_node_ids"`
	MinimumRecall        float64         `json:"minimum_recall"`
	SyntheticDistractors int             `json:"synthetic_distractors"`
	DegradeChannel       string          `json:"degrade_channel"`
	Lexical              []evalCandidate `json:"lexical"`
	Semantic             []evalCandidate `json:"semantic"`
}

type evalCandidate struct {
	NodeID  string  `json:"node_id"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// evaluationCandidates 执行该函数负责的核心处理逻辑。
func evaluationCandidates(inputs []evalCandidate, createdAt time.Time) []agentevidence.ScoredCandidate {
	output := make([]agentevidence.ScoredCandidate, 0, len(inputs))
	for _, input := range inputs {
		output = append(output, agentevidence.ScoredCandidate{Candidate: makeEvalCandidate(input.NodeID, input.Content, createdAt), Score: input.Score})
	}
	return output
}

// makeEvalCandidate 执行该函数负责的核心处理逻辑。
func makeEvalCandidate(nodeID, content string, createdAt time.Time) agentevidence.Candidate {
	return agentevidence.Candidate{SourceID: "chunk-" + nodeID, ResourceID: "resource-eval", VersionID: "version-current", NodeID: nodeID, SourceType: "document_node", Content: content, CreatedAt: createdAt}
}
