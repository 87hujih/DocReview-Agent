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

	documentparser "agent_project/apps/server/internal/document/parser"
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

// TestAppendMessageBuildsTaskSuggestionFromPolicyDecisionNotReplySideEffect 验证任务建议只来自 deliberation / gate，而不是 reply 副产物。
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModeAnswerThenTaskCard,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				NeedsClarification:       false,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把学生手册第二章整理成可执行的修订任务"),
				Confidence:               0.92,
				Reasons:                  []string{"用户明确要求直接进入执行"},
			}),
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
	if !strings.Contains(reply.Content, "尚未写回原文件") {
		t.Fatalf("expected proposal-phase reply audit to keep non-executed wording, got %q", reply.Content)
	}

	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if suggestion.Instruction != "请把学生手册第二章整理成可执行的修订任务" {
		t.Fatalf("expected suggestion to come from deliberation decision, got %q", suggestion.Instruction)
	}
}

// TestAppendMessageUsesDeliberationBeforeReply 验证非流式聊天回复路径会先跑 deliberation，再调用 responder。
func TestAppendMessageUsesDeliberationBeforeReply(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历对话")
	order := make([]string, 0, 2)
	deliberator := &fakeDeliberationAgent{
		result: &DeliberationDecision{
			RequestKind:         "analysis",
			ResponseMode:        ResponseModeAnswerOnly,
			ChatFulfillable:     true,
			WorkflowCommitment:  false,
			EvidenceSufficiency: "sufficient",
			Confidence:          0.83,
			Reasons:             []string{"这是文件内分析请求，应进入 responder"},
		},
		onDeliberate: func(_ RuntimeState) {
			order = append(order, "deliberate")
		},
	}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先分析第三个项目为什么显得薄弱。"},
		onReply: func(input ChatCompletionInput) {
			order = append(order, "reply")
			if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerOnly {
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

	if _, err := service.AppendMessage(context.Background(), session.ID, "详细分析第三个项目为什么显得弱"); err != nil {
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把第三个项目改成产品经理版本"),
				Confidence:               0.86,
				Reasons:                  []string{"用户已经进入执行语义"},
			}),
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("deliberation 初版 instruction"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求开始执行"},
			}),
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
				NeedsClarification:    true,
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

// TestAppendMessageInvokesVerifierOnlyAfterPlannerPromotesWorkflow 验证 verifier 只会在 planner 放行 workflow 后执行。
func TestAppendMessageInvokesVerifierOnlyAfterPlannerPromotesWorkflow(t *testing.T) {
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
			CandidateInstruction: stringPointer("planner instruction"),
			Confidence:           0.9,
			Reasons:              []string{"planner 认为当前请求需要 workflow"},
		},
	}
	verifier := &fakeWorkflowVerifier{
		result: &WorkflowVerificationDecision{
			ApproveWorkflow: true,
			Confidence:      0.92,
			Reasons:         []string{"verifier 放行当前 promotion"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "这件事适合进入任务流。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把第三个项目改成产品经理版本"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求开始执行"},
			}),
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "直接开始改第三个项目，创建任务"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 1 {
		t.Fatalf("expected planner to run once, got %d", planner.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected verifier to run once after planner promotion, got %d", verifier.calls)
	}
	if verifier.lastPlan == nil || !verifier.lastPlan.ShouldEnterWorkflow {
		t.Fatalf("expected verifier to receive promoted planner result, got %#v", verifier.lastPlan)
	}
}

// TestAppendMessageDoesNotInvokeVerifierForReadbackDecision 验证阅读型请求不会触发 verifier。
func TestAppendMessageDoesNotInvokeVerifierForReadbackDecision(t *testing.T) {
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
	verifier := &fakeWorkflowVerifier{}
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
				Confidence:          0.84,
				Reasons:             []string{"这是文件内阅读请求"},
			},
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 0 {
		t.Fatalf("expected readback request to skip planner, got %d calls", planner.calls)
	}
	if verifier.calls != 0 {
		t.Fatalf("expected readback request to skip verifier, got %d calls", verifier.calls)
	}
}

// TestAppendMessageSkipsTaskSuggestionWhenVerifierDowngradesToChat 验证 verifier 收回聊天通道后不会弹任务建议。
func TestAppendMessageSkipsTaskSuggestionWhenVerifierDowngradesToChat(t *testing.T) {
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
				t.Fatalf("expected responder to receive downgraded answer-only decision, got %#v", input.Decision)
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
				Confidence:          0.78,
				Reasons:             []string{"deliberation 先把它当成 workflow 候选"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("planner instruction"),
				Confidence:           0.82,
				Reasons:              []string{"planner 倾向进入 workflow"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				DowngradeToChat: true,
				Confidence:      0.87,
				Reasons:         []string{"当前请求仍可在聊天里完成"},
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

// TestAppendMessageUsesVerifierClarificationInsteadOfTaskSuggestion 验证 verifier 要求澄清时只回复澄清文本。
func TestAppendMessageUsesVerifierClarificationInsteadOfTaskSuggestion(t *testing.T) {
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
				t.Fatalf("expected responder to receive verifier clarification, got %#v", input.Decision)
			}
			if input.Decision.ClarificationQuestion == nil || *input.Decision.ClarificationQuestion != "你是想先输出原文，还是直接开始改写？" {
				t.Fatalf("expected clarification question from verifier, got %#v", input.Decision.ClarificationQuestion)
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
				Confidence:          0.73,
				Reasons:             []string{"先让 planner 判断是否真的该进入 workflow"},
			},
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("整理第三个项目"),
				Confidence:           0.79,
				Reasons:              []string{"planner 倾向进入 workflow"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				NeedsClarification:    true,
				ClarificationQuestion: stringPointer("你是想先输出原文，还是直接开始改写？"),
				Confidence:            0.88,
				Reasons:               []string{"当前目标仍有歧义"},
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
		t.Fatalf("expected clarification to stay in text lane, got %s", result.Messages[1].Kind)
	}
}

// TestAppendMessageUsesVerifierRevisedInstructionWhenApproved 验证 verifier 放行时可收紧最终任务 instruction。
func TestAppendMessageUsesVerifierRevisedInstructionWhenApproved(t *testing.T) {
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请直接开始整理第二章"),
				Confidence:               0.89,
				Reasons:                  []string{"用户明确要求开始执行"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("planner instruction A"),
				Confidence:           0.91,
				Reasons:              []string{"planner 认为材料充足"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				ApproveWorkflow:    true,
				RevisedInstruction: stringPointer("verifier instruction B"),
				Confidence:         0.94,
				Reasons:            []string{"verifier 收紧了候选 instruction"},
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
	if suggestion.Instruction != "verifier instruction B" {
		t.Fatalf("expected suggestion to use verifier instruction, got %q", suggestion.Instruction)
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请直接开始整理第二章"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求直接开始修改"},
			}),
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把第二章整理成任务"),
				Confidence:               0.76,
				Reasons:                  []string{"先让 planner 判断是否真的需要 workflow"},
			}),
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请直接开始整理第二章"),
				Confidence:               0.91,
				Reasons:                  []string{"用户明确要求直接开始修改"},
			}),
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

// TestAppendMessageStreamInvokesVerifierAfterWorkflowPlanner 验证流式 workflow promotion 会在 planner 之后调用 verifier。
func TestAppendMessageStreamInvokesVerifierAfterWorkflowPlanner(t *testing.T) {
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
			CandidateInstruction: stringPointer("planner instruction"),
			Confidence:           0.9,
			Reasons:              []string{"planner 认为当前请求需要 workflow"},
		},
	}
	verifier := &fakeWorkflowVerifier{
		result: &WorkflowVerificationDecision{
			ApproveWorkflow: true,
			Confidence:      0.93,
			Reasons:         []string{"verifier 放行当前 promotion"},
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请直接开始整理第二章"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求直接开始修改"},
			}),
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
	)

	err := service.AppendMessageStream(context.Background(), session.ID, "直接开始整理第二章", func(StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if planner.calls != 1 {
		t.Fatalf("expected stream planner to run once, got %d", planner.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected stream verifier to run once, got %d", verifier.calls)
	}
}

// TestAppendMessageStreamDoesNotEmitTaskSuggestionWhenVerifierDowngrades 验证流式路径被 verifier 收回聊天后不会发 task_suggestion。
func TestAppendMessageStreamDoesNotEmitTaskSuggestionWhenVerifierDowngrades(t *testing.T) {
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
					t.Fatalf("expected stream responder to receive downgraded answer-only decision, got %#v", input.Decision)
				}
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把第二章整理成任务"),
				Confidence:               0.77,
				Reasons:                  []string{"先让 planner 判断是否真的需要 workflow"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("planner instruction"),
				Confidence:           0.81,
				Reasons:              []string{"planner 倾向进入 workflow"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				DowngradeToChat: true,
				Confidence:      0.86,
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
		t.Fatal("expected no task_suggestion event when verifier downgrades workflow")
	}
}

// TestAppendMessageStreamEmitsTaskSuggestionWhenVerifierApproves 验证流式路径会在 verifier 放行后发 task_suggestion。
func TestAppendMessageStreamEmitsTaskSuggestionWhenVerifierApproves(t *testing.T) {
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
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请直接开始整理第二章"),
				Confidence:               0.91,
				Reasons:                  []string{"用户明确要求直接开始修改"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("planner instruction A"),
				Confidence:           0.93,
				Reasons:              []string{"planner 认为当前材料足够"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				ApproveWorkflow:    true,
				RevisedInstruction: stringPointer("verifier instruction B"),
				Confidence:         0.95,
				Reasons:            []string{"verifier 收紧了候选 instruction"},
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
		t.Fatal("expected stream task suggestion event after verifier approval")
	}

	suggestion := decodeTaskSuggestionPayload(t, suggestionEvent.Payload)
	if suggestion.Instruction != "verifier instruction B" {
		t.Fatalf("expected stream suggestion to use verifier instruction, got %q", suggestion.Instruction)
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
	service := NewService(
		repo,
		importer,
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "这轮已经满足执行条件，我先给你任务建议。",
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: &instruction,
				CandidatePlanGoal:        stringPointer("把简历改成产品经理版本"),
				Confidence:               0.9,
				Reasons:                  []string{"同轮内联材料已经足以进入执行"},
			}),
		}),
	)

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
			RequestKind:         "analysis",
			ResponseMode:        ResponseModeAnswerWithGrounding,
			ChatFulfillable:     true,
			WorkflowCommitment:  false,
			EvidenceSufficiency: "sufficient",
			Confidence:          0.72,
			Reasons:             []string{"本轮仍应停留在聊天回答"},
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

// TestAppendMessageReturnsDeterministicReadForCurrentFileExcerpt 验证启用 direct access 后，读取型请求会直接返回 canonical read 结果。
func TestAppendMessageReturnsDeterministicReadForCurrentFileExcerpt(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件阅读")
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "第三个项目正文"},
		},
	}
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("deterministic read should not call chat responder reply")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected user + assistant messages only, got %d", len(result.Messages))
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != "第三个项目正文" {
		t.Fatalf("expected deterministic reply %q, got %q", "第三个项目正文", reply.Content)
	}
}

