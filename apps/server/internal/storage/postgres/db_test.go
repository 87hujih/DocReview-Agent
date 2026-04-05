package postgres

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

func TestMigrationCreatesAllTables(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
	`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	tableNames, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect table names: %v", err)
	}

	expectedTables := []string{
		"resources",
		"resource_versions",
		"resource_chunks",
		"tasks",
		"task_steps",
		"task_artifacts",
		"approvals",
		"execution_jobs",
	}

	for _, expectedTable := range expectedTables {
		if !slices.Contains(tableNames, expectedTable) {
			t.Fatalf("expected table %q to exist, got %v", expectedTable, tableNames)
		}
	}

	var extensionName string
	if err := pool.QueryRow(ctx, `SELECT extname FROM pg_extension WHERE extname = 'vector'`).Scan(&extensionName); err != nil {
		t.Fatalf("query vector extension: %v", err)
	}

	if extensionName != "vector" {
		t.Fatalf("expected vector extension, got %q", extensionName)
	}

	var trigramExtension string
	if err := pool.QueryRow(ctx, `SELECT extname FROM pg_extension WHERE extname = 'pg_trgm'`).Scan(&trigramExtension); err != nil {
		t.Fatalf("query pg_trgm extension: %v", err)
	}

	if trigramExtension != "pg_trgm" {
		t.Fatalf("expected pg_trgm extension, got %q", trigramExtension)
	}
}

func TestResourceCRUD(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, err := repo.Create(ctx, "新员工手册-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	gotResource, err := repo.GetByID(ctx, resource.ID)
	if err != nil {
		t.Fatalf("get resource by id: %v", err)
	}

	if gotResource.Title != resource.Title {
		t.Fatalf("expected title %q, got %q", resource.Title, gotResource.Title)
	}

	version, err := repo.CreateVersion(ctx, resource.ID, 1, "这是第一版文档内容", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	currentVersion, err := repo.GetCurrentVersion(ctx, resource.ID)
	if err != nil {
		t.Fatalf("get current version: %v", err)
	}

	if currentVersion.Content != version.Content {
		t.Fatalf("expected current version content %q, got %q", version.Content, currentVersion.Content)
	}

	chunkEmbedding := testVector(0.25)
	chunk := &ResourceChunk{
		ResourceID:   resource.ID,
		VersionID:    version.ID,
		ChunkIndex:   0,
		SectionTitle: "入职流程",
		Content:      "办理入职时需提交身份证明和银行账户信息。",
		Embedding:    chunkEmbedding,
	}
	if err := repo.CreateChunk(ctx, chunk); err != nil {
		t.Fatalf("create chunk: %v", err)
	}

	chunks, err := repo.SearchChunks(ctx, chunkEmbedding, 1)
	if err != nil {
		t.Fatalf("search chunks: %v", err)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	if chunks[0].ResourceID != resource.ID {
		t.Fatalf("expected chunk resource id %q, got %q", resource.ID, chunks[0].ResourceID)
	}

	filteredChunks, err := repo.SearchChunksByResource(ctx, chunkEmbedding, 1, resource.ID)
	if err != nil {
		t.Fatalf("search chunks by resource: %v", err)
	}

	if len(filteredChunks) != 1 {
		t.Fatalf("expected 1 filtered chunk, got %d", len(filteredChunks))
	}

	if filteredChunks[0].ResourceID != resource.ID {
		t.Fatalf("expected filtered chunk resource id %q, got %q", resource.ID, filteredChunks[0].ResourceID)
	}

	otherResource, err := repo.Create(ctx, "考勤制度-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create other resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, otherResource.ID)
	})

	otherVersion, err := repo.CreateVersion(ctx, otherResource.ID, 1, "这是另一份制度文档", "original")
	if err != nil {
		t.Fatalf("create other version: %v", err)
	}

	attendanceChunk := &ResourceChunk{
		ResourceID:   resource.ID,
		VersionID:    version.ID,
		ChunkIndex:   1,
		SectionTitle: "考勤管理",
		Content:      "员工必须按时完成考勤签到和签退。",
		Embedding:    testVector(0.45),
	}
	if err := repo.CreateChunk(ctx, attendanceChunk); err != nil {
		t.Fatalf("create attendance chunk: %v", err)
	}

	otherChunk := &ResourceChunk{
		ResourceID:   otherResource.ID,
		VersionID:    otherVersion.ID,
		ChunkIndex:   0,
		SectionTitle: "差旅制度",
		Content:      "员工出差前需完成审批。",
		Embedding:    testVector(0.55),
	}
	if err := repo.CreateChunk(ctx, otherChunk); err != nil {
		t.Fatalf("create other chunk: %v", err)
	}

	lexicalChunks, err := repo.SearchChunksLexical(ctx, "考勤", 5)
	if err != nil {
		t.Fatalf("search lexical chunks: %v", err)
	}

	if len(lexicalChunks) == 0 {
		t.Fatal("expected lexical search to return at least one chunk")
	}

	if lexicalChunks[0].ID != attendanceChunk.ID {
		t.Fatalf("expected lexical search to rank attendance chunk first, got %q", lexicalChunks[0].ID)
	}

	filteredLexicalChunks, err := repo.SearchChunksLexicalByResource(ctx, "考勤", 5, resource.ID)
	if err != nil {
		t.Fatalf("search lexical chunks by resource: %v", err)
	}

	if len(filteredLexicalChunks) == 0 {
		t.Fatal("expected lexical search by resource to return at least one chunk")
	}

	for _, chunk := range filteredLexicalChunks {
		if chunk.ResourceID != resource.ID {
			t.Fatalf("expected filtered lexical chunk resource id %q, got %q", resource.ID, chunk.ResourceID)
		}
	}

	resources, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}

	if !containsResource(resources, resource.ID) {
		t.Fatalf("expected resources list to contain %q", resource.ID)
	}
}

func TestTaskAndStepsCRUD(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "任务资源-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	var taskID string
	var taskStatus string
	var taskInstruction string
	if err := pool.QueryRow(ctx, `
		INSERT INTO tasks (resource_id, instruction, status)
		VALUES ($1, $2, $3)
		RETURNING id, instruction, status
	`, resource.ID, "请整理并修订考勤制度", "planning").Scan(&taskID, &taskInstruction, &taskStatus); err != nil {
		t.Fatalf("insert task with SQL: %v", err)
	}

	if taskInstruction != "请整理并修订考勤制度" {
		t.Fatalf("expected instruction %q, got %q", "请整理并修订考勤制度", taskInstruction)
	}

	if taskStatus != "planning" {
		t.Fatalf("expected task status %q, got %q", "planning", taskStatus)
	}

	var stepID string
	var stepName string
	var stepStatus string
	var startedAt time.Time
	if err := pool.QueryRow(ctx, `
		INSERT INTO task_steps (task_id, step_name, status, started_at)
		VALUES ($1, $2, $3, now())
		RETURNING id, step_name, status, started_at
	`, taskID, "planner", "running").Scan(&stepID, &stepName, &stepStatus, &startedAt); err != nil {
		t.Fatalf("insert task step with SQL: %v", err)
	}

	if stepName != "planner" {
		t.Fatalf("expected step name %q, got %q", "planner", stepName)
	}

	if stepStatus != "running" {
		t.Fatalf("expected step status %q, got %q", "running", stepStatus)
	}

	if startedAt.IsZero() {
		t.Fatal("expected started_at to be set")
	}
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := testContext(t)
	databaseURL := testDatabaseURL()

	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return pool
}

func testDatabaseURL() string {
	cfg := appconfig.Load()
	return cfg.DatabaseURL
}

func cleanupResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := testContext(t)
	if _, err := pool.Exec(ctx, `DELETE FROM tasks WHERE resource_id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup tasks for resource %q: %v", resourceID, err)
	}

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

func testVector(seed float32) pgvector.Vector {
	values := make([]float32, 1024)
	for index := range values {
		values[index] = seed + float32(index%7)/10
	}

	return pgvector.NewVector(values)
}

func containsResource(resources []Resource, id string) bool {
	for _, resource := range resources {
		if resource.ID == id {
			return true
		}
	}

	return false
}
