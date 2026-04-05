package retriever

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/ingest"
	"agent_project/apps/server/internal/storage/postgres"
)

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

func TestSearchIntegration(t *testing.T) {
	if strings.TrimSpace(os.Getenv("SILICONFLOW_API_KEY")) == "" {
		t.Skip("skipping: SILICONFLOW_API_KEY not set")
	}

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("skipping: DATABASE_URL not set for remote integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := appconfig.Load()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("skipping: database not available: %v", err)
	}
	defer pool.Close()

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	repo := postgres.NewResourceRepo(pool)
	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}

	tempDir := t.TempDir()
	markdownPath := filepath.Join(tempDir, "attendance-policy.md")
	markdown := strings.Join([]string{
		"# Attendance Policy",
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

	retrieverService := NewService(repo, emb)
	citations, err := retrieverService.Search(ctx, "考勤", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(citations) == 0 {
		t.Fatal("expected at least one citation")
	}
}
