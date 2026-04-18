package assistant

import (
	"context"
	"encoding/json"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestSessionContextProjectorInitializesEmptySnapshot 验证`sessionContextProjector`在写入或副作用路径下的行为，防止同类回归。
func TestSessionContextProjectorInitializesEmptySnapshot(t *testing.T) {
	repo := newFakeSessionContextSnapshotProjectorRepo()
	projector := NewSessionContextProjector(repo)

	if err := projector.InitSession(context.Background(), "session-1"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	snapshot := repo.mustGet("session-1")
	if snapshot.SessionID != "session-1" {
		t.Fatalf("expected session id %q, got %q", "session-1", snapshot.SessionID)
	}
	if string(snapshot.ConfirmedConstraintsJSON) != "[]" {
		t.Fatalf("expected confirmed constraints default [] , got %s", string(snapshot.ConfirmedConstraintsJSON))
	}
	if snapshot.SummaryBaseSequenceNo != 0 {
		t.Fatalf("expected summary_base_sequence_no 0, got %d", snapshot.SummaryBaseSequenceNo)
	}
}

// TestSessionContextProjectorProjectsReadySessionFile 验证`sessionContextProjector`在写入或副作用路径下的行为，防止同类回归。
func TestSessionContextProjectorProjectsReadySessionFile(t *testing.T) {
	repo := newFakeSessionContextSnapshotProjectorRepo()
	projector := NewSessionContextProjector(repo)
	ctx := context.Background()

	if err := projector.InitSession(ctx, "session-1"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	if err := projector.ProjectSessionFileReady(ctx, SessionFileReadyProjection{
		SessionID:       "session-1",
		ResourceID:      "resource-1",
		ResourceTitle:   "学生守则 v1",
		ResourceSource:  "upload",
		SourceMessageID: "message-1",
	}); err != nil {
		t.Fatalf("project first ready file: %v", err)
	}
	if err := projector.ProjectSessionFileReady(ctx, SessionFileReadyProjection{
		SessionID:       "session-1",
		ResourceID:      "resource-2",
		ResourceTitle:   "学生守则 v2",
		ResourceSource:  "upload",
		SourceMessageID: "message-2",
	}); err != nil {
		t.Fatalf("project second ready file: %v", err)
	}

	snapshot := repo.mustGet("session-1")
	if snapshot.ActiveResourceID == nil || *snapshot.ActiveResourceID != "resource-2" {
		t.Fatalf("expected active resource id %q, got %#v", "resource-2", snapshot.ActiveResourceID)
	}
	if snapshot.ActiveResourceTitle == nil || *snapshot.ActiveResourceTitle != "学生守则 v2" {
		t.Fatalf("expected active resource title overwrite, got %#v", snapshot.ActiveResourceTitle)
	}
	if snapshot.ActiveResourceSourceType == nil || *snapshot.ActiveResourceSourceType != "upload" {
		t.Fatalf("expected active resource source type %q, got %#v", "upload", snapshot.ActiveResourceSourceType)
	}
	if snapshot.ActiveResourceSourceMessageID == nil || *snapshot.ActiveResourceSourceMessageID != "message-2" {
		t.Fatalf("expected active resource source message id %q, got %#v", "message-2", snapshot.ActiveResourceSourceMessageID)
	}
}

// TestSessionContextProjectorProjectsTaskSuggestionAndTaskCreated 验证`sessionContextProjector`在写入或副作用路径下的行为，防止同类回归。
func TestSessionContextProjectorProjectsTaskSuggestionAndTaskCreated(t *testing.T) {
	repo := newFakeSessionContextSnapshotProjectorRepo()
	projector := NewSessionContextProjector(repo)
	ctx := context.Background()

	if err := projector.InitSession(ctx, "session-1"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	if err := projector.ProjectTaskSuggestionCreated(ctx, TaskSuggestionProjection{
		SessionID:   "session-1",
		MessageID:   "suggestion-1",
		Instruction: "请整理学生守则第二章",
	}); err != nil {
		t.Fatalf("project task suggestion: %v", err)
	}

	snapshot := repo.mustGet("session-1")
	if snapshot.PendingTaskSuggestionMessageID == nil || *snapshot.PendingTaskSuggestionMessageID != "suggestion-1" {
		t.Fatalf("expected pending suggestion message id %q, got %#v", "suggestion-1", snapshot.PendingTaskSuggestionMessageID)
	}
	if snapshot.PendingTaskInstruction == nil || *snapshot.PendingTaskInstruction != "请整理学生守则第二章" {
		t.Fatalf("expected pending task instruction to be stored, got %#v", snapshot.PendingTaskInstruction)
	}

	if err := projector.ProjectTaskCreated(ctx, TaskCreatedProjection{
		SessionID:       "session-1",
		TaskID:          "task-1",
		Status:          "pending",
		SourceMessageID: "suggestion-1",
	}); err != nil {
		t.Fatalf("project task created: %v", err)
	}

	snapshot = repo.mustGet("session-1")
	if snapshot.PendingTaskSuggestionMessageID != nil || snapshot.PendingTaskInstruction != nil {
		t.Fatalf("expected pending suggestion to be cleared after task created, got %#v", snapshot)
	}
	if snapshot.LatestTaskID == nil || *snapshot.LatestTaskID != "task-1" {
		t.Fatalf("expected latest task id %q, got %#v", "task-1", snapshot.LatestTaskID)
	}
	if snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != "pending" {
		t.Fatalf("expected latest task status %q, got %#v", "pending", snapshot.LatestTaskStatus)
	}
	if snapshot.LatestTaskSourceMessageID == nil || *snapshot.LatestTaskSourceMessageID != "suggestion-1" {
		t.Fatalf("expected latest task source message id %q, got %#v", "suggestion-1", snapshot.LatestTaskSourceMessageID)
	}
}

// TestSessionContextProjectorProjectsTaskStatusIdempotently 验证`sessionContextProjector`在写入或副作用路径下的行为，防止同类回归。
func TestSessionContextProjectorProjectsTaskStatusIdempotently(t *testing.T) {
	repo := newFakeSessionContextSnapshotProjectorRepo()
	projector := NewSessionContextProjector(repo)
	ctx := context.Background()

	if err := projector.InitSession(ctx, "session-1"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if err := projector.ProjectTaskCreated(ctx, TaskCreatedProjection{
		SessionID:       "session-1",
		TaskID:          "task-1",
		Status:          "pending",
		SourceMessageID: "suggestion-1",
	}); err != nil {
		t.Fatalf("project initial task created: %v", err)
	}

	beforeChanges := repo.changeCount
	if err := projector.ProjectTaskStatusChanged(ctx, stringPointer("suggestion-1"), "task-1", "executing"); err != nil {
		t.Fatalf("project task status change: %v", err)
	}
	if err := projector.ProjectTaskStatusChanged(ctx, stringPointer("suggestion-1"), "task-1", "executing"); err != nil {
		t.Fatalf("project duplicated task status change: %v", err)
	}
	if err := projector.ProjectTaskStatusChanged(ctx, nil, "task-1", "completed"); err != nil {
		t.Fatalf("project nil source message id should no-op: %v", err)
	}

	snapshot := repo.mustGet("session-1")
	if snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != "executing" {
		t.Fatalf("expected latest task status %q, got %#v", "executing", snapshot.LatestTaskStatus)
	}
	if repo.changeCount != beforeChanges+1 {
		t.Fatalf("expected only one effective state change after duplicated status projection, got changeCount=%d before=%d", repo.changeCount, beforeChanges)
	}
}

// TestSessionContextProjectorProjectsGroundingState 验证`sessionContextProjector`在写入或副作用路径下的行为，防止同类回归。
func TestSessionContextProjectorProjectsGroundingState(t *testing.T) {
	repo := newFakeSessionContextSnapshotProjectorRepo()
	projector := NewSessionContextProjector(repo)
	ctx := context.Background()

	if err := projector.InitSession(ctx, "session-1"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	if err := projector.ProjectGroundingState(ctx, GroundingStateProjection{
		SessionID:         "session-1",
		ActiveSectionID:   "section-campushub",
		ActiveSectionType: "project",
		ActiveEntityName:  "CampusHub",
		LastEnumeratedEntities: []postgres.EnumeratedEntity{
			{SectionID: "section-campushub", SectionType: "project", EntityName: "CampusHub", Ordinal: 1},
		},
		OrdinalReferenceFrame: []postgres.OrdinalReference{
			{Ordinal: 1, SectionID: "section-campushub", SectionType: "project", EntityName: "CampusHub"},
		},
	}); err != nil {
		t.Fatalf("project grounding state: %v", err)
	}

	snapshot := repo.mustGet("session-1")
	if snapshot.ActiveSectionID == nil || *snapshot.ActiveSectionID != "section-campushub" {
		t.Fatalf("expected active section id %q, got %#v", "section-campushub", snapshot.ActiveSectionID)
	}
	if snapshot.ActiveEntityName == nil || *snapshot.ActiveEntityName != "CampusHub" {
		t.Fatalf("expected active entity name %q, got %#v", "CampusHub", snapshot.ActiveEntityName)
	}
	if string(snapshot.OrdinalReferenceFrameJSON) == "[]" {
		t.Fatalf("expected ordinal reference frame to be stored, got %s", string(snapshot.OrdinalReferenceFrameJSON))
	}
}

// fakeSessionContextSnapshotProjectorRepo 作为会话上下文快照投影器仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeSessionContextSnapshotProjectorRepo struct {
	changeCount int
	records     map[string]*postgres.SessionContextSnapshotRecord
}

// newFakeSessionContextSnapshotProjectorRepo 为测试场景处理 `newFake会话上下文快照投影器仓储` 的辅助步骤，减少重复搭建逻辑。
func newFakeSessionContextSnapshotProjectorRepo() *fakeSessionContextSnapshotProjectorRepo {
	return &fakeSessionContextSnapshotProjectorRepo{
		records: make(map[string]*postgres.SessionContextSnapshotRecord),
	}
}

// CreateEmpty 实现测试替身需要的 `CreateEmpty` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) CreateEmpty(_ context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error) {
	if existing, ok := r.records[sessionID]; ok {
		return cloneSnapshotRecord(existing), nil
	}

	record := &postgres.SessionContextSnapshotRecord{
		SessionID:                sessionID,
		ConfirmedConstraintsJSON: []byte("[]"),
	}
	r.records[sessionID] = record
	r.changeCount++
	return cloneSnapshotRecord(record), nil
}

// GetBySessionID 实现测试替身需要的 `GetBySessionID` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) GetBySessionID(_ context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error) {
	record, ok := r.records[sessionID]
	if !ok {
		return nil, nil
	}

	return cloneSnapshotRecord(record), nil
}

// UpsertActiveResource 实现测试替身需要的 `UpsertActiveResource` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) UpsertActiveResource(_ context.Context, params postgres.UpsertActiveResourceParams) error {
	record := r.ensureRecord(params.SessionID)
	if sameOptionalString(record.ActiveResourceID, params.ResourceID) &&
		sameOptionalString(record.ActiveResourceTitle, params.ResourceTitle) &&
		sameOptionalString(record.ActiveResourceSourceType, params.ResourceSource) &&
		sameOptionalPointer(record.ActiveResourceSourceMessageID, params.SourceMessageID) {
		return nil
	}

	record.ActiveResourceID = stringPointer(params.ResourceID)
	record.ActiveResourceTitle = stringPointer(params.ResourceTitle)
	record.ActiveResourceSourceType = stringPointer(params.ResourceSource)
	record.ActiveResourceSourceMessageID = cloneStringPointer(params.SourceMessageID)
	r.changeCount++
	return nil
}

// UpsertPendingTaskSuggestion 实现测试替身需要的 `UpsertPendingTaskSuggestion` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) UpsertPendingTaskSuggestion(_ context.Context, params postgres.UpsertPendingTaskSuggestionParams) error {
	record := r.ensureRecord(params.SessionID)
	if sameOptionalString(record.PendingTaskSuggestionMessageID, params.MessageID) &&
		sameOptionalString(record.PendingTaskInstruction, params.Instruction) {
		return nil
	}

	record.PendingTaskSuggestionMessageID = stringPointer(params.MessageID)
	record.PendingTaskInstruction = stringPointer(params.Instruction)
	r.changeCount++
	return nil
}

// UpsertLatestTask 实现测试替身需要的 `UpsertLatestTask` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) UpsertLatestTask(_ context.Context, params postgres.UpsertLatestTaskParams) error {
	record := r.ensureRecord(params.SessionID)
	if sameOptionalString(record.LatestTaskID, params.TaskID) &&
		sameOptionalString(record.LatestTaskStatus, params.Status) &&
		sameOptionalPointer(record.LatestTaskSourceMessageID, params.SourceMessageID) {
		return nil
	}

	record.LatestTaskID = stringPointer(params.TaskID)
	record.LatestTaskStatus = stringPointer(params.Status)
	record.LatestTaskSourceMessageID = cloneStringPointer(params.SourceMessageID)
	r.changeCount++
	return nil
}

