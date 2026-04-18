package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/filestore"
	"agent_project/apps/server/internal/storage/postgres"
)

func TestStartConversationUsesResponderReply(t *testing.T) {
	repo := newFakeSessionRepo()
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "当然可以，我们先把目标和限制条件说清楚。",
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

	result, err := service.StartConversation(context.Background(), "我想先聊聊这周要做什么")
	if err != nil {
		t.Fatalf("start conversation: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != "当然可以，我们先把目标和限制条件说清楚。" {
		t.Fatalf("expected responder reply to be persisted, got %q", reply.Content)
	}
}

func TestStartConversationCreatesEmptyContextSnapshot(t *testing.T) {
	repo := newFakeSessionRepo()
	projector := &fakeSessionContextProjector{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "先把目标梳理一下。"}},
		nil,
		WithSessionContextProjector(projector),
	)

	result, err := service.StartConversation(context.Background(), "先聊一下学生守则")
	if err != nil {
		t.Fatalf("start conversation: %v", err)
	}

	if len(projector.initSessionIDs) != 1 {
		t.Fatalf("expected projector to initialize 1 session snapshot, got %d", len(projector.initSessionIDs))
	}
	if projector.initSessionIDs[0] != result.Session.ID {
		t.Fatalf("expected initialized session id %q, got %q", result.Session.ID, projector.initSessionIDs[0])
	}
}

func TestStartConversationDoesNotSuggestTaskForCapabilityQuestion(t *testing.T) {
	repo := newFakeSessionRepo()
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "我可以回答问题、整理信息，也可以在需要时给出任务建议。",
		},
	}, nil)

	result, err := service.StartConversation(context.Background(), "帮我简单介绍一下你能做什么，再顺带说下什么时候适合创建任务。")
	if err != nil {
		t.Fatalf("start conversation: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages without task suggestion, got %d", len(result.Messages))
	}
}

func TestAppendMessageCreatesTaskSuggestionFromResponderInstructionWhenReadinessPasses(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "学生手册.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})
	instruction := "请把学生手册第二章整理成可执行的修订任务"
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply:           "这件事已经适合进入任务流了，我先给你一张建议卡。",
			TaskInstruction: &instruction,
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第二章做成后续事项")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 new messages, got %d", len(result.Messages))
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != "这件事已经适合进入任务流了，我先给你一张建议卡。" {
		t.Fatalf("expected responder text reply, got %q", reply.Content)
	}

	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if suggestion.Instruction != instruction {
		t.Fatalf("expected suggestion instruction %q, got %q", instruction, suggestion.Instruction)
	}
}

func TestAppendMessagePassesLatestResourceContextAndCitationsToResponder(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	resourceID := "resource-1"

	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "学生手册.md",
			ResourceID:    resourceID,
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	responder := &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "我先根据你刚上传的资料梳理考勤条款。",
		},
	}
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{
				CitationID:   "cite_1",
				ResourceID:   resourceID,
				SectionTitle: "第二章 考勤",
				Snippet:      "员工迟到早退需要登记并按制度处理。",
			},
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, retriever)

	if _, err := service.AppendMessage(context.Background(), session.ID, "考勤条款有什么风险"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if responder.lastInput == nil {
		t.Fatal("expected responder to receive chat input")
	}

	if responder.lastInput.Resource == nil || responder.lastInput.Resource.ID != resourceID {
		t.Fatalf("expected responder to receive latest resource %q, got %#v", resourceID, responder.lastInput.Resource)
	}

	if retriever.calls != 1 || retriever.resourceID != resourceID || retriever.query != "考勤条款有什么风险" || retriever.limit != 4 {
		t.Fatalf("unexpected retriever call: %#v", retriever)
	}

	if len(responder.lastInput.Citations) != 1 || responder.lastInput.Citations[0].CitationID != "cite_1" {
		t.Fatalf("expected retriever citation to be forwarded")
	}
}

func TestAppendMessageStreamPassesSearchByResourceCitationsToResponder(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	resourceID := "resource-1"
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "学生手册.md",
			ResourceID:    resourceID,
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	responder := &fakeChatResponder{stream: &fakeChatStream{chunks: []string{"我先看资料。"}}}
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{
				CitationID:   "cite_1",
				ResourceID:   resourceID,
				SectionTitle: "第二章 考勤",
				Snippet:      "考勤内容",
			},
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, retriever)

	err := service.AppendMessageStream(context.Background(), session.ID, "继续看考勤", func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if retriever.calls != 1 || retriever.resourceID != resourceID || retriever.query != "当前问题：继续看考勤" || retriever.limit != 4 {
		t.Fatalf("unexpected retriever call: %#v", retriever)
	}
	if len(responder.lastInput.Citations) != 1 || responder.lastInput.Citations[0].CitationID != "cite_1" {
		t.Fatalf("expected stream responder to receive retriever citation")
	}
}

func TestAppendMessageUsesOrdinalGroundingBeforeAnsweringForFirstProject(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("项目问答")
	projector := &fakeSessionContextProjector{}
	retriever := &fakeResourceCitationRetriever{
		search: func(_ context.Context, resourceID string, query string, _ int) ([]citation.Citation, error) {
			switch query {
			case "针对第一个项目，给出修改示例":
				return []citation.Citation{
					{
						CitationID:   "cite_1",
						ResourceID:   resourceID,
						SectionTitle: "全文",
						Snippet:      "项目描述：",
					},
				}, nil
			case "CampusHub 项目做了什么":
				return []citation.Citation{
					{
						CitationID:   "cite_1",
						ResourceID:   resourceID,
						SectionID:    "section-campushub",
						SectionType:  "project",
						SectionTitle: "CampusHub",
						Snippet:      "负责活动发布、报名与签到全流程。",
						Window:       &citation.Window{GroupID: "project-1"},
					},
				}, nil
			default:
				return nil, nil
			}
		},
	}
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                session.ID,
			ActiveResourceID:         stringPointer("resource-1"),
			ActiveResourceTitle:      stringPointer("项目资料"),
			ActiveResourceSourceType: stringPointer("upload"),
			ConfirmedConstraintsJSON: []byte("[]"),
			OrdinalReferenceFrameJSON: []byte(`[{
				"ordinal":1,
				"section_id":"section-campushub",
				"section_type":"project",
				"entity_name":"CampusHub"
			}]`),
		},
	}, retriever, repo)
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "可以先从活动发布、报名和签到三个模块拆分改动。",
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		retriever,
		WithReplyContextLoader(loader),
		WithSessionContextProjector(projector),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "针对第一个项目，给出修改示例"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if retriever.calls != 2 {
		t.Fatalf("expected retriever to run original query and repair query, got %d calls", retriever.calls)
	}
	if len(retriever.queries) != 2 || retriever.queries[0] != "针对第一个项目，给出修改示例" || retriever.queries[1] != "CampusHub 项目做了什么" {
		t.Fatalf("expected ordinal repair query sequence, got %#v", retriever.queries)
	}
	if responder.lastInput == nil || responder.lastInput.GroundedTarget == nil {
		t.Fatalf("expected responder to receive grounded target, got %#v", responder.lastInput)
	}
	if responder.lastInput.GroundedTarget.SectionID != "section-campushub" {
		t.Fatalf("expected grounded target section, got %#v", responder.lastInput.GroundedTarget)
	}
	if len(responder.lastInput.Citations) != 1 || responder.lastInput.Citations[0].SectionID != "section-campushub" {
		t.Fatalf("expected repaired section evidence, got %#v", responder.lastInput.Citations)
	}
	if len(projector.groundingCalls) != 1 {
		t.Fatalf("expected 1 grounding projection, got %d", len(projector.groundingCalls))
	}
	if projector.groundingCalls[0].ActiveSectionID != "section-campushub" || projector.groundingCalls[0].ActiveEntityName != "CampusHub" {
		t.Fatalf("expected projector to persist ordinal grounding, got %#v", projector.groundingCalls[0])
	}
}

