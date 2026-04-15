package postgres

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// TestMigrationCreatesAllTables 验证迁移会创建所需表和 vector 扩展。
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
		"assistant_sessions",
		"assistant_messages",
		"tasks",
		"task_steps",
		"task_artifacts",
		"task_events",
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

func TestMigrationCreatesTaskQueryIndexes(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = 'public'
		  AND indexname = ANY($1)
	`, []string{
		"idx_tasks_created_at_id",
		"idx_task_steps_task_created_id",
		"idx_task_artifacts_task_created_id",
	})
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer rows.Close()

	indexNames, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect indexes: %v", err)
	}

	expectedIndexes := []string{
		"idx_tasks_created_at_id",
		"idx_task_steps_task_created_id",
		"idx_task_artifacts_task_created_id",
	}
	for _, expectedIndex := range expectedIndexes {
		if !slices.Contains(indexNames, expectedIndex) {
			t.Fatalf("expected index %q to exist, got %v", expectedIndex, indexNames)
		}
	}
}

// TestRunMigrationsWaitsForAdvisoryLock 验证迁移会等待数据库级 advisory lock，避免并发执行。
func TestRunMigrationsWaitsForAdvisoryLock(t *testing.T) {
	pool := newRawTestPool(t)
	lockPool := newRawTestPool(t)

	lockCtx := testContext(t)
	lockConn, err := lockPool.Acquire(lockCtx)
	if err != nil {
		t.Fatalf("acquire lock connection: %v", err)
	}

	if _, err := lockConn.Exec(lockCtx, `SELECT pg_advisory_lock($1, $2)`, migrationLockNamespace, migrationLockKey); err != nil {
		t.Fatalf("acquire advisory lock: %v", err)
	}
	t.Cleanup(func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := lockConn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1, $2)`, migrationLockNamespace, migrationLockKey); err != nil {
			t.Fatalf("release advisory lock: %v", err)
		}

		lockConn.Release()
	})

	blockedCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = RunMigrations(blockedCtx, pool)
	if err == nil {
		t.Fatal("expected run migrations to wait for advisory lock and time out")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected migration lock wait to end with context deadline, got %v", err)
	}
}

// TestResourceCRUD 验证资源、版本和分块的基本读写流程。
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

func TestCreateDocumentGraphRollsBackOnChunkFailure(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	method := reflect.ValueOf(repo).MethodByName("CreateDocumentGraph")
	if !method.IsValid() {
		t.Fatal("expected resource repo to expose CreateDocumentGraph")
	}

	paramsType := method.Type().In(1)
	paramsValue := reflect.New(paramsType).Elem()
	title := "事务回滚测试-" + uniqueSuffix()

	paramsValue.FieldByName("Title").SetString(title)
	paramsValue.FieldByName("SourceType").SetString("upload")
	paramsValue.FieldByName("VersionSource").SetString("assistant_upload")
	paramsValue.FieldByName("Content").SetString("# 标题\n正文")

	chunksField := paramsValue.FieldByName("Chunks")
	chunkType := chunksField.Type().Elem()
	chunksValue := reflect.MakeSlice(chunksField.Type(), 2, 2)

	firstChunk := reflect.New(chunkType).Elem()
	firstChunk.FieldByName("ChunkIndex").SetInt(0)
	firstChunk.FieldByName("SectionTitle").SetString("标题")
	firstChunk.FieldByName("Content").SetString("第一段")
	firstChunk.FieldByName("Embedding").Set(reflect.ValueOf(testVector(0.31)))
	chunksValue.Index(0).Set(firstChunk)

	secondChunk := reflect.New(chunkType).Elem()
	secondChunk.FieldByName("ChunkIndex").SetInt(1)
	secondChunk.FieldByName("SectionTitle").SetString("标题")
	secondChunk.FieldByName("Content").SetString("第二段")
	secondChunk.FieldByName("Embedding").Set(reflect.ValueOf(pgvector.NewVector([]float32{0.1, 0.2})))
	chunksValue.Index(1).Set(secondChunk)

	chunksField.Set(chunksValue)

	results := method.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		paramsValue,
	})
	if len(results) != 3 {
		t.Fatalf("expected 3 return values, got %d", len(results))
	}

	if results[2].IsNil() {
		t.Fatal("expected chunk write failure to return error")
	}

	var resourceCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM resources
		WHERE title = $1
	`, title).Scan(&resourceCount); err != nil {
		t.Fatalf("count resources after rollback: %v", err)
	}

	if resourceCount != 0 {
		t.Fatalf("expected rollback to leave 0 resources, got %d", resourceCount)
	}

	var versionCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM resource_versions
		WHERE content = $1
	`, "# 标题\n正文").Scan(&versionCount); err != nil {
		t.Fatalf("count versions after rollback: %v", err)
	}

	if versionCount != 0 {
		t.Fatalf("expected rollback to leave 0 versions, got %d", versionCount)
	}
}

