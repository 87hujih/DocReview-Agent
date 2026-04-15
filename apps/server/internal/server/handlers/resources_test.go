package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/testsupport/postgrescleanup"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestListResourcesHandler 验证资源列表接口会返回刚创建的资源。
func TestListResourcesHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, nil)
	engine := server.New()
	engine.GET("/api/resources", handler.List)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "资源列表测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, resource.Title) {
		t.Fatalf("expected body to contain resource title %q, got %q", resource.Title, body)
	}
}

// TestGetResourceByIDHandler 验证资源详情接口会包含当前版本信息。
func TestGetResourceByIDHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, nil)
	engine := server.New()
	engine.GET("/api/resources/:id", handler.GetByID)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "资源详情测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	if _, err := repo.CreateVersion(ctx, resource.ID, 1, "版本内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resource.ID, nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, "\"current_version\"") {
		t.Fatalf("expected body to contain current_version, got %q", body)
	}
}

// TestGetResourceNotFound 验证查询不存在的资源会返回 404。
func TestGetResourceNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, nil)
	engine := server.New()
	engine.GET("/api/resources/:id", handler.GetByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/00000000-0000-0000-0000-000000000000", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestExportCurrentResourceVersionHandler(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, nil)
	engine := server.New()
	engine.GET("/api/resources/:id/export", handler.ExportCurrentVersion)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "修订结果导出-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	if _, err := repo.CreateVersion(ctx, resource.ID, 1, "旧版本", "original"); err != nil {
		t.Fatalf("create original version: %v", err)
	}
	if _, err := repo.CreateVersion(ctx, resource.ID, 2, "# 修订结果\n最终内容", "task_revision"); err != nil {
		t.Fatalf("create revised version: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resource.ID+"/export", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
	if contentType := string(response.Header.Peek("Content-Type")); !strings.Contains(contentType, "text/markdown") {
		t.Fatalf("expected markdown content type, got %q", contentType)
	}
	if disposition := string(response.Header.Peek("Content-Disposition")); !strings.Contains(disposition, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", disposition)
	}
	if body := string(response.Body()); body != "# 修订结果\n最终内容" {
		t.Fatalf("expected current version body, got %q", body)
	}
}

func TestExportCurrentResourceVersionHandlerWithoutCurrentVersion(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, nil)
	engine := server.New()
	engine.GET("/api/resources/:id/export", handler.ExportCurrentVersion)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "无版本资源-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resource.ID+"/export", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
	if body := string(response.Body()); !strings.Contains(body, "资源没有可导出的当前版本") {
		t.Fatalf("expected missing version error, got %q", body)
	}
}

func TestSearchResourceMissingQuery(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, fakeResourceSearchService{})
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/00000000-0000-0000-0000-000000000000/search", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, "查询参数 q 不能为空") {
		t.Fatalf("expected missing query error, got %q", body)
	}
}

func TestSearchResourceInvalidID(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, fakeResourceSearchService{})
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/not-a-uuid/search?q=考勤", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if body := string(response.Body()); !strings.Contains(body, "资源 ID 非法") {
		t.Fatalf("expected invalid id error, got %q", body)
	}
}

func TestSearchResourceNotFound(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, fakeResourceSearchService{})
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/00000000-0000-0000-0000-000000000000/search?q=考勤", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestSearchResourceMissingCurrentVersion(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, fakeResourceSearchService{})
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "缺少当前版本搜索测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resource.ID+"/search?q=考勤", nil).Result()

	if response.StatusCode() != consts.StatusConflict {
		t.Fatalf("expected status %d, got %d", consts.StatusConflict, response.StatusCode())
	}
	if body := string(response.Body()); !strings.Contains(body, "资源当前版本不存在，无法检索") {
		t.Fatalf("expected missing current version error, got %q", body)
	}
}

func TestSearchResourceSuccess(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, fakeResourceSearchService{
		citations: []citation.Citation{
			{
				CitationID:   "cite_1",
				SectionTitle: "考勤管理",
				Snippet:      "新版本考勤条款",
			},
		},
	})
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "资源搜索成功测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})
	if _, err := repo.CreateVersion(ctx, resource.ID, 1, "## 考勤管理\n新版本考勤条款", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resource.ID+"/search?q=考勤", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
	body := string(response.Body())
	if !strings.Contains(body, "\"citations\"") || !strings.Contains(body, "新版本考勤条款") {
		t.Fatalf("expected citations in response, got %q", body)
	}
}

func TestSearchResourceNoHits(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, fakeResourceSearchService{})
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	ctx := testContext(t)
	resource, err := repo.Create(ctx, "资源搜索空结果测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})
	if _, err := repo.CreateVersion(ctx, resource.ID, 1, "## 考勤管理\n当前版本没有命中内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resource.ID+"/search?q=考勤", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
	if body := string(response.Body()); !strings.Contains(body, "\"citations\":[]") {
		t.Fatalf("expected empty citations, got %q", body)
	}
}

func TestNewSearchResourcesResponseEncodesNilCitationsAsEmptyArray(t *testing.T) {
	payload, err := json.Marshal(newSearchResourcesResponse("考勤", nil))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	if body := string(payload); !strings.Contains(body, "\"citations\":[]") {
		t.Fatalf("expected empty citations, got %q", body)
	}
}

// newHandlerTestPool 为 handler 测试创建已完成迁移的数据库连接池。
func newHandlerTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := testContext(t)
	cfg := appconfig.Load()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("database not available: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return pool
}

// cleanupResource 清理 handler 测试中创建的资源数据。
func cleanupResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := testContext(t)
	if err := postgrescleanup.CleanupResourceTree(ctx, pool, resourceID); err != nil {
		t.Fatalf("cleanup resource tree for resource %q: %v", resourceID, err)
	}
}

// testContext 为 handler 测试创建带超时的上下文。
func testContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// uniqueSuffix 生成测试数据使用的唯一后缀。
func uniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

type fakeResourceSearchService struct {
	citations []citation.Citation
	err       error
}

func (f fakeResourceSearchService) SearchByResource(context.Context, string, string, int) ([]citation.Citation, error) {
	return f.citations, f.err
}
