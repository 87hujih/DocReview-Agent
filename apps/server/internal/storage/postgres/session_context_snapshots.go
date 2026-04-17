package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionContextSnapshotRecord 表示会话上下文快照在 PostgreSQL 中的持久化形态。
type SessionContextSnapshotRecord struct {
	SessionID                      string
	ActiveResourceID               *string
	ActiveResourceTitle            *string
	ActiveResourceSourceType       *string
	ActiveResourceSourceMessageID  *string
	ActiveSectionID                *string
	ActiveSectionType              *string
	ActiveEntityName               *string
	PendingTaskSuggestionMessageID *string
	PendingTaskInstruction         *string
	LatestTaskID                   *string
	LatestTaskStatus               *string
	LatestTaskSourceMessageID      *string
	ConfirmedConstraintsJSON       []byte
	LastCitationWindowsJSON        []byte
	LastEnumeratedEntitiesJSON     []byte
	OrdinalReferenceFrameJSON      []byte
	RollingSummary                 *string
	SummaryBaseSequenceNo          int
	CreatedAt                      time.Time
	UpdatedAt                      time.Time
}

// UpsertActiveResourceParams 描述活跃资源投影需要刷新的字段。
type UpsertActiveResourceParams struct {
	SessionID       string
	ResourceID      string
	ResourceTitle   string
	ResourceSource  string
	SourceMessageID *string
}

// UpsertPendingTaskSuggestionParams 描述待确认任务建议投影需要刷新的字段。
type UpsertPendingTaskSuggestionParams struct {
	SessionID   string
	MessageID   string
	Instruction string
}

// UpsertLatestTaskParams 描述最近任务投影需要刷新的字段。
type UpsertLatestTaskParams struct {
	SessionID       string
	TaskID          string
	Status          string
	SourceMessageID *string
}

// CitationWindow 描述一次回答中围绕某个 section 取出的证据窗口。
type CitationWindow struct {
	SectionID     string `json:"section_id"`
	SectionType   string `json:"section_type,omitempty"`
	WindowGroupID string `json:"window_group_id,omitempty"`
}

// EnumeratedEntity 描述最近一次 assistant 列举出的实体项。
type EnumeratedEntity struct {
	SectionID   string `json:"section_id"`
	SectionType string `json:"section_type,omitempty"`
	EntityName  string `json:"entity_name,omitempty"`
	Ordinal     int    `json:"ordinal,omitempty"`
}

// OrdinalReference 保存 ordinal 到具体 section 的映射。
type OrdinalReference struct {
	Ordinal     int    `json:"ordinal"`
	SectionID   string `json:"section_id"`
	SectionType string `json:"section_type,omitempty"`
	EntityName  string `json:"entity_name,omitempty"`
}

// UpdateGroundingStateParams 描述 grounding 状态更新所需的最小字段。
type UpdateGroundingStateParams struct {
	SessionID              string
	ActiveSectionID        *string
	ActiveSectionType      *string
	ActiveEntityName       *string
	LastCitationWindows    []CitationWindow
	LastEnumeratedEntities []EnumeratedEntity
	OrdinalReferenceFrame  []OrdinalReference
}

// SessionContextSnapshotRepo 封装会话上下文快照表的最小读写能力。
type SessionContextSnapshotRepo struct {
	pool *pgxpool.Pool
}

// NewSessionContextSnapshotRepo 使用连接池创建快照仓储。
func NewSessionContextSnapshotRepo(pool *pgxpool.Pool) *SessionContextSnapshotRepo {
	return &SessionContextSnapshotRepo{pool: pool}
}

// CreateEmpty 为会话创建一条空快照；若已存在则复用原记录并刷新 updated_at。
func (r *SessionContextSnapshotRepo) CreateEmpty(ctx context.Context, sessionID string) (*SessionContextSnapshotRecord, error) {
	record, err := scanSessionContextSnapshot(r.pool.QueryRow(ctx, `
		INSERT INTO session_context_snapshots (session_id)
		VALUES ($1)
		ON CONFLICT (session_id) DO UPDATE
		SET updated_at = now()
		RETURNING session_id,
		          active_resource_id,
		          active_resource_title,
		          active_resource_source_type,
		          active_resource_source_message_id,
		          active_section_id,
		          active_section_type,
		          active_entity_name,
		          pending_task_suggestion_message_id,
		          pending_task_instruction,
		          latest_task_id,
		          latest_task_status,
		          latest_task_source_message_id,
		          confirmed_constraints_json,
		          last_citation_windows_json,
		          last_enumerated_entities_json,
		          ordinal_reference_frame_json,
		          rolling_summary,
		          summary_base_sequence_no,
		          created_at,
		          updated_at
	`, sessionID))
	if err != nil {
		return nil, err
	}

	return &record, nil
}

