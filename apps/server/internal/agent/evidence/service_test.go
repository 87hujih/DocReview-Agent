package evidence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
)

// TestSearchDefaultsToCurrentVersion 验证对应场景下的正常路径与失败路径。
func TestSearchDefaultsToCurrentVersion(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	repo := &fakeRepository{
		scope: agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload"},
		lexical: []agentevidence.ScoredCandidate{{Candidate: agentevidence.Candidate{
			SourceID: "chunk-1", ResourceID: "resource-1", VersionID: "version-current", NodeID: "node-current",
			SourceType: "document_node", Content: "current policy", CreatedAt: createdAt,
		}, Score: 0.9}},
	}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, LexicalIndexVersion: "lexical-v1",
		CandidateLimit: 8, FusionAlgorithm: agentevidence.FusionWeightedSum,
		LexicalWeight: 1, MinimumFusedScore: 0.2,
		RerankProfileVersion: "rerank-disabled-v1", Now: func() time.Time { return createdAt },
	}, repo, nil, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	set, err := service.Search(context.Background(), agentevidence.SearchRequest{
		WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 5,
	})
	if err != nil {
		t.Fatalf("search current version: %v", err)
	}
	if repo.requestedVersionID != "" || repo.requestedHistory {
		t.Fatalf("default search requested history: version=%q history=%t", repo.requestedVersionID, repo.requestedHistory)
	}
	if set.VersionID != "version-current" || len(set.Evidence) != 1 || set.Evidence[0].NodeID != "node-current" {
		t.Fatalf("current-version EvidenceSet = %#v", set)
	}
}

// TestSearchRejectsHistoricalScopeResolvedToDifferentVersion 验证对应场景下的正常路径与失败路径。
func TestSearchRejectsHistoricalScopeResolvedToDifferentVersion(t *testing.T) {
	repo := &fakeRepository{scope: agentevidence.Scope{
		WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-other", SourceType: "upload",
	}}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, LexicalIndexVersion: "lexical-v1",
		CandidateLimit: 8, FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 1,
		MinimumFusedScore: 0.2, RerankProfileVersion: "rerank-disabled-v1",
	}, repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Search(context.Background(), agentevidence.SearchRequest{
		WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-requested",
		IncludeHistory: true, Query: "policy", Limit: 5,
	})
	if !errors.Is(err, agentevidence.ErrScopeNotFound) {
		t.Fatalf("historical scope mismatch error = %v", err)
	}
}

// TestSearchDegradesLexicalFailureToSemanticEvidence 验证对应场景下的正常路径与失败路径。
func TestSearchDegradesLexicalFailureToSemanticEvidence(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	repo := &fakeRepository{
		scope:      agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload", EmbeddingProfile: "embedding-v1"},
		vectorType: "vector(3)",
		lexicalErr: errors.New("处理失败：词法索引不可用"),
		lexical: []agentevidence.ScoredCandidate{{Candidate: agentevidence.Candidate{
			SourceID: "chunk-partial", ResourceID: "resource-1", VersionID: "version-current", NodeID: "node-partial",
			SourceType: "document_node", Content: "unconfirmed partial result", CreatedAt: createdAt,
		}, Score: 1}},
		semantic: []agentevidence.ScoredCandidate{{Candidate: agentevidence.Candidate{
			SourceID: "chunk-semantic", ResourceID: "resource-1", VersionID: "version-current", NodeID: "node-semantic",
			SourceType: "document_node", Content: "semantic policy", CreatedAt: createdAt,
		}, Score: 0.85}},
	}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, SemanticEnabled: true,
		LexicalIndexVersion: "lexical-v1", SemanticIndexVersion: "hnsw-v1", CandidateLimit: 8,
		FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 0.4, VectorWeight: 0.6,
		MinimumFusedScore:    0.2,
		Embedding:            agentevidence.EmbeddingProfile{Version: "embedding-v1", Model: "embed-model", Dimensions: 3, VectorType: "vector(3)"},
		RerankProfileVersion: "rerank-disabled-v1", Now: func() time.Time { return createdAt },
	}, repo, fixedEmbedder{vector: []float32{0.1, 0.2, 0.3}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	set, err := service.Search(context.Background(), agentevidence.SearchRequest{
		WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 5,
	})
	if err != nil {
		t.Fatalf("semantic degradation search: %v", err)
	}
	if len(set.Evidence) != 1 || set.Evidence[0].NodeID != "node-semantic" || set.Evidence[0].VectorScore != 0.85 {
		t.Fatalf("semantic evidence = %#v", set.Evidence)
	}
	if !hasProcess(set.Process, agentevidence.StageRecall, agentevidence.ProcessDegraded, agentevidence.ChannelLexical) {
		t.Fatalf("lexical degradation provenance missing: %#v", set.Process)
	}
}