// TestAppendMessageDeterministicReadDoesNotCreateTaskSuggestion 验证 deterministic read 请求不会额外生成任务建议。
func TestAppendMessageDeterministicReadDoesNotCreateTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件阅读")
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第一个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected no task suggestion for deterministic read, got %d messages", len(result.Messages))
	}
}

// TestAppendMessageStreamUsesDeterministicReadForCurrentFileExcerpt 验证流式路径会复用 deterministic read，不再调用聊天模型流式回复。
func TestAppendMessageStreamUsesDeterministicReadForCurrentFileExcerpt(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件阅读")
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "第三个项目正文"},
		},
	}
	responder := &fakeChatResponder{
		onStream: func(ChatCompletionInput) {
			t.Fatal("deterministic read should not call chat responder stream")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	var events []StreamEvent
	if err := service.AppendMessageStream(context.Background(), session.ID, "把第三个项目先输出一遍", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected message_started -> delta -> completed, got %d events", len(events))
	}
	if events[0].Type != StreamEventMessageStarted || events[1].Type != StreamEventMessageDelta || events[2].Type != StreamEventMessageCompleted {
		t.Fatalf("expected deterministic read stream events, got %#v", []string{events[0].Type, events[1].Type, events[2].Type})
	}
	if events[1].Delta != "第三个项目正文" {
		t.Fatalf("expected deterministic delta %q, got %q", "第三个项目正文", events[1].Delta)
	}
}

// TestAppendMessageBuildsGroundedAnalysisContextFromCanonicalSectionContent 验证分析型请求会把 canonical section 正文注入 responder 输入。
func TestAppendMessageBuildsGroundedAnalysisContextFromCanonicalSectionContent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件分析")
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "这是第三个项目的完整正文"},
		},
	}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "这个项目的主要问题是结果表达偏弱。"},
		onReply: func(input ChatCompletionInput) {
			if !strings.Contains(input.CanonicalAnalysisContext, "这是第三个项目的完整正文") {
				t.Fatalf("expected canonical analysis context, got %q", input.CanonicalAnalysisContext)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求需要先读 section 再分析"},
			},
		}),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "第三个项目的问题是什么"); err != nil {
		t.Fatalf("append message: %v", err)
	}
}

// TestAppendMessageAnalyzeNaturalQuestionDoesNotDependOnTopKCitations 验证自然分析问法在当前文件 ready 时会直接走 canonical 文档，而不是先依赖 topK citations。
func TestAppendMessageAnalyzeNaturalQuestionDoesNotDependOnTopKCitations(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件分析")
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
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "这是第三个项目的完整正文"},
		},
	}
	retriever := &fakeResourceCitationRetriever{}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "这个项目的主要问题是结果表达偏弱。"},
		onReply: func(input ChatCompletionInput) {
			if !strings.Contains(input.CanonicalAnalysisContext, "这是第三个项目的完整正文") {
				t.Fatalf("expected canonical analysis context, got %q", input.CanonicalAnalysisContext)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		retriever,
		WithCurrentDocumentLoader(NewCurrentDocumentLoader(currentFileReader)),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "结合我刚上传的简历，详细分析第三个项目的问题"); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if retriever.calls != 0 {
		t.Fatalf("expected natural analysis question to avoid eager topK retrieval, got %d calls", retriever.calls)
	}
}

// TestAppendMessageFallsBackOnlyWithinCurrentFile 验证分析型请求定位失败后只会在当前文件内做一次 fallback，并继续收敛到 canonical section。
func TestAppendMessageFallsBackOnlyWithinCurrentFile(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件分析")
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "这是第三个项目的完整正文"},
		},
	}
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{
				SectionID:    "section-3",
				SectionType:  "project",
				SectionTitle: "慢跑计划",
				Snippet:      "这是第三个项目的片段命中。",
			},
		},
	}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "这个项目的主要问题是结果表达偏弱。"},
		onReply: func(input ChatCompletionInput) {
			if !strings.Contains(input.CanonicalAnalysisContext, "这是第三个项目的完整正文") {
				t.Fatalf("expected canonical analysis context after fallback, got %q", input.CanonicalAnalysisContext)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		retriever,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.88,
				Reasons:             []string{"当前请求需要先读 section 再分析"},
			},
		}),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "这个项目的问题是什么"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if retriever.calls != 1 {
		t.Fatalf("expected exactly 1 current-file fallback retrieval, got %d", retriever.calls)
	}
	if retriever.resourceID != "resource-1" {
		t.Fatalf("expected fallback retrieval to stay within current file resource, got %q", retriever.resourceID)
	}
}

// TestAppendMessageReturnsExplicitFailureWhenCurrentFileTargetCannotBeLocated 验证当前文件内 fallback 仍失败时，助手会明确说明无法稳定定位目标内容。
func TestAppendMessageReturnsExplicitFailureWhenCurrentFileTargetCannotBeLocated(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件分析")
	currentFileReader := &fakeCurrentFileSectionReader{}
	retriever := &fakeResourceCitationRetriever{}
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("explicit failure path should not call chat responder")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		retriever,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "partial",
				Confidence:          0.66,
				Reasons:             []string{"当前请求需要先定位 section"},
			},
		}),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "这个项目的问题是什么")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if retriever.calls != 1 {
		t.Fatalf("expected exactly 1 current-file fallback retrieval, got %d", retriever.calls)
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != buildCurrentFileFailureReply("这个项目的问题是什么") {
		t.Fatalf("expected explicit failure reply, got %q", reply.Content)
	}
}

// TestAppendMessageListsAllProjectItemsFromOutlineWhenSemanticProjectSectionsMissing 验证当前文件缺少 project sections 时，仍会从 outline 列出全部项目。
func TestAppendMessageListsAllProjectItemsFromOutlineWhenSemanticProjectSectionsMissing(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("outline 项目列表")
	currentDocument := mustLoadCurrentDocumentForServiceTest(t, &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-outline-1",
			ResourceID: "resource-outline-1",
			Content:    "## 项目经历\n### 1. CampusHub\n负责活动报名。\n### 2. 选课助手\n负责排课推荐。\n### 3. 慢跑计划\n负责路线分析。",
		},
		versionStructure: &postgres.ResourceVersionStructure{
			VersionID: "version-outline-1",
			DocumentJSON: mustMarshalOutlineDocumentJSON(t, documentparser.ParsedDocument{
				SourceFormat: "markdown",
				Blocks: []documentparser.Block{
					{Type: documentparser.BlockHeading, Level: 2, Text: "项目经历"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "1. CampusHub"},
					{Type: documentparser.BlockParagraph, Text: "负责活动报名。"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "2. 选课助手"},
					{Type: documentparser.BlockParagraph, Text: "负责排课推荐。"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "3. 慢跑计划"},
					{Type: documentparser.BlockParagraph, Text: "负责路线分析。"},
				},
			}),
		},
	})
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("outline deterministic list should not call chat responder")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: "resource-outline-1", Title: "简历", Source: "upload"},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "这份简历里有哪些项目")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != "1. CampusHub\n2. 选课助手\n3. 慢跑计划" {
		t.Fatalf("expected outline-backed project list, got %q", reply.Content)
	}
}

// TestAppendMessageListsProjectsFromMarkdownFallbackOutlineForSmokePrompt 验证 live-like markdown 简历在缺少 structure_json 时仍能稳定列出项目。
func TestAppendMessageListsProjectsFromMarkdownFallbackOutlineForSmokePrompt(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("markdown fallback list")
	currentDocument := buildLiveLikeResumeCurrentDocumentWithoutStructure()
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("markdown fallback project list should not call chat responder")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: currentDocument.ResourceID, Title: currentDocument.Title, Source: currentDocument.SourceType},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "这份简历里有哪些项目？请按顺序列出来。")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != "1. Alpha 内容中台\n2. Beta 协作系统\n3. Gamma 风险引擎" {
		t.Fatalf("expected markdown fallback project list, got %q", reply.Content)
	}
}

// TestAppendMessageReadsThirdProjectFromOutlineNode 验证读取第三个项目时会直接命中 outline node，而不是依赖 section。
func TestAppendMessageReadsThirdProjectFromOutlineNode(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("outline 项目读取")
	currentDocument := &CurrentDocument{
		ResourceID: "resource-outline-2",
		VersionID:  "version-outline-2",
		Title:      "简历",
		SourceType: "upload",
		FullText:   "项目经历全文",
		Ready:      true,
		Outline: []OutlineNode{
			{NodeID: "document:version-outline-2", NodeKind: OutlineNodeDocument, Title: "全文", CanonicalContent: "项目经历全文"},
			{NodeID: "heading-projects", NodeKind: OutlineNodeHeadingSection, Title: "项目经历", CanonicalContent: "项目经历"},
			{NodeID: "project-1", NodeKind: OutlineNodeProjectItem, Title: "CampusHub", Ordinal: 1, ParentNodeID: "heading-projects", CanonicalContent: "CampusHub 正文"},
			{NodeID: "project-2", NodeKind: OutlineNodeProjectItem, Title: "选课助手", Ordinal: 2, ParentNodeID: "heading-projects", CanonicalContent: "选课助手 正文"},
			{NodeID: "project-3", NodeKind: OutlineNodeProjectItem, Title: "慢跑计划", Ordinal: 3, ParentNodeID: "heading-projects", CanonicalContent: "慢跑计划的完整正文"},
		},
	}
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("outline deterministic read should not call chat responder")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: "resource-outline-2", Title: "简历", Source: "upload"},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != "慢跑计划的完整正文" {
		t.Fatalf("expected outline-backed third project content, got %q", reply.Content)
	}
}

// TestAppendMessageReadsThirdProjectFromMarkdownFallbackOutline 验证 live-like markdown 简历在缺少 structure_json 时也能稳定读取第三个项目。
func TestAppendMessageReadsThirdProjectFromMarkdownFallbackOutline(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("markdown fallback third project")
	currentDocument := buildLiveLikeResumeCurrentDocumentWithoutStructure()
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("markdown fallback excerpt should not call chat responder")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: currentDocument.ResourceID, Title: currentDocument.Title, Source: currentDocument.SourceType},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求属于当前文件直接阅读"},
			},
		}),
		WithDeterministicReadResponder(NewDeterministicReadResponder()),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先完整输出一遍。")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if !strings.Contains(reply.Content, "Gamma 风险引擎") || !strings.Contains(reply.Content, "把风险规则上线周期从 7 天缩短到 1 天") {
		t.Fatalf("expected markdown fallback third project content, got %q", reply.Content)
	}
}