func TestAppendMessageReturnsDeterministicSectionExcerptWithoutCallingResponder(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历阅读")
	projector := &fakeSessionContextProjector{}
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		sectionByOrder: map[string]*postgres.ResourceSection{
			sectionOrderKey("project", 3): {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "第三个项目完整正文"},
		},
		sectionByID: map[string]*postgres.ResourceSection{
			"section-3": {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "第三个项目完整正文"},
		},
	}
	responder := &fakeChatResponder{result: &ChatCompletionResult{Reply: "不该被调用"}}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithActiveFileResourceReader(reader),
		WithSessionContextProjector(projector),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if responder.replyCalls != 0 {
		t.Fatalf("expected deterministic path to skip responder, got %d calls", responder.replyCalls)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages without task suggestion, got %d", len(result.Messages))
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if !strings.Contains(reply.Content, "第三个项目完整正文") {
		t.Fatalf("expected deterministic excerpt reply, got %#v", reply)
	}
	if len(projector.groundingCalls) != 1 || projector.groundingCalls[0].ActiveSectionID != "section-3" {
		t.Fatalf("expected grounding projection for deterministic excerpt, got %#v", projector.groundingCalls)
	}
}

func TestAppendMessageListsProjectsDeterministically(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历阅读")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		sectionsByType: map[string][]postgres.ResourceSection{
			"project": {
				{ID: "section-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub"},
				{ID: "section-2", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手"},
			},
		},
	}
	responder := &fakeChatResponder{result: &ChatCompletionResult{Reply: "不该被调用"}}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithActiveFileResourceReader(reader),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "有哪些项目")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if responder.replyCalls != 0 {
		t.Fatalf("expected deterministic path to skip responder, got %d calls", responder.replyCalls)
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if !strings.Contains(reply.Content, "1. CampusHub") || !strings.Contains(reply.Content, "2. 选课助手") {
		t.Fatalf("expected deterministic project list, got %#v", reply)
	}
}

func TestAppendMessageDoesNotCreateTaskSuggestionForReadRequest(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历阅读")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		sectionByOrder: map[string]*postgres.ResourceSection{
			sectionOrderKey("project", 3): {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "第三个项目完整正文"},
		},
		sectionByID: map[string]*postgres.ResourceSection{
			"section-3": {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "第三个项目完整正文"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{},
		nil,
		WithActiveFileResourceReader(reader),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages without task suggestion, got %d", len(result.Messages))
	}
	if result.Messages[1].Kind != KindText {
		t.Fatalf("expected deterministic read request to persist text reply only, got %#v", result.Messages[1])
	}
}

func TestAppendMessageFeedsSectionTextIntoResponderForTransformRequest(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历分析")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		sectionByOrder: map[string]*postgres.ResourceSection{
			sectionOrderKey("project", 3): {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "原始第三个项目正文"},
		},
		sectionByID: map[string]*postgres.ResourceSection{
			"section-3": {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "原始第三个项目正文"},
		},
	}
	responder := &fakeChatResponder{result: &ChatCompletionResult{Reply: "可以把亮点前置。"}}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithActiveFileResourceReader(reader),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "第三个项目怎么优化"); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if responder.lastInput == nil || responder.lastInput.GroundedSection == nil {
		t.Fatalf("expected grounded section input, got %#v", responder.lastInput)
	}
	if responder.lastInput.GroundedSection.SectionText != "原始第三个项目正文" {
		t.Fatalf("expected concrete section text, got %#v", responder.lastInput.GroundedSection)
	}
}

func TestAppendMessageRejectsReadRequestWhenSectionCannotBeLocated(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历阅读")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	responder := &fakeChatResponder{result: &ChatCompletionResult{Reply: "不该被调用"}}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithActiveFileResourceReader(&fakeActiveFileResourceReader{
			currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if responder.replyCalls != 0 {
		t.Fatalf("expected missing section path to skip responder, got %d calls", responder.replyCalls)
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if !strings.Contains(reply.Content, "证据不足") && !strings.Contains(reply.Content, "无法定位") {
		t.Fatalf("expected abstain reply when section cannot be located, got %#v", reply)
	}
}

func TestAppendMessageTriggersAsyncSummaryRefreshAfterPersistingAssistantReply(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("长上下文会话")
	seedTextConversationHistory(t, repo, session.ID, 10)

	summaryRepo := &fakeSummarySnapshotRepo{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                session.ID,
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}

	sawPersistedAssistantReply := false
	summarizer := &fakeConversationSummarizer{
		result: &SummaryResult{Summary: "当前目标：继续优化第二章。\n关键结论：已保留考勤规则。\n待继续事项：比较改写方案。"},
		onSummarize: func(input SummaryInput) {
			messages, _ := repo.ListMessages(context.Background(), session.ID)
			for _, message := range messages {
				if message.Role != RoleAssistant || message.Kind != KindText {
					continue
				}

				payload, _ := unmarshalTextPayload(message.Payload)
				if payload.Content == "这是新的助手回复。" {
					sawPersistedAssistantReply = true
					return
				}
			}
		},
	}

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "这是新的助手回复。"}},
		nil,
		WithConversationSummarizer(summarizer, summaryRepo),
		WithSummaryAsyncRunner(func(task func()) { task() }),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "继续优化第二章"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if summarizer.calls != 1 {
		t.Fatalf("expected summarizer to be called once, got %d", summarizer.calls)
	}
	if !sawPersistedAssistantReply {
		t.Fatal("expected summary refresh to run after assistant reply was persisted")
	}
	if len(summarizer.lastInput.Transcript) != 12 {
		t.Fatalf("expected summarizer transcript to include 12 unsummarized text messages, got %d", len(summarizer.lastInput.Transcript))
	}
	if summaryRepo.advanceCalls != 1 || summaryRepo.lastNextBaseSequenceNo != 12 {
		t.Fatalf("expected rolling summary to advance to sequence 12, got repo=%#v", summaryRepo)
	}
}

