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

// TestStartConversationUsesResponderReply 验证`startConversation`在依赖选择路径下的行为，防止同类回归。
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

// TestStartConversationCreatesEmptyContextSnapshot 验证`startConversation`在写入或副作用路径下的行为，防止同类回归。
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

// TestStartConversationDoesNotSuggestTaskForCapabilityQuestion 验证`startConversationDoesNotSuggestTaskForCapabilityQuestion`在特定边界条件下的行为，防止同类回归。
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

// TestAppendMessageBuildsTaskSuggestionFromPolicyDecisionNotReplySideEffect 验证任务建议只来自 deliberation/policy，不来自 reply 副产物。
func TestAppendMessageBuildsTaskSuggestionFromPolicyDecisionNotReplySideEffect(t *testing.T) {
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
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply:           "这件事已经适合进入任务流了，我先给你一张建议卡。",
			TaskInstruction: stringPointer("模型自己编的旧字段"),
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModeAnswerThenTaskCard,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				NeedsClarification:       false,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把学生手册第二章整理成可执行的修订任务"),
				Confidence:               0.92,
				Reasons:                  []string{"用户明确要求直接进入执行"},
			},
		}),
	)

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
	if suggestion.Instruction != "请把学生手册第二章整理成可执行的修订任务" {
		t.Fatalf("expected suggestion to come from deliberation decision, got %q", suggestion.Instruction)
	}
}

