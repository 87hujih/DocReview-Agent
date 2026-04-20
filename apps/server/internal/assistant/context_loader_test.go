package assistant

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
)

// TestContextLoaderUsesSnapshotBeforeHistoryFallback 验证`contextLoader`在依赖选择路径下的行为，防止同类回归。
func TestContextLoaderUsesSnapshotBeforeHistoryFallback(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{
				CitationID:   "cite-snapshot",
				ResourceID:   "resource-snapshot",
				SectionTitle: "第二章",
				Snippet:      "这是快照资源命中的片段。",
			},
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                "session-1",
			ActiveResourceID:         stringPointer("resource-snapshot"),
			ActiveResourceTitle:      stringPointer("快照资源"),
			ActiveResourceSourceType: stringPointer("upload"),
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}, retriever)

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", []postgres.AssistantMessage{
		{
			ID:         "message-history-file",
			SessionID:  "session-1",
			Role:       RoleAssistant,
			Kind:       KindSessionFile,
			SequenceNo: 1,
			Payload: mustJSON(t, SessionFilePayload{
				FileName:      "旧文件.md",
				ResourceID:    "resource-history",
				ResourceTitle: "历史资源",
				SourceType:    "upload",
				Status:        "ready",
			}),
			CreatedAt: time.Now(),
		},
	}, "请总结第二章")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}

	if replyContext.Snapshot == nil || replyContext.Snapshot.ActiveResource == nil {
		t.Fatalf("expected snapshot active resource, got %#v", replyContext.Snapshot)
	}
	if replyContext.ActiveResource == nil || replyContext.ActiveResource.ID != "resource-snapshot" {
		t.Fatalf("expected snapshot resource to win, got %#v", replyContext.ActiveResource)
	}
	if retriever.calls != 1 || retriever.resourceID != "resource-snapshot" {
		t.Fatalf("expected retriever to use snapshot resource, got %#v", retriever)
	}
}

// TestContextLoaderFallsBackToLatestResourceFromMessages 验证`contextLoader`在回退路径下的行为，防止同类回归。
func TestContextLoaderFallsBackToLatestResourceFromMessages(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{
				CitationID:   "cite-history",
				ResourceID:   "resource-history",
				SectionTitle: "第三章",
				Snippet:      "这是历史资源命中的片段。",
			},
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{}, retriever)

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", []postgres.AssistantMessage{
		{
			ID:         "message-history-file",
			SessionID:  "session-1",
			Role:       RoleAssistant,
			Kind:       KindSessionFile,
			SequenceNo: 1,
			Payload: mustJSON(t, SessionFilePayload{
				FileName:      "学生手册.md",
				ResourceID:    "resource-history",
				ResourceTitle: "学生手册",
				SourceType:    "upload",
				Status:        "ready",
			}),
			CreatedAt: time.Now(),
		},
	}, "请总结第三章")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}

	if replyContext.Snapshot != nil {
		t.Fatalf("expected no snapshot, got %#v", replyContext.Snapshot)
	}
	if replyContext.ActiveResource == nil || replyContext.ActiveResource.ID != "resource-history" {
		t.Fatalf("expected history fallback resource, got %#v", replyContext.ActiveResource)
	}
	if retriever.calls != 1 || retriever.resourceID != "resource-history" {
		t.Fatalf("expected retriever to use history fallback resource, got %#v", retriever)
	}
}