func TestAppendMessageStreamSummaryRefreshFailureDoesNotFailMainTurn(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("流式长上下文会话")
	seedTextConversationHistory(t, repo, session.ID, 10)

	summaryRepo := &fakeSummarySnapshotRepo{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                session.ID,
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}
	summarizer := &fakeConversationSummarizer{
		err: errors.New("summary refresh failed"),
	}

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{stream: &fakeChatStream{chunks: []string{"新的", "流式回复"}}},
		nil,
		WithConversationSummarizer(summarizer, summaryRepo),
		WithSummaryAsyncRunner(func(task func()) { task() }),
	)

	err := service.AppendMessageStream(context.Background(), session.ID, "继续看看第二章", func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if summarizer.calls != 1 {
		t.Fatalf("expected summarizer to be called once, got %d", summarizer.calls)
	}
	if summaryRepo.advanceCalls != 0 {
		t.Fatalf("expected failed summary refresh not to advance rolling summary, got %d", summaryRepo.advanceCalls)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	last := messages[len(messages)-1]
	payload, err := unmarshalTextPayload(last.Payload)
	if err != nil {
		t.Fatalf("unmarshal last persisted reply: %v", err)
	}
	if payload.Content != "新的流式回复" {
		t.Fatalf("expected persisted stream reply to survive summary failure, got %q", payload.Content)
	}
}

func TestSummaryRefreshSkipsWhenUnsummarizedTextIsBelowThreshold(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("短会话")
	seedTextConversationHistory(t, repo, session.ID, 9)

	summaryRepo := &fakeSummarySnapshotRepo{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                session.ID,
			ConfirmedConstraintsJSON: []byte("[]"),
		},
	}
	summarizer := &fakeConversationSummarizer{
		result: &SummaryResult{Summary: "不应触发"},
	}

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "新的助手回复。"}},
		nil,
		WithConversationSummarizer(summarizer, summaryRepo),
		WithSummaryAsyncRunner(func(task func()) { task() }),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "继续看看第二章"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if summarizer.calls != 0 {
		t.Fatalf("expected summary refresh to skip below threshold, got %d calls", summarizer.calls)
	}
	if summaryRepo.advanceCalls != 0 {
		t.Fatalf("expected no rolling summary advance below threshold, got %d", summaryRepo.advanceCalls)
	}
}

func TestAppendMessageDoesNotCallRetrieverWithoutReadyResource(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("空白会话")
	responder := &fakeChatResponder{result: &ChatCompletionResult{Reply: "先继续聊需求。"}}
	retriever := &fakeResourceCitationRetriever{}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, retriever)

	if _, err := service.AppendMessage(context.Background(), session.ID, "先看看目标"); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if retriever.calls != 0 {
		t.Fatalf("expected no retriever call without ready resource, got %d", retriever.calls)
	}
}

func TestAppendMessageInlineMaterialUsesInlineTextSourceType(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
			Version: &postgres.ResourceVersion{
				ID:         "version-inline",
				ResourceID: "resource-inline",
				Content:    "项目经历\n- 负责增长策略\n- 负责数据分析",
				Source:     "assistant_inline_text",
			},
		},
	}
	service := NewService(repo, importer, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先看这段正文。"},
	}, nil)

	_, err := service.AppendMessage(context.Background(), session.ID, strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析

请直接帮我改成产品经理版本
`))
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if importer.lastInput == nil {
		t.Fatal("expected inline material to call importer")
	}
	if importer.lastInput.SourceType != "inline_text" {
		t.Fatalf("expected inline import source type %q, got %q", "inline_text", importer.lastInput.SourceType)
	}
	if importer.lastInput.VersionSource != "assistant_inline_text" {
		t.Fatalf("expected inline import version source %q, got %q", "assistant_inline_text", importer.lastInput.VersionSource)
	}
	if importer.lastInput.FileName != "对话粘贴正文.md" {
		t.Fatalf("expected synthetic inline filename, got %q", importer.lastInput.FileName)
	}
}

func TestStartConversationPersistsInlineMaterialBeforeAssistantReply(t *testing.T) {
	repo := newFakeSessionRepo()
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	service := NewService(repo, importer, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先看这段正文。"},
	}, nil)

	result, err := service.StartConversation(context.Background(), strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析
`))
	if err != nil {
		t.Fatalf("start conversation: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected user/session_file/assistant messages, got %d", len(result.Messages))
	}
	if result.Messages[0].Kind != KindText || result.Messages[1].Kind != KindSessionFile || result.Messages[2].Kind != KindText {
		t.Fatalf("expected message order text -> session_file -> text, got kinds=%q/%q/%q", result.Messages[0].Kind, result.Messages[1].Kind, result.Messages[2].Kind)
	}

	filePayload := decodeSessionFilePayload(t, result.Messages[1].Payload)
	if filePayload.SourceType != "inline_text" {
		t.Fatalf("expected inline session file source type %q, got %q", "inline_text", filePayload.SourceType)
	}
}

func TestAppendMessageInlineMaterialCreatesSessionFileMessageBeforeReply(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	service := NewService(repo, importer, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先看这段正文。"},
	}, nil)

	result, err := service.AppendMessage(context.Background(), session.ID, strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析
`))
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected user/session_file/assistant messages, got %d", len(result.Messages))
	}
	if result.Messages[0].Kind != KindText || result.Messages[1].Kind != KindSessionFile || result.Messages[2].Kind != KindText {
		t.Fatalf("expected message order text -> session_file -> text, got kinds=%q/%q/%q", result.Messages[0].Kind, result.Messages[1].Kind, result.Messages[2].Kind)
	}
}

func TestAppendMessageInlineMaterialAndExecutionCreatesTaskSuggestionInSameTurn(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	instruction := "请把这份简历改成产品经理版本"
	service := NewService(repo, importer, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply:           "这轮已经满足执行条件，我先给你任务建议。",
			TaskInstruction: &instruction,
		},
	}, nil)

	result, err := service.AppendMessage(context.Background(), session.ID, strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析

请直接帮我改成产品经理版本
`))
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 4 {
		t.Fatalf("expected user/session_file/assistant/task_suggestion messages, got %d", len(result.Messages))
	}
	if result.Messages[1].Kind != KindSessionFile || result.Messages[2].Kind != KindText || result.Messages[3].Kind != KindTaskSuggestion {
		t.Fatalf("expected session_file before assistant reply and same-turn suggestion, got kinds=%q/%q/%q", result.Messages[1].Kind, result.Messages[2].Kind, result.Messages[3].Kind)
	}

	filePayload := decodeSessionFilePayload(t, result.Messages[1].Payload)
	if filePayload.SourceType != "inline_text" {
		t.Fatalf("expected inline session file source type %q, got %q", "inline_text", filePayload.SourceType)
	}

	suggestion := decodeTaskSuggestionPayload(t, result.Messages[3].Payload)
	if !suggestion.CanCreate {
		t.Fatal("expected same-turn inline material suggestion to be creatable")
	}
	if suggestion.ResourceID == nil || *suggestion.ResourceID != "resource-inline" {
		t.Fatalf("expected suggestion resource id %q, got %#v", "resource-inline", suggestion.ResourceID)
	}
}

func TestAppendMessageInlineMaterialWithoutExecutionDoesNotCreateTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	service := NewService(repo, importer, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先分析这段内容适合怎么改。"},
	}, nil)

	result, err := service.AppendMessage(context.Background(), session.ID, strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析
`))
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected no same-turn task suggestion without execution intent, got %d messages", len(result.Messages))
	}
}

func TestAppendMessageInlineMaterialProjectsActiveResourceIntoSnapshot(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	projector := &fakeSessionContextProjector{}
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	service := NewService(
		repo,
		importer,
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先看这段正文。"}},
		nil,
		WithSessionContextProjector(projector),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析
`)); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(projector.fileReadyCalls) != 1 {
		t.Fatalf("expected 1 inline session_file projection, got %d", len(projector.fileReadyCalls))
	}
	call := projector.fileReadyCalls[0]
	if call.ResourceID != "resource-inline" {
		t.Fatalf("expected projected resource id %q, got %q", "resource-inline", call.ResourceID)
	}
	if call.ResourceSource != "inline_text" {
		t.Fatalf("expected projected resource source %q, got %q", "inline_text", call.ResourceSource)
	}
	if call.SourceMessageID != "message-appended-b" {
		t.Fatalf("expected projected inline source message id %q, got %q", "message-appended-b", call.SourceMessageID)
	}
}