// TestSearchAppliesVersionedRerankAfterFusion 验证对应场景下的正常路径与失败路径。
func TestSearchAppliesVersionedRerankAfterFusion(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepository{
		scope: agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload"},
		lexical: []agentevidence.ScoredCandidate{
			{Candidate: agentevidence.Candidate{SourceID: "chunk-1", ResourceID: "resource-1", VersionID: "version-current", NodeID: "node-first", SourceType: "document_node", Content: "first", CreatedAt: createdAt}, Score: 0.9},
			{Candidate: agentevidence.Candidate{SourceID: "chunk-2", ResourceID: "resource-1", VersionID: "version-current", NodeID: "node-second", SourceType: "document_node", Content: "second", CreatedAt: createdAt}, Score: 0.8},
		},
	}
	reranker := &fixedReranker{results: []agentevidence.RerankResult{{Index: 1, Score: 0.95}, {Index: 0, Score: 0.7}}}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, LexicalIndexVersion: "lexical-v1",
		CandidateLimit: 8, FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 1,
		MinimumFusedScore: 0.2, RerankEnabled: true, RerankProfileVersion: "rerank-v3", RerankModel: "reranker-model",
		Now: func() time.Time { return createdAt },
	}, repo, nil, reranker)
	if err != nil {
		t.Fatal(err)
	}

	set, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if reranker.calls != 1 || len(set.Evidence) != 2 || set.Evidence[0].NodeID != "node-second" {
		t.Fatalf("reranked EvidenceSet=%#v calls=%d", set.Evidence, reranker.calls)
	}
	if provenance := set.Evidence[0].Provenance.Rerank; !provenance.Applied || provenance.ProfileVersion != "rerank-v3" || provenance.AfterRank != 1 {
		t.Fatalf("rerank provenance=%#v", provenance)
	}
}

// TestSearchDegradesSemanticFailureToLexicalEvidence 验证对应场景下的正常路径与失败路径。
func TestSearchDegradesSemanticFailureToLexicalEvidence(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 10, 30, 0, 0, time.UTC)
	repo := &fakeRepository{
		scope:      agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload", EmbeddingProfile: "embedding-v1"},
		vectorType: "vector(3)", semanticErr: errors.New("处理失败：hnsw 不可用"),
		lexical: []agentevidence.ScoredCandidate{{Candidate: candidate("chunk-lexical", "node-lexical", "lexical policy", createdAt), Score: 0.75}},
	}
	service := newHybridService(t, repo, fixedEmbedder{vector: []float32{0.1, 0.2, 0.3}}, nil, createdAt)

	set, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Evidence) != 1 || set.Evidence[0].NodeID != "node-lexical" || set.Evidence[0].LexicalScore != 0.75 {
		t.Fatalf("lexical degradation evidence=%#v", set.Evidence)
	}
	if !hasProcess(set.Process, agentevidence.StageRecall, agentevidence.ProcessDegraded, agentevidence.ChannelSemantic) {
		t.Fatalf("semantic degradation provenance missing: %#v", set.Process)
	}
}

// TestSearchFusesScoresAndFiltersBelowVersionedThreshold 验证对应场景下的正常路径与失败路径。
func TestSearchFusesScoresAndFiltersBelowVersionedThreshold(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 11, 0, 0, 0, time.UTC)
	shared := candidate("chunk-shared", "node-shared", "shared policy", createdAt)
	repo := &fakeRepository{
		scope:      agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload", EmbeddingProfile: "embedding-v1"},
		vectorType: "vector(3)",
		lexical:    []agentevidence.ScoredCandidate{{Candidate: candidate("chunk-lexical", "node-lexical", "lexical only", createdAt), Score: 0.9}, {Candidate: shared, Score: 0.6}},
		semantic:   []agentevidence.ScoredCandidate{{Candidate: shared, Score: 0.8}},
	}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-threshold-v2", LexicalEnabled: true, SemanticEnabled: true,
		LexicalIndexVersion: "lexical-v1", SemanticIndexVersion: "hnsw-v1", CandidateLimit: 8,
		FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 0.5, VectorWeight: 0.5,
		MinimumFusedScore:    0.5,
		Embedding:            agentevidence.EmbeddingProfile{Version: "embedding-v1", Model: "embed-model", Dimensions: 3, VectorType: "vector(3)"},
		RerankProfileVersion: "rerank-disabled-v1", Now: func() time.Time { return createdAt },
	}, repo, fixedEmbedder{vector: []float32{0.1, 0.2, 0.3}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	set, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Evidence) != 1 || set.Evidence[0].NodeID != "node-shared" || set.Evidence[0].FusedScore != 0.7 {
		t.Fatalf("thresholded fusion=%#v", set.Evidence)
	}
	if set.Evidence[0].Provenance.Fusion.ProfileVersion != "retrieval-threshold-v2" || set.Evidence[0].Provenance.Fusion.Threshold != 0.5 {
		t.Fatalf("fusion provenance=%#v", set.Evidence[0].Provenance.Fusion)
	}
}