// TestContextLoaderFallsBackWhenSnapshotActiveResourceIsIncomplete 验证`contextLoader`在回退路径下的行为，防止同类回归。
func TestContextLoaderFallsBackWhenSnapshotActiveResourceIsIncomplete(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{
				CitationID:   "cite-history",
				ResourceID:   "resource-history",
				SectionTitle: "第四章",
				Snippet:      "这是回退后的资源片段。",
			},
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                "session-1",
			ActiveResourceID:         stringPointer("resource-snapshot"),
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}, retriever)

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", []postgres.AssistantMessage{
		{
			ID:         "message-history-file",
			SessionID:  "session-1",
			Role:       RoleAssistant,
			Kind:       KindSessionFile,
			SequenceNo: 1,
			Payload: mustJSON(t, SessionFilePayload{
				FileName:      "当前资料.md",
				ResourceID:    "resource-history",
				ResourceTitle: "当前资料",
				SourceType:    "upload",
				Status:        "ready",
			}),
			CreatedAt: time.Now(),
		},
	}, "请总结第四章")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}

	if replyContext.Snapshot == nil {
		t.Fatal("expected snapshot to be loaded")
	}
	if replyContext.ActiveResource == nil || replyContext.ActiveResource.ID != "resource-history" {
		t.Fatalf("expected incomplete snapshot to fall back to history resource, got %#v", replyContext.ActiveResource)
	}
	if retriever.calls != 1 || retriever.resourceID != "resource-history" {
		t.Fatalf("expected retriever to use history fallback resource, got %#v", retriever)
	}
}

// TestContextLoaderLoadsRecentTextTurnsAfterSummaryBase 验证`contextLoader`在依赖选择路径下的行为，防止同类回归。
func TestContextLoaderLoadsRecentTextTurnsAfterSummaryBase(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{}
	messageReader := &fakeAssistantMessageWindowReader{
		messages: []postgres.AssistantMessage{
			{
				ID:         "message-3",
				SessionID:  "session-1",
				Role:       RoleAssistant,
				Kind:       KindSessionFile,
				SequenceNo: 3,
				Payload:    mustJSON(t, SessionFilePayload{FileName: "students.md", ResourceID: "resource-snapshot", ResourceTitle: "快照资源", SourceType: "upload", Status: "ready"}),
				CreatedAt:  time.Now(),
			},
			{
				ID:         "message-4",
				SessionID:  "session-1",
				Role:       RoleUser,
				Kind:       KindText,
				SequenceNo: 4,
				Payload:    mustJSON(t, TextPayload{Content: "请继续整理第二章"}),
				CreatedAt:  time.Now(),
			},
			{
				ID:         "message-5",
				SessionID:  "session-1",
				Role:       RoleAssistant,
				Kind:       KindText,
				SequenceNo: 5,
				Payload:    mustJSON(t, TextPayload{Content: "我先保留最近文本窗口。"}),
				CreatedAt:  time.Now(),
			},
			{
				ID:         "message-6",
				SessionID:  "session-1",
				Role:       RoleAssistant,
				Kind:       KindTaskSuggestion,
				SequenceNo: 6,
				Payload:    []byte(`{"instruction":"整理第二章","status_message":"资源已明确"}`),
				CreatedAt:  time.Now(),
			},
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                "session-1",
			ActiveResourceID:         stringPointer("resource-snapshot"),
			ActiveResourceTitle:      stringPointer("快照资源"),
			ActiveResourceSourceType: stringPointer("upload"),
			ConfirmedConstraintsJSON: []byte("[]"),
			RollingSummary:           stringPointer("第二章已经完成首轮梳理。"),
			SummaryBaseSequenceNo:    3,
		},
	}, retriever, messageReader)

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", nil, "请继续整理第二章")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}

	if messageReader.calls != 1 || messageReader.afterSequenceNo != 3 {
		t.Fatalf("expected message window reader to load after summary base 3, got %#v", messageReader)
	}
	if len(replyContext.History) != 2 {
		t.Fatalf("expected 2 recent text turns, got %d", len(replyContext.History))
	}
	if replyContext.History[0].SequenceNo != 4 || replyContext.History[0].Kind != KindText {
		t.Fatalf("expected first recent turn to be text sequence 4, got %#v", replyContext.History[0])
	}
	if replyContext.History[1].SequenceNo != 5 || replyContext.History[1].Kind != KindText {
		t.Fatalf("expected second recent turn to be text sequence 5, got %#v", replyContext.History[1])
	}
}

