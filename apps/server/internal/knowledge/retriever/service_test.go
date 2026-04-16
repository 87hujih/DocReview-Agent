package retriever

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/ingest"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/pgvector/pgvector-go"
)

func TestShouldRunSearchIntegrationRequiresExplicitOptIn(t *testing.T) {
	cfg := appconfig.Config{
		DatabaseURL:       "postgres://postgres:postgres@127.0.0.1:55432/agent_project?sslmode=disable",
		SiliconFlowAPIKey: "test-key",
	}

	t.Setenv("KNOWLEDGE_RETRIEVER_INTEGRATION", "")

	shouldRun, reason := shouldRunSearchIntegration(cfg)
	if shouldRun {
		t.Fatal("expected integration test to require explicit opt-in")
	}
	if !strings.Contains(reason, "KNOWLEDGE_RETRIEVER_INTEGRATION=1") {
		t.Fatalf("expected opt-in reason, got %q", reason)
	}
}

func TestShouldRunSearchIntegrationAllowsExplicitOptInWithLocalDB(t *testing.T) {
	cfg := appconfig.Config{
		DatabaseURL:       "postgres://postgres:postgres@127.0.0.1:55432/agent_project?sslmode=disable",
		SiliconFlowAPIKey: "test-key",
	}

	t.Setenv("KNOWLEDGE_RETRIEVER_INTEGRATION", "1")
	t.Setenv("ALLOW_NONLOCAL_DB", "")

	shouldRun, reason := shouldRunSearchIntegration(cfg)
	if !shouldRun {
		t.Fatalf("expected integration test to run, got reason %q", reason)
	}
}

func TestShouldRunSearchIntegrationBlocksNonLocalDBWithoutOverride(t *testing.T) {
	cfg := appconfig.Config{
		DatabaseURL:       "postgres://postgres:postgres@10.0.0.8:5432/agent_project?sslmode=disable",
		SiliconFlowAPIKey: "test-key",
	}

	t.Setenv("KNOWLEDGE_RETRIEVER_INTEGRATION", "1")
	t.Setenv("ALLOW_NONLOCAL_DB", "")

	shouldRun, reason := shouldRunSearchIntegration(cfg)
	if shouldRun {
		t.Fatal("expected nonlocal database to require explicit override")
	}
	if !strings.Contains(reason, "ALLOW_NONLOCAL_DB=1") {
		t.Fatalf("expected nonlocal db reason, got %q", reason)
	}
}

// TestBuildCitationsFromChunks 验证检索分块会被映射成稳定的 citation 结构。
func TestBuildCitationsFromChunks(t *testing.T) {
	longContent := strings.Repeat("a", 205)
	chunks := []postgres.ResourceChunk{
		{ResourceID: "res-1", SectionTitle: "Section 1", Content: "alpha"},
		{ResourceID: "res-2", SectionTitle: "Section 2", Content: longContent},
		{ResourceID: "res-3", SectionTitle: "Section 3", Content: "gamma"},
	}

	citations := citation.BuildFromChunks(chunks)

	if len(citations) != 3 {
		t.Fatalf("expected 3 citations, got %d", len(citations))
	}

	expectedIDs := []string{"cite_1", "cite_2", "cite_3"}
	for index, citationItem := range citations {
		if citationItem.CitationID != expectedIDs[index] {
			t.Fatalf("expected citation id %q, got %q", expectedIDs[index], citationItem.CitationID)
		}
	}

	if citations[1].Snippet != longContent[:200]+"..." {
		t.Fatalf("expected truncated snippet, got %q", citations[1].Snippet)
	}
}

func TestMergeUniqueChunks(t *testing.T) {
	semantic := []postgres.ResourceChunk{
		{ID: "chunk-1", Content: "alpha"},
		{ID: "chunk-2", Content: "beta"},
	}
	lexical := []postgres.ResourceChunk{
		{ID: "chunk-2", Content: "beta"},
		{ID: "chunk-3", Content: "gamma"},
	}

	merged := mergeUniqueChunks(semantic, lexical)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged chunks, got %d", len(merged))
	}

	expectedIDs := []string{"chunk-1", "chunk-2", "chunk-3"}
	for index, chunk := range merged {
		if chunk.ID != expectedIDs[index] {
			t.Fatalf("expected merged chunk %d to be %q, got %q", index, expectedIDs[index], chunk.ID)
		}
	}
}

