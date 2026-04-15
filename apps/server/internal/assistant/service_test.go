package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

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

func TestStartConversationDoesNotSuggestTaskForCapabilityQuestion(t *testing.T) {
	repo := newFakeSessionRepo()
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
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

func TestAppendMessageCreatesTaskSuggestionFromResponderInstruction(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	instruction := "请把学生手册第二章整理成可执行的修订任务"
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply:           "这件事已经适合进入任务流了，我先给你一张建议卡。",
			TaskInstruction: &instruction,
		},
	}
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

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
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, retriever)

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
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, retriever)

	err := service.AppendMessageStream(context.Background(), session.ID, "继续看考勤", func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if retriever.calls != 1 || retriever.resourceID != resourceID || retriever.query != "继续看考勤" || retriever.limit != 4 {
		t.Fatalf("unexpected retriever call: %#v", retriever)
	}
	if len(responder.lastInput.Citations) != 1 || responder.lastInput.Citations[0].CitationID != "cite_1" {
		t.Fatalf("expected stream responder to receive retriever citation")
	}
}

func TestAppendMessageDoesNotCallRetrieverWithoutReadyResource(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("空白会话")
	responder := &fakeChatResponder{result: &ChatCompletionResult{Reply: "先继续聊需求。"}}
	retriever := &fakeResourceCitationRetriever{}
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, retriever)

	if _, err := service.AppendMessage(context.Background(), session.ID, "先看看目标"); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if retriever.calls != 0 {
		t.Fatalf("expected no retriever call without ready resource, got %d", retriever.calls)
	}
}

func TestStartConversationCreatesSessionAndTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply: "我先帮你梳理这份学生守则，再给你任务建议。",
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

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}

	if result.Messages[0].Role != RoleUser || result.Messages[0].Kind != KindText {
		t.Fatalf("expected first message to be user text, got role=%q kind=%q", result.Messages[0].Role, result.Messages[0].Kind)
	}

	if result.Messages[2].Kind != KindTaskSuggestion {
		t.Fatalf("expected third message to be task suggestion, got %q", result.Messages[2].Kind)
	}

	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if suggestion.CanCreate {
		t.Fatal("expected task suggestion without uploaded resource to be non-creatable")
	}

	if suggestion.Instruction != "请帮我修改这份学生守则，并整理成任务" {
		t.Fatalf("expected suggestion instruction to match input, got %q", suggestion.Instruction)
	}
}

func TestAppendMessageUsesLatestUploadedResourceForTaskSuggestion(t *testing.T) {
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

	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
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

func TestStartConversationStreamPersistsUserFirstAndAssistantAfterCompletion(t *testing.T) {
	repo := newFakeSessionRepo()
	responder := &fakeChatResponder{
		stream: &fakeChatStream{
			chunks: []string{"当然可以", "，我们先梳理目标。"},
		},
	}
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

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
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

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
	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{}, responder, nil)

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
	importer := fakeDocumentImporter{
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
	importer := fakeDocumentImporter{
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

	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{
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

	service := NewService(repo, fakeDocumentImporter{}, &fakeTaskCreator{
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
	service := NewService(repo, fakeDocumentImporter{}, taskCreator, &fakeChatResponder{}, nil)

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

type fakeDocumentImporter struct {
	result *ImportDocumentResult
	err    error
}

func (i fakeDocumentImporter) ImportDocument(context.Context, ImportDocumentInput) (*ImportDocumentResult, error) {
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
	lastInput *ChatCompletionInput
	result    *ChatCompletionResult
	err       error
	stream    *fakeChatStream
}

func (r *fakeChatResponder) Reply(_ context.Context, input ChatCompletionInput) (*ChatCompletionResult, error) {
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
	err    error

	calls      int
	resourceID string
	query      string
	limit      int
}

func (r *fakeResourceCitationRetriever) SearchByResource(_ context.Context, resourceID string, query string, limit int) ([]citation.Citation, error) {
	r.calls++
	r.resourceID = resourceID
	r.query = query
	r.limit = limit
	if r.err != nil {
		return nil, r.err
	}

	return r.result, nil
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