// ClearPendingTaskSuggestion 实现测试替身需要的 `ClearPendingTaskSuggestion` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) ClearPendingTaskSuggestion(_ context.Context, sessionID string) error {
	record := r.ensureRecord(sessionID)
	if record.PendingTaskSuggestionMessageID == nil && record.PendingTaskInstruction == nil {
		return nil
	}

	record.PendingTaskSuggestionMessageID = nil
	record.PendingTaskInstruction = nil
	r.changeCount++
	return nil
}

// UpdateLatestTaskStatusBySourceMessageID 实现测试替身需要的 `UpdateLatestTaskStatusBySourceMessageID` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) UpdateLatestTaskStatusBySourceMessageID(_ context.Context, sourceMessageID string, status string) error {
	for _, record := range r.records {
		if record.LatestTaskSourceMessageID == nil || *record.LatestTaskSourceMessageID != sourceMessageID {
			continue
		}
		if sameOptionalString(record.LatestTaskStatus, status) {
			return nil
		}

		record.LatestTaskStatus = stringPointer(status)
		r.changeCount++
		return nil
	}

	return nil
}

// UpdateGroundingState 实现测试替身需要的 `UpdateGroundingState` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) UpdateGroundingState(_ context.Context, params postgres.UpdateGroundingStateParams) error {
	record := r.ensureRecord(params.SessionID)
	record.ActiveSectionID = cloneStringPointer(params.ActiveSectionID)
	record.ActiveSectionType = cloneStringPointer(params.ActiveSectionType)
	record.ActiveEntityName = cloneStringPointer(params.ActiveEntityName)
	record.LastCitationWindowsJSON = mustMarshalGroundingJSON(params.LastCitationWindows)
	record.LastEnumeratedEntitiesJSON = mustMarshalGroundingJSON(params.LastEnumeratedEntities)
	record.OrdinalReferenceFrameJSON = mustMarshalGroundingJSON(params.OrdinalReferenceFrame)
	r.changeCount++
	return nil
}

