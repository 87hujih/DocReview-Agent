package assistant

import (
	"context"
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
