package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/testsupport/postgrescleanup"
	"agent_project/apps/server/internal/testsupport/postgrestest"

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
		WHERE table_schema = current_schema()
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
		"assistant_task_notifications",
		"session_context_snapshots",
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

// TestMigrationCreatesTaskQueryIndexes 验证`migration`在写入或副作用路径下的行为，防止同类回归。
func TestMigrationCreatesTaskQueryIndexes(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = current_schema()
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

// TestRunMigrationsRepairsLegacyResourceSectionsTable 验证 011 迁移能兼容已存在旧版 resource_sections 表的数据库。
func TestRunMigrationsRepairsLegacyResourceSectionsTable(t *testing.T) {
	pool := newRawTestPool(t)
	ctx := testContext(t)

	if err := runMigrationsBefore(ctx, pool, "011_grounded_structured_document_rag_phase1.sql"); err != nil {
		t.Fatalf("run migrations before 011: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		CREATE TABLE resource_sections (
			id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			resource_id           UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
			version_id            UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE,
			section_key           TEXT        NOT NULL,
			section_type          TEXT        NOT NULL,
			title                 TEXT        NOT NULL DEFAULT '',
			canonical_entity_name TEXT,
			aliases_json          JSONB       NOT NULL DEFAULT '[]'::jsonb,
			summary               TEXT        NOT NULL DEFAULT '',
			content               TEXT        NOT NULL DEFAULT '',
			page_start            INT,
			page_end              INT,
			metadata_json         JSONB       NOT NULL DEFAULT '{}'::jsonb,
			created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create legacy resource_sections: %v", err)
	}

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations against legacy resource_sections: %v", err)
	}

	type columnInfo struct {
		ColumnName string
		IsNullable string
	}

	rows, err := pool.Query(ctx, `
		SELECT column_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'resource_sections'
		  AND column_name = 'section_order'
	`)
	if err != nil {
		t.Fatalf("query resource_sections.section_order: %v", err)
	}
	defer rows.Close()

	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (columnInfo, error) {
		var info columnInfo
		err := row.Scan(&info.ColumnName, &info.IsNullable)
		return info, err
	})
	if err != nil {
		t.Fatalf("collect section_order column info: %v", err)
	}
	if len(columns) != 1 {
		t.Fatalf("expected section_order column to exist once, got %d", len(columns))
	}
	if columns[0].IsNullable != "NO" {
		t.Fatalf("expected section_order to be non-nullable, got %q", columns[0].IsNullable)
	}

	var indexCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'resource_sections'
		  AND indexname = 'idx_resource_sections_version_order'
	`).Scan(&indexCount); err != nil {
		t.Fatalf("query idx_resource_sections_version_order: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("expected idx_resource_sections_version_order to exist once, got %d", indexCount)
	}
}

// TestMigrationAlignsResourceChunkGroundedColumnsWithDefaults 验证`migrationAlignsResourceChunkGroundedColumnsWithDefaults`在特定边界条件下的行为，防止同类回归。
func TestMigrationAlignsResourceChunkGroundedColumnsWithDefaults(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	type columnInfo struct {
		ColumnName    string
		IsNullable    string
		ColumnDefault *string
	}

	rows, err := pool.Query(ctx, `
		SELECT column_name, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'resource_chunks'
		  AND column_name = ANY($1)
		ORDER BY column_name
	`, []string{
		"chunk_role",
		"metadata_json",
		"page_end",
		"page_start",
		"section_type",
		"window_group_id",
	})
	if err != nil {
		t.Fatalf("query resource_chunks grounded columns: %v", err)
	}
	defer rows.Close()

	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (columnInfo, error) {
		var info columnInfo
		err := row.Scan(&info.ColumnName, &info.IsNullable, &info.ColumnDefault)
		return info, err
	})
	if err != nil {
		t.Fatalf("collect resource_chunks grounded columns: %v", err)
	}

	expected := map[string]struct {
		nullable string
		def      string
	}{
		"chunk_role":      {nullable: "NO", def: "'section_body'::text"},
		"metadata_json":   {nullable: "NO", def: "'{}'::jsonb"},
		"page_end":        {nullable: "NO", def: "0"},
		"page_start":      {nullable: "NO", def: "0"},
		"section_type":    {nullable: "NO", def: "'document'::text"},
		"window_group_id": {nullable: "NO", def: "''::text"},
	}
	if len(columns) != len(expected) {
		t.Fatalf("expected %d grounded columns, got %d (%v)", len(expected), len(columns), columns)
	}

	for _, column := range columns {
		want, ok := expected[column.ColumnName]
		if !ok {
			t.Fatalf("unexpected grounded column info: %#v", column)
		}
		if column.IsNullable != want.nullable {
			t.Fatalf("expected %s nullable=%s, got %s", column.ColumnName, want.nullable, column.IsNullable)
		}
		if column.ColumnDefault == nil || *column.ColumnDefault != want.def {
			t.Fatalf("expected %s default %q, got %#v", column.ColumnName, want.def, column.ColumnDefault)
		}
	}
}

// TestMigrationAlignsResourceVersionStructureColumnsWithDefaults 验证`migrationAlignsResourceVersionStructureColumnsWithDefaults`在特定边界条件下的行为，防止同类回归。
func TestMigrationAlignsResourceVersionStructureColumnsWithDefaults(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	type columnInfo struct {
		ColumnName    string
		IsNullable    string
		ColumnDefault *string
	}

	rows, err := pool.Query(ctx, `
		SELECT column_name, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'resource_version_structures'
		  AND column_name = ANY($1)
		ORDER BY column_name
	`, []string{
		"parser_name",
		"parser_version",
		"quality_flags_json",
		"source_format",
	})
	if err != nil {
		t.Fatalf("query resource_version_structures columns: %v", err)
	}
	defer rows.Close()

	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (columnInfo, error) {
		var info columnInfo
		err := row.Scan(&info.ColumnName, &info.IsNullable, &info.ColumnDefault)
		return info, err
	})
	if err != nil {
		t.Fatalf("collect resource_version_structures columns: %v", err)
	}

	expected := map[string]struct {
		nullable string
		def      string
	}{
		"parser_name":        {nullable: "NO", def: "''::text"},
		"parser_version":     {nullable: "NO", def: "''::text"},
		"quality_flags_json": {nullable: "NO", def: "'[]'::jsonb"},
		"source_format":      {nullable: "NO", def: "''::text"},
	}
	if len(columns) != len(expected) {
		t.Fatalf("expected %d structure columns, got %d (%v)", len(expected), len(columns), columns)
	}

	for _, column := range columns {
		want, ok := expected[column.ColumnName]
		if !ok {
			t.Fatalf("unexpected structure column info: %#v", column)
		}
		if column.IsNullable != want.nullable {
			t.Fatalf("expected %s nullable=%s, got %s", column.ColumnName, want.nullable, column.IsNullable)
		}
		if column.ColumnDefault == nil || *column.ColumnDefault != want.def {
			t.Fatalf("expected %s default %q, got %#v", column.ColumnName, want.def, column.ColumnDefault)
		}
	}
}

// TestMigrationAddsAssistantSuggestionSourceColumn 验证`migration`在写入或副作用路径下的行为，防止同类回归。
func TestMigrationAddsAssistantSuggestionSourceColumn(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	var currentSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil {
		t.Fatalf("query current schema: %v", err)
	}
	if currentSchema == "" {
		t.Fatal("expected isolated schema, got empty current_schema()")
	}
	if currentSchema == "public" {
		t.Fatalf("expected isolated schema instead of public, got %q", currentSchema)
	}

	type columnInfo struct {
		TableSchema string
		ColumnName  string
		IsNullable  string
	}

	rows, err := pool.Query(ctx, `
		SELECT table_schema, column_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'tasks'
		  AND column_name = 'source_message_id'
	`)
	if err != nil {
		t.Fatalf("query task source_message_id column: %v", err)
	}
	defer rows.Close()

	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (columnInfo, error) {
		var info columnInfo
		err := row.Scan(&info.TableSchema, &info.ColumnName, &info.IsNullable)
		return info, err
	})
	if err != nil {
		t.Fatalf("collect source_message_id column info: %v", err)
	}

	if len(columns) != 1 {
		t.Fatalf("expected tasks.source_message_id column to exist once, got %d", len(columns))
	}
	if columns[0].TableSchema != currentSchema {
		t.Fatalf("expected tasks.source_message_id in current schema %q, got %q", currentSchema, columns[0].TableSchema)
	}
	if columns[0].IsNullable != "YES" {
		t.Fatalf("expected tasks.source_message_id to be nullable, got %q", columns[0].IsNullable)
	}

	var indexSchema string
	var indexName string
	if err := pool.QueryRow(ctx, `
		SELECT schemaname, indexname
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'tasks'
		  AND indexname = 'idx_tasks_source_message_id_unique'
	`).Scan(&indexSchema, &indexName); err != nil {
		t.Fatalf("query source_message_id unique index: %v", err)
	}

	if indexSchema != currentSchema {
		t.Fatalf("expected source_message_id unique index in current schema %q, got %q", currentSchema, indexSchema)
	}
	if indexName != "idx_tasks_source_message_id_unique" {
		t.Fatalf("expected source_message_id unique index, got %q", indexName)
	}
}

// TestMigrationAddsExecutionChainBaseVersionColumns 验证`migration`在写入或副作用路径下的行为，防止同类回归。
func TestMigrationAddsExecutionChainBaseVersionColumns(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND (
			(table_name = 'approvals' AND column_name = 'base_version_id')
			OR
			(table_name = 'execution_jobs' AND column_name = 'base_version_id')
		  )
		ORDER BY table_name, column_name
	`)
	if err != nil {
		t.Fatalf("query base version columns: %v", err)
	}
	defer rows.Close()

	type columnInfo struct {
		TableName  string
		ColumnName string
		IsNullable string
	}

	columns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (columnInfo, error) {
		var info columnInfo
		err := row.Scan(&info.TableName, &info.ColumnName, &info.IsNullable)
		return info, err
	})
	if err != nil {
		t.Fatalf("collect base version columns: %v", err)
	}

	expected := map[string]string{
		"approvals.base_version_id":      "YES",
		"execution_jobs.base_version_id": "YES",
	}
	if len(columns) != len(expected) {
		t.Fatalf("expected %d base version columns, got %d (%v)", len(expected), len(columns), columns)
	}

	for _, column := range columns {
		key := column.TableName + "." + column.ColumnName
		expectedNullable, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected column info returned: %#v", column)
		}
		if column.IsNullable != expectedNullable {
			t.Fatalf("expected %s nullable=%s, got %s", key, expectedNullable, column.IsNullable)
		}
	}
}

