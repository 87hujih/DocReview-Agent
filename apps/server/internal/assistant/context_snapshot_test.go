package assistant

import (
	"encoding/json"
	"reflect"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestSessionContextSnapshotFromRecordDecodesAdvisorState 验证快照记录中的 advisor state 能被稳定解码并恢复。
func TestSessionContextSnapshotFromRecordDecodesAdvisorState(t *testing.T) {
	record := &postgres.SessionContextSnapshotRecord{
		SessionID:                  "session-1",
		ConfirmedConstraintsJSON:   []byte("[]"),
		LastCitationWindowsJSON:    []byte("[]"),
		LastEnumeratedEntitiesJSON: []byte("[]"),
		OrdinalReferenceFrameJSON:  []byte("[]"),
	}
	mustSetBytesField(t, record, "PendingClarificationJSON", mustMarshalJSON(t, map[string]any{
		"kind":             "execution_confirmation",
		"question":         "要不要按这个方向直接改？",
		"asked_message_id": "message-clarify-1",
		"options":          []string{"先分析", "直接修改"},
	}))
	mustSetBytesField(t, record, "AdvisoryContextJSON", mustMarshalJSON(t, map[string]any{
		"diagnosis":           "第三个项目缺少量化结果。",
		"recommendations":     []string{"补结果", "补指标"},
		"preferred_direction": "按结果导向重写",
		"source_message_id":   "message-advice-1",
	}))
	mustSetBytesField(t, record, "PendingProposalJSON", mustMarshalJSON(t, map[string]any{
		"proposal_id":                     "proposal-1",
		"instruction":                     "把第三个项目改成问题-动作-结果结构",
		"plan_goal":                       "产出可执行的简历改写任务",
		"proposed_message_id":             "message-proposal-1",
		"requires_explicit_authorization": true,
	}))
	mustSetBytesField(t, record, "AuthorizationStateJSON", mustMarshalJSON(t, map[string]any{
		"status":                  "pending",
		"granted_for_proposal_id": "proposal-1",
		"granted_by_message_id":   "message-authorize-1",
	}))
	mustSetBytesField(t, record, "ExecutionStateJSON", mustMarshalJSON(t, map[string]any{
		"task_id":            "task-1",
		"task_status":        "planning",
		"source_proposal_id": "proposal-1",
		"started_at":         "2026-04-20T08:00:00Z",
	}))

	snapshot, err := SessionContextSnapshotFromRecord(record)
	if err != nil {
		t.Fatalf("snapshot from record: %v", err)
	}

	pendingClarification := mustReadPointerStructField(t, snapshot, "PendingClarification")
	if got := mustReadStringField(t, pendingClarification, "Kind"); got != "execution_confirmation" {
		t.Fatalf("expected pending clarification kind %q, got %q", "execution_confirmation", got)
	}
	if got := mustReadStringField(t, pendingClarification, "Question"); got != "要不要按这个方向直接改？" {
		t.Fatalf("expected pending clarification question, got %q", got)
	}
	if got := mustReadStringSliceField(t, pendingClarification, "Options"); len(got) != 2 || got[1] != "直接修改" {
		t.Fatalf("expected pending clarification options, got %#v", got)
	}

	advisoryContext := mustReadPointerStructField(t, snapshot, "AdvisoryContext")
	if got := mustReadStringField(t, advisoryContext, "Diagnosis"); got != "第三个项目缺少量化结果。" {
		t.Fatalf("expected advisory diagnosis, got %q", got)
	}
	if got := mustReadStringSliceField(t, advisoryContext, "Recommendations"); len(got) != 2 || got[0] != "补结果" {
		t.Fatalf("expected advisory recommendations, got %#v", got)
	}

	pendingProposal := mustReadPointerStructField(t, snapshot, "PendingProposal")
	if got := mustReadStringField(t, pendingProposal, "ProposalID"); got != "proposal-1" {
		t.Fatalf("expected proposal id %q, got %q", "proposal-1", got)
	}
	if !mustReadBoolField(t, pendingProposal, "RequiresExplicitAuthorization") {
		t.Fatal("expected pending proposal to require explicit authorization")
	}

	authorizationState := mustReadPointerStructField(t, snapshot, "AuthorizationState")
	if got := mustReadStringField(t, authorizationState, "Status"); got != "pending" {
		t.Fatalf("expected authorization status %q, got %q", "pending", got)
	}

	executionState := mustReadPointerStructField(t, snapshot, "ExecutionState")
	if got := mustReadStringField(t, executionState, "TaskID"); got != "task-1" {
		t.Fatalf("expected execution task id %q, got %q", "task-1", got)
	}
}

// TestSessionContextSnapshotFromRecordBuildsNodeAwareGroundingState 验证 legacy grounding 字段会同步恢复为 node-aware 视图。
func TestSessionContextSnapshotFromRecordBuildsNodeAwareGroundingState(t *testing.T) {
	record := &postgres.SessionContextSnapshotRecord{
		SessionID:                  "session-node-1",
		ConfirmedConstraintsJSON:   []byte("[]"),
		LastCitationWindowsJSON:    []byte("[]"),
		LastEnumeratedEntitiesJSON: []byte("[]"),
		OrdinalReferenceFrameJSON: mustMarshalJSON(t, []map[string]any{
			{
				"ordinal":      3,
				"section_id":   "project-3",
				"section_type": "project_item",
				"entity_name":  "慢跑计划",
			},
		}),
	}
	record.ActiveSectionID = stringPointer("project-3")
	record.ActiveSectionType = stringPointer("project_item")

	snapshot, err := SessionContextSnapshotFromRecord(record)
	if err != nil {
		t.Fatalf("snapshot from record: %v", err)
	}
	if snapshot.ActiveNode == nil || snapshot.ActiveNode.ID != "project-3" || snapshot.ActiveNode.Kind != "project_item" {
		t.Fatalf("expected active node to be restored from legacy grounding fields, got %#v", snapshot.ActiveNode)
	}
	if len(snapshot.NodeReferenceFrame) != 1 {
		t.Fatalf("expected node reference frame to be restored, got %#v", snapshot.NodeReferenceFrame)
	}
	if snapshot.NodeReferenceFrame[0].NodeID != "project-3" || snapshot.NodeReferenceFrame[0].NodeKind != "project_item" {
		t.Fatalf("expected node reference mapping, got %#v", snapshot.NodeReferenceFrame)
	}
}

// mustMarshalJSON 在测试里强制构造 JSON，失败时立即终止当前用例。
func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return body
}

