// Package 证据 包含 the 工作区-scoped PostgreSQL 适配器 用于 the
// 类型化的 Agent EvidenceSet 检索 服务.
package evidence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentevidence "agent_project/apps/server/internal/agent/evidence"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type Repository struct {
	db DB
}

// NewRepository 校验依赖并创建对应实例。
func NewRepository(db DB) *Repository { return &Repository{db: db} }

const currentScopeSQL = `
	SELECT r.workspace_id::text, r.id::text, rv.id::text, r.source_type,
	       COALESCE(rv.embedding_profile, '')
	FROM resources AS r
	JOIN resource_versions AS rv ON rv.resource_id = r.id
	WHERE r.workspace_id = $1
	  AND r.id = $2
	ORDER BY rv.version_number DESC
	LIMIT 1
`

const historicalScopeSQL = `
	SELECT r.workspace_id::text, r.id::text, rv.id::text, r.source_type,
	       COALESCE(rv.embedding_profile, '')
	FROM resources AS r
	JOIN resource_versions AS rv ON rv.resource_id = r.id
	WHERE r.workspace_id = $1
	  AND r.id = $2
	  AND rv.id = $3
	LIMIT 1
`

const lexicalSearchSQL = `
	SELECT rc.id::text, rc.resource_id::text, rc.version_id::text,
	       rc.canonical_node_id, 'document_node', rc.content,
	       CASE
	           WHEN lower(COALESCE(rc.section_title, '') || ' ' || rc.content) LIKE '%' || $4 || '%' THEN 1.0
	           ELSE GREATEST(0.0, similarity(lower(COALESCE(rc.section_title, '') || ' ' || rc.content), $4))
	       END AS lexical_score,
	       rc.created_at
	FROM resource_chunks AS rc
	JOIN resources AS r ON r.id = rc.resource_id
	WHERE r.workspace_id = $1
	  AND rc.resource_id = $2
	  AND rc.version_id = $3
	  AND rc.canonical_node_id IS NOT NULL
	  AND (
	      lower(COALESCE(rc.section_title, '') || ' ' || rc.content) LIKE '%' || $4 || '%'
	      OR lower(COALESCE(rc.section_title, '') || ' ' || rc.content) % $4
	  )
	ORDER BY lexical_score DESC, rc.chunk_index ASC, rc.id ASC
	LIMIT $5
`

const semanticSearchSQL = `
	SELECT rc.id::text, rc.resource_id::text, rc.version_id::text,
	       rc.canonical_node_id, 'document_node', rc.content,
	       GREATEST(0.0, LEAST(1.0, 1.0 - (rc.embedding <=> $1))) AS vector_score,
	       rc.created_at
	FROM resource_chunks AS rc
	JOIN resources AS r ON r.id = rc.resource_id
	WHERE r.workspace_id = $2
	  AND rc.resource_id = $3
	  AND rc.version_id = $4
	  AND rc.canonical_node_id IS NOT NULL
	  AND rc.embedding IS NOT NULL
	  AND rc.embedding_status = 'ready'
	  AND rc.embedding_profile = $5
	  AND rc.embedding_model = $6
	  AND rc.embedding_dimensions = $7
	  AND vector_dims(rc.embedding) = $7
	  AND rc.retrieval_index_version = $8
	ORDER BY rc.embedding <=> $1, rc.chunk_index ASC, rc.id ASC
	LIMIT $9
`

const embeddingVectorTypeSQL = `
	SELECT format_type(attribute.atttypid, attribute.atttypmod)
	FROM pg_attribute AS attribute
	JOIN pg_class AS relation ON relation.oid = attribute.attrelid
	JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
	WHERE namespace.nspname = current_schema()
	  AND relation.relname = 'resource_chunks'
	  AND attribute.attname = 'embedding'
	  AND NOT attribute.attisdropped
`

// ResolveScope 执行该函数负责的核心处理逻辑。
func (repo *Repository) ResolveScope(ctx context.Context, workspaceID, resourceID, versionID string, includeHistory bool) (agentevidence.Scope, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	resourceID = strings.TrimSpace(resourceID)
	versionID = strings.TrimSpace(versionID)
	if workspaceID == "" || resourceID == "" || (includeHistory && versionID == "") || (!includeHistory && versionID != "") {
		return agentevidence.Scope{}, fmt.Errorf("工作区_id、resource_id、和明确的 history 作用域无效")
	}
	if repo == nil || repo.db == nil {
		return agentevidence.Scope{}, fmt.Errorf("证据数据库不能为空")
	}
	query := currentScopeSQL
	args := []any{workspaceID, resourceID}
	if includeHistory {
		query = historicalScopeSQL
		args = append(args, versionID)
	}
	var scope agentevidence.Scope
	err := repo.db.QueryRow(ctx, query, args...).Scan(
		&scope.WorkspaceID, &scope.ResourceID, &scope.VersionID, &scope.SourceType, &scope.EmbeddingProfile,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentevidence.Scope{}, nil
	}
	if err != nil {
		return agentevidence.Scope{}, fmt.Errorf("解析证据作用域：%w", err)
	}
	return scope, nil
}