func TestRerankResultsToChunks(t *testing.T) {
	chunks := []postgres.ResourceChunk{
		{ID: "chunk-1", Content: "alpha"},
		{ID: "chunk-2", Content: "beta"},
		{ID: "chunk-3", Content: "gamma"},
	}
	results := []reranker.Result{
		{Index: 2, RelevanceScore: 0.91},
		{Index: 0, RelevanceScore: 0.52},
		{Index: 99, RelevanceScore: 0.01},
	}

	ranked := rerankResultsToChunks(chunks, results, 2)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked chunks, got %d", len(ranked))
	}

	if ranked[0].ID != "chunk-3" {
		t.Fatalf("expected first ranked chunk %q, got %q", "chunk-3", ranked[0].ID)
	}

	if ranked[1].ID != "chunk-1" {
		t.Fatalf("expected second ranked chunk %q, got %q", "chunk-1", ranked[1].ID)
	}
}

func TestSearchByResourceUsesCurrentVersionOnly(t *testing.T) {
	repo := &fakeRetrieverRepo{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-current",
			ResourceID: "resource-1",
		},
		semanticByVersion: []postgres.ResourceChunk{
			{
				ID:           "chunk-current",
				ResourceID:   "resource-1",
				VersionID:    "version-current",
				SectionTitle: "考勤管理",
				Content:      "新版本考勤条款",
			},
		},
		lexicalByVersion: []postgres.ResourceChunk{
			{
				ID:           "chunk-current",
				ResourceID:   "resource-1",
				VersionID:    "version-current",
				SectionTitle: "考勤管理",
				Content:      "新版本考勤条款",
			},
		},
	}
	service := NewService(repo, fakeRetrieverEmbedder{}, fakeRetrieverReranker{
		results: []reranker.Result{
			{Index: 0, RelevanceScore: 0.99},
		},
	})

	citations, err := service.SearchByResource(context.Background(), "resource-1", "考勤", 3)
	if err != nil {
		t.Fatalf("search by resource: %v", err)
	}

	if repo.currentVersionLookups != 1 {
		t.Fatalf("expected exactly 1 current version lookup, got %d", repo.currentVersionLookups)
	}
	if repo.lastVersionID != "version-current" {
		t.Fatalf("expected version-scoped search to use %q, got %q", "version-current", repo.lastVersionID)
	}
	if repo.searchByResourceCalls != 0 {
		t.Fatalf("expected resource-scoped search not to be used, got %d calls", repo.searchByResourceCalls)
	}
	if len(citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(citations))
	}
	if !strings.Contains(citations[0].Snippet, "新版本") {
		t.Fatalf("expected citation from current version, got %q", citations[0].Snippet)
	}
}