// TestAppendMessageUsesDeliberationBeforeReply 验证非流式路径会先跑 deliberation，再调用 responder。
func TestAppendMessageUsesDeliberationBeforeReply(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	order := make([]string, 0, 2)
	deliberator := &fakeDeliberationAgent{
		result: &DeliberationDecision{
			RequestKind:        "readback",
			ResponseMode:       ResponseModeAnswerWithGrounding,
			ChatFulfillable:    true,
			WorkflowCommitment: false,
			EvidenceSufficiency:"sufficient",
			Confidence:         0.83,
			Reasons:            []string{"这是文件内阅读请求"},
		},
		onDeliberate: func(state RuntimeState) {
			order = append(order, "deliberate")
			if state.ActiveResource == nil || state.ActiveResource.ID != "resource-1" {
				t.Fatalf("expected deliberation to receive runtime state with active resource, got %#v", state.ActiveResource)
			}
		},
	}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先把第三个项目按原文输出给你。"},
		onReply: func(input ChatCompletionInput) {
			order = append(order, "reply")
			if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerWithGrounding {
				t.Fatalf("expected responder to receive deliberation decision, got %#v", input.Decision)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithDeliberationAgent(deliberator),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(order) != 2 || order[0] != "deliberate" || order[1] != "reply" {
		t.Fatalf("expected call order deliberate -> reply, got %#v", order)
	}
}

// TestAppendMessageInvokesWorkflowPlannerOnlyWhenPolicyAllowsPlanning 验证只有 workflow candidate 才会调用 planner。
func TestAppendMessageInvokesWorkflowPlannerOnlyWhenPolicyAllowsPlanning(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("请把第三个项目改成产品经理版本"),
			Confidence:           0.91,
			Reasons:              []string{"用户明确要求开始执行"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先给你一张任务建议卡。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "workflow_command",
				ResponseMode:        ResponseModePlanThenAnswer,
				ChatFulfillable:     false,
				WorkflowCommitment:  true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.86,
				Reasons:             []string{"用户已经进入执行语义"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "直接开始改第三个项目，创建任务"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 1 {
		t.Fatalf("expected planner to run once, got %d", planner.calls)
	}
	if planner.lastState == nil || planner.lastState.ActiveResource == nil || planner.lastState.ActiveResource.ID != "resource-1" {
		t.Fatalf("expected planner to receive runtime state with active resource, got %#v", planner.lastState)
	}
	if planner.lastDecision == nil || planner.lastDecision.ResponseMode != ResponseModePlanThenAnswer {
		t.Fatalf("expected planner to receive workflow candidate decision, got %#v", planner.lastDecision)
	}
}

// TestAppendMessageDoesNotInvokeWorkflowPlannerForReadbackDecision 验证阅读型请求不会调用 planner。
func TestAppendMessageDoesNotInvokeWorkflowPlannerForReadbackDecision(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	planner := &fakeWorkflowPlanner{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先把第三个项目原文输出给你。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.85,
				Reasons:             []string{"这是文件内阅读请求"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 0 {
		t.Fatalf("expected readback request to skip planner, got %d calls", planner.calls)
	}
}

// TestAppendMessageUsesWorkflowPlannerInstructionForTaskSuggestion 验证任务建议使用 planner 收敛后的 instruction。
func TestAppendMessageUsesWorkflowPlannerInstructionForTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "students.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "这件事适合进入任务流。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("deliberation 初版 instruction"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求开始执行"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("planner 收敛后的 instruction"),
				CandidatePlanGoal:    stringPointer("把修订目标整理成可执行任务"),
				Confidence:           0.93,
				Reasons:              []string{"planner 认为材料足够且目标明确"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接开始整理第二章")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected user/assistant/task_suggestion messages, got %d", len(result.Messages))
	}

	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if suggestion.Instruction != "planner 收敛后的 instruction" {
		t.Fatalf("expected suggestion to use planner instruction, got %q", suggestion.Instruction)
	}
}

// TestAppendMessageSkipsTaskSuggestionWhenPlannerKeepsRequestChatFulfillable 验证 planner 收回聊天通道时不会弹任务建议。
func TestAppendMessageSkipsTaskSuggestionWhenPlannerKeepsRequestChatFulfillable(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先把第三个项目原文输出给你。"},
		onReply: func(input ChatCompletionInput) {
			if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerOnly || !input.Decision.ChatFulfillable {
				t.Fatalf("expected responder to see chat-fulfillable reply decision, got %#v", input.Decision)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "workflow_command",
				ResponseMode:        ResponseModePlanThenAnswer,
				ChatFulfillable:     false,
				WorkflowCommitment:  true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.79,
				Reasons:             []string{"需要 planner 判断是否真的要进入 workflow"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ChatFulfillable: true,
				Confidence:      0.83,
				Reasons:         []string{"当前请求仍可直接在聊天里完成"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected only user and assistant messages, got %d", len(result.Messages))
	}
	if result.Messages[1].Kind != KindText {
		t.Fatalf("expected assistant text reply, got %s", result.Messages[1].Kind)
	}
}

// TestAppendMessageTurnsPlannerClarificationIntoAssistantReplyWithoutTaskSuggestion 验证 planner 要求澄清时只回 assistant 文本。
func TestAppendMessageTurnsPlannerClarificationIntoAssistantReplyWithoutTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "你是想先输出原文，还是直接开始改写？"},
		onReply: func(input ChatCompletionInput) {
			if input.Decision == nil || !input.Decision.NeedsClarification {
				t.Fatalf("expected responder to receive clarification decision, got %#v", input.Decision)
			}
			if input.Decision.ClarificationQuestion == nil || *input.Decision.ClarificationQuestion != "你是想先输出原文，还是直接开始改写？" {
				t.Fatalf("expected clarification question to come from planner, got %#v", input.Decision)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "workflow_command",
				ResponseMode:        ResponseModePlanThenAnswer,
				ChatFulfillable:     false,
				WorkflowCommitment:  true,
				EvidenceSufficiency: "partial",
				Confidence:          0.72,
				Reasons:             []string{"planner 需要先分流阅读还是改写"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				NeedsClarification:   true,
				ClarificationQuestion: stringPointer("你是想先输出原文，还是直接开始改写？"),
				Confidence:            0.87,
				Reasons:               []string{"用户目标仍有歧义"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "整理一下第三个项目")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected only user and assistant messages, got %d", len(result.Messages))
	}
	if result.Messages[1].Kind != KindText {
		t.Fatalf("expected clarification to stay in assistant text lane, got %s", result.Messages[1].Kind)
	}
}

// TestAppendMessagePassesLatestResourceContextAndCitationsToResponder 验证`appendMessage`在合法输入或兼容路径下的行为，防止同类回归。
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

// TestAppendMessageStreamPassesSearchByResourceCitationsToResponder 验证`appendMessageStream`在合法输入或兼容路径下的行为，防止同类回归。
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

// TestStartConversationStreamRunsDeliberationBeforeMessageStarted 验证流式首轮会在发出 message_started 前完成 deliberation。
func TestStartConversationStreamRunsDeliberationBeforeMessageStarted(t *testing.T) {
	repo := newFakeSessionRepo()
	order := make([]string, 0, 3)
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{chunks: []string{"当然可以，我们先看第三个项目。"}},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "insufficient",
				Confidence:          0.8,
				Reasons:             []string{"首轮先按聊天回答处理"},
			},
			onDeliberate: func(RuntimeState) {
				order = append(order, "deliberate")
			},
		}),
	)

	err := service.StartConversationStream(context.Background(), "帮我先看看第三个项目", func(event StreamEvent) error {
		if event.Type == StreamEventMessageStarted {
			order = append(order, "message_started")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("start conversation stream: %v", err)
	}

	if len(order) != 2 || order[0] != "deliberate" || order[1] != "message_started" {
		t.Fatalf("expected deliberation before message_started, got %#v", order)
	}
}

// TestAppendMessageStreamInvokesWorkflowPlannerForWorkflowCandidate 验证流式 workflow candidate 会调用 planner。
func TestAppendMessageStreamInvokesWorkflowPlannerForWorkflowCandidate(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "students.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("请把学生手册第二章整理成可执行任务"),
			Confidence:           0.91,
			Reasons:              []string{"用户明确要求直接开始修改"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{chunks: []string{"这件事适合进入任务流。"}},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "workflow_command",
				ResponseMode:        ResponseModePlanThenAnswer,
				ChatFulfillable:     false,
				WorkflowCommitment:  true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.88,
				Reasons:             []string{"用户明确要求直接开始修改"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	err := service.AppendMessageStream(context.Background(), session.ID, "直接开始整理第二章", func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if planner.calls != 1 {
		t.Fatalf("expected stream path to run planner once, got %d", planner.calls)
	}
}

// TestAppendMessageStreamDoesNotEmitTaskSuggestionWhenPlannerKeepsChatFulfillable 验证流式路径被 planner 收回聊天后不会发 task_suggestion。
func TestAppendMessageStreamDoesNotEmitTaskSuggestionWhenPlannerKeepsChatFulfillable(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "students.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	var emittedTaskSuggestion bool
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{chunks: []string{"我先把第二章原文输出给你。"}},
			onStream: func(input ChatCompletionInput) {
				if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerOnly {
					t.Fatalf("expected stream responder to see answer-only decision, got %#v", input.Decision)
				}
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "workflow_command",
				ResponseMode:        ResponseModePlanThenAnswer,
				ChatFulfillable:     false,
				WorkflowCommitment:  true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.76,
				Reasons:             []string{"先让 planner 判断是否真的需要 workflow"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ChatFulfillable: true,
				Confidence:      0.82,
				Reasons:         []string{"当前请求仍可在聊天里直接完成"},
			},
		}),
	)

	err := service.AppendMessageStream(context.Background(), session.ID, "把第二章先输出一遍", func(event StreamEvent) error {
		if event.Type == StreamEventTaskSuggestion {
			emittedTaskSuggestion = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if emittedTaskSuggestion {
		t.Fatal("expected no task_suggestion event when planner keeps request chat-fulfillable")
	}
}

// TestAppendMessageStreamEmitsTaskSuggestionWhenPlannerPromotesWorkflow 验证流式路径会基于 planner 结论发 task_suggestion。
func TestAppendMessageStreamEmitsTaskSuggestionWhenPlannerPromotesWorkflow(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册优化")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "students.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	var suggestionEvent *postgres.AssistantMessage
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"这件事适合进入任务流。"},
				result: &ChatCompletionResult{
					Reply:           "这件事适合进入任务流。",
					TaskInstruction: stringPointer("模型旧字段，不应再被使用"),
				},
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "workflow_command",
				ResponseMode:        ResponseModePlanThenAnswer,
				ChatFulfillable:     false,
				WorkflowCommitment:  true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.91,
				Reasons:             []string{"用户明确要求直接开始修改"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("请把学生手册第二章整理成可执行任务"),
				Confidence:           0.94,
				Reasons:              []string{"planner 认为当前材料足够且目标明确"},
			},
		}),
	)

	err := service.AppendMessageStream(context.Background(), session.ID, "直接开始整理第二章", func(event StreamEvent) error {
		if event.Type == StreamEventTaskSuggestion {
			suggestionEvent = event.Message
		}
		return nil
	})
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if suggestionEvent == nil {
		t.Fatal("expected stream task suggestion event")
	}

	suggestion := decodeTaskSuggestionPayload(t, suggestionEvent.Payload)
	if suggestion.Instruction != "请把学生手册第二章整理成可执行任务" {
		t.Fatalf("expected stream suggestion to come from deliberation decision, got %q", suggestion.Instruction)
	}
}

// TestAppendMessageUsesOrdinalGroundingBeforeAnsweringForFirstProject 验证`appendMessage`在依赖选择路径下的行为，防止同类回归。
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

// TestAppendMessageTriggersAsyncSummaryRefreshAfterPersistingAssistantReply 验证`appendMessageTriggersAsyncSummaryRefreshAfterPersistingAssistantReply`在特定边界条件下的行为，防止同类回归。
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

// TestAppendMessageStreamSummaryRefreshFailureDoesNotFailMainTurn 验证`appendMessageStreamSummaryRefreshFailureDoesNotFailMainTurn`在特定边界条件下的行为，防止同类回归。
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

// TestSummaryRefreshSkipsWhenUnsummarizedTextIsBelowThreshold 验证`summaryRefresh`在跳过或空操作路径下的行为，防止同类回归。
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

// TestAppendMessageDoesNotCallRetrieverWithoutReadyResource 验证`appendMessageDoesNotCallRetrieverWithoutReadyResource`在特定边界条件下的行为，防止同类回归。
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

// TestAppendMessageInlineMaterialUsesInlineTextSourceType 验证`appendMessageInlineMaterial`在依赖选择路径下的行为，防止同类回归。
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

// TestStartConversationPersistsInlineMaterialBeforeAssistantReply 验证`startConversation`在写入或副作用路径下的行为，防止同类回归。
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

// TestAppendMessageInlineMaterialCreatesSessionFileMessageBeforeReply 验证`appendMessageInlineMaterial`在写入或副作用路径下的行为，防止同类回归。
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

// TestAppendMessageInlineMaterialAndExecutionCreatesTaskSuggestionInSameTurn 验证`appendMessageInlineMaterialAndExecution`在写入或副作用路径下的行为，防止同类回归。
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

// TestAppendMessageInlineMaterialWithoutExecutionDoesNotCreateTaskSuggestion 验证`appendMessageInlineMaterialWithoutExecutionDoesNotCreateTaskSuggestion`在特定边界条件下的行为，防止同类回归。
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

// TestAppendMessageInlineMaterialProjectsActiveResourceIntoSnapshot 验证`appendMessageInlineMaterial`在写入或副作用路径下的行为，防止同类回归。
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

// TestStartConversationDoesNotCreateTaskSuggestionWithoutMaterial 验证`startConversationDoesNotCreateTaskSuggestionWithoutMaterial`在特定边界条件下的行为，防止同类回归。
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

// TestAppendMessageDoesNotCreateTaskSuggestionForDiscussionQuestion 验证`appendMessageDoesNotCreateTaskSuggestionForDiscussionQuestion`在特定边界条件下的行为，防止同类回归。
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

// TestAppendMessageDoesNotDependOnModelTaskInstruction 验证 service 不再依赖 reply 里的 task_instruction 来驱动任务建议。
func TestAppendMessageDoesNotDependOnModelTaskInstruction(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("学生手册会话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "students.md",
			ResourceID:    "resource-1",
			ResourceTitle: "学生手册",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})
	instruction := "请把学生手册整理成执行任务"
	service := NewService(repo, &fakeDocumentImporter{}, &fakeTaskCreator{}, &fakeChatResponder{
		result: &ChatCompletionResult{
			Reply:           "要进入任务流还需要先给我可处理的材料。",
			TaskInstruction: &instruction,
		},
	}, nil, WithDeliberationAgent(&fakeDeliberationAgent{
		result: &DeliberationDecision{
			RequestKind:        "analysis",
			ResponseMode:       ResponseModeAnswerWithGrounding,
			ChatFulfillable:    true,
			WorkflowCommitment: false,
			EvidenceSufficiency:"sufficient",
			Confidence:         0.72,
			Reasons:            []string{"本轮仍应停留在聊天回答"},
		},
	}))

	result, err := service.AppendMessage(context.Background(), session.ID, "请先告诉我还缺什么材料")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 new messages when policy keeps request in answer lane, got %d", len(result.Messages))
	}
}

// TestAppendMessageKeepsReadbackRequestWithoutTaskSuggestion 验证已有资源下的 readback 请求只会回复，不会弹任务建议。
func TestAppendMessageKeepsReadbackRequestWithoutTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先把第三个项目原文输出给你。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.88,
				Reasons:             []string{"这是当前资源内的阅读型请求"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected readback request to persist only user+assistant messages, got %d", len(result.Messages))
	}
}

// TestAppendMessageCreatesTaskSuggestionForExecutionRequestWithReadyResource 验证`appendMessage`在写入或副作用路径下的行为，防止同类回归。
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

// TestAppendMessageProjectsPendingTaskSuggestionIntoSnapshot 验证`appendMessage`在写入或副作用路径下的行为，防止同类回归。
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

// TestStartConversationStreamPersistsUserFirstAndAssistantAfterCompletion 验证`startConversationStream`在写入或副作用路径下的行为，防止同类回归。
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

// TestStartConversationStreamEmitsSessionFileBeforeAssistantReplyWhenInlineMaterialDetected 验证`startConversationStream`在写入或副作用路径下的行为，防止同类回归。
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

// TestAppendMessageStreamEmitsSessionFileBeforeAssistantReplyWhenInlineMaterialDetected 验证`appendMessageStream`在写入或副作用路径下的行为，防止同类回归。
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

// TestAppendMessageStreamDoesNotPersistAssistantOnCancellation 验证`appendMessageStreamDoesNotPersistAssistantOnCancellation`在特定边界条件下的行为，防止同类回归。
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

// TestStartConversationStreamReturnsEmptyReplyError 验证`startConversationStream`在返回值分支下的行为，防止同类回归。
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

// TestUploadFilePersistsSessionFileMessage 验证`uploadFile`在写入或副作用路径下的行为，防止同类回归。
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

// TestUploadFileProjectsActiveResourceIntoSnapshot 验证`uploadFile`在写入或副作用路径下的行为，防止同类回归。
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

// TestUploadFileStoresOriginalFileAndPersistsFileID 验证`uploadFile`在写入或副作用路径下的行为，防止同类回归。
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

// TestConfirmTaskSuggestionCreatesTaskCreatedMessage 验证`confirmTaskSuggestion`在写入或副作用路径下的行为，防止同类回归。
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

// TestConfirmTaskSuggestionTaskCreatedDetailURLIncludesSessionQuery 验证`confirmTaskSuggestionTaskCreatedDetailURL`在流程控制路径下的行为，防止同类回归。
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

// TestConfirmTaskSuggestionProjectsLatestTaskIntoSnapshot 验证`confirmTaskSuggestion`在写入或副作用路径下的行为，防止同类回归。
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

// TestConfirmTaskSuggestionAppendsFailureSystemMessageWhenTaskCreationFails 验证`confirmTaskSuggestionAppendsFailureSystemMessageWhenTaskCreationFails`在特定边界条件下的行为，防止同类回归。
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

// TestConfirmTaskSuggestionReturnsExistingTaskWithoutAppendingDuplicateTaskCreatedMessage 验证`confirmTaskSuggestion`在返回值分支下的行为，防止同类回归。
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

// fakeSessionRepo 作为会话仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeSessionRepo struct {
	sessions map[string]postgres.AssistantSession
	messages map[string][]postgres.AssistantMessage
}

// newFakeSessionRepo 为测试场景处理 `newFake会话仓储` 的辅助步骤，减少重复搭建逻辑。
func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{
		sessions: make(map[string]postgres.AssistantSession),
		messages: make(map[string][]postgres.AssistantMessage),
	}
}

// ListSessions 实现测试替身需要的 `ListSessions` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionRepo) ListSessions(context.Context) ([]postgres.AssistantSession, error) {
	items := make([]postgres.AssistantSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		items = append(items, session)
	}

	return items, nil
}