// mustSetBytesField 通过反射为结构体字段写入字节切片，避免测试与未实现字段产生编译耦合。
func mustSetBytesField(t *testing.T, target any, fieldName string, value []byte) {
	t.Helper()

	field := mustStructFieldValue(t, target, fieldName)
	if field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.Uint8 {
		t.Fatalf("expected field %s to be []byte, got %s", fieldName, field.Type())
	}

	field.SetBytes(value)
}

// mustSetPointerStructField 通过反射为结构体指针字段写入嵌套结构，避免测试与未实现字段产生编译耦合。
func mustSetPointerStructField(t *testing.T, target any, fieldName string, fields map[string]any) {
	t.Helper()

	field := mustStructFieldValue(t, target, fieldName)
	if field.Kind() != reflect.Pointer || field.Type().Elem().Kind() != reflect.Struct {
		t.Fatalf("expected field %s to be pointer to struct, got %s", fieldName, field.Type())
	}

	nested := reflect.New(field.Type().Elem())
	fillStructFields(t, nested.Elem(), fields)
	field.Set(nested)
}

// mustStructFieldValue 读取结构体字段，找不到字段时直接让测试失败。
func mustStructFieldValue(t *testing.T, target any, fieldName string) reflect.Value {
	t.Helper()

	var value reflect.Value
	if reflected, ok := target.(reflect.Value); ok {
		value = reflected
	} else {
		value = reflect.ValueOf(target)
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			t.Fatalf("target for field %s is nil", fieldName)
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		t.Fatalf("expected struct target for field %s, got %s", fieldName, value.Type())
	}

	field := value.FieldByName(fieldName)
	if !field.IsValid() {
		t.Fatalf("expected field %s on %T", fieldName, target)
	}

	return field
}

// fillStructFields 为反射构造出的结构体填充值，统一不同测试的嵌套组装逻辑。
func fillStructFields(t *testing.T, target reflect.Value, fields map[string]any) {
	t.Helper()

	if target.Kind() != reflect.Struct {
		t.Fatalf("expected struct target, got %s", target.Type())
	}

	for fieldName, rawValue := range fields {
		field := target.FieldByName(fieldName)
		if !field.IsValid() {
			t.Fatalf("expected nested field %s on %s", fieldName, target.Type())
		}

		switch field.Kind() {
		case reflect.String:
			stringValue, ok := rawValue.(string)
			if !ok {
				t.Fatalf("expected string for field %s, got %T", fieldName, rawValue)
			}
			field.SetString(stringValue)
		case reflect.Bool:
			boolValue, ok := rawValue.(bool)
			if !ok {
				t.Fatalf("expected bool for field %s, got %T", fieldName, rawValue)
			}
			field.SetBool(boolValue)
		case reflect.Slice:
			stringSlice, ok := rawValue.([]string)
			if !ok || field.Type().Elem().Kind() != reflect.String {
				t.Fatalf("expected []string for field %s, got %T", fieldName, rawValue)
			}
			field.Set(reflect.ValueOf(stringSlice))
		default:
			t.Fatalf("unsupported field kind %s on %s", field.Kind(), fieldName)
		}
	}
}

// mustReadPointerStructField 读取结构体里的指针结构体字段，并要求它已经被设置。
func mustReadPointerStructField(t *testing.T, target any, fieldName string) reflect.Value {
	t.Helper()

	field := mustStructFieldValue(t, target, fieldName)
	if field.Kind() != reflect.Pointer || field.Type().Elem().Kind() != reflect.Struct {
		t.Fatalf("expected field %s to be pointer to struct, got %s", fieldName, field.Type())
	}
	if field.IsNil() {
		t.Fatalf("expected field %s to be populated", fieldName)
	}

	return field.Elem()
}

// mustReadStringField 读取结构体中的字符串字段。
func mustReadStringField(t *testing.T, target any, fieldName string) string {
	t.Helper()

	field := mustStructFieldValue(t, target, fieldName)
	if field.Kind() != reflect.String {
		t.Fatalf("expected field %s to be string, got %s", fieldName, field.Type())
	}

	return field.String()
}

// mustReadBoolField 读取结构体中的布尔字段。
func mustReadBoolField(t *testing.T, target any, fieldName string) bool {
	t.Helper()

	field := mustStructFieldValue(t, target, fieldName)
	if field.Kind() != reflect.Bool {
		t.Fatalf("expected field %s to be bool, got %s", fieldName, field.Type())
	}

	return field.Bool()
}

// mustReadStringSliceField 读取结构体中的字符串切片字段。
func mustReadStringSliceField(t *testing.T, target any, fieldName string) []string {
	t.Helper()

	field := mustStructFieldValue(t, target, fieldName)
	if field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.String {
		t.Fatalf("expected field %s to be []string, got %s", fieldName, field.Type())
	}

	result := make([]string, field.Len())
	for index := 0; index < field.Len(); index++ {
		result[index] = field.Index(index).String()
	}

	return result
}
