package documentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
	documentcommit "agent_project/apps/server/internal/document/commit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const currentVersionSQL = `
SELECT version.id, version.version_number, version.source, version.created_at
FROM resource_versions AS version
JOIN canonical_documents AS canonical ON canonical.version_id = version.id
WHERE version.resource_id = $1
ORDER BY version.version_number DESC, version.id DESC
LIMIT 1`

const readNodesSQL = `
WITH selected_version AS (
	SELECT version.id
	FROM resource_versions AS version
	JOIN canonical_documents AS canonical ON canonical.version_id = version.id
	WHERE version.resource_id = $1 AND ($2 = '' OR version.id::text = $2)
	ORDER BY version.version_number DESC, version.id DESC
	LIMIT 1
)
SELECT node.node_id, node.resource_id, node.version_id, node.node_type, node.content,
       node.content_hash, node.attributes_json
FROM document_nodes AS node
JOIN selected_version ON selected_version.id = node.version_id
WHERE node.resource_id = $1 AND node.node_id = ANY($3::text[])
ORDER BY node.sibling_order, node.node_id`

const searchNodesSQL = `
WITH selected_version AS (
	SELECT version.id
	FROM resource_versions AS version
	JOIN canonical_documents AS canonical ON canonical.version_id = version.id
	WHERE version.resource_id = $1 AND ($2 = '' OR version.id::text = $2)
	ORDER BY version.version_number DESC, version.id DESC
	LIMIT 1
)
SELECT node.node_id, node.resource_id, node.version_id, node.node_type, node.content,
       node.content_hash, node.attributes_json
FROM document_nodes AS node
JOIN selected_version ON selected_version.id = node.version_id
WHERE node.resource_id = $1 AND length(node.content) > 0
  AND node.content ILIKE '%' || $3 || '%'