// TestSearchRequiresExplicitHistoricalRequest 验证对应场景下的正常路径与失败路径。
func TestSearchRequiresExplicitHistoricalRequest(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 11, 30, 0, 0, time.UTC)
	repo := &fakeRepository{
		scope:   agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-old", SourceType: "upload"},
		lexical: []agentevidence.ScoredCandidate{{Candidate: agentevidence.Candidate{SourceID: "chunk-old", ResourceID: "resource-1", VersionID: "version-old", NodeID: "node-old", SourceType: "document_node", Content: "old policy", CreatedAt: createdAt}, Score: 0.8}},
	}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, LexicalIndexVersion: "lexical-v1", CandidateLimit: 8,
		FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 1, MinimumFusedScore: 0.2,
		RerankProfileVersion: "rerank-disabled-v1", Now: func() time.Time { return createdAt },
	}, repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-old", Query: "old", Limit: 1}); !errors.Is(err, agentevidence.ErrInvalidSearchRequest) {
		t.Fatalf("implicit historical request error=%v", err)
	}
	set, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-old", IncludeHistory: true, Query: "old", Limit: 1})
	if err != nil || len(set.Evidence) != 1 || set.VersionID != "version-old" || !repo.requestedHistory {
		t.Fatalf("explicit history set=%#v err=%v", set, err)
	}
	if set.Evidence[0].Provenance.Filtering[0].Reason != "explicit_historical_version" {
		t.Fatalf("historical provenance=%#v", set.Evidence[0].Provenance.Filtering)
	}
}

// TestSearchFailsOnEmbeddingDimensionMismatch 验证对应场景下的正常路径与失败路径。
func TestSearchFailsOnEmbeddingDimensionMismatch(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepository{scope: agentevidence.Scope{
		WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload", EmbeddingProfile: "embedding-v1",
	}, vectorType: "vector(3)"}
	service := newHybridService(t, repo, fixedEmbedder{vector: []float32{0.1, 0.2}}, nil, createdAt)

	_, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 3})
	if !errors.Is(err, agentevidence.ErrEmbeddingProfileMismatch) {
		t.Fatalf("embedding dimension mismatch error=%v", err)
	}
}

// TestSearchRetainsFusionOrderWhenRerankerFails 验证对应场景下的正常路径与失败路径。
func TestSearchRetainsFusionOrderWhenRerankerFails(t *testing.T) {
	createdAt := time.Date(2026, time.August, 10, 12, 30, 0, 0, time.UTC)
	repo := &fakeRepository{
		scope:   agentevidence.Scope{WorkspaceID: "workspace-1", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload"},
		lexical: []agentevidence.ScoredCandidate{{Candidate: candidate("chunk-1", "node-first", "first", createdAt), Score: 0.9}, {Candidate: candidate("chunk-2", "node-second", "second", createdAt), Score: 0.8}},
	}
	reranker := &fixedReranker{err: errors.New("处理失败：reranker 不可用")}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, LexicalIndexVersion: "lexical-v1", CandidateLimit: 8,
		FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 1, MinimumFusedScore: 0.2,
		RerankEnabled: true, RerankProfileVersion: "rerank-v3", RerankModel: "reranker-model", Now: func() time.Time { return createdAt },
	}, repo, nil, reranker)
	if err != nil {
		t.Fatal(err)
	}

	set, err := service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-1", ResourceID: "resource-1", Query: "policy", Limit: 2})
	if err != nil || len(set.Evidence) != 2 || set.Evidence[0].NodeID != "node-first" {
		t.Fatalf("rerank degradation set=%#v err=%v", set, err)
	}
	if set.Evidence[0].Provenance.Rerank.DegradedReason != "reranker_failed" || !hasProcess(set.Process, agentevidence.StageRerank, agentevidence.ProcessDegraded, "") {
		t.Fatalf("rerank degradation provenance=%#v process=%#v", set.Evidence[0].Provenance.Rerank, set.Process)
	}
}

