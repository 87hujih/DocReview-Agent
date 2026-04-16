package assistant

import (
	"encoding/json"

	"agent_project/apps/server/internal/storage/postgres"
)

// ConfirmedConstraint 表示用户已经明确确认、允许沉淀到快照中的约束。
type ConfirmedConstraint struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SnapshotActiveResource 表示当前会话活跃资源的快照视图。
type SnapshotActiveResource struct {
	ID              string
	Title           string
	SourceType      string
	SourceMessageID string
}

// SnapshotPendingTaskSuggestion 表示当前待确认任务建议的快照视图。
type SnapshotPendingTaskSuggestion struct {
	MessageID   string
	Instruction string
}

// SnapshotLatestTask 表示最近已创建任务的快照视图。
type SnapshotLatestTask struct {
	ID              string
	Status          string
	SourceMessageID string
}

// SessionContextSnapshot 表示助手回复所需的结构化会话上下文快照。
type SessionContextSnapshot struct {
	SessionID             string
	ActiveResource        *SnapshotActiveResource
	PendingTaskSuggestion *SnapshotPendingTaskSuggestion
	LatestTask            *SnapshotLatestTask
	ConfirmedConstraints  []ConfirmedConstraint
	RollingSummary        *string
	SummaryBaseSequenceNo int
}

// SessionContextSnapshotFromRecord 把 PostgreSQL 记录转换成领域快照。
func SessionContextSnapshotFromRecord(record *postgres.SessionContextSnapshotRecord) (*SessionContextSnapshot, error) {
	if record == nil {
		return nil, nil
	}

	constraints := make([]ConfirmedConstraint, 0)
	if len(record.ConfirmedConstraintsJSON) > 0 {
		if err := json.Unmarshal(record.ConfirmedConstraintsJSON, &constraints); err != nil {
			return nil, err
		}
	}

	snapshot := &SessionContextSnapshot{
		SessionID:             record.SessionID,
		ConfirmedConstraints:  constraints,
		RollingSummary:        cloneOptionalString(record.RollingSummary),
		SummaryBaseSequenceNo: record.SummaryBaseSequenceNo,
	}

	if record.ActiveResourceID != nil && record.ActiveResourceTitle != nil && record.ActiveResourceSourceType != nil {
		snapshot.ActiveResource = &SnapshotActiveResource{
			ID:         *record.ActiveResourceID,
			Title:      *record.ActiveResourceTitle,
			SourceType: *record.ActiveResourceSourceType,
		}
		if record.ActiveResourceSourceMessageID != nil {
			snapshot.ActiveResource.SourceMessageID = *record.ActiveResourceSourceMessageID
		}
	}

	if record.PendingTaskSuggestionMessageID != nil && record.PendingTaskInstruction != nil {
		snapshot.PendingTaskSuggestion = &SnapshotPendingTaskSuggestion{
			MessageID:   *record.PendingTaskSuggestionMessageID,
			Instruction: *record.PendingTaskInstruction,
		}
	}

	if record.LatestTaskID != nil && record.LatestTaskStatus != nil {
		snapshot.LatestTask = &SnapshotLatestTask{
			ID:     *record.LatestTaskID,
			Status: *record.LatestTaskStatus,
		}
		if record.LatestTaskSourceMessageID != nil {
			snapshot.LatestTask.SourceMessageID = *record.LatestTaskSourceMessageID
		}
	}

	return snapshot, nil
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}