// TestTaskAndStepsCRUD 验证任务与任务步骤相关表可正常写入。
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

func TestCountChunksByVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, repo, ctx, "版本分块统计测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	count, err := repo.CountChunksByVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("count chunks before insert: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 chunks before insert, got %d", count)
	}

	if err := repo.CreateChunk(ctx, &ResourceChunk{
		ResourceID:   resource.ID,
		VersionID:    version.ID,
		ChunkIndex:   0,
		SectionTitle: "考勤管理",
		Content:      "员工必须在九点前完成签到。",
		Embedding:    testVector(0.42),
	}); err != nil {
		t.Fatalf("create chunk: %v", err)
	}

	count, err = repo.CountChunksByVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("count chunks after insert: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 chunk after insert, got %d", count)
	}
}

func TestReplaceVersionChunksIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, repo, ctx, "版本分块替换测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	if err := repo.CreateChunk(ctx, &ResourceChunk{
		ResourceID:   resource.ID,
		VersionID:    version.ID,
		ChunkIndex:   0,
		SectionTitle: "旧章节",
		Content:      "旧版本正文",
		Embedding:    testVector(0.11),
	}); err != nil {
		t.Fatalf("seed old chunk: %v", err)
	}

	inputs := []ResourceChunkInput{
		{
			ChunkIndex:   0,
			SectionTitle: "考勤管理",
			Content:      "第一版正文",
			Embedding:    testVector(0.21),
		},
	}

	if err := repo.ReplaceVersionChunks(ctx, version.ID, resource.ID, inputs); err != nil {
		t.Fatalf("replace version chunks first run: %v", err)
	}
	if err := repo.ReplaceVersionChunks(ctx, version.ID, resource.ID, inputs); err != nil {
		t.Fatalf("replace version chunks second run: %v", err)
	}

	count, err := repo.CountChunksByVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("count chunks after replace: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 chunk after repeated replace, got %d", count)
	}

	rows, err := pool.Query(ctx, `
		SELECT section_title, content, chunk_index
		FROM resource_chunks
		WHERE version_id = $1
	`, version.ID)
	if err != nil {
		t.Fatalf("query version chunks: %v", err)
	}
	defer rows.Close()

	type storedChunk struct {
		sectionTitle string
		content      string
		chunkIndex   int
	}

	storedChunks := make([]storedChunk, 0)
	for rows.Next() {
		var chunk storedChunk
		if err := rows.Scan(&chunk.sectionTitle, &chunk.content, &chunk.chunkIndex); err != nil {
			t.Fatalf("scan stored chunk: %v", err)
		}

		storedChunks = append(storedChunks, chunk)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored chunks: %v", err)
	}

	if len(storedChunks) != 1 {
		t.Fatalf("expected exactly 1 stored chunk row, got %d", len(storedChunks))
	}
	if storedChunks[0].sectionTitle != "考勤管理" {
		t.Fatalf("expected section title %q, got %q", "考勤管理", storedChunks[0].sectionTitle)
	}
	if storedChunks[0].content != "第一版正文" {
		t.Fatalf("expected content %q, got %q", "第一版正文", storedChunks[0].content)
	}
	if storedChunks[0].chunkIndex != 0 {
		t.Fatalf("expected chunk index 0, got %d", storedChunks[0].chunkIndex)
	}
}

func TestSearchChunksByVersionExcludesOlderVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, oldVersion := seedResourceVersion(t, repo, ctx, "版本语义检索测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	newVersion, err := repo.CreateVersion(ctx, resource.ID, 2, "## 考勤管理\n新版本考勤条款", "agent_edit")
	if err != nil {
		t.Fatalf("create new version: %v", err)
	}

	seedVersionChunk(t, repo, ctx, resource.ID, oldVersion.ID, 0, "考勤管理", "旧版本考勤条款", testVector(0.21))
	seedVersionChunk(t, repo, ctx, resource.ID, newVersion.ID, 0, "考勤管理", "新版本考勤条款", testVector(0.41))

	chunks, err := repo.SearchChunksByVersion(ctx, testVector(0.41), 5, newVersion.ID)
	if err != nil {
		t.Fatalf("search chunks by version: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk from current version, got %d", len(chunks))
	}
	if chunks[0].VersionID != newVersion.ID {
		t.Fatalf("expected version id %q, got %q", newVersion.ID, chunks[0].VersionID)
	}
	if chunks[0].Content != "新版本考勤条款" {
		t.Fatalf("expected current version content %q, got %q", "新版本考勤条款", chunks[0].Content)
	}
}

func TestSearchChunksLexicalByVersionExcludesOlderVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, oldVersion := seedResourceVersion(t, repo, ctx, "版本词法检索测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	newVersion, err := repo.CreateVersion(ctx, resource.ID, 2, "## 考勤管理\n新版本考勤条款", "agent_edit")
	if err != nil {
		t.Fatalf("create new version: %v", err)
	}

	seedVersionChunk(t, repo, ctx, resource.ID, oldVersion.ID, 0, "考勤管理", "旧版本考勤条款", testVector(0.22))
	seedVersionChunk(t, repo, ctx, resource.ID, newVersion.ID, 0, "考勤管理", "新版本考勤条款", testVector(0.42))

	chunks, err := repo.SearchChunksLexicalByVersion(ctx, "考勤", 5, newVersion.ID)
	if err != nil {
		t.Fatalf("search lexical chunks by version: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 lexical chunk from current version, got %d", len(chunks))
	}
	if chunks[0].VersionID != newVersion.ID {
		t.Fatalf("expected lexical version id %q, got %q", newVersion.ID, chunks[0].VersionID)
	}
	if chunks[0].Content != "新版本考勤条款" {
		t.Fatalf("expected lexical content %q, got %q", "新版本考勤条款", chunks[0].Content)
	}
}

// newTestPool 为 PostgreSQL 存储测试创建可复用连接池并执行迁移。
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

// newRawTestPool 为需要自行控制迁移时机的测试创建数据库连接池。
func newRawTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := testContext(t)
	databaseURL := testDatabaseURL()

	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("new raw pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// testDatabaseURL 从当前配置中读取测试数据库地址。
func testDatabaseURL() string {
	cfg := appconfig.Load()
	return cfg.DatabaseURL
}

// cleanupResource 按资源维度清理测试过程中插入的任务和资源数据。
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

// testContext 为数据库测试创建带超时的上下文。
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

func seedResourceVersion(t *testing.T, repo *ResourceRepo, ctx context.Context, title string) (*Resource, *ResourceVersion) {
	t.Helper()

	resource, err := repo.Create(ctx, title, "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}

	version, err := repo.CreateVersion(ctx, resource.ID, 1, "## 考勤管理\n员工必须在九点前签到。", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	return resource, version
}

func seedVersionChunk(
	t *testing.T,
	repo *ResourceRepo,
	ctx context.Context,
	resourceID string,
	versionID string,
	chunkIndex int,
	sectionTitle string,
	content string,
	embedding pgvector.Vector,
) {
	t.Helper()

	if err := repo.CreateChunk(ctx, &ResourceChunk{
		ResourceID:   resourceID,
		VersionID:    versionID,
		ChunkIndex:   chunkIndex,
		SectionTitle: sectionTitle,
		Content:      content,
		Embedding:    embedding,
	}); err != nil {
		t.Fatalf("create version chunk: %v", err)
	}
}

// testVector 生成固定维度的测试向量，便于插入和相似度查询。
func testVector(seed float32) pgvector.Vector {
	values := make([]float32, 1024)
	for index := range values {
		values[index] = seed + float32(index%7)/10
	}

	return pgvector.NewVector(values)
}

// containsResource 判断资源列表中是否包含目标资源。
func containsResource(resources []Resource, id string) bool {
	for _, resource := range resources {
		if resource.ID == id {
			return true
		}
	}

	return false
}