func TestStartConversationDoesNotCreateTaskSuggestionWithoutMaterial(t *testing.T) {
	repo := newFakeSessionRepo()
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "我可以先帮你分析问题，但要真正创建任务还需要明确材料。",
		},
	}, nil)

	result, err := service.StartConversation(context.Background(), "请帮我修改这份学生守则，并整理成任务")
	if err != nil {
		t.Fatalf("start conversation: %v", err)
	}

	if result.Session.ID == "" {
		t.Fatal("expected session id to be set")
	}

	if result.Session.Title == "" {
		t.Fatal("expected session title to be generated")
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages without task suggestion, got %d", len(result.Messages))
	}

	if result.Messages[0].Role != RoleUser || result.Messages[0].Kind != KindText {
		t.Fatalf("expected first message to be user text, got role=%q kind=%q", result.Messages[0].Role, result.Messages[0].Kind)
	}
}

func TestAppendMessageDoesNotCreateTaskSuggestionForDiscussionQuestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "学生手册.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "我先帮你分析第二章哪里还有问题。",
		},
	}, nil)

	result, err := service.AppendMessage(context.Background(), session.ID, "这份学生手册第二章还有什么需要优化的吗？")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 new messages without task suggestion, got %d", len(result.Messages))
	}
}

func TestAppendMessageIgnoresModelTaskInstructionWhenReadinessGateFails(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("空白会话")
	instruction := "请把学生手册整理成执行任务"
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply:           "要进入任务流还需要先给我可处理的材料。",
			TaskInstruction: &instruction,
		},
	}, nil)

	result, err := service.AppendMessage(context.Background(), session.ID, "请先告诉我还缺什么材料")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 new messages when readiness gate fails, got %d", len(result.Messages))
	}
}

func TestAppendMessageCreatesTaskSuggestionForExecutionRequestWithReadyResource(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	resourceID := "resource-1"

	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "学生手册.md",
			ResourceID:    resourceID,
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "我先基于当前资料看第二章，再帮你收敛成任务。",
		},
	}, nil)
	result, err := service.AppendMessage(context.Background(), session.ID, "请帮我检查并修订第二章")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 new messages, got %d", len(result.Messages))
	}

	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if !suggestion.CanCreate {
		t.Fatal("expected task suggestion to be creatable when session already has a resource")
	}

	if suggestion.ResourceID == nil || *suggestion.ResourceID != resourceID {
		t.Fatalf("expected suggestion resource id %q, got %#v", resourceID, suggestion.ResourceID)
	}
}

func TestAppendMessageProjectsPendingTaskSuggestionIntoSnapshot(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "学生手册.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})
	projector := &fakeSessionContextProjector{}
	instruction := "请把第二章整理成任务"
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply:           "我先给你一张任务建议卡。",
				TaskInstruction: &instruction,
			},
		},
		nil,
		WithSessionContextProjector(projector),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "请把第二章整理成任务"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(projector.taskSuggestionCalls) != 1 {
		t.Fatalf("expected 1 task suggestion projection, got %d", len(projector.taskSuggestionCalls))
	}
	call := projector.taskSuggestionCalls[0]
	if call.SessionID != session.ID {
		t.Fatalf("expected projected session id %q, got %q", session.ID, call.SessionID)
	}
	if call.MessageID != "message-appended-d" {
		t.Fatalf("expected projected suggestion message id %q, got %q", "message-appended-d", call.MessageID)
	}
	if call.Instruction != instruction {
		t.Fatalf("expected projected instruction %q, got %q", instruction, call.Instruction)
	}
}

func TestStartConversationStreamPersistsUserFirstAndAssistantAfterCompletion(t *testing.T) {
	repo := newFakeSessionRepo()
	responder := &fakeChatResponder{
		stream: &fakeChatStream{
			chunks: []string{"当然可以", "，我们先梳理目标。"},
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

	var events []StreamEvent
	err := service.StartConversationStream(context.Background(), "帮我梳理这周学习安排", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("start conversation stream: %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	if events[0].Type != StreamEventSessionCreated {
		t.Fatalf("expected first event %q, got %q", StreamEventSessionCreated, events[0].Type)
	}

	if events[1].Type != StreamEventMessageStarted {
		t.Fatalf("expected second event %q, got %q", StreamEventMessageStarted, events[1].Type)
	}

	if events[2].Type != StreamEventMessageDelta || events[2].Delta != "当然可以" {
		t.Fatalf("expected first delta event, got %#v", events[2])
	}

	if events[3].Type != StreamEventMessageDelta || events[3].Delta != "，我们先梳理目标。" {
		t.Fatalf("expected second delta event, got %#v", events[3])
	}

	if events[4].Type != StreamEventMessageCompleted {
		t.Fatalf("expected final event %q, got %q", StreamEventMessageCompleted, events[4].Type)
	}

	sessionID := events[0].Session.ID
	messages, err := repo.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(messages))
	}

	reply := decodeTextPayload(t, messages[1].Payload)
	if reply.Content != "当然可以，我们先梳理目标。" {
		t.Fatalf("expected persisted reply %q, got %q", "当然可以，我们先梳理目标。", reply.Content)
	}
}

func TestStartConversationStreamEmitsSessionFileBeforeAssistantReplyWhenInlineMaterialDetected(t *testing.T) {
	repo := newFakeSessionRepo()
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	responder := &fakeChatResponder{
		stream: &fakeChatStream{chunks: []string{"我先看这段正文。"}},
	}
	service := NewService(repo, importer, &fakeTaskCreator{}, responder, nil)

	var events []StreamEvent
	err := service.StartConversationStream(context.Background(), strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析
`), func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("start conversation stream: %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events with inline session_file, got %d", len(events))
	}
	if events[0].Type != StreamEventSessionCreated || events[1].Type != StreamEventSessionFile || events[2].Type != StreamEventMessageStarted {
		t.Fatalf("expected session_created -> session_file -> message_started, got %#v", []string{events[0].Type, events[1].Type, events[2].Type})
	}
	if events[1].Message == nil || events[1].Message.Kind != KindSessionFile {
		t.Fatalf("expected second event to carry session_file message, got %#v", events[1].Message)
	}

	sessionID := events[0].Session.ID
	messages, err := repo.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected persisted user/session_file/assistant messages, got %d", len(messages))
	}
	if messages[1].Kind != KindSessionFile || messages[2].Kind != KindText {
		t.Fatalf("expected persisted order text -> session_file -> text, got %q/%q/%q", messages[0].Kind, messages[1].Kind, messages[2].Kind)
	}
}

func TestAppendMessageStreamEmitsSessionFileBeforeAssistantReplyWhenInlineMaterialDetected(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-inline",
				Title:      "对话粘贴正文",
				SourceType: "inline_text",
			},
		},
	}
	responder := &fakeChatResponder{
		stream: &fakeChatStream{chunks: []string{"我先看这段正文。"}},
	}
	service := NewService(repo, importer, &fakeTaskCreator{}, responder, nil)

	var events []StreamEvent
	err := service.AppendMessageStream(context.Background(), session.ID, strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析
`), func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 events with inline session_file, got %d", len(events))
	}
	if events[0].Type != StreamEventSessionFile || events[1].Type != StreamEventMessageStarted {
		t.Fatalf("expected session_file -> message_started, got %#v", []string{events[0].Type, events[1].Type})
	}
	if events[0].Message == nil || events[0].Message.Kind != KindSessionFile {
		t.Fatalf("expected first event to carry session_file message, got %#v", events[0].Message)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected persisted user/session_file/assistant messages, got %d", len(messages))
	}
	if messages[1].Kind != KindSessionFile || messages[2].Kind != KindText {
		t.Fatalf("expected persisted order text -> session_file -> text, got %q/%q/%q", messages[0].Kind, messages[1].Kind, messages[2].Kind)
	}
}