// GetSessionByID 实现测试替身需要的 `GetSessionByID` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionRepo) GetSessionByID(_ context.Context, id string) (*postgres.AssistantSession, error) {
	session, ok := r.sessions[id]
	if !ok {
		return nil, nil
	}

	return &session, nil
}

// ListMessages 实现测试替身需要的 `ListMessages` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionRepo) ListMessages(_ context.Context, sessionID string) ([]postgres.AssistantMessage, error) {
	items := r.messages[sessionID]
	cloned := make([]postgres.AssistantMessage, len(items))
	copy(cloned, items)
	return cloned, nil
}

// ListMessagesAfterSequence 实现测试替身需要的 `ListMessagesAfterSequence` 接口方法，为用例分支提供可控返回。
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

// GetMessageByID 实现测试替身需要的 `GetMessageByID` 接口方法，为用例分支提供可控返回。
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

// CreateSessionWithMessages 实现测试替身需要的 `CreateSessionWithMessages` 接口方法，为用例分支提供可控返回。
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

// AppendMessages 实现测试替身需要的 `AppendMessages` 接口方法，为用例分支提供可控返回。
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

// DeleteSession 实现测试替身需要的 `DeleteSession` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionRepo) DeleteSession(_ context.Context, id string) (bool, error) {
	if _, ok := r.sessions[id]; !ok {
		return false, nil
	}

	delete(r.sessions, id)
	delete(r.messages, id)
	return true, nil
}

