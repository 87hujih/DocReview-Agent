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

func TestSearchByResourceUsesTargetResolutionForProjectSections(t *testing.T) {
	repo := &fakeRetrieverRepo{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-grounded",
			ResourceID: "resource-1",
		},
		sectionsByVersion: []postgres.ResourceSection{
			{
				ID:                  "section-campushub",
				ResourceID:          "resource-1",
				VersionID:           "version-grounded",
				SectionKey:          "project-1",
				SectionType:         "project",
				SectionOrder:        1,
				Title:               "CampusHub校园活动平台",
				CanonicalEntityName: optionalRetrieverString("CampusHub"),
				AliasesJSON:         []byte(`["CampusHub","CampusHub校园活动平台"]`),
				Summary:             "面向校园活动的统一平台",
				Content:             "负责活动发布、报名与签到全流程。",
				MetadataJSON:        []byte(`{"tech_stack":["Go","Redis","gRPC"]}`),
			},
			{
				ID:                  "section-shopflow",
				ResourceID:          "resource-1",
				VersionID:           "version-grounded",
				SectionKey:          "project-2",
				SectionType:         "project",
				SectionOrder:        2,
				Title:               "ShopFlow电商后台",
				CanonicalEntityName: optionalRetrieverString("ShopFlow"),
				AliasesJSON:         []byte(`["ShopFlow","ShopFlow电商后台"]`),
				Summary:             "订单与库存管理后台",
				Content:             "负责库存同步与售后流程治理。",
				MetadataJSON:        []byte(`{"tech_stack":["Java","MySQL"]}`),
			},
		},
		chunksByVersion: []postgres.ResourceChunk{
			{
				ID:             "chunk-project-1-summary",
				ResourceID:     "resource-1",
				VersionID:      "version-grounded",
				SectionTitle:   "CampusHub校园活动平台",
				Content:        "CampusHub校园活动平台\n面向校园活动的统一平台",
				SectionID:      optionalRetrieverString("section-campushub"),
				SectionType:    optionalRetrieverString("project"),
				ChunkRole:      optionalRetrieverString("section_summary"),
				WindowGroupID:  optionalRetrieverString("project-1"),
				OrderInSection: optionalRetrieverInt(1),
			},
			{
				ID:             "chunk-project-1-work",
				ResourceID:     "resource-1",
				VersionID:      "version-grounded",
				SectionTitle:   "CampusHub校园活动平台",
				Content:        "负责活动发布、报名与签到全流程。",
				SectionID:      optionalRetrieverString("section-campushub"),
				SectionType:    optionalRetrieverString("project"),
				ChunkRole:      optionalRetrieverString("project_work"),
				WindowGroupID:  optionalRetrieverString("project-1"),
				OrderInSection: optionalRetrieverInt(5),
			},
			{
				ID:             "chunk-project-1-tech",
				ResourceID:     "resource-1",
				VersionID:      "version-grounded",
				SectionTitle:   "CampusHub校园活动平台",
				Content:        "Go Redis gRPC",
				SectionID:      optionalRetrieverString("section-campushub"),
				SectionType:    optionalRetrieverString("project"),
				ChunkRole:      optionalRetrieverString("tech_stack"),
				WindowGroupID:  optionalRetrieverString("project-1"),
				OrderInSection: optionalRetrieverInt(3),
			},
			{
				ID:             "chunk-project-2-summary",
				ResourceID:     "resource-1",
				VersionID:      "version-grounded",
				SectionTitle:   "ShopFlow电商后台",
				Content:        "ShopFlow电商后台\n订单与库存管理后台",
				SectionID:      optionalRetrieverString("section-shopflow"),
				SectionType:    optionalRetrieverString("project"),
				ChunkRole:      optionalRetrieverString("section_summary"),
				WindowGroupID:  optionalRetrieverString("project-2"),
				OrderInSection: optionalRetrieverInt(1),
			},
			{
				ID:             "chunk-project-2-work",
				ResourceID:     "resource-1",
				VersionID:      "version-grounded",
				SectionTitle:   "ShopFlow电商后台",
				Content:        "负责库存同步与售后流程治理。",
				SectionID:      optionalRetrieverString("section-shopflow"),
				SectionType:    optionalRetrieverString("project"),
				ChunkRole:      optionalRetrieverString("project_work"),
				WindowGroupID:  optionalRetrieverString("project-2"),
				OrderInSection: optionalRetrieverInt(5),
			},
			{
				ID:             "chunk-project-2-tech",
				ResourceID:     "resource-1",
				VersionID:      "version-grounded",
				SectionTitle:   "ShopFlow电商后台",
				Content:        "Java MySQL",
				SectionID:      optionalRetrieverString("section-shopflow"),
				SectionType:    optionalRetrieverString("project"),
				ChunkRole:      optionalRetrieverString("tech_stack"),
				WindowGroupID:  optionalRetrieverString("project-2"),
				OrderInSection: optionalRetrieverInt(3),
			},
		},
	}
	service := NewService(repo, fakeRetrieverEmbedder{}, nil)

	t.Run("list sections", func(t *testing.T) {
		citations, err := service.SearchByResource(context.Background(), "resource-1", "有哪些项目", 3)
		if err != nil {
			t.Fatalf("search by resource: %v", err)
		}
		if len(citations) != 2 {
			t.Fatalf("expected 2 citations, got %d", len(citations))
		}
		if citations[0].SectionID != "section-campushub" || citations[1].SectionID != "section-shopflow" {
			t.Fatalf("expected ordered section citations, got %#v", citations)
		}
	})

	t.Run("detail by entity", func(t *testing.T) {
		citations, err := service.SearchByResource(context.Background(), "resource-1", "CampusHub 做了什么", 3)
		if err != nil {
			t.Fatalf("search by resource: %v", err)
		}
		if len(citations) != 1 {
			t.Fatalf("expected 1 citation, got %d", len(citations))
		}
		if citations[0].SectionID != "section-campushub" {
			t.Fatalf("expected CampusHub section citation, got %#v", citations[0])
		}
		if citations[0].Window == nil || citations[0].Window.GroupID != "project-1" {
			t.Fatalf("expected citation window to merge project-1, got %#v", citations[0].Window)
		}
		if !strings.Contains(citations[0].Snippet, "活动发布") {
			t.Fatalf("expected merged section evidence, got %q", citations[0].Snippet)
		}
	})

	t.Run("detail by ordinal", func(t *testing.T) {
		citations, err := service.SearchByResource(context.Background(), "resource-1", "第一个项目做了什么", 3)
		if err != nil {
			t.Fatalf("search by resource: %v", err)
		}
		if len(citations) != 1 || citations[0].SectionID != "section-campushub" {
			t.Fatalf("expected first project citation, got %#v", citations)
		}
	})

	t.Run("aggregate tech stack", func(t *testing.T) {
		citations, err := service.SearchByResource(context.Background(), "resource-1", "用了哪些技术栈", 3)
		if err != nil {
			t.Fatalf("search by resource: %v", err)
		}
		if len(citations) != 2 {
			t.Fatalf("expected 2 citations, got %d", len(citations))
		}
		if !strings.Contains(citations[0].Snippet, "Go") || !strings.Contains(citations[1].Snippet, "Java") {
			t.Fatalf("expected tech stack snippets, got %#v", citations)
		}
	})

	if repo.currentVersionLookups != 4 {
		t.Fatalf("expected 4 current version lookups, got %d", repo.currentVersionLookups)
	}
	if repo.listSectionsCalls != 4 {
		t.Fatalf("expected 4 section lookups, got %d", repo.listSectionsCalls)
	}
	if repo.listChunksCalls != 3 {
		t.Fatalf("expected 3 chunk lookups for detail/aggregate paths, got %d", repo.listChunksCalls)
	}
	if repo.searchByVersionCalls != 0 {
		t.Fatalf("expected target-first retrieval to avoid legacy search, got %d calls", repo.searchByVersionCalls)
	}
}