// TestMigrationCreatesAssistantTaskNotificationsTable 验证`migration`在写入或副作用路径下的行为，防止同类回归。
func TestMigrationCreatesAssistantTaskNotificationsTable(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations again: %v", err)
	}

	var tableCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'assistant_task_notifications'
	`).Scan(&tableCount); err != nil {
		t.Fatalf("query assistant_task_notifications table: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("expected assistant_task_notifications table to exist once, got %d", tableCount)
	}

	rows, err := pool.Query(ctx, `
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		WHERE tc.table_schema = current_schema()
		  AND tc.table_name = 'assistant_task_notifications'
		  AND tc.constraint_type = 'PRIMARY KEY'
		ORDER BY kcu.ordinal_position
	`)
	if err != nil {
		t.Fatalf("query assistant_task_notifications primary key: %v", err)
	}
	defer rows.Close()

	pkColumns, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect assistant_task_notifications primary key columns: %v", err)
	}

	expectedColumns := []string{"task_id", "status"}
	if !reflect.DeepEqual(pkColumns, expectedColumns) {
		t.Fatalf("expected assistant_task_notifications primary key columns %v, got %v", expectedColumns, pkColumns)
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

	lexicalToken := "考勤隔离测试-" + uniqueSuffix()
	attendanceChunk := &ResourceChunk{
		ResourceID:   resource.ID,
		VersionID:    version.ID,
		ChunkIndex:   1,
		SectionTitle: lexicalToken,
		Content:      "员工必须按时完成" + lexicalToken + "签到和签退。",
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

	lexicalChunks, err := repo.SearchChunksLexical(ctx, lexicalToken, 5)
	if err != nil {
		t.Fatalf("search lexical chunks: %v", err)
	}

	if len(lexicalChunks) == 0 {
		t.Fatal("expected lexical search to return at least one chunk")
	}

	if lexicalChunks[0].ID != attendanceChunk.ID {
		t.Fatalf("expected lexical search to rank attendance chunk first, got %q", lexicalChunks[0].ID)
	}

	filteredLexicalChunks, err := repo.SearchChunksLexicalByResource(ctx, lexicalToken, 5, resource.ID)
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

// TestResourceRepoGetVersionByID 验证`resourceRepoGetVersionByID`在特定边界条件下的行为，防止同类回归。
func TestResourceRepoGetVersionByID(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, err := repo.Create(ctx, "版本读取测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	version, err := repo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	got, err := repo.GetVersionByID(ctx, version.ID)
	if err != nil {
		t.Fatalf("get version by id: %v", err)
	}
	if got == nil {
		t.Fatal("expected version, got nil")
	}
	if got.ID != version.ID {
		t.Fatalf("expected version id %q, got %q", version.ID, got.ID)
	}
}

// TestCreateDocumentGraphRollsBackOnChunkFailure 验证`createDocumentGraph`在回滚路径下的行为，防止同类回归。
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

// TestCountChunksByVersion 验证`countChunksByVersion`在特定边界条件下的行为，防止同类回归。
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

	var (
		sectionType   *string
		chunkRole     *string
		windowGroupID *string
		pageStart     *int
		pageEnd       *int
	)
	if err := pool.QueryRow(ctx, `
		SELECT section_type, chunk_role, window_group_id, page_start, page_end
		FROM resource_chunks
		WHERE version_id = $1
		  AND chunk_index = 0
	`, version.ID).Scan(&sectionType, &chunkRole, &windowGroupID, &pageStart, &pageEnd); err != nil {
		t.Fatalf("query inserted chunk defaults: %v", err)
	}
	if sectionType == nil || *sectionType != "section" {
		t.Fatalf("expected default section_type %q, got %#v", "section", sectionType)
	}
	if chunkRole == nil || *chunkRole != "section_body" {
		t.Fatalf("expected default chunk_role %q, got %#v", "section_body", chunkRole)
	}
	if windowGroupID == nil || *windowGroupID != "考勤管理" {
		t.Fatalf("expected default window_group_id %q, got %#v", "考勤管理", windowGroupID)
	}
	if pageStart == nil || *pageStart != 0 || pageEnd == nil || *pageEnd != 0 {
		t.Fatalf("expected default pages 0-0, got %#v-%#v", pageStart, pageEnd)
	}
}

// TestReplaceVersionChunksIsIdempotent 验证`replaceVersionChunksIsIdempotent`在特定边界条件下的行为，防止同类回归。
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
		SELECT section_title,
		       content,
		       chunk_index,
		       section_type,
		       chunk_role,
		       window_group_id,
		       page_start,
		       page_end
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
		sectionType  *string
		chunkRole    *string
		windowGroup  *string
		pageStart    *int
		pageEnd      *int
	}

	storedChunks := make([]storedChunk, 0)
	for rows.Next() {
		var chunk storedChunk
		if err := rows.Scan(&chunk.sectionTitle, &chunk.content, &chunk.chunkIndex, &chunk.sectionType, &chunk.chunkRole, &chunk.windowGroup, &chunk.pageStart, &chunk.pageEnd); err != nil {
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
	if storedChunks[0].sectionType == nil || *storedChunks[0].sectionType != "section" {
		t.Fatalf("expected stored section_type %q, got %#v", "section", storedChunks[0].sectionType)
	}
	if storedChunks[0].chunkRole == nil || *storedChunks[0].chunkRole != "section_body" {
		t.Fatalf("expected stored chunk_role %q, got %#v", "section_body", storedChunks[0].chunkRole)
	}
	if storedChunks[0].windowGroup == nil || *storedChunks[0].windowGroup != "考勤管理" {
		t.Fatalf("expected stored window_group_id %q, got %#v", "考勤管理", storedChunks[0].windowGroup)
	}
	if storedChunks[0].pageStart == nil || *storedChunks[0].pageStart != 0 || storedChunks[0].pageEnd == nil || *storedChunks[0].pageEnd != 0 {
		t.Fatalf("expected stored pages 0-0, got %#v-%#v", storedChunks[0].pageStart, storedChunks[0].pageEnd)
	}
}

// TestSearchChunksByVersionExcludesOlderVersion 验证`searchChunksByVersionExcludesOlderVersion`在特定边界条件下的行为，防止同类回归。
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

// TestSearchChunksLexicalByVersionExcludesOlderVersion 验证`searchChunksLexicalByVersionExcludesOlderVersion`在特定边界条件下的行为，防止同类回归。
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

// TestCleanupResourceTreeIsIdempotent 验证`cleanupResourceTreeIsIdempotent`在特定边界条件下的行为，防止同类回归。
func TestCleanupResourceTreeIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	eventRepo := NewTaskEventRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "资源树清理测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "验证共享 cleanup helper")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := taskRepo.AddStep(ctx, task.ID, "planner"); err != nil {
		t.Fatalf("add step: %v", err)
	}
	if _, err := taskRepo.AddArtifact(ctx, task.ID, "diff_preview", []byte(`{"sections":[]}`)); err != nil {
		t.Fatalf("add artifact: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := eventRepo.Add(ctx, TaskEventCreateParams{
		TaskID:    task.ID,
		RunID:     &job.ID,
		Source:    "cleanup_test",
		Level:     "info",
		EventType: "cleanup.seed",
		Message:   "seed cleanup data",
		Payload:   []byte(`{"resource_id":"` + resource.ID + `"}`),
	}); err != nil {
		t.Fatalf("add task event: %v", err)
	}

	if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}

	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM resources WHERE id = $1`, resource.ID)
	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM tasks WHERE id = $1`, task.ID)
	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM approvals WHERE id = $1`, approvalRecord.ID)
	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM execution_jobs WHERE id = $1`, job.ID)
	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM task_steps WHERE task_id = $1`, task.ID)
	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM task_artifacts WHERE task_id = $1`, task.ID)
	assertRowMissing(t, ctx, pool, `SELECT COUNT(*) FROM task_events WHERE task_id = $1`, task.ID)

	if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
		t.Fatalf("second cleanup: %v", err)
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
	return postgrestest.NewIsolatedPool(t, ctx, databaseURL, "storage_postgres", NewPool, RunMigrations)
}

// newRawTestPool 为需要自行控制迁移时机的测试创建数据库连接池。
func newRawTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := testContext(t)
	databaseURL := testDatabaseURL()
	return postgrestest.NewRawIsolatedPool(t, ctx, databaseURL, "storage_postgres_raw", NewPool)
}

// runMigrationsBefore 在测试里按顺序执行指定迁移之前的所有 SQL，便于构造历史 schema。
func runMigrationsBefore(ctx context.Context, pool *pgxpool.Pool, stopBefore string) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var migrationNames []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if entry.Name() >= stopBefore {
			continue
		}

		migrationNames = append(migrationNames, entry.Name())
	}

	sort.Strings(migrationNames)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, migrationLockNamespace, migrationLockKey); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}

	for _, migrationName := range migrationNames {
		statement, err := migrationsFS.ReadFile("migrations/" + migrationName)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", migrationName, err)
		}
		if _, err := tx.Exec(ctx, string(statement)); err != nil {
			return fmt.Errorf("exec migration %s: %w", migrationName, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration tx: %w", err)
	}

	return nil
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
	if err := postgrescleanup.CleanupResourceTree(ctx, pool, resourceID); err != nil {
		t.Fatalf("cleanup resource tree for resource %q: %v", resourceID, err)
	}
}

// assertRowMissing 封装 `RowMissing` 的断言逻辑，避免用例重复展开校验细节。
func assertRowMissing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, id string) {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, query, id).Scan(&count); err != nil {
		t.Fatalf("query cleanup result: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected query %q to return 0 rows for %q, got %d", query, id, count)
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

// seedResourceVersion 为测试场景补齐 `资源版本` 所需数据，减少重复造数。
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

// seedVersionChunk 为测试场景补齐 `版本chunk` 所需数据，减少重复造数。
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