// TestAppendMessageAnalyzeUsesResolvedNodeCanonicalContent 验证分析链会使用 resolved node 的 canonical 内容，而不是退回 section/citation 猜测。
func TestAppendMessageAnalyzeUsesResolvedNodeCanonicalContent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("outline 项目分析")
	currentDocument := testCurrentDocumentWithProjects()
	currentDocument.ResourceID = "resource-outline-3"
	currentDocument.VersionID = "version-outline-3"
	currentDocument.Outline[4].CanonicalContent = "慢跑计划的完整正文，包含问题、动作和结果。"
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "第三个项目的问题是结果表达偏弱。"},
		onReply: func(input ChatCompletionInput) {
			if !strings.Contains(input.CanonicalAnalysisContext, "慢跑计划的完整正文，包含问题、动作和结果。") {
				t.Fatalf("expected node canonical content in analysis context, got %q", input.CanonicalAnalysisContext)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: "resource-outline-3", Title: "简历", Source: "upload"},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前请求需要先读 node 再分析"},
			},
		}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "结合这份简历，详细分析第三个项目的问题"); err != nil {
		t.Fatalf("append message: %v", err)
	}
}

// TestAppendMessageReturnsExplicitFailureWhenProjectOrdinalMissing 验证缺少第三个项目时会直接返回显式失败，而不是继续 fallback。
func TestAppendMessageReturnsExplicitFailureWhenProjectOrdinalMissing(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("outline 缺失第三项目")
	currentDocument := testCurrentDocumentWithProjects()
	currentDocument.ResourceID = "resource-outline-4"
	currentDocument.VersionID = "version-outline-4"
	currentDocument.Outline = currentDocument.Outline[:4]
	retriever := &fakeResourceCitationRetriever{}
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("missing ordinal should return explicit failure before chat responder")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		retriever,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: "resource-outline-4", Title: "简历", Source: "upload"},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "partial",
				Confidence:          0.7,
				Reasons:             []string{"当前请求需要先定位 project_item"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "把第三个项目先输出一遍")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if retriever.calls != 0 {
		t.Fatalf("expected node-aware failure to skip retrieval fallback, got %d calls", retriever.calls)
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != buildCurrentFileFailureReply("把第三个项目先输出一遍") {
		t.Fatalf("expected explicit current-file failure, got %q", reply.Content)
	}
}

// TestAppendMessageDoesNotFallbackToSecondProjectWhenThirdProjectMissing 验证缺少第三个项目时不会把第二个项目当成第三个项目。
func TestAppendMessageDoesNotFallbackToSecondProjectWhenThirdProjectMissing(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("outline 不回落到第二项目")
	currentDocument := testCurrentDocumentWithProjects()
	currentDocument.ResourceID = "resource-outline-5"
	currentDocument.VersionID = "version-outline-5"
	currentDocument.Outline = currentDocument.Outline[:4]
	retriever := &fakeResourceCitationRetriever{
		result: []citation.Citation{
			{SectionID: "project-2", SectionType: string(OutlineNodeProjectItem), SectionTitle: "选课助手", Snippet: "选课助手的片段"},
		},
	}
	responder := &fakeChatResponder{
		onReply: func(ChatCompletionInput) {
			t.Fatal("missing ordinal should not fall back to another project")
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		retriever,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource:  &resourceContext{ID: "resource-outline-5", Title: "简历", Source: "upload"},
				CurrentDocument: currentDocument,
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "partial",
				Confidence:          0.7,
				Reasons:             []string{"当前请求需要先定位 project_item"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "结合简历分析第三个项目的问题")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if retriever.calls != 0 {
		t.Fatalf("expected node-aware miss to skip retrieval fallback, got %d calls", retriever.calls)
	}
	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if reply.Content != buildCurrentFileFailureReply("结合简历分析第三个项目的问题") {
		t.Fatalf("expected explicit current-file failure, got %q", reply.Content)
	}
}

// TestAppendMessageWorkflowProposalDoesNotClaimMutationBeforeTaskCreation 验证 proposal 阶段不会声称已经修改完成。
func TestAppendMessageWorkflowProposalDoesNotClaimMutationBeforeTaskCreation(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("workflow proposal")
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "我已经帮你修改第三个项目，并更新到文件中了。",
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModeAnswerThenTaskCard,
				ConversationMode:         "execute",
				WorkflowCommitment:       true,
				ProposalReady:            true,
				CandidateTaskInstruction: stringPointer("把第三个项目改成问题-动作-结果结构"),
				Confidence:               0.9,
				Reasons:                  []string{"当前请求应先进入 proposal 阶段"},
			}),
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接帮我改第三个项目，开始执行")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected user + assistant + task_suggestion, got %d", len(result.Messages))
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if strings.Contains(reply.Content, "更新到文件") || strings.Contains(reply.Content, "已经帮你修改") {
		t.Fatalf("expected proposal reply to avoid completion claim, got %q", reply.Content)
	}
	if !strings.Contains(reply.Content, "尚未写回原文件") {
		t.Fatalf("expected proposal reply to stay in proposal phase, got %q", reply.Content)
	}
	if result.Messages[2].Kind != KindTaskSuggestion {
		t.Fatalf("expected task suggestion after proposal reply, got %#v", result.Messages[2])
	}
}

// TestAppendMessageStreamWorkflowProposalDoesNotClaimMutationBeforeTaskCreation 验证流式 proposal 阶段也不会声称已经修改完成。
func TestAppendMessageStreamWorkflowProposalDoesNotClaimMutationBeforeTaskCreation(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("workflow proposal stream")
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我已经帮你修改第三个项目，并更新到文件中了。"},
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModeAnswerThenTaskCard,
				ConversationMode:         "execute",
				WorkflowCommitment:       true,
				ProposalReady:            true,
				CandidateTaskInstruction: stringPointer("把第三个项目改成问题-动作-结果结构"),
				Confidence:               0.9,
				Reasons:                  []string{"当前请求应先进入 proposal 阶段"},
			}),
		}),
	)

	if err := service.AppendMessageStream(context.Background(), session.ID, "直接帮我改第三个项目，开始执行", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) < 3 {
		t.Fatalf("expected persisted assistant reply + task suggestion, got %d messages", len(messages))
	}

	reply := decodeTextPayload(t, messages[len(messages)-2].Payload)
	if strings.Contains(reply.Content, "更新到文件") || strings.Contains(reply.Content, "已经帮你修改") {
		t.Fatalf("expected stream proposal reply to avoid completion claim, got %q", reply.Content)
	}
	if !strings.Contains(reply.Content, "尚未写回原文件") {
		t.Fatalf("expected stream proposal reply to stay in proposal phase, got %q", reply.Content)
	}
	if messages[len(messages)-1].Kind != KindTaskSuggestion {
		t.Fatalf("expected trailing task suggestion, got %#v", messages[len(messages)-1])
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

	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "我先基于当前资料看第二章，再帮你收敛成任务。",
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请帮我检查并修订第二章"),
				CandidatePlanGoal:        stringPointer("产出第二章修订任务"),
				Confidence:               0.9,
				Reasons:                  []string{"当前请求已经明确要求进入执行"},
			}),
		}),
	)
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

// TestAppendMessageDeterministicallyPromotesExplicitExecutionWhenDeliberationStaysAdvice 验证显式执行请求不会因为 deliberation 偏向聊天稿而丢失任务卡。
func TestAppendMessageDeterministicallyPromotesExplicitExecutionWhenDeliberationStaysAdvice(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历改写")
	currentDocument := buildLiveLikeResumeCurrentDocumentWithoutStructure()
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 收敛后的 instruction"),
			CandidatePlanGoal:    stringPointer("强化第三个项目说服力"),
			Confidence:           0.9,
			Reasons:              []string{"显式执行请求不应停留在聊天改写"},
		},
	}
	verifier := &fakeWorkflowVerifier{
		result: &WorkflowVerificationDecision{
			ApproveWorkflow:    true,
			RevisedInstruction: stringPointer("verifier 收紧后的 instruction"),
			Confidence:         0.93,
			Reasons:            []string{"当前材料已足够进入 proposal"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "好的，我先整理一版第三个项目的改法。",
			},
			onReply: func(input ChatCompletionInput) {
				if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerThenTaskCard {
					t.Fatalf("expected responder to receive proposal decision, got %#v", input.Decision)
				}
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     currentDocument.ResourceID,
					Title:  currentDocument.Title,
					Source: currentDocument.SourceType,
				},
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
				CurrentDocument: currentDocument,
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerOnly,
				ConversationMode:    "advise",
				RequestedNextStep:   "answer",
				ChatFulfillable:     true,
				WorkflowCommitment:  false,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.64,
				Reasons:             []string{"模型把请求收成聊天内改写"},
			},
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接帮我改第三个项目，开始执行。")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if planner.calls != 1 {
		t.Fatalf("expected planner to run once after deterministic promotion, got %d", planner.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected verifier to run once after deterministic promotion, got %d", verifier.calls)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected user + assistant + task_suggestion, got %d", len(result.Messages))
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if !strings.Contains(reply.Content, "尚未写回原文件") {
		t.Fatalf("expected proposal-phase wording, got %q", reply.Content)
	}
	if result.Messages[2].Kind != KindTaskSuggestion {
		t.Fatalf("expected task suggestion, got %#v", result.Messages[2])
	}
	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if suggestion.Instruction != "verifier 收紧后的 instruction" {
		t.Fatalf("expected verifier instruction, got %q", suggestion.Instruction)
	}
}

// TestAppendMessageStreamDeterministicallyPromotesExplicitExecutionWhenDeliberationStaysAdvice 验证流式路径也会对显式执行请求做 deterministic promotion。
func TestAppendMessageStreamDeterministicallyPromotesExplicitExecutionWhenDeliberationStaysAdvice(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("简历改写 stream")
	currentDocument := buildLiveLikeResumeCurrentDocumentWithoutStructure()
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 收敛后的 instruction"),
			CandidatePlanGoal:    stringPointer("强化第三个项目说服力"),
			Confidence:           0.9,
			Reasons:              []string{"显式执行请求不应停留在聊天改写"},
		},
	}
	verifier := &fakeWorkflowVerifier{
		result: &WorkflowVerificationDecision{
			ApproveWorkflow:    true,
			RevisedInstruction: stringPointer("verifier 收紧后的 instruction"),
			Confidence:         0.92,
			Reasons:            []string{"当前材料已足够进入 proposal"},
		},
	}
	var events []StreamEvent
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"好的，我先整理一版第三个项目的改法。"},
				result: &ChatCompletionResult{Reply: "好的，我先整理一版第三个项目的改法。"},
			},
			onStream: func(input ChatCompletionInput) {
				if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerThenTaskCard {
					t.Fatalf("expected stream responder to receive proposal decision, got %#v", input.Decision)
				}
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     currentDocument.ResourceID,
					Title:  currentDocument.Title,
					Source: currentDocument.SourceType,
				},
				Snapshot:        &SessionContextSnapshot{SessionID: session.ID},
				CurrentDocument: currentDocument,
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerOnly,
				ConversationMode:    "advise",
				RequestedNextStep:   "answer",
				ChatFulfillable:     true,
				WorkflowCommitment:  false,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.64,
				Reasons:             []string{"模型把请求收成聊天内改写"},
			},
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
	)

	err := service.AppendMessageStream(context.Background(), session.ID, "直接帮我改第三个项目，开始执行。", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("append message stream: %v", err)
	}
	if planner.calls != 1 {
		t.Fatalf("expected stream planner to run once after deterministic promotion, got %d", planner.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected stream verifier to run once after deterministic promotion, got %d", verifier.calls)
	}
	if streamTaskSuggestionCount(events) != 1 {
		t.Fatalf("expected one task suggestion event, got %#v", events)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) < 3 {
		t.Fatalf("expected persisted assistant reply + task suggestion, got %d messages", len(messages))
	}
	reply := decodeTextPayload(t, messages[len(messages)-2].Payload)
	if !strings.Contains(reply.Content, "尚未写回原文件") {
		t.Fatalf("expected stream proposal-phase wording, got %q", reply.Content)
	}
	if messages[len(messages)-1].Kind != KindTaskSuggestion {
		t.Fatalf("expected trailing task suggestion, got %#v", messages[len(messages)-1])
	}
	suggestion := decodeTaskSuggestionPayload(t, messages[len(messages)-1].Payload)
	if suggestion.Instruction != "verifier 收紧后的 instruction" {
		t.Fatalf("expected verifier instruction, got %q", suggestion.Instruction)
	}
}