// TestSearchRejectsCrossWorkspaceScopeBeforeRecall 验证对应场景下的正常路径与失败路径。
func TestSearchRejectsCrossWorkspaceScopeBeforeRecall(t *testing.T) {
	repo := &fakeRepository{scope: agentevidence.Scope{
		WorkspaceID: "workspace-b", ResourceID: "resource-1", VersionID: "version-current", SourceType: "upload",
	}}
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, LexicalIndexVersion: "lexical-v1", CandidateLimit: 8,
		FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 1, MinimumFusedScore: 0.2,
		RerankProfileVersion: "rerank-disabled-v1",
	}, repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Search(context.Background(), agentevidence.SearchRequest{WorkspaceID: "workspace-a", ResourceID: "resource-1", Query: "policy", Limit: 3})
	if !errors.Is(err, agentevidence.ErrScopeNotFound) || repo.lexicalCalls != 0 || repo.semanticCalls != 0 {
		t.Fatalf("cross-workspace error=%v lexical=%d semantic=%d", err, repo.lexicalCalls, repo.semanticCalls)
	}
}

type fakeRepository struct {
	scope              agentevidence.Scope
	lexical            []agentevidence.ScoredCandidate
	semantic           []agentevidence.ScoredCandidate
	lexicalErr         error
	semanticErr        error
	vectorType         string
	lexicalCalls       int
	semanticCalls      int
	requestedVersionID string
	requestedHistory   bool
}

// ResolveScope 执行该函数负责的核心处理逻辑。
func (repo *fakeRepository) ResolveScope(_ context.Context, workspaceID, resourceID, versionID string, includeHistory bool) (agentevidence.Scope, error) {
	repo.requestedVersionID = versionID
	repo.requestedHistory = includeHistory
	return repo.scope, nil
}

// EmbeddingVectorType 执行该函数负责的核心处理逻辑。
func (repo *fakeRepository) EmbeddingVectorType(context.Context) (string, error) {
	if repo.vectorType != "" {
		return repo.vectorType, nil
	}
	return "vector(1024)", nil
}

// SearchLexical 执行该函数负责的核心处理逻辑。
func (repo *fakeRepository) SearchLexical(context.Context, agentevidence.Scope, string, int) ([]agentevidence.ScoredCandidate, error) {
	repo.lexicalCalls++
	return append([]agentevidence.ScoredCandidate(nil), repo.lexical...), repo.lexicalErr
}

// SearchSemantic 执行该函数负责的核心处理逻辑。
func (repo *fakeRepository) SearchSemantic(context.Context, agentevidence.Scope, []float32, agentevidence.EmbeddingProfile, int) ([]agentevidence.ScoredCandidate, error) {
	repo.semanticCalls++
	return append([]agentevidence.ScoredCandidate(nil), repo.semantic...), repo.semanticErr
}

type fixedEmbedder struct {
	vector []float32
	err    error
}

type fixedReranker struct {
	results []agentevidence.RerankResult
	err     error
	calls   int
}

// 重排执行该函数负责的核心处理逻辑。
func (reranker *fixedReranker) Rerank(context.Context, string, []string, int) ([]agentevidence.RerankResult, error) {
	reranker.calls++
	return append([]agentevidence.RerankResult(nil), reranker.results...), reranker.err
}

// Embed 执行该函数负责的核心处理逻辑。
func (embedder fixedEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	if embedder.err != nil {
		return nil, embedder.err
	}
	return [][]float32{append([]float32(nil), embedder.vector...)}, nil
}

// hasProcess 执行该函数负责的核心处理逻辑。
func hasProcess(records []agentevidence.ProcessRecord, stage agentevidence.ProcessStage, status agentevidence.ProcessStatus, channel agentevidence.RetrievalChannel) bool {
	for _, record := range records {
		if record.Stage == stage && record.Status == status && record.Channel == channel {
			return true
		}
	}
	return false
}

// 候选结果执行该函数负责的核心处理逻辑。
func candidate(sourceID, nodeID, content string, createdAt time.Time) agentevidence.Candidate {
	return agentevidence.Candidate{SourceID: sourceID, ResourceID: "resource-1", VersionID: "version-current", NodeID: nodeID, SourceType: "document_node", Content: content, CreatedAt: createdAt}
}

// newHybridService 执行该函数负责的核心处理逻辑。
func newHybridService(t *testing.T, repo agentevidence.Repository, embedder agentevidence.Embedder, reranker agentevidence.Reranker, now time.Time) *agentevidence.Service {
	t.Helper()
	service, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, SemanticEnabled: true,
		LexicalIndexVersion: "lexical-v1", SemanticIndexVersion: "hnsw-v1", CandidateLimit: 8,
		FusionAlgorithm: agentevidence.FusionWeightedSum, LexicalWeight: 0.4, VectorWeight: 0.6,
		MinimumFusedScore: 0.2,
		Embedding:         agentevidence.EmbeddingProfile{Version: "embedding-v1", Model: "embed-model", Dimensions: 3, VectorType: "vector(3)"},
		RerankEnabled:     reranker != nil, RerankProfileVersion: "rerank-v1", RerankModel: "reranker-model",
		Now: func() time.Time { return now },
	}, repo, embedder, reranker)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