// TestContextLoaderUsesSummaryAwareRetrievalQuery 验证`contextLoader`在依赖选择路径下的行为，防止同类回归。
func TestContextLoaderUsesSummaryAwareRetrievalQuery(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                      "session-1",
			ActiveResourceID:               stringPointer("resource-snapshot"),
			ActiveResourceTitle:            stringPointer("快照资源"),
			ActiveResourceSourceType:       stringPointer("upload"),
			PendingTaskSuggestionMessageID: stringPointer("suggestion-1"),
			PendingTaskInstruction:         stringPointer("请整理第二章为正式修订任务。"),
			ConfirmedConstraintsJSON:       []byte("[]"),
			RollingSummary:                 stringPointer("用户正在优化学生手册第二章考勤规则。"),
		},
	}, retriever, &fakeAssistantMessageWindowReader{})

	_, err := loader.LoadForReply(context.Background(), "session-1", nil, "继续改第二个")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}

	if !strings.Contains(retriever.query, "当前问题：继续改第二个") {
		t.Fatalf("expected retriever query to include current message, got %q", retriever.query)
	}
	if !strings.Contains(retriever.query, "会话摘要：用户正在优化学生手册第二章考勤规则。") {
		t.Fatalf("expected retriever query to include summary, got %q", retriever.query)
	}
	if !strings.Contains(retriever.query, "待确认任务：请整理第二章为正式修订任务。") {
		t.Fatalf("expected retriever query to include pending task instruction, got %q", retriever.query)
	}
}

// TestContextLoaderCarriesPendingClarificationAndProposal 验证 loader 能把 advisor state 从快照恢复到回复上下文。
func TestContextLoaderCarriesPendingClarificationAndProposal(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{}
	record := &postgres.SessionContextSnapshotRecord{
		SessionID:                "session-1",
		ActiveResourceID:         stringPointer("resource-snapshot"),
		ActiveResourceTitle:      stringPointer("快照资源"),
		ActiveResourceSourceType: stringPointer("upload"),
		ConfirmedConstraintsJSON: []byte("[]"),
	}
	mustSetBytesField(t, record, "PendingClarificationJSON", mustMarshalJSON(t, map[string]any{
		"kind":             "execution_confirmation",
		"question":         "要不要直接修改？",
		"asked_message_id": "message-clarify-1",
		"options":          []string{"先分析", "直接修改"},
	}))
	mustSetBytesField(t, record, "PendingProposalJSON", mustMarshalJSON(t, map[string]any{
		"proposal_id":                      "proposal-1",
		"instruction":                      "把第三个项目改成问题-动作-结果结构",
		"plan_goal":                        "产出可执行的简历改写任务",
		"proposed_message_id":              "message-proposal-1",
		"requires_explicit_authorization":  true,
	}))
	mustSetBytesField(t, record, "AuthorizationStateJSON", mustMarshalJSON(t, map[string]any{
		"status":                  "pending",
		"granted_for_proposal_id": "proposal-1",
		"granted_by_message_id":   "message-authorize-1",
	}))
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{record: record}, retriever)

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", nil, "按你的建议改")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}
	if replyContext == nil || replyContext.Snapshot == nil {
		t.Fatalf("expected reply snapshot, got %#v", replyContext)
	}

	pendingClarification := mustReadPointerStructField(t, replyContext.Snapshot, "PendingClarification")
	if got := mustReadStringField(t, pendingClarification, "Question"); got != "要不要直接修改？" {
		t.Fatalf("expected clarification question, got %q", got)
	}

	pendingProposal := mustReadPointerStructField(t, replyContext.Snapshot, "PendingProposal")
	if got := mustReadStringField(t, pendingProposal, "Instruction"); got != "把第三个项目改成问题-动作-结果结构" {
		t.Fatalf("expected proposal instruction, got %q", got)
	}

	authorizationState := mustReadPointerStructField(t, replyContext.Snapshot, "AuthorizationState")
	if got := mustReadStringField(t, authorizationState, "Status"); got != "pending" {
		t.Fatalf("expected authorization status %q, got %q", "pending", got)
	}
}