// TestAppendMessageDirectWorkflowPromotionProjectsAuthorizedProposalState 验证单轮直接进入 workflow 时，service 会把 proposal / authorization 真源一并投影回快照。
func TestAppendMessageDirectWorkflowPromotionProjectsAuthorizedProposalState(t *testing.T) {
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
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "我先给你一张任务建议卡。",
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请把第二章整理成任务"),
				CandidatePlanGoal:        stringPointer("产出第二章修订任务"),
				Confidence:               0.9,
				Reasons:                  []string{"当前请求已经明确要求进入执行"},
			}),
		}),
		WithSessionContextProjector(projector),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "请把第二章整理成任务"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(projector.advisorStateCalls) == 0 {
		t.Fatal("expected advisor state projection for direct workflow promotion")
	}
	lastCall := projector.advisorStateCalls[len(projector.advisorStateCalls)-1]
	if lastCall.PendingProposal == nil || lastCall.PendingProposal.Instruction != "请把第二章整理成任务" {
		t.Fatalf("expected projected pending proposal, got %#v", lastCall.PendingProposal)
	}
	if lastCall.AuthorizationState == nil || lastCall.AuthorizationState.Status != "granted" {
		t.Fatalf("expected granted authorization state, got %#v", lastCall.AuthorizationState)
	}
	if strings.TrimSpace(lastCall.AuthorizationState.GrantedForProposalID) == "" {
		t.Fatalf("expected granted authorization state to point at proposal id, got %#v", lastCall.AuthorizationState)
	}
}

// TestAppendMessageKeepsTaskSuggestionWhenPlannerTriesToDowngradeGrantedExecution 验证已授权执行不会再被 planner 收回成纯聊天回复。
func TestAppendMessageKeepsTaskSuggestionWhenPlannerTriesToDowngradeGrantedExecution(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("planner downgrade")
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{Reply: "好的，我先整理一版第三个项目的改法。"},
			onReply: func(input ChatCompletionInput) {
				if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerThenTaskCard {
					t.Fatalf("expected responder to keep answer_then_task_card, got %#v", input.Decision)
				}
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("把第三个项目改成问题-动作-结果结构"),
				CandidatePlanGoal:        stringPointer("强化第三个项目说服力"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求开始执行"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ChatFulfillable: true,
				Confidence:      0.8,
				Reasons:         []string{"聊天里也能先给一版草稿"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接帮我改第三个项目，开始执行。")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected user + assistant + task_suggestion, got %d", len(result.Messages))
	}
	if result.Messages[2].Kind != KindTaskSuggestion {
		t.Fatalf("expected task suggestion, got %#v", result.Messages[2])
	}
}

// TestAppendMessageKeepsTaskSuggestionWhenVerifierTriesToDowngradeGrantedExecution 验证已授权执行不会再被 verifier 收回成纯聊天回复。
func TestAppendMessageKeepsTaskSuggestionWhenVerifierTriesToDowngradeGrantedExecution(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("verifier downgrade")
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{Reply: "好的，我先整理一版第三个项目的改法。"},
			onReply: func(input ChatCompletionInput) {
				if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerThenTaskCard {
					t.Fatalf("expected responder to keep answer_then_task_card, got %#v", input.Decision)
				}
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot:       &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("把第三个项目改成问题-动作-结果结构"),
				CandidatePlanGoal:        stringPointer("强化第三个项目说服力"),
				Confidence:               0.88,
				Reasons:                  []string{"用户明确要求开始执行"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("planner 收敛后的 instruction"),
				CandidatePlanGoal:    stringPointer("强化第三个项目说服力"),
				Confidence:           0.9,
				Reasons:              []string{"planner 已确认进入 workflow"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				DowngradeToChat: true,
				Confidence:      0.84,
				Reasons:         []string{"聊天里也能先给一版草稿"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接帮我改第三个项目，开始执行。")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected user + assistant + task_suggestion, got %d", len(result.Messages))
	}
	if result.Messages[2].Kind != KindTaskSuggestion {
		t.Fatalf("expected task suggestion, got %#v", result.Messages[2])
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
				Reply: "我先给你一张任务建议卡。",
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ChatFulfillable:          false,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: &instruction,
				CandidatePlanGoal:        stringPointer("产出第二章修订任务"),
				Confidence:               0.9,
				Reasons:                  []string{"当前请求已经明确要求进入执行"},
			}),
		}),
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

// TestStartConversationWithFileCreatesEmptySessionWithOnlySessionFile 验证首个动作直接上传会创建空会话且只写入 `session_file`。
func TestStartConversationWithFileCreatesEmptySessionWithOnlySessionFile(t *testing.T) {
	repo := newFakeSessionRepo()
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
	result, err := service.StartConversationWithFile(context.Background(), "学生手册.md", []byte("# 学生手册\n内容"))
	if err != nil {
		t.Fatalf("start conversation with file: %v", err)
	}

	if result.Session.ID != "session-created" {
		t.Fatalf("expected created session id %q, got %q", "session-created", result.Session.ID)
	}
	if result.Session.Title != "学生手册" {
		t.Fatalf("expected session title %q, got %q", "学生手册", result.Session.Title)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 created message, got %d", len(result.Messages))
	}
	if result.Messages[0].Kind != KindSessionFile {
		t.Fatalf("expected only session_file message, got %q", result.Messages[0].Kind)
	}

	persistedMessages, err := repo.ListMessages(context.Background(), result.Session.ID)
	if err != nil {
		t.Fatalf("list persisted messages: %v", err)
	}
	if len(persistedMessages) != 1 {
		t.Fatalf("expected 1 persisted message, got %d", len(persistedMessages))
	}
	if persistedMessages[0].Kind != KindSessionFile {
		t.Fatalf("expected persisted session_file message, got %q", persistedMessages[0].Kind)
	}

	filePayload := decodeSessionFilePayload(t, result.Messages[0].Payload)
	if filePayload.ResourceID != "resource-uploaded" {
		t.Fatalf("expected payload resource id %q, got %q", "resource-uploaded", filePayload.ResourceID)
	}
	if filePayload.FileName != "学生手册.md" {
		t.Fatalf("expected payload file name %q, got %q", "学生手册.md", filePayload.FileName)
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

// TestStartConversationWithFileStoresOriginalFileAndBackfillsSessionID 验证首屏上传也会保留原文件元数据并回填会话 ID。
func TestStartConversationWithFileStoresOriginalFileAndBackfillsSessionID(t *testing.T) {
	repo := newFakeSessionRepo()
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
	result, err := service.StartConversationWithFile(context.Background(), "学生手册.md", []byte("# 学生手册\n内容"))
	if err != nil {
		t.Fatalf("start conversation with file: %v", err)
	}

	if uploadedFiles.createInput == nil {
		t.Fatal("expected uploaded file metadata to be created")
	}
	if uploadedFiles.createInput.SessionID != nil {
		t.Fatalf("expected uploaded file metadata to be created before session binding, got %#v", uploadedFiles.createInput.SessionID)
	}
	if uploadedFiles.updatedSessionID != result.Session.ID {
		t.Fatalf("expected uploaded file to bind session %q, got %q", result.Session.ID, uploadedFiles.updatedSessionID)
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

// TestAppendMessageStreamRecordsDeliberationAndPolicyEvents 验证`appendMessageStream`会沿用同一套 runtime learning 插桩。
func TestAppendMessageStreamRecordsDeliberationAndPolicyEvents(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-stream-deliberation")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我先把第三个项目内容输出给你。"},
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前轮只需要直接回答"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if err := service.AppendMessageStream(context.Background(), session.ID, "先把第三个项目内容输出一遍", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	recorder.mustFindEvent(t, RuntimeEventTypeDeliberationDecided)
	recorder.mustFindEvent(t, RuntimeEventTypePolicyApplied)
}

// TestAppendMessageStreamRecordsTaskSuggestionCreatedEvent 验证`appendMessageStream`在 workflow promotion 时会记录 task_suggestion.created 事件。
func TestAppendMessageStreamRecordsTaskSuggestionCreatedEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-stream-task-suggestion")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"这件事适合进入任务流。"},
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     "resource-1",
					Title:  "项目资料",
					Source: "upload",
				},
				Snapshot: &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:        stringPointer("产出可审批的修订方案"),
				Confidence:               0.91,
				Reasons:                  []string{"用户明确要求进入执行链路"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:    stringPointer("产出可审批的修订方案"),
				Confidence:           0.88,
				Reasons:              []string{"材料已齐备"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				ApproveWorkflow:    true,
				RevisedInstruction: stringPointer("请开始整理第三个项目"),
				Confidence:         0.9,
				Reasons:            []string{"进入任务流更符合用户目标"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if err := service.AppendMessageStream(context.Background(), session.ID, "直接开始整理第三个项目", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeTaskSuggestionCreated)
	assertRuntimeEventPayloadField(t, event.Payload, "instruction", "请开始整理第三个项目")
}

// TestAppendMessageRecordsDeliberationDecisionEvent 验证`appendMessage`会记录 deliberation 决策事件。
func TestAppendMessageRecordsDeliberationDecisionEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-deliberation")
	recorder := &fakeRuntimeEventRecorder{}
	projector := &fakeRuntimeLearningProjector{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先把内容读给你。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.92,
				Reasons:             []string{"当前请求是阅读型交付"},
			},
		}),
		WithRuntimeLearning(recorder, projector),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "先看看第三个项目内容")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeDeliberationDecided)
	if event.MessageID == nil || *event.MessageID != result.Messages[1].ID {
		t.Fatalf("expected deliberation event message id %q, got %#v", result.Messages[1].ID, event.MessageID)
	}
	assertRuntimeEventPayloadField(t, event.Payload, "request_kind", "readback")
	assertRuntimeEventPayloadField(t, event.Payload, "response_mode", ResponseModeAnswerWithGrounding)
	if len(projector.events) == 0 {
		t.Fatal("expected runtime projector to receive persisted runtime events")
	}
}

