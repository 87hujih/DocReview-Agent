package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
)

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

func TestContextLoaderLoadBaseContextDoesNotFetchCitations(t *testing.T) {
	retriever := &fakeResourceCitationRetriever{}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                  "session-1",
			ActiveResourceID:           stringPointer("resource-1"),
			ActiveResourceTitle:        stringPointer("简历"),
			ActiveResourceSourceType:   stringPointer("upload"),
			ConfirmedConstraintsJSON:   []byte("[]"),
			ActiveSectionID:            stringPointer("section-campushub"),
			ActiveSectionType:          stringPointer("project"),
			ActiveEntityName:           stringPointer("CampusHub"),
			LastEnumeratedEntitiesJSON: []byte(`[{"section_id":"section-campushub","section_type":"project","entity_name":"CampusHub","ordinal":1}]`),
			OrdinalReferenceFrameJSON:  []byte(`[{"ordinal":1,"section_id":"section-campushub","section_type":"project","entity_name":"CampusHub"}]`),
		},
	}, retriever, &fakeAssistantMessageWindowReader{})

	replyContext, err := loader.LoadBaseContext(context.Background(), "session-1", nil, "第一个项目先输出一遍")
	if err != nil {
		t.Fatalf("load base context: %v", err)
	}
	if replyContext == nil || replyContext.ActiveResource == nil {
		t.Fatalf("expected base context to include active resource, got %#v", replyContext)
	}
	if replyContext.GroundedTarget == nil || replyContext.GroundedTarget.SectionID != "section-campushub" {
		t.Fatalf("expected base context to keep grounding target, got %#v", replyContext.GroundedTarget)
	}
	if retriever.calls != 0 {
		t.Fatalf("expected load base context to skip retriever, got %d", retriever.calls)
	}
}

type fakeSessionContextSnapshotReader struct {
	record    *postgres.SessionContextSnapshotRecord
	err       error
	sessionID string
	calls     int
}

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
	return &cloned, nil
}

type fakeAssistantMessageWindowReader struct {
	messages        []postgres.AssistantMessage
	err             error
	sessionID       string
	afterSequenceNo int
	calls           int
}

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