// TestContextLoaderFallsBackWhenSummaryMissing 验证`contextLoader`在回退路径下的行为，防止同类回归。
func TestContextLoaderFallsBackWhenSummaryMissing(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{}
	messageReader := &fakeAssistantMessageWindowReader{
		messages: []postgres.AssistantMessage{
			{
				ID:         "message-1",
				SessionID:  "session-1",
				Role:       RoleAssistant,
				Kind:       KindSessionFile,
				SequenceNo: 1,
				Payload:    mustJSON(t, SessionFilePayload{FileName: "current.md", ResourceID: "resource-snapshot", ResourceTitle: "快照资源", SourceType: "upload", Status: "ready"}),
				CreatedAt:  time.Now(),
			},
			{
				ID:         "message-2",
				SessionID:  "session-1",
				Role:       RoleUser,
				Kind:       KindText,
				SequenceNo: 2,
				Payload:    mustJSON(t, TextPayload{Content: "先看第四章"}),
				CreatedAt:  time.Now(),
			},
			{
				ID:         "message-3",
				SessionID:  "session-1",
				Role:       RoleAssistant,
				Kind:       KindText,
				SequenceNo: 3,
				Payload:    mustJSON(t, TextPayload{Content: "我先梳理第四章的规则。"}),
				CreatedAt:  time.Now(),
			},
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                "session-1",
			ActiveResourceID:         stringPointer("resource-snapshot"),
			ActiveResourceTitle:      stringPointer("快照资源"),
			ActiveResourceSourceType: stringPointer("upload"),
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}, retriever, messageReader)

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", nil, "请总结第四章")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}

	if messageReader.afterSequenceNo != 0 {
		t.Fatalf("expected summary-missing path to load from sequence 0, got %d", messageReader.afterSequenceNo)
	}
	if len(replyContext.History) != 2 {
		t.Fatalf("expected summary-missing path to keep only text turns, got %d", len(replyContext.History))
	}
	if retriever.query != "请总结第四章" {
		t.Fatalf("expected retriever query to fall back to original message, got %q", retriever.query)
	}
}

// TestContextLoaderLoadsCurrentDocumentFromActiveResource 验证 loader 会把当前活跃资源装载成 current document。
func TestContextLoaderLoadsCurrentDocumentFromActiveResource(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{}
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-snapshot",
			Content:    "这是当前文件全文。",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-snapshot", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                "session-1",
			ActiveResourceID:         stringPointer("resource-snapshot"),
			ActiveResourceTitle:      stringPointer("快照资源"),
			ActiveResourceSourceType: stringPointer("upload"),
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}, retriever).WithCurrentDocumentLoader(NewCurrentDocumentLoader(currentFileReader))

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", nil, "结合全文分析第三个项目")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}
	if replyContext.CurrentDocument == nil {
		t.Fatal("expected current document to be loaded")
	}
	if replyContext.CurrentDocument.ResourceID != "resource-snapshot" || replyContext.CurrentDocument.VersionID != "version-1" {
		t.Fatalf("unexpected current document identity: %#v", replyContext.CurrentDocument)
	}
	if replyContext.CurrentDocument.FullText != "这是当前文件全文。" {
		t.Fatalf("expected current document full text, got %q", replyContext.CurrentDocument.FullText)
	}
}