// TestAppendMessageRecordsPolicyDecisionEvent 验证`appendMessage`会记录 policy 裁决事件。
func TestAppendMessageRecordsPolicyDecisionEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-policy")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先帮你看看。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.9,
				Reasons:             []string{"当前可以直接回答"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "先输出第三个项目"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypePolicyApplied)
	assertRuntimeEventPayloadField(t, event.Payload, "allow_answer", true)
	assertRuntimeEventPayloadField(t, event.Payload, "allow_task_suggestion", false)
}

// TestAppendMessageRecordsActionGateDecisionEvent 验证`appendMessage`会记录独立的 action gate 事件。
func TestAppendMessageRecordsActionGateDecisionEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-action-gate")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "这件事适合进入任务流。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     "resource-1",
					Title:  "项目资料",
					Source: "upload",
				},
				Snapshot: &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				ConversationMode:         "execute",
				RequestedNextStep:        "promote_to_workflow",
				ProposalReady:            true,
				AwaitingAuthorization:    false,
				WorkflowCommitment:       true,
				ChatFulfillable:          false,
				CandidateTaskInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:        stringPointer("产出可审批的修订方案"),
				EvidenceSufficiency:      "sufficient",
				Confidence:               0.91,
				Reasons:                  []string{"当前已满足进入 action gate 的执行契约"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "直接开始整理第三个项目"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeActionGateApplied)
	assertRuntimeEventPayloadField(t, event.Payload, "conversation_mode", "execute")
	assertRuntimeEventPayloadField(t, event.Payload, "allow_workflow_promotion", true)
	assertRuntimeEventPayloadField(t, event.Payload, "allow_task_suggestion", true)
}

// TestAppendMessageRecordsPlannerAndVerifierEventsWhenUsed 验证`appendMessage`在 workflow promotion 时会记录 planner 和 verifier 事件。
func TestAppendMessageRecordsPlannerAndVerifierEventsWhenUsed(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-planner-verifier")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "这件事适合进入任务流。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     "resource-1",
					Title:  "项目资料",
					Source: "upload",
				},
				Snapshot: &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:        stringPointer("产出可审批的修订方案"),
				Confidence:               0.91,
				Reasons:                  []string{"用户明确要求进入执行链路"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:    stringPointer("产出可审批的修订方案"),
				Confidence:           0.88,
				Reasons:              []string{"材料已齐备"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				ApproveWorkflow:    true,
				RevisedInstruction: stringPointer("请开始整理第三个项目"),
				Confidence:         0.9,
				Reasons:            []string{"进入任务流更符合用户目标"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接开始整理第三个项目")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	plannerEvent := recorder.mustFindEvent(t, RuntimeEventTypePlannerUsed)
	verifierEvent := recorder.mustFindEvent(t, RuntimeEventTypeVerifierUsed)
	expectedMessageID := runtimeDecisionMessageID(result.Messages)
	if expectedMessageID == nil {
		t.Fatalf("expected runtime decision message id, got %#v", result.Messages)
	}
	if plannerEvent.MessageID == nil || *plannerEvent.MessageID != *expectedMessageID {
		t.Fatalf("expected planner event message id %q, got %#v", *expectedMessageID, plannerEvent.MessageID)
	}
	if verifierEvent.MessageID == nil || *verifierEvent.MessageID != *expectedMessageID {
		t.Fatalf("expected verifier event message id %q, got %#v", *expectedMessageID, verifierEvent.MessageID)
	}
}

// TestAppendMessageRecordsClarificationPromptedEvent 验证`appendMessage`在澄清分支会记录 clarification.prompted 事件。
func TestAppendMessageRecordsClarificationPromptedEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-clarification")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "你是想先输出原文，还是直接创建任务？"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "other",
				ResponseMode:          ResponseModeClarifyFirst,
				NeedsClarification:    true,
				ClarificationQuestion: stringPointer("你是想先输出原文，还是直接创建任务？"),
				EvidenceSufficiency:   "partial",
				Confidence:            0.62,
				Reasons:               []string{"当前请求既可能是阅读，也可能是执行"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "整理成表格"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeClarificationPrompted)
	assertRuntimeEventPayloadField(t, event.Payload, "question", "你是想先输出原文，还是直接创建任务？")
}

// TestAppendMessageRecordsClarificationResolvedToChatEvent 验证`appendMessage`在澄清后回到聊天模式时会记录 clarification.resolved_to_chat 事件。
func TestAppendMessageRecordsClarificationResolvedToChatEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-clarification-to-chat")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "clarification-1",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindText,
		SequenceNo: 1,
		Payload:    mustJSON(t, TextPayload{Content: "你是想先输出原文，还是直接创建任务？"}),
		CreatedAt:  time.Now(),
	})

	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先把原文内容输出给你。"}},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.83,
				Reasons:             []string{"用户已经明确要先看内容"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "先输出原文"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeClarificationResolvedChat)
	if event.MessageID == nil || *event.MessageID != "clarification-1" {
		t.Fatalf("expected clarification resolved chat message id %q, got %#v", "clarification-1", event.MessageID)
	}
	assertRuntimeEventPayloadField(t, event.Payload, "outcome", "resolved_to_chat")
}

// TestAppendMessageRecordsClarificationResolvedToWorkflowEvent 验证`appendMessage`在澄清后进入任务流时会记录 clarification.resolved_to_workflow 事件。
func TestAppendMessageRecordsClarificationResolvedToWorkflowEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-clarification-to-workflow")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "clarification-1",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindText,
		SequenceNo: 1,
		Payload:    mustJSON(t, TextPayload{Content: "你是想先输出原文，还是直接创建任务？"}),
		CreatedAt:  time.Now(),
	})

	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我已经整理出适合进入任务流的方案。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     "resource-1",
					Title:  "项目资料",
					Source: "upload",
				},
				Snapshot: &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:        stringPointer("产出可审批的修订方案"),
				Confidence:               0.9,
				Reasons:                  []string{"用户已经明确要创建任务"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:    stringPointer("产出可审批的修订方案"),
				Confidence:           0.88,
				Reasons:              []string{"材料已齐备"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				ApproveWorkflow:    true,
				RevisedInstruction: stringPointer("请开始整理第三个项目"),
				Confidence:         0.89,
				Reasons:            []string{"进入任务流更符合用户目标"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "直接创建任务"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeClarificationResolvedFlow)
	if event.MessageID == nil || *event.MessageID != "clarification-1" {
		t.Fatalf("expected clarification resolved flow message id %q, got %#v", "clarification-1", event.MessageID)
	}
	assertRuntimeEventPayloadField(t, event.Payload, "outcome", "resolved_to_workflow")
}

// TestAppendMessageRecordsTaskSuggestionCreatedEvent 验证`appendMessage`在创建任务建议时会记录 task_suggestion.created 事件。
func TestAppendMessageRecordsTaskSuggestionCreatedEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-task-suggestion-created")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我已经整理出适合进入任务流的方案。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{
					ID:     "resource-1",
					Title:  "项目资料",
					Source: "upload",
				},
				Snapshot: &SessionContextSnapshot{SessionID: session.ID},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: withExplicitWorkflowPromotion(DeliberationDecision{
				RequestKind:              "workflow_command",
				ResponseMode:             ResponseModePlanThenAnswer,
				WorkflowCommitment:       true,
				EvidenceSufficiency:      "sufficient",
				CandidateTaskInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:        stringPointer("产出可审批的修订方案"),
				Confidence:               0.9,
				Reasons:                  []string{"当前已经具备进入任务流的材料"},
			}),
		}),
		WithWorkflowPlanner(&fakeWorkflowPlanner{
			result: &WorkflowPlanDecision{
				ShouldEnterWorkflow:  true,
				CandidateInstruction: stringPointer("请开始整理第三个项目"),
				CandidatePlanGoal:    stringPointer("产出可审批的修订方案"),
				Confidence:           0.88,
				Reasons:              []string{"材料已齐备"},
			},
		}),
		WithWorkflowVerifier(&fakeWorkflowVerifier{
			result: &WorkflowVerificationDecision{
				ApproveWorkflow:    true,
				RevisedInstruction: stringPointer("请开始整理第三个项目"),
				Confidence:         0.89,
				Reasons:            []string{"任务流更合适"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "直接开始整理第三个项目")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeTaskSuggestionCreated)
	expectedMessageID := runtimeDecisionMessageID(result.Messages)
	if expectedMessageID == nil {
		t.Fatalf("expected runtime decision message id, got %#v", result.Messages)
	}
	if event.MessageID == nil || *event.MessageID != *expectedMessageID {
		t.Fatalf("expected task suggestion event message id %q, got %#v", *expectedMessageID, event.MessageID)
	}
	assertRuntimeEventPayloadField(t, event.Payload, "instruction", "请开始整理第三个项目")
}

// TestConfirmTaskSuggestionRecordsConfirmedEvent 验证`confirmTaskSuggestion`在确认建议时会记录 confirmed 事件。
func TestConfirmTaskSuggestionRecordsConfirmedEvent(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-confirm-task-suggestion")
	suggestionMessage := repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "suggestion-1",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindTaskSuggestion,
		SequenceNo: 1,
		Payload: mustJSON(t, TaskSuggestionPayload{
			ActionLabel:   "确认创建任务",
			CanCreate:     true,
			Instruction:   "请开始整理第三个项目",
			ResourceID:    stringPointer("resource-1"),
			ResourceLabel: "项目资料",
			StatusMessage: "材料已齐备，可以创建任务。",
			Title:         "建议创建任务",
		}),
		CreatedAt: time.Now(),
	})
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{
			result: &postgres.Task{
				ID:          "task-1",
				ResourceID:  "resource-1",
				Instruction: "请开始整理第三个项目",
				Status:      "planning",
			},
		},
		&fakeChatResponder{},
		nil,
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.ConfirmTaskSuggestion(context.Background(), suggestionMessage.ID); err != nil {
		t.Fatalf("confirm task suggestion: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeTaskSuggestionConfirmed)
	if event.MessageID == nil || *event.MessageID != suggestionMessage.ID {
		t.Fatalf("expected confirmed event message id %q, got %#v", suggestionMessage.ID, event.MessageID)
	}
	assertRuntimeEventPayloadField(t, event.Payload, "task_id", "task-1")
}

// TestAppendMessageRecordsIgnoredSuggestionOnNextUserTurn 验证`appendMessage`在下一轮未确认时会记录 ignored 事件。
func TestAppendMessageRecordsIgnoredSuggestionOnNextUserTurn(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-ignored-suggestion")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先把第三个项目内容输出给你。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
						MessageID:   "suggestion-pending",
						Instruction: "请开始整理第三个项目",
					},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.84,
				Reasons:             []string{"当前轮只需要直接回答"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "先把第三个项目内容输出一遍"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeTaskSuggestionIgnored)
	if event.MessageID == nil || *event.MessageID != "suggestion-pending" {
		t.Fatalf("expected ignored event message id %q, got %#v", "suggestion-pending", event.MessageID)
	}
}

