package handlers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

func TestSearchResourceMissingQuery(t *testing.T) {
	pool := newHandlerTestPool(t)
	repo := postgres.NewResourceRepo(pool)
	handler := NewResourceHandler(repo, nil)
	engine := server.New()
	engine.GET("/api/resources/:id/search", handler.Search)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/00000000-0000-0000-0000-000000000000/search", nil).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, "query parameter 'q' is required") {
		t.Fatalf("expected missing query error, got %q", body)
	}
}

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

func cleanupResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := testContext(t)
	if _, err := pool.Exec(ctx, `DELETE FROM resources WHERE id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup resource %q: %v", resourceID, err)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func uniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