func TestAppendMessageStreamDoesNotPersistAssistantOnCancellation(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-user-history",
		SessionID:  session.ID,
		Role:       RoleUser,
		Kind:       KindText,
		SequenceNo: 1,
		Payload:    mustJSON(t, TextPayload{Content: "先看第二章"}),
		CreatedAt:  time.Now(),
	})

	responder := &fakeChatResponder{
		stream: &fakeChatStream{
			chunks: []string{"我先看一下"},
			errs:   []error{nil, context.Canceled},
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

	err := service.AppendMessageStream(context.Background(), session.ID, "继续看看考勤部分", func(StreamEvent) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected only history + latest user message to be persisted, got %d messages", len(messages))
	}

	if messages[1].Role != RoleUser {
		t.Fatalf("expected latest persisted message role %q, got %q", RoleUser, messages[1].Role)
	}
}

func TestStartConversationStreamReturnsEmptyReplyError(t *testing.T) {
	repo := newFakeSessionRepo()
	responder := &fakeChatResponder{
		stream: &fakeChatStream{},
	}
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

	err := service.StartConversationStream(context.Background(), "帮我总结学生手册", func(StreamEvent) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected empty reply error")
	}

	var streamErr *StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("expected StreamError, got %T", err)
	}

	if streamErr.Code != StreamErrorCodeEmptyReply {
		t.Fatalf("expected error code %q, got %q", StreamErrorCodeEmptyReply, streamErr.Code)
	}

	if len(repo.messages) != 1 {
		t.Fatalf("expected only one session to be created, got %d", len(repo.messages))
	}
	for _, messages := range repo.messages {
		if len(messages) != 1 {
			t.Fatalf("expected only user message to be persisted on empty reply, got %d", len(messages))
		}
	}
}

func TestUploadFilePersistsSessionFileMessage(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("空白会话")
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-uploaded",
				Title:      "学生手册",
				SourceType: "upload",
			},
		},
	}

	service := NewService(repo, importer, &fakeTaskCreator{}, &fakeChatResponder{}, nil)
	result, err := service.UploadFile(context.Background(), session.ID, "学生手册.md", []byte("# 学生手册\n内容"))
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}

	if result.Resource == nil || result.Resource.ID != "resource-uploaded" {
		t.Fatalf("expected uploaded resource to be returned, got %#v", result.Resource)
	}

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 appended message, got %d", len(result.Messages))
	}

	if result.Messages[0].Kind != KindSessionFile {
		t.Fatalf("expected session file message, got %q", result.Messages[0].Kind)
	}

	filePayload := decodeSessionFilePayload(t, result.Messages[0].Payload)
	if filePayload.ResourceID != "resource-uploaded" {
		t.Fatalf("expected payload resource id %q, got %q", "resource-uploaded", filePayload.ResourceID)
	}
}

func TestUploadFileProjectsActiveResourceIntoSnapshot(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("空白会话")
	projector := &fakeSessionContextProjector{}
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-uploaded",
				Title:      "学生手册",
				SourceType: "upload",
			},
		},
	}

	service := NewService(
		repo,
		importer,
		&fakeTaskCreator{},
		&fakeChatResponder{},
		nil,
		WithSessionContextProjector(projector),
	)

	if _, err := service.UploadFile(context.Background(), session.ID, "学生手册.md", []byte("# 学生手册\n内容")); err != nil {
		t.Fatalf("upload file: %v", err)
	}

	if len(projector.fileReadyCalls) != 1 {
		t.Fatalf("expected 1 file ready projection, got %d", len(projector.fileReadyCalls))
	}
	call := projector.fileReadyCalls[0]
	if call.SessionID != session.ID {
		t.Fatalf("expected projected session id %q, got %q", session.ID, call.SessionID)
	}
	if call.ResourceID != "resource-uploaded" {
		t.Fatalf("expected projected resource id %q, got %q", "resource-uploaded", call.ResourceID)
	}
	if call.ResourceTitle != "学生手册" {
		t.Fatalf("expected projected resource title %q, got %q", "学生手册", call.ResourceTitle)
	}
	if call.ResourceSource != "upload" {
		t.Fatalf("expected projected resource source %q, got %q", "upload", call.ResourceSource)
	}
	if call.SourceMessageID != "message-appended-a" {
		t.Fatalf("expected projected source message id %q, got %q", "message-appended-a", call.SourceMessageID)
	}
}

func TestUploadFileStoresOriginalFileAndPersistsFileID(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("原文件保存")
	fileStore := &fakeFileStore{
		result: &filestore.StoredFile{
			SHA256:     "sha256-uploaded",
			SizeBytes:  int64(len([]byte("# 学生手册\n内容"))),
			StorageKey: "sh/sha256-uploaded",
		},
	}
	uploadedFiles := &fakeUploadedFileRepo{
		result: &postgres.UploadedFile{
			ID:               "file-1",
			OriginalFilename: "学生手册.md",
			ContentType:      "text/plain; charset=utf-8",
			SizeBytes:        int64(len([]byte("# 学生手册\n内容"))),
			SHA256:           "sha256-uploaded",
			StorageKey:       "sh/sha256-uploaded",
		},
	}
	importer := &fakeDocumentImporter{
		result: &ImportDocumentResult{
			Resource: &postgres.Resource{
				ID:         "resource-uploaded",
				Title:      "学生手册",
				SourceType: "upload",
			},
		},
	}

	service := NewService(
		repo,
		importer,
		&fakeTaskCreator{},
		&fakeChatResponder{},
		nil,
		WithUploadedFileStorage(fileStore, uploadedFiles),
	)
	result, err := service.UploadFile(context.Background(), session.ID, "学生手册.md", []byte("# 学生手册\n内容"))
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}

	if string(fileStore.savedContent) != "# 学生手册\n内容" {
		t.Fatalf("expected original bytes to be saved, got %q", string(fileStore.savedContent))
	}
	if uploadedFiles.createInput == nil {
		t.Fatal("expected uploaded file metadata to be created")
	}
	if uploadedFiles.createInput.SessionID == nil || *uploadedFiles.createInput.SessionID != session.ID {
		t.Fatalf("expected session id %q, got %#v", session.ID, uploadedFiles.createInput.SessionID)
	}
	if uploadedFiles.updatedFileID != "file-1" {
		t.Fatalf("expected updated file id %q, got %q", "file-1", uploadedFiles.updatedFileID)
	}
	if uploadedFiles.updatedResourceID != "resource-uploaded" {
		t.Fatalf("expected uploaded file to bind resource %q, got %q", "resource-uploaded", uploadedFiles.updatedResourceID)
	}

	filePayload := decodeSessionFilePayload(t, result.Messages[0].Payload)
	if filePayload.FileID != "file-1" {
		t.Fatalf("expected payload file id %q, got %q", "file-1", filePayload.FileID)
	}
}

