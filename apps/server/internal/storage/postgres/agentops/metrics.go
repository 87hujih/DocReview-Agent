package agentops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/operations"
)

const metricsSQL = `
WITH settings AS (
    SELECT $1::uuid AS workspace_id, $2::timestamptz AS window_start, $3::timestamptz AS collected_at,
           NULLIF($4, '')::uuid AS resource_id
), scoped_runs AS (
    SELECT run.* FROM agent_runs AS run, settings
    WHERE run.workspace_id = settings.workspace_id
      AND run.runtime_mode = 'durable'
      AND (settings.resource_id IS NULL OR run.resource_id = settings.resource_id)
), windowed_runs AS (
    SELECT run.* FROM scoped_runs AS run, settings
    WHERE run.created_at >= settings.window_start
), scoped_steps AS (
    SELECT step.* FROM agent_steps AS step JOIN scoped_runs AS run ON run.id = step.run_id
), scoped_attempts AS (
    SELECT attempt.* FROM agent_attempts AS attempt
    JOIN scoped_steps AS step ON step.id = attempt.step_id, settings
    WHERE attempt.started_at >= settings.window_start
), all_scoped_calls AS (
    SELECT call.* FROM tool_calls AS call
    JOIN scoped_runs AS run ON run.id = call.run_id
), scoped_calls AS (
    SELECT call.* FROM all_scoped_calls AS call,
    settings
    WHERE call.created_at >= settings.window_start
), scoped_outbox AS (
    SELECT event.* FROM outbox_events AS event, settings
    WHERE EXISTS (
        SELECT 1 FROM scoped_runs AS run
        WHERE (event.aggregate_type = 'agent_run' AND event.aggregate_id = run.id::text)
           OR event.payload_json ->> 'run_id' = run.id::text
           OR event.id::text IN (
               SELECT COALESCE(call.output_json #>> '{output,commit,outbox_id}', '')
               FROM all_scoped_calls AS call WHERE call.run_id = run.id
           )
    )
), scoped_approvals AS (
    SELECT approval.* FROM agent_tool_approvals AS approval
    JOIN scoped_runs AS run ON run.id = approval.run_id
), scoped_comparisons AS (
    SELECT comparison.* FROM agent_cutover_comparisons AS comparison, settings
    WHERE comparison.workspace_id = settings.workspace_id
      AND (settings.resource_id IS NULL OR comparison.resource_id = settings.resource_id)
      AND comparison.created_at >= settings.window_start
)
SELECT
    COALESCE((SELECT jsonb_object_agg(status, count) FROM (SELECT status, count(*) AS count FROM windowed_runs GROUP BY status) AS counts), '{}'::jsonb),
    COALESCE((SELECT jsonb_object_agg(status, count) FROM (SELECT status, count(*) AS count FROM scoped_steps GROUP BY status) AS counts), '{}'::jsonb),
    jsonb_build_object(
        'queued_steps', (SELECT count(*) FROM scoped_steps WHERE status = 'queued'),
        'running_steps', (SELECT count(*) FROM scoped_steps WHERE status = 'running'),
        'expired_step_leases', (SELECT count(*) FROM scoped_steps, settings WHERE status = 'running' AND lease_expires_at <= settings.collected_at),
        'oldest_queued_age_seconds', COALESCE((SELECT GREATEST(extract(epoch FROM (settings.collected_at - min(step.created_at))), 0) FROM scoped_steps AS step, settings WHERE step.status = 'queued' GROUP BY settings.collected_at), 0),
        'waiting_approvals', (SELECT count(*) FROM scoped_approvals WHERE status = 'pending'),
        'oldest_approval_age_seconds', COALESCE((SELECT GREATEST(extract(epoch FROM (settings.collected_at - min(approval.created_at))), 0) FROM scoped_approvals AS approval, settings WHERE approval.status = 'pending' GROUP BY settings.collected_at), 0)
    ),
    jsonb_build_object(
        'attempts', (SELECT count(*) FROM scoped_attempts),
        'errors', (SELECT count(*) FROM scoped_attempts WHERE error_category IS NOT NULL),
        'error_rate', COALESCE((SELECT count(*) FILTER (WHERE error_category IS NOT NULL)::double precision / NULLIF(count(*), 0) FROM scoped_attempts), 0),
        'input_tokens', COALESCE((SELECT sum(input_tokens) FROM scoped_attempts), 0),
        'output_tokens', COALESCE((SELECT sum(output_tokens) FROM scoped_attempts), 0),
        'cost', COALESCE((SELECT sum(cost)::double precision FROM scoped_attempts), 0)
    ),
    jsonb_build_object(
        'pending', (SELECT count(*) FROM scoped_outbox WHERE status = 'pending'),
        'publishing', (SELECT count(*) FROM scoped_outbox WHERE status = 'publishing'),
        'dead_letters', (SELECT count(*) FROM scoped_outbox WHERE status = 'dead_letter'),
        'expired_leases', (SELECT count(*) FROM scoped_outbox, settings WHERE status = 'publishing' AND lease_expires_at <= settings.collected_at),
        'oldest_pending_age_seconds', COALESCE((SELECT GREATEST(extract(epoch FROM (settings.collected_at - min(event.created_at))), 0) FROM scoped_outbox AS event, settings WHERE event.status = 'pending' GROUP BY settings.collected_at), 0)
    ),
    jsonb_build_object(
        'calls', (SELECT count(*) FROM scoped_calls WHERE tool_name = 'retrieval.search'),
        'succeeded', (SELECT count(*) FROM scoped_calls WHERE tool_name = 'retrieval.search' AND status = 'succeeded'),
        'lexical_degraded', (SELECT count(*) FROM scoped_calls AS call WHERE call.tool_name = 'retrieval.search' AND (EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(call.output_json #> '{output,evidence_set,process}', '[]'::jsonb)) AS process WHERE process ->> 'stage' = 'degradation' AND process ->> 'channel' = 'lexical') OR COALESCE(call.output_json #> '{output,summary,degradations}', '[]'::jsonb) ? 'lexical')),
        'semantic_degraded', (SELECT count(*) FROM scoped_calls AS call WHERE call.tool_name = 'retrieval.search' AND (EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(call.output_json #> '{output,evidence_set,process}', '[]'::jsonb)) AS process WHERE process ->> 'stage' = 'degradation' AND process ->> 'channel' = 'semantic') OR COALESCE(call.output_json #> '{output,summary,degradations}', '[]'::jsonb) ? 'semantic')),
        'web_degraded', (SELECT count(*) FROM scoped_calls WHERE tool_name = 'web.search' AND status = 'failed' AND error_category IN ('rate_limited', 'timeout', 'retryable_upstream')),
        'profile_mismatches', (SELECT count(*) FROM scoped_calls WHERE tool_name = 'retrieval.search' AND status = 'failed' AND (error_category = 'terminal_upstream' OR error_category IS NULL) AND (error_json #>> '{details,reason_code}' = 'embedding_profile_mismatch' OR error_json #>> '{details,reason_code}' IS NULL)),
        'missing_node_citations', (SELECT count(*) FROM scoped_calls AS call CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(call.output_json #> '{output,evidence_set,evidence}') = 'array' THEN call.output_json #> '{output,evidence_set,evidence}' ELSE COALESCE(call.output_json #> '{output,summary,citations}', '[]'::jsonb) END) AS evidence WHERE call.tool_name = 'retrieval.search' AND COALESCE(evidence ->> 'node_id', '') = ''),
        'average_evidence_count', COALESCE((SELECT avg(CASE WHEN jsonb_typeof(call.output_json #> '{output,evidence_set,evidence}') = 'array' THEN jsonb_array_length(call.output_json #> '{output,evidence_set,evidence}') ELSE COALESCE((call.output_json #>> '{output,summary,evidence_count}')::integer, 0) END)::double precision FROM scoped_calls AS call WHERE call.tool_name = 'retrieval.search' AND call.status = 'succeeded'), 0)
    ),
    jsonb_build_object(
        'matched', (SELECT count(*) FROM scoped_comparisons WHERE status = 'matched'),
        'diverged', (SELECT count(*) FROM scoped_comparisons WHERE status = 'diverged'),
        'unavailable', (SELECT count(*) FROM scoped_comparisons WHERE status = 'unavailable')
    )`

