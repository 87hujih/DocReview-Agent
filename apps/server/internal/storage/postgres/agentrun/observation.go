package agentrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 观察结果 为 the immutable, auditable 结果 that 一个 类型化的 orchestration
// 步骤 persisted. 状态 carries only 有界的 引用; this 记录 retains
// the full structured 负载 outside 模型 prompts.
type Observation struct {
	ID             string
	RunID          string
	StepID         string
	ObservationKey string
	Kind           string
	Action         string
	ToolCallID     *string
	PayloadJSON    json.RawMessage
	ContentHash    string
	Novel          bool
	CreatedAt      time.Time
}

// ListObservations 按作用域读取并返回所需数据。
func (r *Repository) ListObservations(ctx context.Context, runID string) ([]Observation, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("agent 运行数据库不能为空")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, run_id, step_id, observation_key, kind, action, tool_call_id,
		       payload_json, content_hash, novel, created_at
		FROM agent_observations
		WHERE run_id = $1
		ORDER BY created_at, id
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	observations := make([]Observation, 0)
	for rows.Next() {
		var observation Observation
		if err := rows.Scan(
			&observation.ID, &observation.RunID, &observation.StepID, &observation.ObservationKey,
			&observation.Kind, &observation.Action, &observation.ToolCallID, &observation.PayloadJSON,
			&observation.ContentHash, &observation.Novel, &observation.CreatedAt,
		); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return observations, nil
}