// seedSession 实现测试替身需要的 `seedSession` 接口方法，为用例分支提供可控返回。
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

// seedMessage 实现测试替身需要的 `seedMessage` 接口方法，为用例分支提供可控返回。
func (r *fakeSessionRepo) seedMessage(sessionID string, message postgres.AssistantMessage) postgres.AssistantMessage {
	r.messages[sessionID] = append(r.messages[sessionID], message)
	return message
}

// seedTextConversationHistory 为测试场景补齐 `文本会话历史` 所需数据，减少重复造数。
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

// fakeDocumentImporter 作为文档Importer的测试替身，用于在用例里提供可控的依赖行为。
type fakeDocumentImporter struct {
	result    *ImportDocumentResult
	err       error
	lastInput *ImportDocumentInput
}

// ImportDocument 实现测试替身需要的 `ImportDocument` 接口方法，为用例分支提供可控返回。
func (i *fakeDocumentImporter) ImportDocument(_ context.Context, input ImportDocumentInput) (*ImportDocumentResult, error) {
	cloned := input
	cloned.Content = append([]byte(nil), input.Content...)
	i.lastInput = &cloned
	if i.err != nil {
		return nil, i.err
	}

	return i.result, nil
}

// fakeFileStore 作为文件Store的测试替身，用于在用例里提供可控的依赖行为。
type fakeFileStore struct {
	result       *filestore.StoredFile
	err          error
	savedContent []byte
}

