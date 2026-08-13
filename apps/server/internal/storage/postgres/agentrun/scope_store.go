package agentrun

import (
	"context"
	"fmt"
	"strings"

	agenttools "agent_project/apps/server/internal/agent/tools"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const resolveSecurityScopeSQL = `
SELECT run.principal_type, run.principal_id, run.workspace_id, run.resource_id,
       COALESCE(turn.request_id, run.request_id, '')
FROM agent_runs AS run
LEFT JOIN agent_turns AS turn ON turn.id = run.turn_id
WHERE run.id = $1
  AND run.runtime_mode = 'durable'
  AND run.principal_type IS NOT NULL
  AND run.principal_id IS NOT NULL
  AND run.trust_source IS NOT NULL
  AND length(btrim(run.trust_source)) > 0
  AND run.workspace_id IS NOT NULL
  AND run.resource_id IS NOT NULL`

// SecurityScopeStore resolves only immutable 可信的 facts persisted when 一个
// 持久化的 轮次 was accepted. 步骤 输入 和 模型 输出 不能 influence it.
type SecurityScopeStore struct {
	pool *pgxpool.Pool
}

// NewSecurityScopeStore 校验依赖并创建对应实例。
func NewSecurityScopeStore(pool *pgxpool.Pool) *SecurityScopeStore {
	return &SecurityScopeStore{pool: pool}
}

// ResolveToolScope 执行该函数负责的核心处理逻辑。
func (store *SecurityScopeStore) ResolveToolScope(ctx context.Context, runID string) (agenttools.SecurityContext, error) {
	runID = strings.TrimSpace(runID)
	if _, err := uuid.Parse(runID); err != nil {
		return agenttools.SecurityContext{}, fmt.Errorf("run_id 必须为一个 UUID")
	}
	if store == nil || store.pool == nil {
		return agenttools.SecurityContext{}, fmt.Errorf("可信的运行作用域数据库不能为空")
	}
	var security agenttools.SecurityContext
	err := store.pool.QueryRow(ctx, resolveSecurityScopeSQL, runID).Scan(
		&security.PrincipalType, &security.PrincipalID, &security.WorkspaceID, &security.ResourceID, &security.RequestID,
	)
	if err == pgx.ErrNoRows {
		return agenttools.SecurityContext{}, fmt.Errorf("可信的持久化的运行作用域未找到")
	}
	if err != nil {
		return agenttools.SecurityContext{}, err
	}
	security.PrincipalType = strings.TrimSpace(security.PrincipalType)
	security.PrincipalID = strings.TrimSpace(security.PrincipalID)
	security.WorkspaceID = strings.TrimSpace(security.WorkspaceID)
	security.ResourceID = strings.TrimSpace(security.ResourceID)
	if (security.PrincipalType != "user" && security.PrincipalType != "service") ||
		security.PrincipalID == "" || security.WorkspaceID == "" || security.ResourceID == "" {
		return agenttools.SecurityContext{}, fmt.Errorf("持久化ed 持久化的运行作用域为不完整")
	}
	return security, nil
}