// TestSearchIntegration 验证导入文档后能够通过真实 embedding、reranker 和数据库完成一次检索。
func TestSearchIntegration(t *testing.T) {
	cfg := appconfig.Load()
	if shouldRun, reason := shouldRunSearchIntegration(cfg); !shouldRun {
		t.Skip(reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "knowledge_retriever", postgres.NewPool, postgres.RunMigrations)

	repo := postgres.NewResourceRepo(pool)
	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}
	rerankerClient := reranker.New(cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.RerankerModel)

	tempDir := t.TempDir()
	markdownPath := filepath.Join(tempDir, "attendance-policy.md")
	title := fmt.Sprintf("Attendance Policy %d", time.Now().UnixNano())
	markdown := strings.Join([]string{
		"# " + title,
		"",
		"## 考勤管理",
		"员工应按时完成签到、签退和请假审批。",
	}, "\n")
	if err := os.WriteFile(markdownPath, []byte(markdown), 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	ingestService := ingest.NewService(repo, emb)
	if err := ingestService.ImportDirectory(ctx, tempDir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	resources, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}

	var resourceID string
	for _, resource := range resources {
		if resource.Title == title {
			resourceID = resource.ID
			break
		}
	}

	if resourceID == "" {
		t.Fatal("expected imported resource to be available")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM resources WHERE id = $1`, resourceID); err != nil {
			t.Fatalf("cleanup imported resource: %v", err)
		}
	})

	retrieverService := NewService(repo, emb, rerankerClient)
	citations, err := retrieverService.SearchByResource(ctx, resourceID, "考勤", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(citations) == 0 {
		t.Fatal("expected at least one citation")
	}
}

func shouldRunSearchIntegration(cfg appconfig.Config) (bool, string) {
	if strings.TrimSpace(os.Getenv("KNOWLEDGE_RETRIEVER_INTEGRATION")) != "1" {
		return false, "skipping: integration test requires KNOWLEDGE_RETRIEVER_INTEGRATION=1"
	}

	if strings.TrimSpace(cfg.SiliconFlowAPIKey) == "" {
		return false, "skipping: SILICONFLOW_API_KEY not configured"
	}

	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return false, "skipping: DATABASE_URL not configured"
	}

	if !isLocalDatabaseHost(cfg.DatabaseURL) && strings.TrimSpace(os.Getenv("ALLOW_NONLOCAL_DB")) != "1" {
		return false, "skipping: nonlocal database requires ALLOW_NONLOCAL_DB=1"
	}

	return true, ""
}

func isLocalDatabaseHost(databaseURL string) bool {
	lowerURL := strings.ToLower(strings.TrimSpace(databaseURL))
	switch {
	case strings.Contains(lowerURL, "@127.0.0.1:"),
		strings.Contains(lowerURL, "@localhost:"),
		strings.Contains(lowerURL, "@[::1]:"):
		return true
	default:
		return false
	}
}

type fakeRetrieverRepo struct {
	currentVersion        *postgres.ResourceVersion
	semanticByVersion     []postgres.ResourceChunk
	lexicalByVersion      []postgres.ResourceChunk
	currentVersionLookups int
	lastVersionID         string
	searchByResourceCalls int
}

func (r *fakeRetrieverRepo) GetCurrentVersion(context.Context, string) (*postgres.ResourceVersion, error) {
	r.currentVersionLookups++
	return r.currentVersion, nil
}

func (r *fakeRetrieverRepo) SearchChunks(context.Context, pgvector.Vector, int) ([]postgres.ResourceChunk, error) {
	return nil, nil
}

func (r *fakeRetrieverRepo) SearchChunksLexical(context.Context, string, int) ([]postgres.ResourceChunk, error) {
	return nil, nil
}

func (r *fakeRetrieverRepo) SearchChunksByResource(context.Context, pgvector.Vector, int, string) ([]postgres.ResourceChunk, error) {
	r.searchByResourceCalls++
	return nil, nil
}

func (r *fakeRetrieverRepo) SearchChunksLexicalByResource(context.Context, string, int, string) ([]postgres.ResourceChunk, error) {
	r.searchByResourceCalls++
	return nil, nil
}

func (r *fakeRetrieverRepo) SearchChunksByVersion(_ context.Context, _ pgvector.Vector, _ int, versionID string) ([]postgres.ResourceChunk, error) {
	r.lastVersionID = versionID
	return r.semanticByVersion, nil
}

func (r *fakeRetrieverRepo) SearchChunksLexicalByVersion(_ context.Context, _ string, _ int, versionID string) ([]postgres.ResourceChunk, error) {
	r.lastVersionID = versionID
	return r.lexicalByVersion, nil
}

type fakeRetrieverEmbedder struct{}

func (fakeRetrieverEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	values := make([]float32, 1024)
	for index := range values {
		values[index] = 0.4 + float32(index%5)/10
	}

	return [][]float32{values}, nil
}

type fakeRetrieverReranker struct {
	results []reranker.Result
}

func (r fakeRetrieverReranker) Rerank(context.Context, string, []string, int) ([]reranker.Result, error) {
	return r.results, nil
}