// Save 实现测试替身需要的 `Save` 接口方法，为用例分支提供可控返回。
func (s *fakeFileStore) Save(_ context.Context, _ string, content []byte) (*filestore.StoredFile, error) {
	s.savedContent = append([]byte(nil), content...)
	if s.err != nil {
		return nil, s.err
	}

	return s.result, nil
}

// fakeUploadedFileRepo 作为Uploaded文件仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeUploadedFileRepo struct {
	createInput       *postgres.UploadedFileCreateParams
	result            *postgres.UploadedFile
	updatedFileID     string
	updatedResourceID string
}

// Create 实现测试替身需要的 `Create` 接口方法，为用例分支提供可控返回。
func (r *fakeUploadedFileRepo) Create(_ context.Context, input postgres.UploadedFileCreateParams) (*postgres.UploadedFile, error) {
	r.createInput = &input
	if r.result != nil {
		return r.result, nil
	}

	return &postgres.UploadedFile{ID: "file-default"}, nil
}

// UpdateResourceID 实现测试替身需要的 `UpdateResourceID` 接口方法，为用例分支提供可控返回。
func (r *fakeUploadedFileRepo) UpdateResourceID(_ context.Context, fileID string, resourceID string) error {
	r.updatedFileID = fileID
	r.updatedResourceID = resourceID
	return nil
}

