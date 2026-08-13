package evidence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	agentevidence "agent_project/apps/server/internal/agent/evidence"

	"github.com/jackc/pgx/v5"
)

// TestResolveScopeDefaultsToWorkspaceCurrentVersion 验证对应场景下的正常路径与失败路径。
func TestResolveScopeDefaultsToWorkspaceCurrentVersion(t *testing.T) {
	db := &scopeDB{row: scopeRow{values: []any{"workspace-a", "resource-1", "version-current", "upload", "embedding-v1"}}}
	repo := NewRepository(db)

	scope, err := repo.ResolveScope(context.Background(), "workspace-a", "resource-1", "", false)
	if err != nil {
		t.Fatalf("resolve current scope: %v", err)
	}
	if scope.WorkspaceID != "workspace-a" || scope.ResourceID != "resource-1" || scope.VersionID != "version-current" {
		t.Fatalf("scope=%#v", scope)
	}
	if len(db.args) != 2 || db.args[0] != "workspace-a" || db.args[1] != "resource-1" {
		t.Fatalf("scope query args=%#v", db.args)
	}
	normalized := strings.ToLower(db.sql)
	for _, required := range []string{"r.workspace_id = $1", "r.id = $2", "order by rv.version_number desc", "limit 1"} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("current scope query missing %q: %s", required, db.sql)
		}
	}
}

// TestSearchLexicalRechecksWorkspaceResourceAndVersionScope 验证对应场景下的正常路径与失败路径。
func TestSearchLexicalRechecksWorkspaceResourceAndVersionScope(t *testing.T) {
	sentinel := errors.New("查询停止ped")
	db := &scopeDB{queryErr: sentinel}
	repo := NewRepository(db)
	scope := retrievalScope("workspace-a", "resource-1", "version-current")

	_, err := repo.SearchLexical(context.Background(), scope, "policy", 8)
	if !errors.Is(err, sentinel) {
		t.Fatalf("lexical query error=%v", err)
	}
	if len(db.queryArgs) != 5 || db.queryArgs[0] != "workspace-a" || db.queryArgs[1] != "resource-1" || db.queryArgs[2] != "version-current" {
		t.Fatalf("lexical query args=%#v", db.queryArgs)
	}
	normalized := strings.ToLower(db.querySQL)
	for _, required := range []string{"join resources as r", "r.workspace_id = $1", "rc.resource_id = $2", "rc.version_id = $3", "similarity", "limit $5"} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("lexical query missing %q: %s", required, db.querySQL)
		}
	}
}

// TestSearchSemanticRequiresExactWorkspaceAndEmbeddingProfile 验证对应场景下的正常路径与失败路径。
func TestSearchSemanticRequiresExactWorkspaceAndEmbeddingProfile(t *testing.T) {
	sentinel := errors.New("查询停止ped")
	db := &scopeDB{queryErr: sentinel}
	repo := NewRepository(db)
	scope := retrievalScope("workspace-a", "resource-1", "version-current")
	profile := agentevidence.EmbeddingProfile{Version: "embedding-v1", Model: "embed-model", Dimensions: 3, VectorType: "vector(3)", IndexVersion: "hnsw-v1"}

	_, err := repo.SearchSemantic(context.Background(), scope, []float32{0.1, 0.2, 0.3}, profile, 8)
	if !errors.Is(err, sentinel) {
		t.Fatalf("semantic query error=%v", err)
	}
	if len(db.queryArgs) != 9 || db.queryArgs[1] != "workspace-a" || db.queryArgs[2] != "resource-1" || db.queryArgs[3] != "version-current" ||
		db.queryArgs[4] != "embedding-v1" || db.queryArgs[5] != "embed-model" || db.queryArgs[6] != 3 || db.queryArgs[7] != "hnsw-v1" {
		t.Fatalf("semantic query args=%#v", db.queryArgs)
	}
	normalized := strings.ToLower(db.querySQL)
	for _, required := range []string{
		"join resources as r", "r.workspace_id = $2", "rc.resource_id = $3", "rc.version_id = $4",
		"rc.embedding_status = 'ready'", "rc.embedding_profile = $5", "rc.embedding_model = $6",
		"rc.embedding_dimensions = $7", "rc.retrieval_index_version = $8", "order by rc.embedding <=> $1", "limit $9",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("semantic query missing %q: %s", required, db.querySQL)
		}
	}
}

type scopeDB struct {
	row       scopeRow
	sql       string
	args      []any
	querySQL  string
	queryArgs []any
	queryErr  error
}

// QueryRow 执行该函数负责的核心处理逻辑。
func (db *scopeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.sql = sql
	db.args = append([]any(nil), args...)
	return db.row
}

// 查询 执行该函数负责的核心处理逻辑。
func (db *scopeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.querySQL = sql
	db.queryArgs = append([]any(nil), args...)
	if db.queryErr != nil {
		return nil, db.queryErr
	}
	return nil, fmt.Errorf("非预期的查询 call")
}

type scopeRow struct {
	values []any
	err    error
}

// retrievalScope 执行该函数负责的核心处理逻辑。
func retrievalScope(workspaceID, resourceID, versionID string) agentevidence.Scope {
	return agentevidence.Scope{WorkspaceID: workspaceID, ResourceID: resourceID, VersionID: versionID, SourceType: "upload", EmbeddingProfile: "embedding-v1"}
}

// Scan 执行该函数负责的核心处理逻辑。
func (row scopeRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) != len(row.values) {
		return fmt.Errorf("scan destinations=%d 值=%d", len(dest), len(row.values))
	}
	for index := range dest {
		target := reflect.ValueOf(dest[index])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return fmt.Errorf("处理失败：destination %d 为 not 一个 pointer", index)
		}
		value := reflect.ValueOf(row.values[index])
		if !value.Type().AssignableTo(target.Elem().Type()) {
			return fmt.Errorf("值 %d 类型 %s 不能 assign 用于 %s", index, value.Type(), target.Elem().Type())
		}
		target.Elem().Set(value)
	}
	return nil
}