func TestConfirmTaskSuggestionCreatesTaskCreatedMessage(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	resourceID := "resource-1"

	suggestionMessage := repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-suggestion",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindTaskSuggestion,
		SequenceNo: 1,
		Payload: mustJSON(t, TaskSuggestionPayload{
			ActionLabel:   "确认创建任务",
			CanCreate:     true,
			Instruction:   "请修订第二章",
			ResourceID:    &resourceID,
			ResourceLabel: "学生手册 · upload",
			StatusMessage: "资源已明确，可以创建任务。",
			Title:         "建议创建任务",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{
		result: &postgres.Task{
			ID:          "task-1",
			ResourceID:  resourceID,
			Instruction: "请修订第二章",
			Status:      "pending",
		},
	}, &fakeChatResponder{}, nil)

	result, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID)
	if err != nil {
		t.Fatalf("confirm task suggestion: %v", err)
	}

	if result.Task == nil || result.Task.ID != "task-1" {
		t.Fatalf("expected created task to be returned, got %#v", result.Task)
	}

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 appended message, got %d", len(result.Messages))
	}

	if result.Messages[0].Kind != KindTaskCreated {
		t.Fatalf("expected task_created message, got %q", result.Messages[0].Kind)
	}

	createdPayload := decodeTaskCreatedPayload(t, result.Messages[0].Payload)
	if createdPayload.TaskID != "task-1" {
		t.Fatalf("expected task id %q, got %q", "task-1", createdPayload.TaskID)
	}
}

func TestConfirmTaskSuggestionTaskCreatedDetailURLIncludesSessionQuery(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("student-manual")
	resourceID := "resource-1"

	suggestionMessage := repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-suggestion",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindTaskSuggestion,
		SequenceNo: 1,
		Payload: mustJSON(t, TaskSuggestionPayload{
			ActionLabel:   "确认创建任务",
			CanCreate:     true,
			Instruction:   "请修订第二章",
			ResourceID:    &resourceID,
			ResourceLabel: "学生手册 · upload",
			StatusMessage: "资源已明确，可以创建任务。",
			Title:         "建议创建任务",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{
		result: &postgres.Task{
			ID:          "task-1",
			ResourceID:  resourceID,
			Instruction: "请修订第二章",
			Status:      "pending",
		},
	}, &fakeChatResponder{}, nil)

	result, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID)
	if err != nil {
		t.Fatalf("confirm task suggestion: %v", err)
	}

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 appended message, got %d", len(result.Messages))
	}

	createdPayload := decodeTaskCreatedPayload(t, result.Messages[0].Payload)
	expectedURL := "/tasks/task-1?session=" + session.ID
	if createdPayload.DetailURL != expectedURL {
		t.Fatalf("expected detail url %q, got %q", expectedURL, createdPayload.DetailURL)
	}
}

func TestConfirmTaskSuggestionProjectsLatestTaskIntoSnapshot(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	projector := &fakeSessionContextProjector{}
	resourceID := "resource-1"

	suggestionMessage := repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-suggestion",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindTaskSuggestion,
		SequenceNo: 1,
		Payload: mustJSON(t, TaskSuggestionPayload{
			ActionLabel:   "确认创建任务",
			CanCreate:     true,
			Instruction:   "请修订第二章",
			ResourceID:    &resourceID,
			ResourceLabel: "学生手册 · upload",
			StatusMessage: "资源已明确，可以创建任务。",
			Title:         "建议创建任务",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{
			result: &postgres.Task{
				ID:          "task-1",
				ResourceID:  resourceID,
				Instruction: "请修订第二章",
				Status:      "pending",
			},
		},
		&fakeChatResponder{},
		nil,
		WithSessionContextProjector(projector),
	)

	if _, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID); err != nil {
		t.Fatalf("confirm task suggestion: %v", err)
	}

	if len(projector.taskCreatedCalls) != 1 {
		t.Fatalf("expected 1 task created projection, got %d", len(projector.taskCreatedCalls))
	}
	call := projector.taskCreatedCalls[0]
	if call.SessionID != session.ID {
		t.Fatalf("expected projected session id %q, got %q", session.ID, call.SessionID)
	}
	if call.TaskID != "task-1" {
		t.Fatalf("expected projected task id %q, got %q", "task-1", call.TaskID)
	}
	if call.Status != "pending" {
		t.Fatalf("expected projected task status %q, got %q", "pending", call.Status)
	}
	if call.SourceMessageID != suggestionMessage.ID {
		t.Fatalf("expected projected source message id %q, got %q", suggestionMessage.ID, call.SourceMessageID)
	}
}

func TestConfirmTaskSuggestionAppendsFailureSystemMessageWhenTaskCreationFails(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	resourceID := "resource-1"

	suggestionMessage := repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-suggestion",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindTaskSuggestion,
		SequenceNo: 1,
		Payload: mustJSON(t, TaskSuggestionPayload{
			ActionLabel:   "确认创建任务",
			CanCreate:     true,
			Instruction:   "请修订第二章",
			ResourceID:    &resourceID,
			ResourceLabel: "学生手册 · upload",
			StatusMessage: "资源已明确，可以创建任务。",
			Title:         "建议创建任务",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{
		err: errors.New("task create failed"),
	}, &fakeChatResponder{}, nil)

	result, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID)
	if err != nil {
		t.Fatalf("confirm task suggestion: %v", err)
	}

	if result.Task != nil {
		t.Fatalf("expected no task to be created, got %#v", result.Task)
	}

	if result.ErrorMessage == nil {
		t.Fatal("expected task creation error message")
	}

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 appended system message, got %d", len(result.Messages))
	}

	if result.Messages[0].Kind != KindSystem {
		t.Fatalf("expected system message, got %q", result.Messages[0].Kind)
	}
}

func TestConfirmTaskSuggestionReturnsExistingTaskWithoutAppendingDuplicateTaskCreatedMessage(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	resourceID := "resource-1"

	suggestionMessage := repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-suggestion",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindTaskSuggestion,
		SequenceNo: 1,
		Payload: mustJSON(t, TaskSuggestionPayload{
			ActionLabel:   "确认创建任务",
			CanCreate:     true,
			Instruction:   "请修订第二章",
			ResourceID:    &resourceID,
			ResourceLabel: "学生手册 · upload",
			StatusMessage: "资源已明确，可以创建任务。",
			Title:         "建议创建任务",
		}),
		CreatedAt: time.Now(),
	})

	taskCreator := &fakeTaskCreator{
		result: &postgres.Task{
			ID:          "task-1",
			ResourceID:  resourceID,
			Instruction: "请修订第二章",
			Status:      "pending",
		},
	}
	service := NewService(repo, &fakeDocumentImporter{}, taskCreator, &fakeChatResponder{}, nil)

	firstResult, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID)
	if err != nil {
		t.Fatalf("confirm task suggestion first time: %v", err)
	}
	if len(firstResult.Messages) != 1 {
		t.Fatalf("expected first confirm to append 1 task_created message, got %d", len(firstResult.Messages))
	}

	secondResult, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID)
	if err != nil {
		t.Fatalf("confirm task suggestion second time: %v", err)
	}
	if secondResult.Task == nil || secondResult.Task.ID != "task-1" {
		t.Fatalf("expected duplicate confirm to return existing task, got %#v", secondResult.Task)
	}
	if len(secondResult.Messages) != 0 {
		t.Fatalf("expected duplicate confirm to append 0 messages, got %d", len(secondResult.Messages))
	}
	if taskCreator.calls != 2 {
		t.Fatalf("expected current implementation to call task creator twice before idempotency fix, got %d", taskCreator.calls)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}

	taskCreatedCount := 0
	for _, message := range messages {
		if message.Kind == KindTaskCreated {
			taskCreatedCount++
		}
	}
	if taskCreatedCount != 1 {
		t.Fatalf("expected session to contain exactly 1 task_created message, got %d", taskCreatedCount)
	}
}