// fakeTaskCreator 作为任务Creator的测试替身，用于在用例里提供可控的依赖行为。
type fakeTaskCreator struct {
	result *postgres.Task
	err    error
	calls  int
	seen   map[string]bool
}

// CreateTaskFromAssistantSuggestion 实现测试替身需要的 `CreateTaskFromAssistantSuggestion` 接口方法，为用例分支提供可控返回。
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

// fakeChatResponder 作为聊天Responder的测试替身，用于在用例里提供可控的依赖行为。
type fakeChatResponder struct {
	lastInput *ChatCompletionInput
	result    *ChatCompletionResult
	err       error
	stream    *fakeChatStream
	onReply   func(ChatCompletionInput)
	onStream  func(ChatCompletionInput)
}

// Reply 实现测试替身需要的 `Reply` 接口方法，为用例分支提供可控返回。
func (r *fakeChatResponder) Reply(_ context.Context, input ChatCompletionInput) (*ChatCompletionResult, error) {
	copied := input
	copied.History = append([]postgres.AssistantMessage(nil), input.History...)
	copied.Citations = append([]citation.Citation(nil), input.Citations...)
	r.lastInput = &copied
	if r.onReply != nil {
		r.onReply(copied)
	}

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

// Stream 实现测试替身需要的 `Stream` 接口方法，为用例分支提供可控返回。
func (r *fakeChatResponder) Stream(_ context.Context, input ChatCompletionInput) (chatStream, error) {
	copied := input
	copied.History = append([]postgres.AssistantMessage(nil), input.History...)
	copied.Citations = append([]citation.Citation(nil), input.Citations...)
	r.lastInput = &copied
	if r.onStream != nil {
		r.onStream(copied)
	}

	if r.err != nil {
		return nil, r.err
	}
	if r.stream == nil {
		return &fakeChatStream{}, nil
	}

	return r.stream, nil
}

// fakeChatStream 作为聊天流式消息的测试替身，用于在用例里提供可控的依赖行为。
type fakeChatStream struct {
	chunks    []string
	errs      []error
	index     int
	result    *ChatCompletionResult
	resultErr error
}

// Recv 实现测试替身需要的 `Recv` 接口方法，为用例分支提供可控返回。
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

// Close 实现测试替身需要的 `Close` 接口方法，为用例分支提供可控返回。
func (s *fakeChatStream) Close() error {
	return nil
}

// Result 返回测试流式结果的最终聚合值，便于覆盖带副产物的旧协议兼容场景。
func (s *fakeChatStream) Result() (*ChatCompletionResult, error) {
	if s.resultErr != nil {
		return nil, s.resultErr
	}
	if s.result != nil {
		return s.result, nil
	}

	return &ChatCompletionResult{
		Reply: strings.Join(s.chunks, ""),
	}, nil
}

// fakeDeliberationAgent 作为 deliberation agent 的测试替身，用于在用例里提供可控的依赖行为。
type fakeDeliberationAgent struct {
	result       *DeliberationDecision
	err          error
	lastState    *RuntimeState
	onDeliberate func(RuntimeState)
}

// Deliberate 实现测试替身需要的 `Deliberate` 接口方法，为用例分支提供可控返回。
func (a *fakeDeliberationAgent) Deliberate(_ context.Context, state RuntimeState) (*DeliberationDecision, error) {
	cloned := state
	cloned.Citations = append([]citation.Citation(nil), state.Citations...)
	cloned.History = append([]postgres.AssistantMessage(nil), state.History...)
	a.lastState = &cloned
	if a.onDeliberate != nil {
		a.onDeliberate(cloned)
	}
	if a.err != nil {
		return nil, a.err
	}
	if a.result == nil {
		return &DeliberationDecision{
			RequestKind:         "readback",
			ResponseMode:        ResponseModeAnswerWithGrounding,
			ChatFulfillable:     true,
			EvidenceSufficiency: "sufficient",
			Confidence:          0.5,
			Reasons:             []string{"default fake deliberation"},
		}, nil
	}

	clonedDecision := *a.result
	clonedDecision.ClarificationQuestion = normalizeOptionalText(a.result.ClarificationQuestion)
	clonedDecision.CandidateTaskInstruction = normalizeOptionalText(a.result.CandidateTaskInstruction)
	clonedDecision.CandidatePlanGoal = normalizeOptionalText(a.result.CandidatePlanGoal)
	clonedDecision.Reasons = normalizeDecisionReasons(a.result.Reasons)
	return &clonedDecision, nil
}

// fakeWorkflowPlanner 作为 workflow planner 的测试替身，用于在用例里提供可控的依赖行为。
type fakeWorkflowPlanner struct {
	result       *WorkflowPlanDecision
	err          error
	calls        int
	lastState    *RuntimeState
	lastDecision *DeliberationDecision
	onPlan       func(RuntimeState, *DeliberationDecision)
}

// Plan 实现测试替身需要的 `Plan` 接口方法，为用例分支提供可控返回。
func (p *fakeWorkflowPlanner) Plan(_ context.Context, state RuntimeState, decision *DeliberationDecision) (*WorkflowPlanDecision, error) {
	p.calls++

	clonedState := state
	clonedState.Citations = append([]citation.Citation(nil), state.Citations...)
	clonedState.History = append([]postgres.AssistantMessage(nil), state.History...)
	p.lastState = &clonedState

	if decision != nil {
		clonedDecision := *decision
		clonedDecision.ClarificationQuestion = normalizeOptionalText(decision.ClarificationQuestion)
		clonedDecision.CandidateTaskInstruction = normalizeOptionalText(decision.CandidateTaskInstruction)
		clonedDecision.CandidatePlanGoal = normalizeOptionalText(decision.CandidatePlanGoal)
		clonedDecision.Reasons = normalizeDecisionReasons(decision.Reasons)
		p.lastDecision = &clonedDecision
	} else {
		p.lastDecision = nil
	}

	if p.onPlan != nil {
		p.onPlan(clonedState, p.lastDecision)
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.result == nil {
		return &WorkflowPlanDecision{
			ChatFulfillable: true,
			Confidence:      0.5,
			Reasons:         []string{"default fake workflow planner"},
		}, nil
	}

	clonedPlan := *p.result
	clonedPlan.ClarificationQuestion = normalizeOptionalText(p.result.ClarificationQuestion)
	clonedPlan.CandidateInstruction = normalizeOptionalText(p.result.CandidateInstruction)
	clonedPlan.CandidatePlanGoal = normalizeOptionalText(p.result.CandidatePlanGoal)
	clonedPlan.MissingMaterials = append([]string(nil), p.result.MissingMaterials...)
	clonedPlan.Reasons = normalizeDecisionReasons(p.result.Reasons)
	return &clonedPlan, nil
}

// fakeResourceCitationRetriever 作为资源引用检索器的测试替身，用于在用例里提供可控的依赖行为。
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

// SearchByResource 实现测试替身需要的 `SearchByResource` 接口方法，为用例分支提供可控返回。
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

// fakeConversationSummarizer 作为会话Summarizer的测试替身，用于在用例里提供可控的依赖行为。
type fakeConversationSummarizer struct {
	result      *SummaryResult
	err         error
	calls       int
	lastInput   SummaryInput
	onSummarize func(input SummaryInput)
}

// Summarize 实现测试替身需要的 `Summarize` 接口方法，为用例分支提供可控返回。
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

// fakeSummarySnapshotRepo 作为摘要快照仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeSummarySnapshotRepo struct {
	record                 *postgres.SessionContextSnapshotRecord
	err                    error
	advanceCalls           int
	lastSummary            *string
	lastNextBaseSequenceNo int
}

// GetBySessionID 实现测试替身需要的 `GetBySessionID` 接口方法，为用例分支提供可控返回。
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

// AdvanceRollingSummary 实现测试替身需要的 `AdvanceRollingSummary` 接口方法，为用例分支提供可控返回。
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

// fakeSessionContextProjector 作为会话上下文投影器的测试替身，用于在用例里提供可控的依赖行为。
type fakeSessionContextProjector struct {
	err error

	initSessionIDs      []string
	fileReadyCalls      []SessionFileReadyProjection
	taskSuggestionCalls []TaskSuggestionProjection
	taskCreatedCalls    []TaskCreatedProjection
	groundingCalls      []GroundingStateProjection
}

// InitSession 实现测试替身需要的 `InitSession` 接口方法，为用例分支提供可控返回。
func (p *fakeSessionContextProjector) InitSession(_ context.Context, sessionID string) error {
	if p.err != nil {
		return p.err
	}

	p.initSessionIDs = append(p.initSessionIDs, sessionID)
	return nil
}

// ProjectSessionFileReady 实现测试替身需要的 `ProjectSessionFileReady` 接口方法，为用例分支提供可控返回。
func (p *fakeSessionContextProjector) ProjectSessionFileReady(_ context.Context, projection SessionFileReadyProjection) error {
	if p.err != nil {
		return p.err
	}

	p.fileReadyCalls = append(p.fileReadyCalls, projection)
	return nil
}

// ProjectTaskSuggestionCreated 实现测试替身需要的 `ProjectTaskSuggestionCreated` 接口方法，为用例分支提供可控返回。
func (p *fakeSessionContextProjector) ProjectTaskSuggestionCreated(_ context.Context, projection TaskSuggestionProjection) error {
	if p.err != nil {
		return p.err
	}

	p.taskSuggestionCalls = append(p.taskSuggestionCalls, projection)
	return nil
}

// ProjectTaskCreated 实现测试替身需要的 `ProjectTaskCreated` 接口方法，为用例分支提供可控返回。
func (p *fakeSessionContextProjector) ProjectTaskCreated(_ context.Context, projection TaskCreatedProjection) error {
	if p.err != nil {
		return p.err
	}

	p.taskCreatedCalls = append(p.taskCreatedCalls, projection)
	return nil
}

// ProjectGroundingState 实现测试替身需要的 `ProjectGroundingState` 接口方法，为用例分支提供可控返回。
func (p *fakeSessionContextProjector) ProjectGroundingState(_ context.Context, projection GroundingStateProjection) error {
	if p.err != nil {
		return p.err
	}

	p.groundingCalls = append(p.groundingCalls, projection)
	return nil
}

// decodeTaskSuggestionPayload 在测试里解码 `任务建议载荷`，便于直接断言结构化内容。
func decodeTaskSuggestionPayload(t *testing.T, payload []byte) TaskSuggestionPayload {
	t.Helper()

	var value TaskSuggestionPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal task suggestion payload: %v", err)
	}

	return value
}

// decodeSessionFilePayload 在测试里解码 `会话文件载荷`，便于直接断言结构化内容。
func decodeSessionFilePayload(t *testing.T, payload []byte) SessionFilePayload {
	t.Helper()

	var value SessionFilePayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal session file payload: %v", err)
	}

	return value
}

// decodeTaskCreatedPayload 在测试里解码 `任务Created载荷`，便于直接断言结构化内容。
func decodeTaskCreatedPayload(t *testing.T, payload []byte) TaskCreatedPayload {
	t.Helper()

	var value TaskCreatedPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal task created payload: %v", err)
	}

	return value
}

// decodeTextPayload 在测试里解码 `文本载荷`，便于直接断言结构化内容。
func decodeTextPayload(t *testing.T, payload []byte) TextPayload {
	t.Helper()

	var value TextPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal text payload: %v", err)
	}

	return value
}

// mustJSON 在测试里强制构造 `JSON`，失败时立即终止当前用例。
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	return payload
}