// GetBySessionID 按会话 ID 读取快照；不存在时返回 nil。
func (r *SessionContextSnapshotRepo) GetBySessionID(ctx context.Context, sessionID string) (*SessionContextSnapshotRecord, error) {
	record, err := scanSessionContextSnapshot(r.pool.QueryRow(ctx, `
		SELECT session_id,
		       active_resource_id,
		       active_resource_title,
		       active_resource_source_type,
		       active_resource_source_message_id,
		       active_section_id,
		       active_section_type,
		       active_entity_name,
		       pending_task_suggestion_message_id,
		       pending_task_instruction,
		       latest_task_id,
		       latest_task_status,
		       latest_task_source_message_id,
		       confirmed_constraints_json,
		       last_citation_windows_json,
		       last_enumerated_entities_json,
		       ordinal_reference_frame_json,
		       rolling_summary,
		       summary_base_sequence_no,
		       created_at,
		       updated_at
		FROM session_context_snapshots
		WHERE session_id = $1
	`, sessionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &record, nil
}

// UpsertActiveResource 覆盖会话当前活跃资源。
func (r *SessionContextSnapshotRepo) UpsertActiveResource(ctx context.Context, params UpsertActiveResourceParams) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (
		    session_id,
		    active_resource_id,
		    active_resource_title,
		    active_resource_source_type,
		    active_resource_source_message_id
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (session_id) DO UPDATE
		SET active_resource_id = EXCLUDED.active_resource_id,
		    active_resource_title = EXCLUDED.active_resource_title,
		    active_resource_source_type = EXCLUDED.active_resource_source_type,
		    active_resource_source_message_id = EXCLUDED.active_resource_source_message_id,
		    updated_at = now()
	`, params.SessionID, params.ResourceID, strings.TrimSpace(params.ResourceTitle), strings.TrimSpace(params.ResourceSource), params.SourceMessageID)
	return err
}

// UpsertPendingTaskSuggestion 覆盖会话当前最近待确认的任务建议。
func (r *SessionContextSnapshotRepo) UpsertPendingTaskSuggestion(ctx context.Context, params UpsertPendingTaskSuggestionParams) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (
		    session_id,
		    pending_task_suggestion_message_id,
		    pending_task_instruction
		)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id) DO UPDATE
		SET pending_task_suggestion_message_id = EXCLUDED.pending_task_suggestion_message_id,
		    pending_task_instruction = EXCLUDED.pending_task_instruction,
		    updated_at = now()
	`, params.SessionID, params.MessageID, strings.TrimSpace(params.Instruction))
	return err
}

// UpsertLatestTask 覆盖会话最近一个已创建任务及其状态。
func (r *SessionContextSnapshotRepo) UpsertLatestTask(ctx context.Context, params UpsertLatestTaskParams) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (
		    session_id,
		    latest_task_id,
		    latest_task_status,
		    latest_task_source_message_id
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (session_id) DO UPDATE
		SET latest_task_id = EXCLUDED.latest_task_id,
		    latest_task_status = EXCLUDED.latest_task_status,
		    latest_task_source_message_id = EXCLUDED.latest_task_source_message_id,
		    updated_at = now()
	`, params.SessionID, params.TaskID, strings.TrimSpace(params.Status), params.SourceMessageID)
	return err
}

// ClearPendingTaskSuggestion 清理会话当前待确认的任务建议字段。
func (r *SessionContextSnapshotRepo) ClearPendingTaskSuggestion(ctx context.Context, sessionID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (session_id, pending_task_suggestion_message_id, pending_task_instruction)
		VALUES ($1, NULL, NULL)
		ON CONFLICT (session_id) DO UPDATE
		SET pending_task_suggestion_message_id = NULL,
		    pending_task_instruction = NULL,
		    updated_at = now()
	`, sessionID)
	return err
}

// UpdateConfirmedConstraints 更新用户已确认约束的 JSON 投影。
func (r *SessionContextSnapshotRepo) UpdateConfirmedConstraints(ctx context.Context, sessionID string, constraints any) error {
	body, err := marshalSnapshotJSON(constraints, []byte("[]"))
	if err != nil {
		return err
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (session_id, confirmed_constraints_json)
		VALUES ($1, $2::jsonb)
		ON CONFLICT (session_id) DO UPDATE
		SET confirmed_constraints_json = EXCLUDED.confirmed_constraints_json,
		    updated_at = now()
	`, sessionID, string(body))
	return err
}

// UpdateRollingSummary 更新滚动摘要与已覆盖消息序号。
func (r *SessionContextSnapshotRepo) UpdateRollingSummary(ctx context.Context, sessionID string, summary *string, summaryBaseSequenceNo int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (session_id, rolling_summary, summary_base_sequence_no)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id) DO UPDATE
		SET rolling_summary = EXCLUDED.rolling_summary,
		    summary_base_sequence_no = EXCLUDED.summary_base_sequence_no,
		    updated_at = now()
	`, sessionID, trimOptionalString(summary), summaryBaseSequenceNo)
	return err
}