// TestContextLoaderDoesNotEagerLoadCitationsWhenCurrentDocumentReady 验证当前文件 ready 时不会默认先打 citation 检索。
func TestContextLoaderDoesNotEagerLoadCitationsWhenCurrentDocumentReady(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{CitationID: "cite-1", ResourceID: "resource-snapshot", Snippet: "不应被默认加载"},
		},
	}
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-snapshot",
			Content:    "这是当前文件全文。",
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                "session-1",
			ActiveResourceID:         stringPointer("resource-snapshot"),
			ActiveResourceTitle:      stringPointer("快照资源"),
			ActiveResourceSourceType: stringPointer("upload"),
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}, retriever).WithCurrentDocumentLoader(NewCurrentDocumentLoader(currentFileReader))

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", nil, "结合全文分析第三个项目")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}
	if replyContext.CurrentDocument == nil || !replyContext.CurrentDocument.Ready {
		t.Fatalf("expected ready current document, got %#v", replyContext.CurrentDocument)
	}
	if retriever.calls != 0 {
		t.Fatalf("expected eager retrieval to be skipped, got %d calls", retriever.calls)
	}
	if len(replyContext.Citations) != 0 {
		t.Fatalf("expected no eager citations, got %#v", replyContext.Citations)
	}
}

// fakeSessionContextSnapshotReader 作为会话上下文快照读取器的测试替身，用于在用例里提供可控的依赖行为。
type fakeSessionContextSnapshotReader struct {
	record    *postgres.SessionContextSnapshotRecord
	err       error
	sessionID string
	calls     int
}

// GetBySessionID 实现测试替身需要的 `GetBySessionID` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionContextSnapshotReader) GetBySessionID(_ context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error) {
	r.calls++
	r.sessionID = sessionID
	if r.err != nil {
		return nil, r.err
	}
	if r.record == nil {
		return nil, nil
	}

	cloned := *r.record
	copyOptionalBytesField(&cloned, r.record, "PendingClarificationJSON")
	copyOptionalBytesField(&cloned, r.record, "AdvisoryContextJSON")
	copyOptionalBytesField(&cloned, r.record, "PendingProposalJSON")
	copyOptionalBytesField(&cloned, r.record, "AuthorizationStateJSON")
	copyOptionalBytesField(&cloned, r.record, "ExecutionStateJSON")
	return &cloned, nil
}

// copyOptionalBytesField 复制可选 JSON 字段；字段尚未实现时保持静默，交由断言阶段报真红灯。
func copyOptionalBytesField(target any, source any, fieldName string) {
	targetValue := reflect.ValueOf(target)
	sourceValue := reflect.ValueOf(source)
	for targetValue.Kind() == reflect.Pointer {
		targetValue = targetValue.Elem()
	}
	for sourceValue.Kind() == reflect.Pointer {
		sourceValue = sourceValue.Elem()
	}

	targetField := targetValue.FieldByName(fieldName)
	sourceField := sourceValue.FieldByName(fieldName)
	if !targetField.IsValid() || !sourceField.IsValid() {
		return
	}
	if targetField.Kind() != reflect.Slice || sourceField.Kind() != reflect.Slice {
		return
	}
	if targetField.Type().Elem().Kind() != reflect.Uint8 || sourceField.Type().Elem().Kind() != reflect.Uint8 {
		return
	}

	targetField.SetBytes(append([]byte(nil), sourceField.Bytes()...))
}

// fakeAssistantMessageWindowReader 作为助手消息窗口读取器的测试替身，用于在用例里提供可控的依赖行为。
type fakeAssistantMessageWindowReader struct {
	messages        []postgres.AssistantMessage
	err             error
	sessionID       string
	afterSequenceNo int
	calls           int
}

// ListMessagesAfterSequence 实现测试替身需要的 `ListMessagesAfterSequence` 接口方法，为用例分支提供可控返回。
func (r *fakeAssistantMessageWindowReader) ListMessagesAfterSequence(_ context.Context, sessionID string, afterSequenceNo int) ([]postgres.AssistantMessage, error) {
	r.calls++
	r.sessionID = sessionID
	r.afterSequenceNo = afterSequenceNo
	if r.err != nil {
		return nil, r.err
	}

	cloned := make([]postgres.AssistantMessage, len(r.messages))
	copy(cloned, r.messages)
	return cloned, nil
}
