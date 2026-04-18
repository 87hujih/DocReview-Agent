package assistant

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/llmclient"
	"github.com/cloudwego/eino/schema"
)

// TestWorkflowPlannerAgentParsesWorkflowPromotionDecision 验证 planner agent 会解析进入 workflow 的结论。
func TestWorkflowPlannerAgentParsesWorkflowPromotionDecision(t *testing.T) {
	agent := newWorkflowPlannerAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"should_enter_workflow": true,
				"chat_fulfillable": false,
				"needs_clarification": false,
				"candidate_instruction": "把第三个项目改成产品经理版本",
				"candidate_plan_goal": "产出可执行的简历修订任务",
				"missing_materials": [],
				"confidence": 0.93,
				"reasons": ["用户明确要求开始执行", "当前资源已足够"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Plan(context.Background(), RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
	}, &DeliberationDecision{
		RequestKind:              "workflow_command",
		ResponseMode:             ResponseModePlanThenAnswer,
		ChatFulfillable:          false,
		WorkflowCommitment:       true,
		EvidenceSufficiency:      "sufficient",
		CandidateTaskInstruction: stringPointer("直接开始改第三个项目，创建任务"),
		CandidatePlanGoal:        stringPointer("产出可执行的修订任务"),
		Confidence:               0.89,
		Reasons:                  []string{"用户已表达执行承诺"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if !decision.ShouldEnterWorkflow || decision.ChatFulfillable || decision.NeedsClarification {
		t.Fatalf("unexpected workflow planning decision: %#v", decision)
	}
	if decision.CandidateInstruction == nil || *decision.CandidateInstruction != "把第三个项目改成产品经理版本" {
		t.Fatalf("expected candidate instruction, got %#v", decision.CandidateInstruction)
	}
}

// TestWorkflowPlannerAgentParsesChatFulfillableDecision 验证 planner agent 会把可直接回答的请求收回聊天通道。
func TestWorkflowPlannerAgentParsesChatFulfillableDecision(t *testing.T) {
	agent := newWorkflowPlannerAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"should_enter_workflow": true,
				"chat_fulfillable": true,
				"needs_clarification": false,
				"candidate_instruction": "把第三个项目改成产品经理版本",
				"candidate_plan_goal": "产出可执行的简历修订任务",
				"confidence": 0.76,
				"reasons": ["当前请求仍可在聊天内直接完成"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Plan(context.Background(), RuntimeState{
		Message: "把第三个项目先输出一遍",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		ChatFulfillable:     false,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "sufficient",
		Confidence:          0.81,
		Reasons:             []string{"接近 workflow 边界，需要 planner 二次收敛"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if decision.ShouldEnterWorkflow || !decision.ChatFulfillable || decision.NeedsClarification {
		t.Fatalf("expected planner to keep request in chat lane, got %#v", decision)
	}
	if decision.CandidateInstruction != nil || decision.CandidatePlanGoal != nil {
		t.Fatalf("expected planner to clear workflow-only fields, got %#v", decision)
	}
}

// TestWorkflowPlannerAgentParsesClarificationDecision 验证 planner agent 会解析澄清型结论。
func TestWorkflowPlannerAgentParsesClarificationDecision(t *testing.T) {
	agent := newWorkflowPlannerAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"should_enter_workflow": false,
				"chat_fulfillable": false,
				"needs_clarification": true,
				"clarification_question": "你是想先让我输出原文，还是直接开始改写？",
				"missing_materials": ["目标岗位"],
				"confidence": 0.84,
				"reasons": ["用户目标仍有歧义"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Plan(context.Background(), RuntimeState{
		Message: "整理一下第三个项目",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		ChatFulfillable:     false,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "partial",
		Confidence:          0.7,
		Reasons:             []string{"用户像是在要求落地执行，但目标还不够明确"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if decision.ShouldEnterWorkflow || decision.ChatFulfillable || !decision.NeedsClarification {
		t.Fatalf("expected clarification decision, got %#v", decision)
	}
	if decision.ClarificationQuestion == nil || *decision.ClarificationQuestion != "你是想先让我输出原文，还是直接开始改写？" {
		t.Fatalf("expected clarification question, got %#v", decision.ClarificationQuestion)
	}
}

// TestWorkflowPlannerAgentPromptMentionsNoTaskCreationSideEffects 验证 planner prompt 明确禁止创建任务副作用。
func TestWorkflowPlannerAgentPromptMentionsNoTaskCreationSideEffects(t *testing.T) {
	var captured []*schema.Message
	agent := newWorkflowPlannerAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
			captured = messages
			return &schema.Message{Content: `{
				"should_enter_workflow": false,
				"chat_fulfillable": true,
				"needs_clarification": false,
				"confidence": 0.8,
				"reasons": ["当前请求在聊天里就能完成"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	_, err := agent.Plan(context.Background(), RuntimeState{
		Message: "把第三个项目先输出一遍",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		ChatFulfillable:     false,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "sufficient",
		Confidence:          0.78,
		Reasons:             []string{"deliberation 认为它接近 workflow 入口"},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("expected captured workflow planner prompt")
	}

	systemPrompt := captured[0].Content
	if !strings.Contains(systemPrompt, "不要创建任务") {
		t.Fatalf("expected planner prompt to forbid task creation side effects, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "只能判断是否进入 workflow") {
		t.Fatalf("expected planner prompt to focus on workflow entry only, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "chat_fulfillable=true") {
		t.Fatalf("expected planner prompt to require explicit chat_fulfillable guidance, got %q", systemPrompt)
	}
}