type fakeSessionRepo struct {
	sessions map[string]postgres.AssistantSession
	messages map[string][]postgres.AssistantMessage
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{
		sessions: make(map[string]postgres.AssistantSession),
		messages: make(map[string][]postgres.AssistantMessage),
	}
}

func (r *fakeSessionRepo) ListSessions(context.Context) ([]postgres.AssistantSession, error) {
	items := make([]postgres.AssistantSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		items = append(items, session)
	}

	return items, nil
}

func (r *fakeSessionRepo) GetSessionByID(_ context.Context, id string) (*postgres.AssistantSession, error) {
	session, ok := r.sessions[id]
	if !ok {
		return nil, nil
	}

	return &session, nil
}

func (r *fakeSessionRepo) ListMessages(_ context.Context, sessionID string) ([]postgres.AssistantMessage, error) {
	items := r.messages[sessionID]
	cloned := make([]postgres.AssistantMessage, len(items))
	copy(cloned, items)
	return cloned, nil
}

func (r *fakeSessionRepo) ListMessagesAfterSequence(_ context.Context, sessionID string, afterSequenceNo int) ([]postgres.AssistantMessage, error) {
	items := r.messages[sessionID]
	filtered := make([]postgres.AssistantMessage, 0, len(items))
	for _, message := range items {
		if message.SequenceNo <= afterSequenceNo {
			continue
		}

		filtered = append(filtered, message)
	}

	cloned := make([]postgres.AssistantMessage, len(filtered))
	copy(cloned, filtered)
	return cloned, nil
}

func (r *fakeSessionRepo) GetMessageByID(_ context.Context, id string) (*postgres.AssistantMessage, error) {
	for _, items := range r.messages {
		for _, message := range items {
			if message.ID == id {
				cloned := message
				return &cloned, nil
			}
		}
	}

	return nil, nil
}

