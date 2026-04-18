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

// SnapshotActiveSection 表示当前会话聚焦的逻辑 section。
type SnapshotActiveSection struct {
	ID   string
	Type string
}

// CitationWindow 表示最近一次回答中命中的 section 证据窗口。
type CitationWindow struct {
	SectionID     string `json:"section_id"`
	SectionType   string `json:"section_type,omitempty"`
	WindowGroupID string `json:"window_group_id,omitempty"`
}

// EnumeratedEntity 表示最近一次 assistant 列举出的实体项。
type EnumeratedEntity struct {
	SectionID   string `json:"section_id"`
	SectionType string `json:"section_type,omitempty"`
	EntityName  string `json:"entity_name,omitempty"`
	Ordinal     int    `json:"ordinal,omitempty"`
}

// OrdinalReference 表示 ordinal 到 section 的映射。
type OrdinalReference struct {
	Ordinal     int    `json:"ordinal"`
	SectionID   string `json:"section_id"`
	SectionType string `json:"section_type,omitempty"`
	EntityName  string `json:"entity_name,omitempty"`
}

// SessionContextSnapshot 表示助手回复所需的结构化会话上下文快照。
type SessionContextSnapshot struct {
	SessionID              string
	ActiveResource         *SnapshotActiveResource
	ActiveSection          *SnapshotActiveSection
	ActiveEntityName       *string
	PendingTaskSuggestion  *SnapshotPendingTaskSuggestion
	LatestTask             *SnapshotLatestTask
	ConfirmedConstraints   []ConfirmedConstraint
	LastCitationWindows    []CitationWindow
	LastEnumeratedEntities []EnumeratedEntity
	OrdinalReferenceFrame  []OrdinalReference
	RollingSummary         *string
	SummaryBaseSequenceNo  int
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
	lastCitationWindows := make([]CitationWindow, 0)
	if len(record.LastCitationWindowsJSON) > 0 {
		if err := json.Unmarshal(record.LastCitationWindowsJSON, &lastCitationWindows); err != nil {
			return nil, err
		}
	}
	lastEnumeratedEntities := make([]EnumeratedEntity, 0)
	if len(record.LastEnumeratedEntitiesJSON) > 0 {
		if err := json.Unmarshal(record.LastEnumeratedEntitiesJSON, &lastEnumeratedEntities); err != nil {
			return nil, err
		}
	}
	ordinalReferenceFrame := make([]OrdinalReference, 0)
	if len(record.OrdinalReferenceFrameJSON) > 0 {
		if err := json.Unmarshal(record.OrdinalReferenceFrameJSON, &ordinalReferenceFrame); err != nil {
			return nil, err
		}
	}

	snapshot := &SessionContextSnapshot{
		SessionID:              record.SessionID,
		ActiveEntityName:       cloneOptionalString(record.ActiveEntityName),
		ConfirmedConstraints:   constraints,
		LastCitationWindows:    lastCitationWindows,
		LastEnumeratedEntities: lastEnumeratedEntities,
		OrdinalReferenceFrame:  ordinalReferenceFrame,
		RollingSummary:         cloneOptionalString(record.RollingSummary),
		SummaryBaseSequenceNo:  record.SummaryBaseSequenceNo,
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

	if record.ActiveSectionID != nil {
		snapshot.ActiveSection = &SnapshotActiveSection{
			ID: *record.ActiveSectionID,
		}
		if record.ActiveSectionType != nil {
			snapshot.ActiveSection.Type = *record.ActiveSectionType
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

// cloneOptionalString 复制可选字符串指针，避免快照复制时共享底层引用。
func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}