// AdvanceRollingSummary 仅在 base 序号前进时推进滚动摘要。
func (r *SessionContextSnapshotRepo) AdvanceRollingSummary(ctx context.Context, sessionID string, summary *string, nextBaseSequenceNo int) (bool, error) {
	result, err := r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (session_id, rolling_summary, summary_base_sequence_no)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id) DO UPDATE
		SET rolling_summary = EXCLUDED.rolling_summary,
		    summary_base_sequence_no = EXCLUDED.summary_base_sequence_no,
		    updated_at = now()
		WHERE session_context_snapshots.summary_base_sequence_no < EXCLUDED.summary_base_sequence_no
	`, sessionID, trimOptionalString(summary), nextBaseSequenceNo)
	if err != nil {
		return false, err
	}

	return result.RowsAffected() > 0, nil
}

// UpdateLatestTaskStatusBySourceMessageID 按任务来源建议消息 ID 更新最近任务状态。
func (r *SessionContextSnapshotRepo) UpdateLatestTaskStatusBySourceMessageID(ctx context.Context, sourceMessageID string, status string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE session_context_snapshots
		SET latest_task_status = $2,
		    updated_at = now()
		WHERE latest_task_source_message_id = $1
		  AND latest_task_status IS DISTINCT FROM $2
	`, strings.TrimSpace(sourceMessageID), strings.TrimSpace(status))
	return err
}

// UpdateGroundingState 更新当前会话的 grounding 状态。
func (r *SessionContextSnapshotRepo) UpdateGroundingState(ctx context.Context, params UpdateGroundingStateParams) error {
	lastCitationWindowsJSON, err := marshalSnapshotJSON(params.LastCitationWindows, []byte("[]"))
	if err != nil {
		return err
	}
	lastEnumeratedEntitiesJSON, err := marshalSnapshotJSON(params.LastEnumeratedEntities, []byte("[]"))
	if err != nil {
		return err
	}
	ordinalReferenceFrameJSON, err := marshalSnapshotJSON(params.OrdinalReferenceFrame, []byte("[]"))
	if err != nil {
		return err
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO session_context_snapshots (
		    session_id,
		    active_section_id,
		    active_section_type,
		    active_entity_name,
		    last_citation_windows_json,
		    last_enumerated_entities_json,
		    ordinal_reference_frame_json
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb)
		ON CONFLICT (session_id) DO UPDATE
		SET active_section_id = EXCLUDED.active_section_id,
		    active_section_type = EXCLUDED.active_section_type,
		    active_entity_name = EXCLUDED.active_entity_name,
		    last_citation_windows_json = EXCLUDED.last_citation_windows_json,
		    last_enumerated_entities_json = EXCLUDED.last_enumerated_entities_json,
		    ordinal_reference_frame_json = EXCLUDED.ordinal_reference_frame_json,
		    updated_at = now()
	`,
		params.SessionID,
		params.ActiveSectionID,
		trimOptionalString(params.ActiveSectionType),
		trimOptionalString(params.ActiveEntityName),
		string(lastCitationWindowsJSON),
		string(lastEnumeratedEntitiesJSON),
		string(ordinalReferenceFrameJSON),
	)
	return err
}

func scanSessionContextSnapshot(row pgx.Row) (SessionContextSnapshotRecord, error) {
	var record SessionContextSnapshotRecord

	err := row.Scan(
		&record.SessionID,
		&record.ActiveResourceID,
		&record.ActiveResourceTitle,
		&record.ActiveResourceSourceType,
		&record.ActiveResourceSourceMessageID,
		&record.ActiveSectionID,
		&record.ActiveSectionType,
		&record.ActiveEntityName,
		&record.PendingTaskSuggestionMessageID,
		&record.PendingTaskInstruction,
		&record.LatestTaskID,
		&record.LatestTaskStatus,
		&record.LatestTaskSourceMessageID,
		&record.ConfirmedConstraintsJSON,
		&record.LastCitationWindowsJSON,
		&record.LastEnumeratedEntitiesJSON,
		&record.OrdinalReferenceFrameJSON,
		&record.RollingSummary,
		&record.SummaryBaseSequenceNo,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return SessionContextSnapshotRecord{}, err
	}

	return record, nil
}

func marshalSnapshotJSON(value any, emptyFallback []byte) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(body) == "null" {
		return emptyFallback, nil
	}

	return body, nil
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	return &trimmed
}