func TestSearchByResourceFallsBackToLegacyChunks(t *testing.T) {
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
				Content:      "旧资源没有 sections，只能走 legacy chunk 检索。",
			},
		},
		lexicalByVersion: []postgres.ResourceChunk{
			{
				ID:           "chunk-current",
				ResourceID:   "resource-1",
				VersionID:    "version-current",
				SectionTitle: "考勤管理",
				Content:      "旧资源没有 sections，只能走 legacy chunk 检索。",
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
	if len(citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(citations))
	}
	if repo.searchByVersionCalls == 0 {
		t.Fatal("expected legacy version-scoped search to be used as fallback")
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
	sectionsByVersion     []postgres.ResourceSection
	chunksByVersion       []postgres.ResourceChunk
	semanticByVersion     []postgres.ResourceChunk
	lexicalByVersion      []postgres.ResourceChunk
	currentVersionLookups int
	listSectionsCalls     int
	listChunksCalls       int
	searchByVersionCalls  int
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
	r.searchByVersionCalls++
	r.lastVersionID = versionID
	return r.semanticByVersion, nil
}

func (r *fakeRetrieverRepo) SearchChunksLexicalByVersion(_ context.Context, _ string, _ int, versionID string) ([]postgres.ResourceChunk, error) {
	r.searchByVersionCalls++
	r.lastVersionID = versionID
	return r.lexicalByVersion, nil
}

func (r *fakeRetrieverRepo) ListSectionsByVersion(_ context.Context, versionID string) ([]postgres.ResourceSection, error) {
	r.listSectionsCalls++
	r.lastVersionID = versionID
	return append([]postgres.ResourceSection(nil), r.sectionsByVersion...), nil
}

func (r *fakeRetrieverRepo) ListChunksByVersion(_ context.Context, versionID string) ([]postgres.ResourceChunk, error) {
	r.listChunksCalls++
	r.lastVersionID = versionID
	return append([]postgres.ResourceChunk(nil), r.chunksByVersion...), nil
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

func optionalRetrieverString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func optionalRetrieverInt(value int) *int {
	if value <= 0 {
		return nil
	}

	return &value
}
