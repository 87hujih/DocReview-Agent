package agentops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/operations"
)

const comparisonListSQL = `
SELECT id::text, workspace_id::text, resource_id::text, COALESCE(run_id::text, ''),
       request_id, comparison_kind, status,
       COALESCE(legacy_result_hash, ''), COALESCE(typed_result_hash, ''),
       COALESCE(legacy_event_hash, ''), COALESCE(typed_event_hash, ''),
       COALESCE(legacy_dto_hash, ''), COALESCE(typed_dto_hash, ''),
       details_json, created_at
FROM agent_cutover_comparisons
WHERE workspace_id = $1 AND resource_id = $2 AND created_at >= $3 AND created_at <= $4
ORDER BY created_at ASC, id ASC
LIMIT $5`

// Comparisons returns immutable comparison identities and hashes for human review.
func (repository *Repository) Comparisons(ctx context.Context, request operations.ComparisonListRequest) (operations.ComparisonList, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	if request.WorkspaceID == "" || request.ResourceID == "" {
		return operations.ComparisonList{}, fmt.Errorf("workspace_id 和 resource_id 不能为空")
	}
	if request.Window < time.Minute || request.Window > 30*24*time.Hour {
		return operations.ComparisonList{}, fmt.Errorf("comparison 时间窗口必须介于一分钟和 30 天之间")
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return operations.ComparisonList{}, fmt.Errorf("comparison limit 必须介于 1 和 1000 之间")
	}
	if repository == nil || repository.pool == nil {
		return operations.ComparisonList{}, fmt.Errorf("operations 数据库不能为空")
	}
	now := time.Now().UTC()
	rows, err := repository.pool.Query(ctx, comparisonListSQL,
		request.WorkspaceID, request.ResourceID, now.Add(-request.Window), now, request.Limit)
	if err != nil {
		return operations.ComparisonList{}, err
	}
	defer rows.Close()
	result := operations.ComparisonList{
		SchemaVersion: "1.1", WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID,
		WindowSeconds: int64(request.Window.Seconds()), CollectedAt: now, Limit: request.Limit,
		Comparisons: make([]operations.ComparisonView, 0),
	}
	for rows.Next() {
		var item operations.ComparisonView
		if err := rows.Scan(
			&item.ID, &item.WorkspaceID, &item.ResourceID, &item.RunID,
			&item.RequestID, &item.ComparisonKind, &item.Status,
			&item.LegacyResultHash, &item.TypedResultHash,
			&item.LegacyEventHash, &item.TypedEventHash,
			&item.LegacyDTOHash, &item.TypedDTOHash,
			&item.DetailsJSON, &item.CreatedAt,
		); err != nil {
			return operations.ComparisonList{}, err
		}
		result.Comparisons = append(result.Comparisons, item)
	}
	if err := rows.Err(); err != nil {
		return operations.ComparisonList{}, err
	}
	return result, nil
}
