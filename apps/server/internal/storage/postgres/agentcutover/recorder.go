package agentcutover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/cutover"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const insertComparisonSQL = `
INSERT INTO agent_cutover_comparisons (
	workspace_id, resource_id, request_id, comparison_kind, status,
	legacy_result_hash, typed_result_hash, legacy_event_hash, typed_event_hash,
	legacy_dto_hash, typed_dto_hash, details_json
)
VALUES ($1,$2,$3,'public_turn',$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (workspace_id, request_id, comparison_kind) DO NOTHING`

const selectComparisonSQL = `
SELECT status, COALESCE(legacy_result_hash, ''), COALESCE(typed_result_hash, ''),
       COALESCE(legacy_event_hash, ''), COALESCE(typed_event_hash, ''),
       COALESCE(legacy_dto_hash, ''), COALESCE(typed_dto_hash, ''), details_json
FROM agent_cutover_comparisons
WHERE workspace_id = $1 AND request_id = $2 AND comparison_kind = 'public_turn'`

type Recorder struct{ pool *pgxpool.Pool }

// NewRecorder 校验依赖并创建对应实例。
func NewRecorder(pool *pgxpool.Pool) *Recorder { return &Recorder{pool: pool} }

// 记录 按领域约束持久化数据。
func (recorder *Recorder) Record(ctx context.Context, comparison cutover.Comparison) error {
	prepared, err := prepare(comparison)
	if err != nil {
		return err
	}
	if recorder == nil || recorder.pool == nil {
		return fmt.Errorf("切换比较结果数据库不能为空")
	}
	tag, err := recorder.pool.Exec(ctx, insertComparisonSQL,
		prepared.WorkspaceID, prepared.ResourceID, prepared.RequestID, prepared.Status,
		nullable(prepared.LegacyResultHash), nullable(prepared.TypedResultHash),
		nullable(prepared.LegacyEventHash), nullable(prepared.TypedEventHash),
		nullable(prepared.LegacyDTOHash), nullable(prepared.TypedDTOHash), prepared.Details,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var stored cutover.Comparison
	stored.RequestID, stored.WorkspaceID, stored.ResourceID = prepared.RequestID, prepared.WorkspaceID, prepared.ResourceID
	if err := recorder.pool.QueryRow(ctx, selectComparisonSQL, prepared.WorkspaceID, prepared.RequestID).Scan(
		&stored.Status, &stored.LegacyResultHash, &stored.TypedResultHash,
		&stored.LegacyEventHash, &stored.TypedEventHash, &stored.LegacyDTOHash, &stored.TypedDTOHash, &stored.Details,
	); err != nil {
		return err
	}
	if !same(stored, prepared) {
		return fmt.Errorf("处理失败：切换比较结果 idempotency conflict")
	}
	return nil
}

// prepare 执行该函数负责的核心处理逻辑。
func prepare(comparison cutover.Comparison) (cutover.Comparison, error) {
	comparison.RequestID = strings.TrimSpace(comparison.RequestID)
	comparison.WorkspaceID = strings.TrimSpace(comparison.WorkspaceID)
	comparison.ResourceID = strings.TrimSpace(comparison.ResourceID)
	if comparison.RequestID == "" {
		return cutover.Comparison{}, fmt.Errorf("比较结果 request_id 不能为空")
	}
	if _, err := uuid.Parse(comparison.WorkspaceID); err != nil {
		return cutover.Comparison{}, fmt.Errorf("比较结果工作区_id 必须为一个 UUID")
	}
	if _, err := uuid.Parse(comparison.ResourceID); err != nil {
		return cutover.Comparison{}, fmt.Errorf("比较结果 resource_id 必须为一个 UUID")
	}
	if comparison.Status != cutover.ComparisonMatched && comparison.Status != cutover.ComparisonDiverged && comparison.Status != cutover.ComparisonUnavailable {
		return cutover.Comparison{}, fmt.Errorf("比较结果状态无效")
	}
	for name, value := range map[string]string{
		"legacy_result_hash": comparison.LegacyResultHash, "typed_result_hash": comparison.TypedResultHash,
		"legacy_event_hash": comparison.LegacyEventHash, "typed_event_hash": comparison.TypedEventHash,
		"legacy_dto_hash": comparison.LegacyDTOHash, "typed_dto_hash": comparison.TypedDTOHash,
	} {
		if value != "" && (len(value) != 71 || !strings.HasPrefix(value, "sha256:")) {
			return cutover.Comparison{}, fmt.Errorf("比较结果 %s 无效", name)
		}
	}
	if len(comparison.Details) == 0 {
		comparison.Details = json.RawMessage(`{}`)
	}
	var details map[string]any
	if json.Unmarshal(comparison.Details, &details) != nil || details == nil {
		return cutover.Comparison{}, fmt.Errorf("比较结果 details 必须是 JSON 对象")
	}
	comparison.Details, _ = json.Marshal(details)
	return comparison, nil
}

// same 执行该函数负责的核心处理逻辑。
func same(left, right cutover.Comparison) bool {
	return left.Status == right.Status && left.LegacyResultHash == right.LegacyResultHash && left.TypedResultHash == right.TypedResultHash &&
		left.LegacyEventHash == right.LegacyEventHash && left.TypedEventHash == right.TypedEventHash &&
		left.LegacyDTOHash == right.LegacyDTOHash && left.TypedDTOHash == right.TypedDTOHash && bytes.Equal(left.Details, right.Details)
}

// nullable 执行该函数负责的核心处理逻辑。
func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

var _ cutover.ComparisonRecorder = (*Recorder)(nil)