func (r *fakeSessionRepo) CreateSessionWithMessages(_ context.Context, title string, inputs []postgres.AssistantMessageInput) (*postgres.AssistantSession, []postgres.AssistantMessage, error) {
	sessionID := "session-created"
	now := time.Now()
	session := postgres.AssistantSession{
		ID:            sessionID,
		Title:         title,
		LastMessageAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	r.sessions[sessionID] = session

	messages := make([]postgres.AssistantMessage, 0, len(inputs))
	for index, input := range inputs {
		message := postgres.AssistantMessage{
			ID:         "message-created-" + string(rune('a'+index)),
			SessionID:  sessionID,
			Role:       input.Role,
			Kind:       input.Kind,
			SequenceNo: index + 1,
			Payload:    input.Payload,
			CreatedAt:  now,
		}
		messages = append(messages, message)
	}

	r.messages[sessionID] = append(r.messages[sessionID], messages...)
	return &session, messages, nil
}

func (r *fakeSessionRepo) AppendMessages(_ context.Context, sessionID string, inputs []postgres.AssistantMessageInput) ([]postgres.AssistantMessage, error) {
	base := len(r.messages[sessionID])
	now := time.Now()
	messages := make([]postgres.AssistantMessage, 0, len(inputs))
	for index, input := range inputs {
		message := postgres.AssistantMessage{
			ID:         "message-appended-" + string(rune('a'+base+index)),
			SessionID:  sessionID,
			Role:       input.Role,
			Kind:       input.Kind,
			SequenceNo: base + index + 1,
			Payload:    input.Payload,
			CreatedAt:  now,
		}
		messages = append(messages, message)
	}

	r.messages[sessionID] = append(r.messages[sessionID], messages...)

	session := r.sessions[sessionID]
	session.LastMessageAt = now
	session.UpdatedAt = now
	r.sessions[sessionID] = session

	return messages, nil
}

func (r *fakeSessionRepo) DeleteSession(_ context.Context, id string) (bool, error) {
	if _, ok := r.sessions[id]; !ok {
		return false, nil
	}

	delete(r.sessions, id)
	delete(r.messages, id)
	return true, nil
}

func (r *fakeSessionRepo) seedSession(title string) postgres.AssistantSession {
	now := time.Now()
	session := postgres.AssistantSession{
		ID:            "session-seeded-" + title,
		Title:         title,
		LastMessageAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	r.sessions[session.ID] = session
	return session
}

func (r *fakeSessionRepo) seedMessage(sessionID string, message postgres.AssistantMessage) postgres.AssistantMessage {
	r.messages[sessionID] = append(r.messages[sessionID], message)
	return message
}

func seedTextConversationHistory(t *testing.T, repo *fakeSessionRepo, sessionID string, count int) {
	t.Helper()

	for index := 0; index < count; index++ {
		role := RoleUser
		if index%2 == 1 {
			role = RoleAssistant
		}

		repo.seedMessage(sessionID, postgres.AssistantMessage{
			ID:         fmt.Sprintf("seed-text-%02d", index+1),
			SessionID:  sessionID,
			Role:       role,
			Kind:       KindText,
			SequenceNo: len(repo.messages[sessionID]) + 1,
			Payload:    mustJSON(t, TextPayload{Content: fmt.Sprintf("历史文本-%02d", index+1)}),
			CreatedAt:  time.Now(),
		})
	}
}

type fakeDocumentImporter struct {
	result    *ImportDocumentResult
	err       error
	lastInput *ImportDocumentInput
}

func (i *fakeDocumentImporter) ImportDocument(_ context.Context, input ImportDocumentInput) (*ImportDocumentResult, error) {
	cloned := input
	cloned.Content = append([]byte(nil), input.Content...)
	i.lastInput = &cloned
	if i.err != nil {
		return nil, i.err
	}

	return i.result, nil
}

type fakeFileStore struct {
	result       *filestore.StoredFile
	err          error
	savedContent []byte
}

func (s *fakeFileStore) Save(_ context.Context, _ string, content []byte) (*filestore.StoredFile, error) {
	s.savedContent = append([]byte(nil), content...)
	if s.err != nil {
		return nil, s.err
	}

	return s.result, nil
}

type fakeUploadedFileRepo struct {
	createInput       *postgres.UploadedFileCreateParams
	result            *postgres.UploadedFile
	updatedFileID     string
	updatedResourceID string
}

func (r *fakeUploadedFileRepo) Create(_ context.Context, input postgres.UploadedFileCreateParams) (*postgres.UploadedFile, error) {
	r.createInput = &input
	if r.result != nil {
		return r.result, nil
	}

	return &postgres.UploadedFile{ID: "file-default"}, nil
}

func (r *fakeUploadedFileRepo) UpdateResourceID(_ context.Context, fileID string, resourceID string) error {
	r.updatedFileID = fileID
	r.updatedResourceID = resourceID
	return nil
}

type fakeTaskCreator struct {
	result *postgres.Task
	err    error
	calls  int
	seen   map[string]bool
}

func (c *fakeTaskCreator) CreateTaskFromAssistantSuggestion(
	_ context.Context,
	_ string,
	_ string,
	sourceMessageID string,
) (*postgres.Task, bool, error) {
	c.calls++
	if c.err != nil {
		return nil, false, c.err
	}
	if c.seen == nil {
		c.seen = make(map[string]bool)
	}
	if c.seen[sourceMessageID] {
		return c.result, false, nil
	}
	c.seen[sourceMessageID] = true

	return c.result, true, nil
}

type fakeChatResponder struct {
	lastInput   *ChatCompletionInput
	result      *ChatCompletionResult
	err         error
	stream      *fakeChatStream
	replyCalls  int
	streamCalls int
}

func (r *fakeChatResponder) Reply(_ context.Context, input ChatCompletionInput) (*ChatCompletionResult, error) {
	r.replyCalls++
	copied := input
	copied.History = append([]postgres.AssistantMessage(nil), input.History...)
	copied.Citations = append([]citation.Citation(nil), input.Citations...)
	r.lastInput = &copied

	if r.err != nil {
		return nil, r.err
	}
	if r.result == nil {
		return &ChatCompletionResult{
			Reply: "默认测试回复",
		}, nil
	}

	return r.result, nil
}

func (r *fakeChatResponder) Stream(_ context.Context, input ChatCompletionInput) (chatStream, error) {
	r.streamCalls++
	copied := input
	copied.History = append([]postgres.AssistantMessage(nil), input.History...)
	copied.Citations = append([]citation.Citation(nil), input.Citations...)
	r.lastInput = &copied

	if r.err != nil {
		return nil, r.err
	}
	if r.stream == nil {
		return &fakeChatStream{}, nil
	}

	return r.stream, nil
}

type fakeChatStream struct {
	chunks []string
	errs   []error
	index  int
}

func (s *fakeChatStream) Recv() (string, error) {
	if s.index < len(s.chunks) {
		chunk := s.chunks[s.index]
		var err error
		if s.index < len(s.errs) {
			err = s.errs[s.index]
		}
		s.index++
		return chunk, err
	}

	errIndex := s.index
	s.index++
	if errIndex < len(s.errs) && s.errs[errIndex] != nil {
		return "", s.errs[errIndex]
	}

	return "", io.EOF
}

func (s *fakeChatStream) Close() error {
	return nil
}

type fakeResourceCitationRetriever struct {
	result []citation.Citation
	search func(context.Context, string, string, int) ([]citation.Citation, error)
	err    error

	calls      int
	resourceID string
	query      string
	limit      int
	queries    []string
}

func (r *fakeResourceCitationRetriever) SearchByResource(_ context.Context, resourceID string, query string, limit int) ([]citation.Citation, error) {
	r.calls++
	r.resourceID = resourceID
	r.query = query
	r.limit = limit
	r.queries = append(r.queries, query)
	if r.search != nil {
		return r.search(context.Background(), resourceID, query, limit)
	}
	if r.err != nil {
		return nil, r.err
	}

	return r.result, nil
}

type fakeConversationSummarizer struct {
	result      *SummaryResult
	err         error
	calls       int
	lastInput   SummaryInput
	onSummarize func(input SummaryInput)
}

func (s *fakeConversationSummarizer) Summarize(_ context.Context, input SummaryInput) (*SummaryResult, error) {
	s.calls++
	s.lastInput = input
	if s.onSummarize != nil {
		s.onSummarize(input)
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.result == nil {
		return nil, nil
	}

	cloned := *s.result
	return &cloned, nil
}

type fakeSummarySnapshotRepo struct {
	record                 *postgres.SessionContextSnapshotRecord
	err                    error
	advanceCalls           int
	lastSummary            *string
	lastNextBaseSequenceNo int
}

func (r *fakeSummarySnapshotRepo) GetBySessionID(_ context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.record == nil || r.record.SessionID != sessionID {
		return nil, nil
	}

	cloned := *r.record
	cloned.RollingSummary = cloneStringPointer(r.record.RollingSummary)
	cloned.ActiveResourceID = cloneStringPointer(r.record.ActiveResourceID)
	cloned.ActiveResourceTitle = cloneStringPointer(r.record.ActiveResourceTitle)
	cloned.ActiveResourceSourceType = cloneStringPointer(r.record.ActiveResourceSourceType)
	cloned.PendingTaskSuggestionMessageID = cloneStringPointer(r.record.PendingTaskSuggestionMessageID)
	cloned.PendingTaskInstruction = cloneStringPointer(r.record.PendingTaskInstruction)
	cloned.LatestTaskID = cloneStringPointer(r.record.LatestTaskID)
	cloned.LatestTaskStatus = cloneStringPointer(r.record.LatestTaskStatus)
	cloned.LatestTaskSourceMessageID = cloneStringPointer(r.record.LatestTaskSourceMessageID)
	return &cloned, nil
}

func (r *fakeSummarySnapshotRepo) AdvanceRollingSummary(_ context.Context, sessionID string, summary *string, nextBaseSequenceNo int) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	if r.record == nil || r.record.SessionID != sessionID {
		return false, nil
	}
	if r.record.SummaryBaseSequenceNo >= nextBaseSequenceNo {
		return false, nil
	}

	r.advanceCalls++
	r.lastSummary = cloneStringPointer(summary)
	r.lastNextBaseSequenceNo = nextBaseSequenceNo
	r.record.RollingSummary = cloneStringPointer(summary)
	r.record.SummaryBaseSequenceNo = nextBaseSequenceNo
	return true, nil
}

type fakeSessionContextProjector struct {
	err error

	initSessionIDs      []string
	fileReadyCalls      []SessionFileReadyProjection
	taskSuggestionCalls []TaskSuggestionProjection
	taskCreatedCalls    []TaskCreatedProjection
	groundingCalls      []GroundingStateProjection
}

func (p *fakeSessionContextProjector) InitSession(_ context.Context, sessionID string) error {
	if p.err != nil {
		return p.err
	}

	p.initSessionIDs = append(p.initSessionIDs, sessionID)
	return nil
}

func (p *fakeSessionContextProjector) ProjectSessionFileReady(_ context.Context, projection SessionFileReadyProjection) error {
	if p.err != nil {
		return p.err
	}

	p.fileReadyCalls = append(p.fileReadyCalls, projection)
	return nil
}

func (p *fakeSessionContextProjector) ProjectTaskSuggestionCreated(_ context.Context, projection TaskSuggestionProjection) error {
	if p.err != nil {
		return p.err
	}

	p.taskSuggestionCalls = append(p.taskSuggestionCalls, projection)
	return nil
}

func (p *fakeSessionContextProjector) ProjectTaskCreated(_ context.Context, projection TaskCreatedProjection) error {
	if p.err != nil {
		return p.err
	}

	p.taskCreatedCalls = append(p.taskCreatedCalls, projection)
	return nil
}

func (p *fakeSessionContextProjector) ProjectGroundingState(_ context.Context, projection GroundingStateProjection) error {
	if p.err != nil {
		return p.err
	}

	p.groundingCalls = append(p.groundingCalls, projection)
	return nil
}

func decodeTaskSuggestionPayload(t *testing.T, payload []byte) TaskSuggestionPayload {
	t.Helper()

	var value TaskSuggestionPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal task suggestion payload: %v", err)
	}

	return value
}

func decodeSessionFilePayload(t *testing.T, payload []byte) SessionFilePayload {
	t.Helper()

	var value SessionFilePayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal session file payload: %v", err)
	}

	return value
}

func decodeTaskCreatedPayload(t *testing.T, payload []byte) TaskCreatedPayload {
	t.Helper()

	var value TaskCreatedPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal task created payload: %v", err)
	}

	return value
}

func decodeTextPayload(t *testing.T, payload []byte) TextPayload {
	t.Helper()

	var value TextPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal text payload: %v", err)
	}

	return value
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	return payload
}