// TestAppendMessageRecordsExplicitCorrectionWhenPendingSuggestionExists 验证`appendMessage`在显式纠正待确认建议时会记录 corrected 事件。
func TestAppendMessageRecordsExplicitCorrectionWhenPendingSuggestionExists(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("runtime-explicit-correction")
	recorder := &fakeRuntimeEventRecorder{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我先按阅读模式帮你看内容。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
						MessageID:   "suggestion-pending",
						Instruction: "请开始整理第三个项目",
					},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "readback",
				ResponseMode:        ResponseModeAnswerWithGrounding,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.82,
				Reasons:             []string{"当前轮只需要直接回答"},
			},
		}),
		WithRuntimeLearning(recorder, &fakeRuntimeLearningProjector{}),
	)

	if _, err := service.AppendMessage(context.Background(), session.ID, "不是这个意思，我只是想先看看内容。"); err != nil {
		t.Fatalf("append message: %v", err)
	}

	event := recorder.mustFindEvent(t, RuntimeEventTypeUserCorrected)
	if event.MessageID == nil || *event.MessageID != "suggestion-pending" {
		t.Fatalf("expected corrected event message id %q, got %#v", "suggestion-pending", event.MessageID)
	}
	assertRuntimeEventPayloadField(t, event.Payload, "reason", RuntimeCorrectionReasonNotThisIntent)
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
	updatedSessionID  string
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

// UpdateSessionID 实现测试替身需要的 `UpdateSessionID` 接口方法，为用例分支提供可控返回。
func (r *fakeUploadedFileRepo) UpdateSessionID(_ context.Context, fileID string, sessionID string) error {
	r.updatedFileID = fileID
	r.updatedSessionID = sessionID
	return nil
}

// TestAppendMessageClarificationThenDirectModifyCreatesProposalState 验证澄清后的“直接修改”会先落成 pending proposal，而不是直接弹任务卡。
func TestAppendMessageClarificationThenDirectModifyCreatesProposalState(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	projector := &fakeSessionContextProjector{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{Reply: "我建议按结果导向重写第三个项目，并补量化指标。要不要我按这个方案执行？"},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingClarification: &SnapshotPendingClarification{
						Kind:           "workflow_branch",
						Question:       "你是想先给草案，还是直接修改？",
						AskedMessageID: "assistant-question-1",
						Options:        []string{"先给我草案看看", "直接修改"},
					},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "analysis",
				ResponseMode:          ResponseModeAnswerOnly,
				ConversationMode:      "confirm",
				RequestedNextStep:     "request_authorization",
				ProposalReady:         true,
				ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
				ProposedPlanGoal:      stringPointer("强化第三个项目说服力"),
				AwaitingAuthorization: true,
				ChatFulfillable:       true,
				EvidenceSufficiency:   "sufficient",
				Confidence:            0.86,
				Reasons:               []string{"用户已经在澄清里明确选择直接修改"},
			},
		}),
		WithSessionContextProjector(projector),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "可以，直接修改吧")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected user + assistant messages, got %d", len(result.Messages))
	}
	if len(projector.taskSuggestionCalls) != 0 {
		t.Fatalf("expected no task suggestion projection before authorization, got %#v", projector.taskSuggestionCalls)
	}
	if len(projector.advisorStateCalls) != 1 {
		t.Fatalf("expected 1 advisor state projection, got %d", len(projector.advisorStateCalls))
	}
	call := projector.advisorStateCalls[0]
	if call.PendingProposal == nil || call.PendingProposal.Instruction != "把第三个项目改成结果导向版本，并补量化指标" {
		t.Fatalf("expected pending proposal to be projected, got %#v", call)
	}
	if call.AuthorizationState == nil || call.AuthorizationState.Status != "pending" {
		t.Fatalf("expected authorization state to stay pending, got %#v", call.AuthorizationState)
	}
}

// TestAppendMessageExplicitAuthorizationAfterAdviceProducesTaskSuggestion 验证建议后的明确授权会走 gate -> planner/verifier -> task_suggestion。
func TestAppendMessageExplicitAuthorizationAfterAdviceProducesTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	projector := &fakeSessionContextProjector{}
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 收敛后的 instruction"),
			CandidatePlanGoal:    stringPointer("强化第三个项目说服力"),
			Confidence:           0.91,
			Reasons:              []string{"用户已明确授权唯一 proposal"},
		},
	}
	verifier := &fakeWorkflowVerifier{
		result: &WorkflowVerificationDecision{
			ApproveWorkflow:    true,
			RevisedInstruction: stringPointer("verifier 收紧后的 instruction"),
			Confidence:         0.93,
			Reasons:            []string{"proposal scope 和材料已足够执行"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{result: &ChatCompletionResult{Reply: "我来开始处理。"}},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingProposal: &SnapshotPendingProposal{
						ProposalID:        "proposal-1",
						Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
						PlanGoal:          "强化第三个项目说服力",
						ProposedMessageID: "assistant-proposal-1",
					},
					AuthorizationState: &SnapshotAuthorizationState{Status: "pending"},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerOnly,
				ConversationMode:    "confirm",
				RequestedNextStep:   "answer",
				ProposalReady:       false,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.66,
				Reasons:             []string{"如果不看 pending state，这只是普通回复"},
			},
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
		WithSessionContextProjector(projector),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "按这个方案改")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 1 {
		t.Fatalf("expected planner to run once after authorization, got %d", planner.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected verifier to run once after planner promotion, got %d", verifier.calls)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected user + assistant + task_suggestion, got %d", len(result.Messages))
	}
	if result.Messages[2].Kind != KindTaskSuggestion {
		t.Fatalf("expected task suggestion message, got %s", result.Messages[2].Kind)
	}
	suggestion := decodeTaskSuggestionPayload(t, result.Messages[2].Payload)
	if suggestion.Instruction != "verifier 收紧后的 instruction" {
		t.Fatalf("expected verifier instruction, got %q", suggestion.Instruction)
	}
	if len(projector.advisorStateCalls) == 0 {
		t.Fatal("expected advisor state projection after authorization")
	}
	lastProjection := projector.advisorStateCalls[len(projector.advisorStateCalls)-1]
	if lastProjection.AuthorizationState == nil || lastProjection.AuthorizationState.Status != "granted" {
		t.Fatalf("expected granted authorization state, got %#v", lastProjection.AuthorizationState)
	}
}

// TestAppendMessageKeepsAdviceModeWhenUserRequestsDraftInsteadOfExecution 验证“先给我草案看看”会把流程收回 advice，不允许 promotion。
func TestAppendMessageKeepsAdviceModeWhenUserRequestsDraftInsteadOfExecution(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 不该被调用"),
			Confidence:           0.8,
			Reasons:              []string{"不应进入"},
		},
	}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "我先给你一个草案。"},
		onReply: func(input ChatCompletionInput) {
			if input.Decision == nil || input.Decision.ResponseMode != ResponseModeAnswerOnly {
				t.Fatalf("expected advice lane to stay answer_only, got %#v", input.Decision)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingProposal: &SnapshotPendingProposal{
						ProposalID:        "proposal-1",
						Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
						PlanGoal:          "强化第三个项目说服力",
						ProposedMessageID: "assistant-proposal-1",
					},
					AuthorizationState: &SnapshotAuthorizationState{Status: "pending"},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "workflow_command",
				ResponseMode:          ResponseModePlanThenAnswer,
				ConversationMode:      "confirm",
				RequestedNextStep:     "promote_to_workflow",
				ProposalReady:         true,
				ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
				AwaitingAuthorization: true,
				WorkflowCommitment:    true,
				ChatFulfillable:       false,
				EvidenceSufficiency:   "sufficient",
				Confidence:            0.8,
				Reasons:               []string{"旧链路会错误继续 promotion"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "先给我草案看看")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 0 {
		t.Fatalf("expected draft request to skip planner, got %d", planner.calls)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected user + assistant messages, got %d", len(result.Messages))
	}
}

// TestAppendMessageRequiresDisambiguationWhenMultipleProposalsExist 验证多建议待选时，“按你的建议改”会继续澄清，不会误授权。
func TestAppendMessageRequiresDisambiguationWhenMultipleProposalsExist(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 不该被调用"),
			Confidence:           0.8,
			Reasons:              []string{"不应进入"},
		},
	}
	responder := &fakeChatResponder{
		result: &ChatCompletionResult{Reply: "你是想按哪条建议改？"},
		onReply: func(input ChatCompletionInput) {
			if input.Decision == nil || !input.Decision.NeedsClarification {
				t.Fatalf("expected clarification decision, got %#v", input.Decision)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingClarification: &SnapshotPendingClarification{
						Kind:           "proposal_selection",
						Question:       "按哪条建议改？",
						AskedMessageID: "assistant-clarification-1",
						Options:        []string{"按结果导向版本改", "按技术深度版本改"},
					},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "workflow_command",
				ResponseMode:          ResponseModePlanThenAnswer,
				ConversationMode:      "confirm",
				RequestedNextStep:     "promote_to_workflow",
				ProposalReady:         true,
				ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
				AwaitingAuthorization: true,
				WorkflowCommitment:    true,
				ChatFulfillable:       false,
				EvidenceSufficiency:   "sufficient",
				Confidence:            0.81,
				Reasons:               []string{"旧链路会错误把模糊授权升级"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "按你的建议改")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if planner.calls != 0 {
		t.Fatalf("expected ambiguous authorization to skip planner, got %d", planner.calls)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected user + assistant messages, got %d", len(result.Messages))
	}
}

// TestAppendMessageStreamClarificationThenDirectModifyCreatesProposalState 验证流式路径也会先形成 pending proposal，而不是直接发任务卡。
func TestAppendMessageStreamClarificationThenDirectModifyCreatesProposalState(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	projector := &fakeSessionContextProjector{}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我建议按结果导向重写第三个项目，并补量化指标。要不要我按这个方案执行？"},
				result: &ChatCompletionResult{Reply: "我建议按结果导向重写第三个项目，并补量化指标。要不要我按这个方案执行？"},
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingClarification: &SnapshotPendingClarification{
						Kind:           "workflow_branch",
						Question:       "你是想先给草案，还是直接修改？",
						AskedMessageID: "assistant-question-1",
						Options:        []string{"先给我草案看看", "直接修改"},
					},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "analysis",
				ResponseMode:          ResponseModeAnswerOnly,
				ConversationMode:      "confirm",
				RequestedNextStep:     "request_authorization",
				ProposalReady:         true,
				ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
				ProposedPlanGoal:      stringPointer("强化第三个项目说服力"),
				AwaitingAuthorization: true,
				ChatFulfillable:       true,
				EvidenceSufficiency:   "sufficient",
				Confidence:            0.86,
				Reasons:               []string{"用户已经在澄清里明确选择直接修改"},
			},
		}),
		WithSessionContextProjector(projector),
	)

	var events []StreamEvent
	if err := service.AppendMessageStream(context.Background(), session.ID, "可以，直接修改吧", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if streamTaskSuggestionCount(events) != 0 {
		t.Fatalf("expected no task suggestion event before authorization, got %#v", events)
	}
	if len(projector.advisorStateCalls) != 1 {
		t.Fatalf("expected 1 advisor state projection, got %d", len(projector.advisorStateCalls))
	}
}