ORDER BY strpos(lower(node.content), lower($3)), node.sibling_order, node.node_id
LIMIT $4`

const authorizeScopeSQL = `
SELECT EXISTS (
	SELECT 1
	FROM agent_runs AS run
	JOIN agent_steps AS step ON step.run_id = run.id
	JOIN workspaces AS workspace ON workspace.id = run.workspace_id
	JOIN resources AS resource ON resource.id = $2 AND resource.workspace_id = workspace.id
	JOIN memberships AS membership ON membership.workspace_id = workspace.id AND membership.user_id = $3
	JOIN users AS account ON account.id = membership.user_id
	WHERE run.workspace_id = $1 AND run.id = $4 AND step.id = $5
	  AND run.resource_id = $2
	  AND run.runtime_mode = 'durable' AND run.principal_type = 'user' AND run.principal_id = $3
	  AND run.trust_source IS NOT NULL AND length(btrim(run.trust_source)) > 0
	  AND membership.status = 'active' AND account.status = 'active' AND workspace.status = 'active'
)`

const authorizeNodesSQL = `
WITH current_version AS (
	SELECT version.id
	FROM resource_versions AS version
	JOIN canonical_documents AS canonical ON canonical.version_id = version.id
	WHERE version.resource_id = $1
	ORDER BY version.version_number DESC, version.id DESC
	LIMIT 1
)
SELECT node.node_id
FROM document_nodes AS node
JOIN current_version ON current_version.id = node.version_id
WHERE node.workspace_id = $2 AND node.resource_id = $1 AND node.node_id = ANY($3::text[])`

const authorizeEvidenceSQL = `
SELECT DISTINCT evidence.value->>'evidence_id'
FROM agent_observations AS observation
JOIN agent_runs AS run ON run.id = observation.run_id
CROSS JOIN LATERAL jsonb_array_elements(
	COALESCE(observation.payload_json #> '{output,evidence_set,evidence}', '[]'::jsonb)
) AS evidence(value)
WHERE observation.run_id = $4 AND run.workspace_id = $1 AND run.resource_id = $2
  AND evidence.value->>'evidence_id' = ANY($3::text[])`

type Repository struct{ pool *pgxpool.Pool }

// New 校验依赖并创建对应实例。
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// GetCurrentVersion 按作用域读取并返回所需数据。
func (repo *Repository) GetCurrentVersion(ctx context.Context, resourceID string) (*builtin.DocumentVersion, error) {
	if err := validUUID(resourceID, "resource_id"); err != nil {
		return nil, err
	}
	if repo == nil || repo.pool == nil {
		return nil, fmt.Errorf("canonical 文档数据库不能为空")
	}
	version := builtin.DocumentVersion{ResourceID: strings.TrimSpace(resourceID)}
	err := repo.pool.QueryRow(ctx, currentVersionSQL, version.ResourceID).Scan(&version.ID, &version.VersionNumber, &version.Source, &version.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &version, err
}

// ReadNodes 执行该函数负责的核心处理逻辑。
func (repo *Repository) ReadNodes(ctx context.Context, input builtin.ReadNodesInput) ([]builtin.DocumentNode, error) {
	if err := validateNodeQuery(input.ResourceID, input.VersionID, input.NodeIDs, 50); err != nil {
		return nil, err
	}
	if repo == nil || repo.pool == nil {
		return nil, fmt.Errorf("canonical 文档数据库不能为空")
	}
	rows, err := repo.pool.Query(ctx, readNodesSQL, strings.TrimSpace(input.ResourceID), strings.TrimSpace(input.VersionID), input.NodeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// SearchNodes 执行该函数负责的核心处理逻辑。
func (repo *Repository) SearchNodes(ctx context.Context, input builtin.SearchNodesInput) ([]builtin.DocumentNode, error) {
	if err := validUUID(input.ResourceID, "resource_id"); err != nil {
		return nil, err
	}
	if input.VersionID != "" {
		if err := validUUID(input.VersionID, "version_id"); err != nil {
			return nil, err
		}
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || len(input.Query) > 500 || input.Limit < 1 || input.Limit > 50 {
		return nil, fmt.Errorf("有界的搜索查询和上限不能为空")
	}
	if repo == nil || repo.pool == nil {
		return nil, fmt.Errorf("canonical 文档数据库不能为空")
	}
	rows, err := repo.pool.Query(ctx, searchNodesSQL, strings.TrimSpace(input.ResourceID), strings.TrimSpace(input.VersionID), input.Query, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// ResolveDocumentAuthorization 执行该函数负责的核心处理逻辑。
func (repo *Repository) ResolveDocumentAuthorization(ctx context.Context, security agenttools.SecurityContext, resourceID string, nodeIDs, evidenceRefs []string) (documentcommit.NodeAuthorization, error) {
	if security.PrincipalType != "user" || strings.TrimSpace(security.PrincipalID) == "" || strings.TrimSpace(security.WorkspaceID) == "" ||
		strings.TrimSpace(security.RunID) == "" || strings.TrimSpace(security.StepID) == "" {
		return documentcommit.NodeAuthorization{}, fmt.Errorf("trusted principal/workspace/run/step scope is required")
	}
	for name, value := range map[string]string{"workspace_id": security.WorkspaceID, "principal_id": security.PrincipalID, "run_id": security.RunID, "step_id": security.StepID, "resource_id": resourceID} {
		if err := validUUID(value, name); err != nil {
			return documentcommit.NodeAuthorization{}, err
		}
	}
	if len(nodeIDs) > 100 || len(evidenceRefs) > 100 {
		return documentcommit.NodeAuthorization{}, fmt.Errorf("authorization 请求超过有界的引用")
	}
	if repo == nil || repo.pool == nil {
		return documentcommit.NodeAuthorization{}, fmt.Errorf("canonical authorization 数据库不能为空")
	}
	var inScope bool
	if err := repo.pool.QueryRow(ctx, authorizeScopeSQL, security.WorkspaceID, resourceID, security.PrincipalID, security.RunID, security.StepID).Scan(&inScope); err != nil {
		return documentcommit.NodeAuthorization{}, err
	}
	if !inScope {
		return documentcommit.NodeAuthorization{}, &agenttools.ToolError{Category: agenttools.ErrorPermissionDenied, Message: "规范文档作用域校验未通过"}
	}
	result := documentcommit.NodeAuthorization{AuthorizedNodeIDs: map[string]struct{}{}, EvidenceRefs: map[string]struct{}{}}
	if err := repo.collectStrings(ctx, authorizeNodesSQL, []any{resourceID, security.WorkspaceID, unique(nodeIDs)}, result.AuthorizedNodeIDs); err != nil {
		return documentcommit.NodeAuthorization{}, err
	}
	if err := repo.collectStrings(ctx, authorizeEvidenceSQL, []any{security.WorkspaceID, resourceID, unique(evidenceRefs), security.RunID}, result.EvidenceRefs); err != nil {
		return documentcommit.NodeAuthorization{}, err
	}
	return result, nil
}

// collectStrings 执行该函数负责的核心处理逻辑。
func (repo *Repository) collectStrings(ctx context.Context, query string, args []any, destination map[string]struct{}) error {
	rows, err := repo.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return err
		}
		destination[value] = struct{}{}
	}
	return rows.Err()
}

// scanNodes 执行该函数负责的核心处理逻辑。
func scanNodes(rows pgx.Rows) ([]builtin.DocumentNode, error) {
	nodes := make([]builtin.DocumentNode, 0)
	for rows.Next() {
		var node builtin.DocumentNode
		var attributes json.RawMessage
		if err := rows.Scan(&node.NodeID, &node.ResourceID, &node.VersionID, &node.Type, &node.Content, &node.ContentHash, &attributes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attributes, &node.Attributes); err != nil {
			return nil, fmt.Errorf("解析 canonical 节点 attributes：%w", err)
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// validateNodeQuery 校验输入及领域约束。
func validateNodeQuery(resourceID, versionID string, nodeIDs []string, maximum int) error {
	if err := validUUID(resourceID, "resource_id"); err != nil {
		return err
	}
	if versionID != "" {
		if err := validUUID(versionID, "version_id"); err != nil {
			return err
		}
	}
	if len(nodeIDs) == 0 || len(nodeIDs) > maximum {
		return fmt.Errorf("有界的 node_ids 不能为空")
	}
	for _, nodeID := range nodeIDs {
		if strings.TrimSpace(nodeID) == "" {
			return fmt.Errorf("node_ids 不能为空")
		}
	}
	return nil
}

// validUUID 执行该函数负责的核心处理逻辑。
func validUUID(value, name string) error {
	if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("处理失败：%s 必须为一个 UUID", name)
	}
	return nil
}

// unique 执行该函数负责的核心处理逻辑。
func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

var _ builtin.DocumentBackend = (*Repository)(nil)
var _ documentcommit.NodeAuthorizer = (*Repository)(nil)
