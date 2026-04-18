package assistant

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/llmclient"
	"github.com/cloudwego/eino/schema"
)

// TestDeliberationAgentParsesReadbackDecision 验证 deliberation agent 会解析阅读型决策。
func TestDeliberationAgentParsesReadbackDecision(t *testing.T) {
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"request_kind":"readback",
				"response_mode":"answer_with_grounding",
				"chat_fulfillable":true,
				"workflow_commitment":false,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"confidence":0.91,
				"reasons":["用户要的是基于当前材料直接读回内容"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Deliberate(context.Background(), RuntimeState{
		Message: "把第三个项目先输出一遍",
	})
	if err != nil {
		t.Fatalf("deliberate: %v", err)
	}

	if decision.RequestKind != "readback" {
		t.Fatalf("expected request kind readback, got %q", decision.RequestKind)
	}
	if decision.ResponseMode != "answer_with_grounding" {
		t.Fatalf("expected response mode answer_with_grounding, got %q", decision.ResponseMode)
	}
	if !decision.ChatFulfillable || decision.WorkflowCommitment || decision.NeedsClarification {
		t.Fatalf("unexpected readback decision: %#v", decision)
	}
}

// TestDeliberationAgentParsesWorkflowCandidateDecision 验证 deliberation agent 会解析任务候选决策。
func TestDeliberationAgentParsesWorkflowCandidateDecision(t *testing.T) {
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"request_kind":"workflow_command",
				"response_mode":"answer_then_task_card",
				"chat_fulfillable":false,
				"workflow_commitment":true,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"candidate_task_instruction":"把第三个项目改成产品经理版本",
				"candidate_plan_goal":"产出可执行的简历改写任务",
				"confidence":0.87,
				"reasons":["用户明确表达了直接开始改写的意图"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Deliberate(context.Background(), RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
	})
	if err != nil {
		t.Fatalf("deliberate: %v", err)
	}

	if decision.ResponseMode != "answer_then_task_card" {
		t.Fatalf("expected response mode answer_then_task_card, got %q", decision.ResponseMode)
	}
	if !decision.WorkflowCommitment {
		t.Fatalf("expected workflow commitment, got %#v", decision)
	}
	if decision.CandidateTaskInstruction == nil || *decision.CandidateTaskInstruction != "把第三个项目改成产品经理版本" {
		t.Fatalf("expected candidate task instruction, got %#v", decision.CandidateTaskInstruction)
	}
	if decision.CandidatePlanGoal == nil || *decision.CandidatePlanGoal != "产出可执行的简历改写任务" {
		t.Fatalf("expected candidate plan goal, got %#v", decision.CandidatePlanGoal)
	}
}

// TestDeliberationAgentRejectsInvalidResponseMode 验证 deliberation agent 会拒绝非法 response mode。
func TestDeliberationAgentRejectsInvalidResponseMode(t *testing.T) {
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"request_kind":"readback",
				"response_mode":"write_reply_now",
				"chat_fulfillable":true,
				"workflow_commitment":false,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"confidence":0.5,
				"reasons":["非法 mode"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	_, err := agent.Deliberate(context.Background(), RuntimeState{Message: "先输出一遍"})
	if err == nil {
		t.Fatal("expected invalid response mode error")
	}
	if !strings.Contains(err.Error(), "response_mode") {
		t.Fatalf("expected response_mode validation error, got %v", err)
	}
}

// TestDeliberationAgentPromptDoesNotMentionReplyTextGeneration 验证 deliberation prompt 只要求结构化决策，不要求生成回复文本。
func TestDeliberationAgentPromptDoesNotMentionReplyTextGeneration(t *testing.T) {
	var captured []*schema.Message
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
			captured = messages
			return &schema.Message{Content: `{
				"request_kind":"readback",
				"response_mode":"answer_with_grounding",
				"chat_fulfillable":true,
				"workflow_commitment":false,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"confidence":0.88,
				"reasons":["用户只是要读回内容"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	_, err := agent.Deliberate(context.Background(), RuntimeState{
		Message: "把第三个项目先输出一遍",
	})
	if err != nil {
		t.Fatalf("deliberate: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("expected captured deliberation prompt")
	}

	systemPrompt := captured[0].Content
	if strings.Contains(systemPrompt, `"reply"`) || strings.Contains(systemPrompt, "给用户的回复") {
		t.Fatalf("expected deliberation prompt to avoid reply generation, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, `"response_mode"`) {
		t.Fatalf("expected deliberation prompt to require structured decision JSON, got %q", systemPrompt)
	}
}