// TestAppendMessageStreamExplicitAuthorizationEmitsTaskSuggestion 验证流式路径在建议后授权时也会发 task_suggestion。
func TestAppendMessageStreamExplicitAuthorizationEmitsTaskSuggestion(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 收敛后的 instruction"),
			CandidatePlanGoal:    stringPointer("强化第三个项目说服力"),
			Confidence:           0.91,
			Reasons:              []string{"用户已明确授权唯一 proposal"},
		},
	}
	verifier := &fakeWorkflowVerifier{
		result: &WorkflowVerificationDecision{
			ApproveWorkflow:    true,
			RevisedInstruction: stringPointer("verifier 收紧后的 instruction"),
			Confidence:         0.93,
			Reasons:            []string{"proposal scope 和材料已足够执行"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我来开始处理。"},
				result: &ChatCompletionResult{Reply: "我来开始处理。"},
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingProposal: &SnapshotPendingProposal{
						ProposalID:        "proposal-1",
						Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
						PlanGoal:          "强化第三个项目说服力",
						ProposedMessageID: "assistant-proposal-1",
					},
					AuthorizationState: &SnapshotAuthorizationState{Status: "pending"},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerOnly,
				ConversationMode:    "confirm",
				RequestedNextStep:   "answer",
				ProposalReady:       false,
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.66,
				Reasons:             []string{"如果不看 pending state，这只是普通回复"},
			},
		}),
		WithWorkflowPlanner(planner),
		WithWorkflowVerifier(verifier),
	)

	var events []StreamEvent
	if err := service.AppendMessageStream(context.Background(), session.ID, "按这个方案改", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if planner.calls != 1 {
		t.Fatalf("expected planner to run once after authorization, got %d", planner.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected verifier to run once after planner promotion, got %d", verifier.calls)
	}
	if streamTaskSuggestionCount(events) != 1 {
		t.Fatalf("expected exactly 1 task suggestion event, got %#v", events)
	}
}

// TestAppendMessageStreamKeepsAdviceModeWhenUserRequestsDraftInsteadOfExecution 验证流式路径里的草案请求同样会阻断 promotion。
func TestAppendMessageStreamKeepsAdviceModeWhenUserRequestsDraftInsteadOfExecution(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 不该被调用"),
			Confidence:           0.8,
			Reasons:              []string{"不应进入"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我先给你一个草案。"},
				result: &ChatCompletionResult{Reply: "我先给你一个草案。"},
			},
		},
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingProposal: &SnapshotPendingProposal{
						ProposalID:        "proposal-1",
						Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
						PlanGoal:          "强化第三个项目说服力",
						ProposedMessageID: "assistant-proposal-1",
					},
					AuthorizationState: &SnapshotAuthorizationState{Status: "pending"},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "workflow_command",
				ResponseMode:          ResponseModePlanThenAnswer,
				ConversationMode:      "confirm",
				RequestedNextStep:     "promote_to_workflow",
				ProposalReady:         true,
				ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
				AwaitingAuthorization: true,
				WorkflowCommitment:    true,
				ChatFulfillable:       false,
				EvidenceSufficiency:   "sufficient",
				Confidence:            0.8,
				Reasons:               []string{"旧链路会错误继续 promotion"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	var events []StreamEvent
	if err := service.AppendMessageStream(context.Background(), session.ID, "先给我草案看看", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if planner.calls != 0 {
		t.Fatalf("expected draft request to skip planner, got %d", planner.calls)
	}
	if streamTaskSuggestionCount(events) != 0 {
		t.Fatalf("expected no task suggestion event, got %#v", events)
	}
}

// TestAppendMessageStreamRequiresDisambiguationWhenMultipleProposalsExist 验证流式路径遇到多建议歧义时也会继续澄清。
func TestAppendMessageStreamRequiresDisambiguationWhenMultipleProposalsExist(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("顾问模式")
	planner := &fakeWorkflowPlanner{
		result: &WorkflowPlanDecision{
			ShouldEnterWorkflow:  true,
			CandidateInstruction: stringPointer("planner 不该被调用"),
			Confidence:           0.8,
			Reasons:              []string{"不应进入"},
		},
	}
	responder := &fakeChatResponder{
		stream: &fakeChatStream{
			chunks: []string{"你是想按哪条建议改？"},
			result: &ChatCompletionResult{Reply: "你是想按哪条建议改？"},
		},
		onStream: func(input ChatCompletionInput) {
			if input.Decision == nil || !input.Decision.NeedsClarification {
				t.Fatalf("expected clarification decision, got %#v", input.Decision)
			}
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		responder,
		nil,
		WithReplyContextLoader(&fakeReplyContextLoader{
			result: &ReplyContext{
				ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
				Snapshot: &SessionContextSnapshot{
					SessionID: session.ID,
					PendingClarification: &SnapshotPendingClarification{
						Kind:           "proposal_selection",
						Question:       "按哪条建议改？",
						AskedMessageID: "assistant-clarification-1",
						Options:        []string{"按结果导向版本改", "按技术深度版本改"},
					},
				},
			},
		}),
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:           "workflow_command",
				ResponseMode:          ResponseModePlanThenAnswer,
				ConversationMode:      "confirm",
				RequestedNextStep:     "promote_to_workflow",
				ProposalReady:         true,
				ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
				AwaitingAuthorization: true,
				WorkflowCommitment:    true,
				ChatFulfillable:       false,
				EvidenceSufficiency:   "sufficient",
				Confidence:            0.81,
				Reasons:               []string{"旧链路会错误把模糊授权升级"},
			},
		}),
		WithWorkflowPlanner(planner),
	)

	var events []StreamEvent
	if err := service.AppendMessageStream(context.Background(), session.ID, "按你的建议改", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	if planner.calls != 0 {
		t.Fatalf("expected ambiguous authorization to skip planner, got %d", planner.calls)
	}
	if streamTaskSuggestionCount(events) != 0 {
		t.Fatalf("expected no task suggestion event, got %#v", events)
	}
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
	copied.CurrentDocument = cloneCurrentDocument(input.CurrentDocument)
	copied.RuntimeState.CurrentDocument = cloneCurrentDocument(input.RuntimeState.CurrentDocument)
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
	copied.CurrentDocument = cloneCurrentDocument(input.CurrentDocument)
	copied.RuntimeState.CurrentDocument = cloneCurrentDocument(input.RuntimeState.CurrentDocument)
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

// fakeReplyContextLoader 作为 reply context loader 的测试替身，用于在用例里提供可控返回。
type fakeReplyContextLoader struct {
	result *ReplyContext
	err    error
}

// LoadForReply 实现测试替身需要的 `LoadForReply` 接口方法，为用例分支提供可控返回。
func (l *fakeReplyContextLoader) LoadForReply(_ context.Context, _ string, _ []postgres.AssistantMessage, _ string) (*ReplyContext, error) {
	if l.err != nil {
		return nil, l.err
	}
	if l.result == nil {
		return &ReplyContext{}, nil
	}

	cloned := *l.result
	cloned.History = cloneAssistantMessages(l.result.History)
	cloned.Citations = cloneCitations(l.result.Citations)
	cloned.ActiveResource = cloneResourceContext(l.result.ActiveResource)
	cloned.GroundedTarget = cloneResolvedReference(l.result.GroundedTarget)
	cloned.Snapshot = cloneSessionContextSnapshot(l.result.Snapshot)
	cloned.CanonicalRead = cloneCanonicalReadResult(l.result.CanonicalRead)
	cloned.CanonicalAnalysisContext = l.result.CanonicalAnalysisContext
	cloned.CurrentFileFailureReply = l.result.CurrentFileFailureReply
	cloned.CurrentDocument = cloneCurrentDocument(l.result.CurrentDocument)
	return &cloned, nil
}

// fakeRuntimeEventRecorder 作为 runtime event recorder 的测试替身，用于在用例里观察 service 的事件写入。
type fakeRuntimeEventRecorder struct {
	events []postgres.AssistantRuntimeEvent
}

// Record 实现测试替身需要的 `Record` 接口方法，为用例分支提供可控返回。
func (r *fakeRuntimeEventRecorder) Record(_ context.Context, input RuntimeRecordInput) (*postgres.AssistantRuntimeEvent, error) {
	payload, err := marshalRuntimePayload(input.Payload)
	if err != nil {
		return nil, err
	}

	event := postgres.AssistantRuntimeEvent{
		ID:        fmt.Sprintf("runtime-event-%d", len(r.events)+1),
		SessionID: strings.TrimSpace(input.SessionID),
		MessageID: normalizeOptionalText(input.MessageID),
		Source:    strings.TrimSpace(input.Source),
		EventType: strings.TrimSpace(input.EventType),
		Payload:   payload,
	}
	r.events = append(r.events, event)
	return &event, nil
}

// mustFindEvent 返回指定类型的 runtime 事件，缺失时立即失败。
func (r *fakeRuntimeEventRecorder) mustFindEvent(t *testing.T, eventType string) *postgres.AssistantRuntimeEvent {
	t.Helper()

	for index := range r.events {
		if r.events[index].EventType == eventType {
			return &r.events[index]
		}
	}

	t.Fatalf("expected runtime event %q, got %#v", eventType, r.events)
	return nil
}

// fakeRuntimeLearningProjector 作为 runtime learning projector 的测试替身，用于观察 service 是否把事件投影出去。
type fakeRuntimeLearningProjector struct {
	events []postgres.AssistantRuntimeEvent
}

// Project 实现测试替身需要的 `Project` 接口方法，为用例分支记录被投影的事件。
func (p *fakeRuntimeLearningProjector) Project(_ context.Context, event *postgres.AssistantRuntimeEvent) error {
	if event == nil {
		return nil
	}

	copied := *event
	copied.Payload = append([]byte(nil), event.Payload...)
	copied.MessageID = cloneOptionalString(event.MessageID)
	p.events = append(p.events, copied)
	return nil
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
	cloned.CurrentDocument = cloneCurrentDocument(state.CurrentDocument)
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

// fakeWorkflowVerifier 作为 workflow verifier 的测试替身，用于在用例里观察复核接缝。
type fakeWorkflowVerifier struct {
	result       *WorkflowVerificationDecision
	err          error
	calls        int
	lastState    *RuntimeState
	lastDecision *DeliberationDecision
	lastPlan     *WorkflowPlanDecision
	onVerify     func(RuntimeState, *DeliberationDecision, *WorkflowPlanDecision)
}

// Verify 实现测试替身需要的 `Verify` 接口方法，为用例分支提供可控返回。
func (v *fakeWorkflowVerifier) Verify(
	_ context.Context,
	state RuntimeState,
	decision *DeliberationDecision,
	plan *WorkflowPlanDecision,
) (*WorkflowVerificationDecision, error) {
	v.calls++

	clonedState := state
	clonedState.Citations = append([]citation.Citation(nil), state.Citations...)
	clonedState.History = append([]postgres.AssistantMessage(nil), state.History...)
	v.lastState = &clonedState

	if decision != nil {
		clonedDecision := *decision
		clonedDecision.ClarificationQuestion = normalizeOptionalText(decision.ClarificationQuestion)
		clonedDecision.CandidateTaskInstruction = normalizeOptionalText(decision.CandidateTaskInstruction)
		clonedDecision.CandidatePlanGoal = normalizeOptionalText(decision.CandidatePlanGoal)
		clonedDecision.Reasons = normalizeDecisionReasons(decision.Reasons)
		v.lastDecision = &clonedDecision
	} else {
		v.lastDecision = nil
	}

	if plan != nil {
		clonedPlan := *plan
		clonedPlan.ClarificationQuestion = normalizeOptionalText(plan.ClarificationQuestion)
		clonedPlan.CandidateInstruction = normalizeOptionalText(plan.CandidateInstruction)
		clonedPlan.CandidatePlanGoal = normalizeOptionalText(plan.CandidatePlanGoal)
		clonedPlan.MissingMaterials = append([]string(nil), plan.MissingMaterials...)
		clonedPlan.Reasons = normalizeDecisionReasons(plan.Reasons)
		v.lastPlan = &clonedPlan
	} else {
		v.lastPlan = nil
	}

	if v.onVerify != nil {
		v.onVerify(clonedState, v.lastDecision, v.lastPlan)
	}
	if v.err != nil {
		return nil, v.err
	}
	if v.result == nil {
		return &WorkflowVerificationDecision{
			ApproveWorkflow: true,
			Confidence:      0.5,
			Reasons:         []string{"default fake workflow verifier"},
		}, nil
	}

	clonedVerification := *v.result
	clonedVerification.ClarificationQuestion = normalizeOptionalText(v.result.ClarificationQuestion)
	clonedVerification.RevisedInstruction = normalizeOptionalText(v.result.RevisedInstruction)
	clonedVerification.Reasons = normalizeDecisionReasons(v.result.Reasons)
	return &clonedVerification, nil
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
	advisorStateCalls   []AdvisorStateProjection
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

// ProjectAdvisorState 实现测试替身需要的 `ProjectAdvisorState` 接口方法，为用例分支记录 advisor state 投影。
func (p *fakeSessionContextProjector) ProjectAdvisorState(_ context.Context, projection AdvisorStateProjection) error {
	if p.err != nil {
		return p.err
	}

	p.advisorStateCalls = append(p.advisorStateCalls, AdvisorStateProjection{
		SessionID:            projection.SessionID,
		PendingClarification: clonePendingClarification(projection.PendingClarification),
		AdvisoryContext:      cloneAdvisoryContext(projection.AdvisoryContext),
		PendingProposal:      clonePendingProposal(projection.PendingProposal),
		AuthorizationState:   cloneAuthorizationState(projection.AuthorizationState),
		ExecutionState:       cloneExecutionState(projection.ExecutionState),
	})
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

func streamTaskSuggestionCount(events []StreamEvent) int {
	count := 0
	for _, event := range events {
		if event.Type == StreamEventTaskSuggestion {
			count++
		}
	}

	return count
}

// withExplicitWorkflowPromotion 把旧测试里的 workflow 候选 decision 归一成 advisor runtime 认可的显式 promotion 契约。
func withExplicitWorkflowPromotion(decision DeliberationDecision) *DeliberationDecision {
	normalized := decision
	if strings.TrimSpace(normalized.ConversationMode) == "" {
		normalized.ConversationMode = "execute"
	}
	if strings.TrimSpace(normalized.RequestedNextStep) == "" {
		normalized.RequestedNextStep = "promote_to_workflow"
	}
	if !normalized.ProposalReady {
		normalized.ProposalReady = true
	}
	normalized.AwaitingAuthorization = false
	if normalized.ProposedInstruction == nil {
		normalized.ProposedInstruction = normalizeOptionalText(normalized.CandidateTaskInstruction)
	}
	if normalized.ProposedPlanGoal == nil {
		normalized.ProposedPlanGoal = normalizeOptionalText(normalized.CandidatePlanGoal)
	}

	return &normalized
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

func cloneSessionContextSnapshot(snapshot *SessionContextSnapshot) *SessionContextSnapshot {
	if snapshot == nil {
		return nil
	}

	cloned := *snapshot
	cloned.ActiveResource = nil
	if snapshot.ActiveResource != nil {
		activeResource := *snapshot.ActiveResource
		cloned.ActiveResource = &activeResource
	}
	cloned.ActiveSection = nil
	if snapshot.ActiveSection != nil {
		activeSection := *snapshot.ActiveSection
		cloned.ActiveSection = &activeSection
	}
	cloned.ActiveEntityName = cloneOptionalString(snapshot.ActiveEntityName)
	cloned.PendingClarification = clonePendingClarification(snapshot.PendingClarification)
	cloned.AdvisoryContext = cloneAdvisoryContext(snapshot.AdvisoryContext)
	cloned.PendingProposal = clonePendingProposal(snapshot.PendingProposal)
	cloned.AuthorizationState = cloneAuthorizationState(snapshot.AuthorizationState)
	cloned.ExecutionState = cloneExecutionState(snapshot.ExecutionState)
	cloned.PendingTaskSuggestion = clonePendingTaskSuggestion(snapshot.PendingTaskSuggestion)
	cloned.LatestTask = cloneLatestTask(snapshot.LatestTask)
	cloned.ConfirmedConstraints = cloneConfirmedConstraints(snapshot.ConfirmedConstraints)
	cloned.RollingSummary = cloneOptionalString(snapshot.RollingSummary)
	cloned.LastCitationWindows = append([]CitationWindow(nil), snapshot.LastCitationWindows...)
	cloned.LastEnumeratedEntities = append([]EnumeratedEntity(nil), snapshot.LastEnumeratedEntities...)
	cloned.OrdinalReferenceFrame = append([]OrdinalReference(nil), snapshot.OrdinalReferenceFrame...)
	return &cloned
}

func cloneCanonicalReadResult(result *CanonicalReadResult) *CanonicalReadResult {
	if result == nil {
		return nil
	}

	cloned := *result
	cloned.Sections = append([]CanonicalReadSectionItem(nil), result.Sections...)
	return &cloned
}

func mustLoadCurrentDocumentForServiceTest(t *testing.T, reader *fakeCurrentFileSectionReader) *CurrentDocument {
	t.Helper()

	document, err := NewCurrentDocumentLoader(reader).Load(context.Background(), &resourceContext{
		ID:     reader.currentVersion.ResourceID,
		Title:  "简历",
		Source: "upload",
	})
	if err != nil {
		t.Fatalf("load current document from reader: %v", err)
	}
	if document == nil {
		t.Fatal("expected current document from reader")
	}

	return document
}

func buildLiveLikeResumeCurrentDocumentWithoutStructure() *CurrentDocument {
	fullText := strings.TrimSpace(`
# 张三简历

## 个人简介
五年前端与平台工程经验，负责过内容平台、协作系统和质量工程。

## 项目经历

### 1. Alpha 内容中台
时间：2024
角色：前端负责人
背景：负责搭建统一内容配置平台。
结果：将页面搭建效率提升 40%。
问题：描述偏结果，缺少具体技术难点与取舍。

### 2. Beta 协作系统
时间：2025
角色：全栈工程师
背景：负责评论、权限和审计模块。
结果：把跨部门协作延迟从 2 天缩短到 4 小时。
问题：缺少个人贡献边界。

### 3. Gamma 风险引擎
时间：2026
角色：架构与工程效率负责人
背景：负责规则编排、回放调试、灰度校验和告警收敛。
技术：Go、PostgreSQL、Redis、React、异步任务队列。
结果：把风险规则上线周期从 7 天缩短到 1 天。
问题：现在这段写法像职责清单，缺少业务价值、复杂性和关键决策的展开。
`)

	sections := []postgres.ResourceSection{
		{ID: "section-profile", VersionID: "version-live-like-resume", SectionType: "section", SectionOrder: 1, Title: "个人简介", Content: "五年前端与平台工程经验，负责过内容平台、协作系统和质量工程。"},
		{ID: "section-projects", VersionID: "version-live-like-resume", SectionType: "section", SectionOrder: 2, Title: "项目经历", Content: "项目经历"},
		{ID: "section-alpha", VersionID: "version-live-like-resume", SectionType: "section", SectionOrder: 3, Title: "1. Alpha 内容中台", Content: "1. Alpha 内容中台\n时间：2024\n角色：前端负责人\n背景：负责搭建统一内容配置平台。\n结果：将页面搭建效率提升 40%。\n问题：描述偏结果，缺少具体技术难点与取舍。"},
		{ID: "section-beta", VersionID: "version-live-like-resume", SectionType: "section", SectionOrder: 4, Title: "2. Beta 协作系统", Content: "2. Beta 协作系统\n时间：2025\n角色：全栈工程师\n背景：负责评论、权限和审计模块。\n结果：把跨部门协作延迟从 2 天缩短到 4 小时。\n问题：缺少个人贡献边界。"},
		{ID: "section-gamma", VersionID: "version-live-like-resume", SectionType: "section", SectionOrder: 5, Title: "3. Gamma 风险引擎", Content: "3. Gamma 风险引擎\n时间：2026\n角色：架构与工程效率负责人\n背景：负责规则编排、回放调试、灰度校验和告警收敛。\n技术：Go、PostgreSQL、Redis、React、异步任务队列。\n结果：把风险规则上线周期从 7 天缩短到 1 天。\n问题：现在这段写法像职责清单，缺少业务价值、复杂性和关键决策的展开。"},
	}

	return &CurrentDocument{
		ResourceID: "resource-live-like-resume",
		VersionID:  "version-live-like-resume",
		Title:      "张三简历",
		SourceType: "upload",
		FullText:   fullText,
		Sections:   append([]postgres.ResourceSection(nil), sections...),
		Outline: NewDocumentOutlineBuilder().Build(BuildDocumentOutlineInput{
			VersionID: "version-live-like-resume",
			FullText:  fullText,
			Sections:  sections,
		}),
		Ready: true,
	}
}

func assertRuntimeEventPayloadField(t *testing.T, payload []byte, field string, expected any) {
	t.Helper()

	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal runtime event payload: %v", err)
	}

	got, ok := value[field]
	if !ok {
		t.Fatalf("expected runtime event payload field %q, got %v", field, value)
	}

	switch expectedValue := expected.(type) {
	case bool:
		gotValue, ok := got.(bool)
		if !ok || gotValue != expectedValue {
			t.Fatalf("expected runtime event payload field %q=%v, got %#v", field, expectedValue, got)
		}
	case string:
		gotValue, ok := got.(string)
		if !ok || gotValue != expectedValue {
			t.Fatalf("expected runtime event payload field %q=%q, got %#v", field, expectedValue, got)
		}
	default:
		t.Fatalf("unsupported runtime event payload assertion type %T", expected)
	}
}