// ensureRecord 实现测试替身需要的 `ensureRecord` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) ensureRecord(sessionID string) *postgres.SessionContextSnapshotRecord {
	if existing, ok := r.records[sessionID]; ok {
		return existing
	}

	record := &postgres.SessionContextSnapshotRecord{
		SessionID:                sessionID,
		ConfirmedConstraintsJSON: []byte("[]"),
	}
	r.records[sessionID] = record
	return record
}

// mustGet 实现测试替身需要的 `mustGet` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotProjectorRepo) mustGet(sessionID string) *postgres.SessionContextSnapshotRecord {
	record, ok := r.records[sessionID]
	if !ok {
		panic("snapshot not found: " + sessionID)
	}

	return record
}

// cloneSnapshotRecord 复制 `快照记录`，避免测试断言时共享可变引用。
func cloneSnapshotRecord(record *postgres.SessionContextSnapshotRecord) *postgres.SessionContextSnapshotRecord {
	if record == nil {
		return nil
	}

	cloned := *record
	cloned.ActiveResourceID = cloneStringPointer(record.ActiveResourceID)
	cloned.ActiveResourceTitle = cloneStringPointer(record.ActiveResourceTitle)
	cloned.ActiveResourceSourceType = cloneStringPointer(record.ActiveResourceSourceType)
	cloned.ActiveResourceSourceMessageID = cloneStringPointer(record.ActiveResourceSourceMessageID)
	cloned.ActiveSectionID = cloneStringPointer(record.ActiveSectionID)
	cloned.ActiveSectionType = cloneStringPointer(record.ActiveSectionType)
	cloned.ActiveEntityName = cloneStringPointer(record.ActiveEntityName)
	cloned.PendingTaskSuggestionMessageID = cloneStringPointer(record.PendingTaskSuggestionMessageID)
	cloned.PendingTaskInstruction = cloneStringPointer(record.PendingTaskInstruction)
	cloned.LatestTaskID = cloneStringPointer(record.LatestTaskID)
	cloned.LatestTaskStatus = cloneStringPointer(record.LatestTaskStatus)
	cloned.LatestTaskSourceMessageID = cloneStringPointer(record.LatestTaskSourceMessageID)
	if record.ConfirmedConstraintsJSON != nil {
		cloned.ConfirmedConstraintsJSON = append([]byte(nil), record.ConfirmedConstraintsJSON...)
	}
	if record.LastCitationWindowsJSON != nil {
		cloned.LastCitationWindowsJSON = append([]byte(nil), record.LastCitationWindowsJSON...)
	}
	if record.LastEnumeratedEntitiesJSON != nil {
		cloned.LastEnumeratedEntitiesJSON = append([]byte(nil), record.LastEnumeratedEntitiesJSON...)
	}
	if record.OrdinalReferenceFrameJSON != nil {
		cloned.OrdinalReferenceFrameJSON = append([]byte(nil), record.OrdinalReferenceFrameJSON...)
	}
	cloned.RollingSummary = cloneStringPointer(record.RollingSummary)
	return &cloned
}

// cloneStringPointer 复制 `StringPointer`，避免测试断言时共享可变引用。
func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// sameOptionalString 比较 `OptionalString` 是否一致，方便测试断言可选值语义。
func sameOptionalString(current *string, next string) bool {
	if current == nil {
		return next == ""
	}

	return *current == next
}

// sameOptionalPointer 比较 `OptionalPointer` 是否一致，方便测试断言可选值语义。
func sameOptionalPointer(current *string, next *string) bool {
	if current == nil || next == nil {
		return current == nil && next == nil
	}

	return *current == *next
}

// mustMarshalGroundingJSON 在测试里强制构造 `MarshalgroundingJSON`，失败时立即终止当前用例。
func mustMarshalGroundingJSON(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}