// 指标 执行该函数负责的核心处理逻辑。
func (repository *Repository) Metrics(ctx context.Context, request operations.MetricsRequest) (operations.MetricsSnapshot, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	if request.WorkspaceID == "" {
		return operations.MetricsSnapshot{}, fmt.Errorf("工作区_id 不能为空")
	}
	if request.Window < time.Minute || request.Window > 30*24*time.Hour {
		return operations.MetricsSnapshot{}, fmt.Errorf("指标时间窗口必须介于一分钟和 30 天之间")
	}
	if repository == nil || repository.pool == nil {
		return operations.MetricsSnapshot{}, fmt.Errorf("operations 数据库不能为空")
	}
	now := time.Now().UTC()
	var runStatusJSON, stepStatusJSON, queueJSON, usageJSON, outboxJSON, retrievalJSON, reconciliationJSON json.RawMessage
	if err := repository.pool.QueryRow(ctx, metricsSQL, request.WorkspaceID, now.Add(-request.Window), now, request.ResourceID).Scan(
		&runStatusJSON, &stepStatusJSON, &queueJSON, &usageJSON, &outboxJSON, &retrievalJSON, &reconciliationJSON,
	); err != nil {
		return operations.MetricsSnapshot{}, err
	}
	result := operations.MetricsSnapshot{
		SchemaVersion: "1.1", WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID,
		WindowSeconds: int64(request.Window.Seconds()), CollectedAt: now,
	}
	for _, item := range []struct {
		raw    json.RawMessage
		target any
	}{
		{runStatusJSON, &result.RunStatus}, {stepStatusJSON, &result.StepStatus},
		{queueJSON, &result.Queue}, {usageJSON, &result.Usage}, {outboxJSON, &result.Outbox},
		{retrievalJSON, &result.Retrieval}, {reconciliationJSON, &result.Reconciliation},
	} {
		if err := json.Unmarshal(item.raw, item.target); err != nil {
			return operations.MetricsSnapshot{}, fmt.Errorf("解析 operations 指标：%w", err)
		}
	}
	return result, nil
}
