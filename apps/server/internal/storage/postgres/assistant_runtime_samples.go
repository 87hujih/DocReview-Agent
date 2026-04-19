package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AssistantRuntimeSampleRecord 表示 assistant runtime 事件折叠后的学习样本。
type AssistantRuntimeSampleRecord struct {
	ID                      string
	SessionID               string
	DecisionMessageID       *string
	RequestKind             string
	ResponseMode            string
	PlannerUsed             bool
	VerifierUsed            bool
	ClarificationAsked      bool
	ClarificationOutcome    string
	TaskSuggestionCreated   bool
	TaskSuggestionConfirmed bool
	TaskSuggestionIgnored   bool
	UserCorrected           bool
	PromotedToWorkflow      bool
	FinalOutcome            string
	Payload                 []byte
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// AssistantRuntimeSampleSummary 表示 assistant runtime 学习样本的最小汇总指标。
type AssistantRuntimeSampleSummary struct {
	TotalSamples              int
	TaskSuggestionCreated     int
	TaskSuggestionConfirmed   int
	TaskSuggestionIgnored     int
	ClarificationAsked        int
	ClarificationResolvedChat int
	ClarificationResolvedFlow int
	UserCorrected             int
	WorkflowDowngraded        int
}

// AssistantRuntimeSampleUpsertParams 描述折叠学习样本时允许更新的字段。
type AssistantRuntimeSampleUpsertParams struct {
	SessionID               string
	DecisionMessageID       string
	RequestKind             *string
	ResponseMode            *string
	PlannerUsed             *bool
	VerifierUsed            *bool
	ClarificationAsked      *bool
	ClarificationOutcome    *string
	TaskSuggestionCreated   *bool
	TaskSuggestionConfirmed *bool
	TaskSuggestionIgnored   *bool
	UserCorrected           *bool
	PromotedToWorkflow      *bool
	FinalOutcome            *string
	Payload                 []byte
}

// AssistantRuntimeSampleRepo 封装 assistant_runtime_samples 表的访问能力。
type AssistantRuntimeSampleRepo struct {
	pool *pgxpool.Pool
}

// NewAssistantRuntimeSampleRepo 使用连接池创建 assistant runtime 样本仓储。
func NewAssistantRuntimeSampleRepo(pool *pgxpool.Pool) *AssistantRuntimeSampleRepo {
	return &AssistantRuntimeSampleRepo{pool: pool}
}

// Upsert 按 decision_message_id 折叠一条学习样本，并保留已存在的决策字段。
func (r *AssistantRuntimeSampleRepo) Upsert(ctx context.Context, params AssistantRuntimeSampleUpsertParams) error {
	normalized := normalizeAssistantRuntimeSampleUpsertParams(params)

	_, err := scanAssistantRuntimeSample(r.pool.QueryRow(ctx, `
		INSERT INTO assistant_runtime_samples (
			session_id,
			decision_message_id,
			request_kind,
			response_mode,
			planner_used,
			verifier_used,
			clarification_asked,
			clarification_outcome,
			task_suggestion_created,
			task_suggestion_confirmed,
			task_suggestion_ignored,
			user_corrected,
			promoted_to_workflow,
			final_outcome,
			payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb)
		ON CONFLICT (decision_message_id) DO UPDATE
		SET session_id = EXCLUDED.session_id,
		    request_kind = COALESCE(NULLIF(EXCLUDED.request_kind, ''), assistant_runtime_samples.request_kind),
		    response_mode = COALESCE(NULLIF(EXCLUDED.response_mode, ''), assistant_runtime_samples.response_mode),
		    planner_used = assistant_runtime_samples.planner_used OR EXCLUDED.planner_used,
		    verifier_used = assistant_runtime_samples.verifier_used OR EXCLUDED.verifier_used,
		    clarification_asked = assistant_runtime_samples.clarification_asked OR EXCLUDED.clarification_asked,
		    clarification_outcome = COALESCE(NULLIF(EXCLUDED.clarification_outcome, ''), assistant_runtime_samples.clarification_outcome),
		    task_suggestion_created = assistant_runtime_samples.task_suggestion_created OR EXCLUDED.task_suggestion_created,
		    task_suggestion_confirmed = assistant_runtime_samples.task_suggestion_confirmed OR EXCLUDED.task_suggestion_confirmed,
		    task_suggestion_ignored = assistant_runtime_samples.task_suggestion_ignored OR EXCLUDED.task_suggestion_ignored,
		    user_corrected = assistant_runtime_samples.user_corrected OR EXCLUDED.user_corrected,
		    promoted_to_workflow = assistant_runtime_samples.promoted_to_workflow OR EXCLUDED.promoted_to_workflow,
		    final_outcome = COALESCE(NULLIF(EXCLUDED.final_outcome, ''), assistant_runtime_samples.final_outcome),
		    payload = CASE
		        WHEN EXCLUDED.payload = '{}'::jsonb THEN assistant_runtime_samples.payload
		        ELSE assistant_runtime_samples.payload || EXCLUDED.payload
		    END,
		    updated_at = now()
		RETURNING id,
		          session_id,
		          decision_message_id,
		          request_kind,
		          response_mode,
		          planner_used,
		          verifier_used,
		          clarification_asked,
		          clarification_outcome,
		          task_suggestion_created,
		          task_suggestion_confirmed,
		          task_suggestion_ignored,
		          user_corrected,
		          promoted_to_workflow,
		          final_outcome,
		          payload,
		          created_at,
		          updated_at
	`, assistantRuntimeSampleUpsertArgs(normalized)...))
	return err
}

// GetByDecisionMessage 按 decision_message_id 读取折叠后的学习样本。
func (r *AssistantRuntimeSampleRepo) GetByDecisionMessage(ctx context.Context, decisionMessageID string) (*AssistantRuntimeSampleRecord, error) {
	record, err := scanAssistantRuntimeSample(r.pool.QueryRow(ctx, `
		SELECT id,
		       session_id,
		       decision_message_id,
		       request_kind,
		       response_mode,
		       planner_used,
		       verifier_used,
		       clarification_asked,
		       clarification_outcome,
		       task_suggestion_created,
		       task_suggestion_confirmed,
		       task_suggestion_ignored,
		       user_corrected,
		       promoted_to_workflow,
		       final_outcome,
		       payload,
		       created_at,
		       updated_at
		FROM assistant_runtime_samples
		WHERE decision_message_id = $1
	`, strings.TrimSpace(decisionMessageID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &record, nil
}

// Summary 返回自指定时间起的 assistant runtime 样本汇总。
func (r *AssistantRuntimeSampleRepo) Summary(ctx context.Context, since time.Time) (*AssistantRuntimeSampleSummary, error) {
	summary := &AssistantRuntimeSampleSummary{}

	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) AS total_samples,
		       COALESCE(SUM(CASE WHEN task_suggestion_created THEN 1 ELSE 0 END), 0) AS task_suggestion_created,
		       COALESCE(SUM(CASE WHEN task_suggestion_confirmed THEN 1 ELSE 0 END), 0) AS task_suggestion_confirmed,
		       COALESCE(SUM(CASE WHEN task_suggestion_ignored THEN 1 ELSE 0 END), 0) AS task_suggestion_ignored,
		       COALESCE(SUM(CASE WHEN clarification_asked THEN 1 ELSE 0 END), 0) AS clarification_asked,
		       COALESCE(SUM(CASE WHEN clarification_outcome = 'resolved_to_chat' THEN 1 ELSE 0 END), 0) AS clarification_resolved_chat,
		       COALESCE(SUM(CASE WHEN clarification_outcome = 'resolved_to_workflow' THEN 1 ELSE 0 END), 0) AS clarification_resolved_flow,
		       COALESCE(SUM(CASE WHEN user_corrected THEN 1 ELSE 0 END), 0) AS user_corrected,
		       COALESCE(SUM(CASE WHEN final_outcome = 'workflow_downgraded' THEN 1 ELSE 0 END), 0) AS workflow_downgraded
		FROM assistant_runtime_samples
		WHERE created_at >= $1
	`, since).Scan(
		&summary.TotalSamples,
		&summary.TaskSuggestionCreated,
		&summary.TaskSuggestionConfirmed,
		&summary.TaskSuggestionIgnored,
		&summary.ClarificationAsked,
		&summary.ClarificationResolvedChat,
		&summary.ClarificationResolvedFlow,
		&summary.UserCorrected,
		&summary.WorkflowDowngraded,
	)
	if err != nil {
		return nil, err
	}

	return summary, nil
}

// normalizeAssistantRuntimeSampleUpsertParams 统一样本写库默认值，避免显式 NULL 绕过数据库默认值。
func normalizeAssistantRuntimeSampleUpsertParams(params AssistantRuntimeSampleUpsertParams) AssistantRuntimeSampleUpsertParams {
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.DecisionMessageID = strings.TrimSpace(params.DecisionMessageID)
	params.RequestKind = optionalTrimmedText(derefOptionalString(params.RequestKind))
	params.ResponseMode = optionalTrimmedText(derefOptionalString(params.ResponseMode))
	params.ClarificationOutcome = optionalTrimmedText(derefOptionalString(params.ClarificationOutcome))
	params.FinalOutcome = optionalTrimmedText(derefOptionalString(params.FinalOutcome))

	if len(params.Payload) == 0 {
		params.Payload = []byte(`{}`)
	}

	return params
}

// assistantRuntimeSampleUpsertArgs 组装 assistant runtime 样本的 upsert 参数。
func assistantRuntimeSampleUpsertArgs(params AssistantRuntimeSampleUpsertParams) []any {
	return []any{
		params.SessionID,
		params.DecisionMessageID,
		derefOptionalString(params.RequestKind),
		derefOptionalString(params.ResponseMode),
		derefOptionalBool(params.PlannerUsed),
		derefOptionalBool(params.VerifierUsed),
		derefOptionalBool(params.ClarificationAsked),
		derefOptionalString(params.ClarificationOutcome),
		derefOptionalBool(params.TaskSuggestionCreated),
		derefOptionalBool(params.TaskSuggestionConfirmed),
		derefOptionalBool(params.TaskSuggestionIgnored),
		derefOptionalBool(params.UserCorrected),
		derefOptionalBool(params.PromotedToWorkflow),
		derefOptionalString(params.FinalOutcome),
		string(params.Payload),
	}
}

// scanAssistantRuntimeSample 把当前数据库行扫描成 assistant runtime 学习样本。
func scanAssistantRuntimeSample(row pgx.Row) (AssistantRuntimeSampleRecord, error) {
	var record AssistantRuntimeSampleRecord

	err := row.Scan(
		&record.ID,
		&record.SessionID,
		&record.DecisionMessageID,
		&record.RequestKind,
		&record.ResponseMode,
		&record.PlannerUsed,
		&record.VerifierUsed,
		&record.ClarificationAsked,
		&record.ClarificationOutcome,
		&record.TaskSuggestionCreated,
		&record.TaskSuggestionConfirmed,
		&record.TaskSuggestionIgnored,
		&record.UserCorrected,
		&record.PromotedToWorkflow,
		&record.FinalOutcome,
		&record.Payload,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return AssistantRuntimeSampleRecord{}, err
	}

	return record, nil
}

// derefOptionalBool 在布尔指针为空时返回 false，便于样本字段按单调状态折叠。
func derefOptionalBool(value *bool) bool {
	if value == nil {
		return false
	}

	return *value
}