// SearchLexical 执行该函数负责的核心处理逻辑。
func (repo *Repository) SearchLexical(ctx context.Context, scope agentevidence.Scope, query string, limit int) ([]agentevidence.ScoredCandidate, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if err := validateSearch(scope, limit); err != nil || query == "" {
		if err != nil {
			return nil, err
		}
		return []agentevidence.ScoredCandidate{}, nil
	}
	if repo == nil || repo.db == nil {
		return nil, fmt.Errorf("证据数据库不能为空")
	}
	rows, err := repo.db.Query(ctx, lexicalSearchSQL, scope.WorkspaceID, scope.ResourceID, scope.VersionID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("搜索词法证据：%w", err)
	}
	defer rows.Close()
	return collectCandidates(rows)
}

// EmbeddingVectorType 执行该函数负责的核心处理逻辑。
func (repo *Repository) EmbeddingVectorType(ctx context.Context) (string, error) {
	if repo == nil || repo.db == nil {
		return "", fmt.Errorf("证据数据库不能为空")
	}
	var vectorType string
	if err := repo.db.QueryRow(ctx, embeddingVectorTypeSQL).Scan(&vectorType); err != nil {
		return "", fmt.Errorf("读取数据库嵌入向量类型：%w", err)
	}
	return strings.TrimSpace(vectorType), nil
}

// SearchSemantic 执行该函数负责的核心处理逻辑。
func (repo *Repository) SearchSemantic(ctx context.Context, scope agentevidence.Scope, vector []float32, profile agentevidence.EmbeddingProfile, limit int) ([]agentevidence.ScoredCandidate, error) {
	if err := validateSearch(scope, limit); err != nil {
		return nil, err
	}
	profile.Version = strings.TrimSpace(profile.Version)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.VectorType = strings.TrimSpace(profile.VectorType)
	profile.IndexVersion = strings.TrimSpace(profile.IndexVersion)
	if profile.Version == "" || profile.Model == "" || profile.VectorType == "" || profile.IndexVersion == "" ||
		profile.Dimensions <= 0 || len(vector) != profile.Dimensions {
		return nil, fmt.Errorf("语义嵌入配置档和向量维度无效")
	}
	if repo == nil || repo.db == nil {
		return nil, fmt.Errorf("证据数据库不能为空")
	}
	rows, err := repo.db.Query(ctx, semanticSearchSQL,
		pgvector.NewVector(vector), scope.WorkspaceID, scope.ResourceID, scope.VersionID,
		profile.Version, profile.Model, profile.Dimensions, profile.IndexVersion, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("搜索语义证据：%w", err)
	}
	defer rows.Close()
	return collectCandidates(rows)
}

// validateSearch 校验输入及领域约束。
func validateSearch(scope agentevidence.Scope, limit int) error {
	if strings.TrimSpace(scope.WorkspaceID) == "" || strings.TrimSpace(scope.ResourceID) == "" ||
		strings.TrimSpace(scope.VersionID) == "" || limit <= 0 || limit > 100 {
		return fmt.Errorf("工作区/资源/版本作用域和候选结果上限不能为空")
	}
	return nil
}

// collectCandidates 执行该函数负责的核心处理逻辑。
func collectCandidates(rows pgx.Rows) ([]agentevidence.ScoredCandidate, error) {
	candidates := make([]agentevidence.ScoredCandidate, 0)
	for rows.Next() {
		var candidate agentevidence.ScoredCandidate
		if err := rows.Scan(
			&candidate.Candidate.SourceID, &candidate.Candidate.ResourceID, &candidate.Candidate.VersionID,
			&candidate.Candidate.NodeID, &candidate.Candidate.SourceType, &candidate.Candidate.Content,
			&candidate.Score, &candidate.Candidate.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan 证据候选结果：%w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate 证据候选结果s：%w", err)
	}
	return candidates, nil
}

var _ agentevidence.Repository = (*Repository)(nil)
