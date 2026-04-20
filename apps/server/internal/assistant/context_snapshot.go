package assistant

import (
	"encoding/json"
	"strings"

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

// SnapshotPendingClarification 表示当前等待用户回答的澄清问题。
type SnapshotPendingClarification struct {
	Kind           string   `json:"kind,omitempty"`
	Question       string   `json:"question,omitempty"`
	AskedMessageID string   `json:"asked_message_id,omitempty"`
	Options        []string `json:"options,omitempty"`
}

// SnapshotAdvisoryContext 表示 assistant 已形成的顾问结论。
type SnapshotAdvisoryContext struct {
	Diagnosis          string   `json:"diagnosis,omitempty"`
	Recommendations    []string `json:"recommendations,omitempty"`
	PreferredDirection string   `json:"preferred_direction,omitempty"`
	SourceMessageID    string   `json:"source_message_id,omitempty"`
}

// SnapshotPendingProposal 表示 assistant 已形成但尚未获授权的 proposal。
type SnapshotPendingProposal struct {
	ProposalID                    string `json:"proposal_id,omitempty"`
	Instruction                   string `json:"instruction,omitempty"`
	PlanGoal                      string `json:"plan_goal,omitempty"`
	ProposedMessageID             string `json:"proposed_message_id,omitempty"`
	RequiresExplicitAuthorization bool   `json:"requires_explicit_authorization,omitempty"`
}

// SnapshotAuthorizationState 表示 proposal 当前的授权状态。
type SnapshotAuthorizationState struct {
	Status               string `json:"status,omitempty"`
	GrantedForProposalID string `json:"granted_for_proposal_id,omitempty"`
	GrantedByMessageID   string `json:"granted_by_message_id,omitempty"`
}

// SnapshotExecutionState 表示 proposal 是否已经真正进入任务流。
type SnapshotExecutionState struct {
	TaskID           string `json:"task_id,omitempty"`
	TaskStatus       string `json:"task_status,omitempty"`
	SourceProposalID string `json:"source_proposal_id,omitempty"`
	StartedAt        string `json:"started_at,omitempty"`
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

// SnapshotActiveNode 表示当前会话聚焦的稳定 outline node。
type SnapshotActiveNode struct {
	ID   string
	Kind string
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

// NodeReference 表示 ordinal 到 outline node 的映射。
type NodeReference struct {
	Ordinal    int    `json:"ordinal"`
	NodeID     string `json:"node_id"`
	NodeKind   string `json:"node_kind,omitempty"`
	EntityName string `json:"entity_name,omitempty"`
}

// SessionContextSnapshot 表示助手回复所需的结构化会话上下文快照。
type SessionContextSnapshot struct {
	SessionID              string
	ActiveResource         *SnapshotActiveResource
	ActiveSection          *SnapshotActiveSection
	ActiveNode             *SnapshotActiveNode
	ActiveEntityName       *string
	PendingClarification   *SnapshotPendingClarification
	AdvisoryContext        *SnapshotAdvisoryContext
	PendingProposal        *SnapshotPendingProposal
	AuthorizationState     *SnapshotAuthorizationState
	ExecutionState         *SnapshotExecutionState
	PendingTaskSuggestion  *SnapshotPendingTaskSuggestion
	LatestTask             *SnapshotLatestTask
	ConfirmedConstraints   []ConfirmedConstraint
	LastCitationWindows    []CitationWindow
	LastEnumeratedEntities []EnumeratedEntity
	OrdinalReferenceFrame  []OrdinalReference
	NodeReferenceFrame     []NodeReference
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
	pendingClarification, err := decodeSnapshotObject(record.PendingClarificationJSON, normalizePendingClarification)
	if err != nil {
		return nil, err
	}
	advisoryContext, err := decodeSnapshotObject(record.AdvisoryContextJSON, normalizeAdvisoryContext)
	if err != nil {
		return nil, err
	}
	pendingProposal, err := decodeSnapshotObject(record.PendingProposalJSON, normalizePendingProposal)
	if err != nil {
		return nil, err
	}
	authorizationState, err := decodeSnapshotObject(record.AuthorizationStateJSON, normalizeAuthorizationState)
	if err != nil {
		return nil, err
	}
	executionState, err := decodeSnapshotObject(record.ExecutionStateJSON, normalizeExecutionState)
	if err != nil {
		return nil, err
	}

	snapshot := &SessionContextSnapshot{
		SessionID:              record.SessionID,
		ActiveEntityName:       cloneOptionalString(record.ActiveEntityName),
		PendingClarification:   pendingClarification,
		AdvisoryContext:        advisoryContext,
		PendingProposal:        pendingProposal,
		AuthorizationState:     authorizationState,
		ExecutionState:         executionState,
		ConfirmedConstraints:   constraints,
		LastCitationWindows:    lastCitationWindows,
		LastEnumeratedEntities: lastEnumeratedEntities,
		OrdinalReferenceFrame:  ordinalReferenceFrame,
		NodeReferenceFrame:     buildNodeReferenceFrame(ordinalReferenceFrame),
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
		snapshot.ActiveNode = &SnapshotActiveNode{
			ID:   *record.ActiveSectionID,
			Kind: strings.TrimSpace(valueOrEmpty(record.ActiveSectionType)),
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

func buildNodeReferenceFrame(frame []OrdinalReference) []NodeReference {
	if len(frame) == 0 {
		return nil
	}

	nodes := make([]NodeReference, 0, len(frame))
	for _, reference := range frame {
		nodeID := strings.TrimSpace(reference.SectionID)
		if nodeID == "" {
			continue
		}
		nodes = append(nodes, NodeReference{
			Ordinal:    reference.Ordinal,
			NodeID:     nodeID,
			NodeKind:   strings.TrimSpace(reference.SectionType),
			EntityName: strings.TrimSpace(reference.EntityName),
		})
	}

	return nodes
}

// cloneOptionalString 复制可选字符串指针，避免快照复制时共享底层引用。
func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

// decodeSnapshotObject 解码对象型快照 JSON，并把空对象归一化为 nil。
func decodeSnapshotObject[T any](body []byte, normalizer func(*T) *T) (*T, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	var value T
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, err
	}

	return normalizer(&value), nil
}

// normalizePendingClarification 归一化待确认澄清状态，避免空对象继续向后传播。
func normalizePendingClarification(value *SnapshotPendingClarification) *SnapshotPendingClarification {
	if value == nil {
		return nil
	}

	value.Kind = strings.TrimSpace(value.Kind)
	value.Question = strings.TrimSpace(value.Question)
	value.AskedMessageID = strings.TrimSpace(value.AskedMessageID)
	value.Options = normalizeStringSlice(value.Options)
	if value.Kind == "" && value.Question == "" && value.AskedMessageID == "" && len(value.Options) == 0 {
		return nil
	}

	return value
}

// normalizeAdvisoryContext 归一化顾问结论状态，避免空对象继续向后传播。
func normalizeAdvisoryContext(value *SnapshotAdvisoryContext) *SnapshotAdvisoryContext {
	if value == nil {
		return nil
	}

	value.Diagnosis = strings.TrimSpace(value.Diagnosis)
	value.Recommendations = normalizeStringSlice(value.Recommendations)
	value.PreferredDirection = strings.TrimSpace(value.PreferredDirection)
	value.SourceMessageID = strings.TrimSpace(value.SourceMessageID)
	if value.Diagnosis == "" && len(value.Recommendations) == 0 && value.PreferredDirection == "" && value.SourceMessageID == "" {
		return nil
	}

	return value
}

// normalizePendingProposal 归一化待确认 proposal，避免空对象继续向后传播。
func normalizePendingProposal(value *SnapshotPendingProposal) *SnapshotPendingProposal {
	if value == nil {
		return nil
	}

	value.ProposalID = strings.TrimSpace(value.ProposalID)
	value.Instruction = strings.TrimSpace(value.Instruction)
	value.PlanGoal = strings.TrimSpace(value.PlanGoal)
	value.ProposedMessageID = strings.TrimSpace(value.ProposedMessageID)
	if value.ProposalID == "" && value.Instruction == "" && value.PlanGoal == "" && value.ProposedMessageID == "" && !value.RequiresExplicitAuthorization {
		return nil
	}

	return value
}

// normalizeAuthorizationState 归一化授权状态，避免空对象继续向后传播。
func normalizeAuthorizationState(value *SnapshotAuthorizationState) *SnapshotAuthorizationState {
	if value == nil {
		return nil
	}

	value.Status = strings.TrimSpace(value.Status)
	value.GrantedForProposalID = strings.TrimSpace(value.GrantedForProposalID)
	value.GrantedByMessageID = strings.TrimSpace(value.GrantedByMessageID)
	if value.Status == "" && value.GrantedForProposalID == "" && value.GrantedByMessageID == "" {
		return nil
	}

	return value
}

// normalizeExecutionState 归一化执行状态，避免空对象继续向后传播。
func normalizeExecutionState(value *SnapshotExecutionState) *SnapshotExecutionState {
	if value == nil {
		return nil
	}

	value.TaskID = strings.TrimSpace(value.TaskID)
	value.TaskStatus = strings.TrimSpace(value.TaskStatus)
	value.SourceProposalID = strings.TrimSpace(value.SourceProposalID)
	value.StartedAt = strings.TrimSpace(value.StartedAt)
	if value.TaskID == "" && value.TaskStatus == "" && value.SourceProposalID == "" && value.StartedAt == "" {
		return nil
	}

	return value
}

// normalizeStringSlice 清理字符串切片里的空值，避免空元素继续向后传播。
func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}
