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
)

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

// TestSearchIntegration 验证导入文档后能够通过真实 embedding、reranker 和数据库完成一次检索。
func TestSearchIntegration(t *testing.T) {
	cfg := appconfig.Load()
	if strings.TrimSpace(cfg.SiliconFlowAPIKey) == "" {
		t.Skip("skipping: SILICONFLOW_API_KEY not configured")
	}

	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		t.Skip("skipping: DATABASE_URL not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("skipping: database not available: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

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
