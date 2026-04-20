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

// TestBuildDeliberationMessagesUsesDocumentNativeRuntimeContext 验证 deliberation prompt 也会消费 document-native 运行时上下文。
func TestBuildDeliberationMessagesUsesDocumentNativeRuntimeContext(t *testing.T) {
	messages := buildDeliberationMessages(RuntimeState{
		Message: "结合全文分析第三个项目",
		CurrentDocument: &CurrentDocument{
			ResourceID: "resource-1",
			VersionID:  "version-1",
			Title:      "产品经理简历",
			SourceType: "upload",
			FullText:   "这是完整正文。",
			Ready:      true,
		},
	})

	if len(messages) == 0 {
		t.Fatal("expected deliberation messages")
	}
	systemPrompt := messages[0].Content
	if !strings.Contains(systemPrompt, "当前文件 canonical 内容已可访问") {
		t.Fatalf("expected deliberation prompt to include document-native runtime context, got %q", systemPrompt)
	}
}

// TestDeliberationAgentReturnsAdviseModeForAnalysisQuestion 验证分析型问题会返回 advisor 模式语义。
func TestDeliberationAgentReturnsAdviseModeForAnalysisQuestion(t *testing.T) {
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"request_kind":"analysis",
				"response_mode":"answer_with_grounding",
				"conversation_mode":"advise",
				"requested_next_step":"give_recommendations",
				"proposal_ready":false,
				"awaiting_authorization":false,
				"chat_fulfillable":true,
				"workflow_commitment":false,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"confidence":0.82,
				"reasons":["用户当前仍在分析阶段"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Deliberate(context.Background(), RuntimeState{
		Message: "第三个项目的问题是什么",
	})
	if err != nil {
		t.Fatalf("deliberate: %v", err)
	}

	if got := mustReadStringField(t, decision, "ConversationMode"); got != "advise" {
		t.Fatalf("expected conversation mode %q, got %q", "advise", got)
	}
	if got := mustReadStringField(t, decision, "RequestedNextStep"); got != "give_recommendations" {
		t.Fatalf("expected requested next step %q, got %q", "give_recommendations", got)
	}
}

// TestDeliberationAgentReturnsConfirmModeWhenConcreteProposalReady 验证已有明确 proposal 时会返回 confirm 模式语义。
func TestDeliberationAgentReturnsConfirmModeWhenConcreteProposalReady(t *testing.T) {
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"request_kind":"workflow_command",
				"response_mode":"answer_then_task_card",
				"conversation_mode":"confirm",
				"requested_next_step":"request_authorization",
				"proposal_ready":true,
				"proposed_instruction":"把第三个项目改成问题-动作-结果结构",
				"proposed_plan_goal":"产出可执行的简历改写任务",
				"awaiting_authorization":true,
				"chat_fulfillable":false,
				"workflow_commitment":true,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"confidence":0.86,
				"reasons":["建议已收敛成一句可执行 instruction"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Deliberate(context.Background(), RuntimeState{
		Message: "那就按这个方向改",
	})
	if err != nil {
		t.Fatalf("deliberate: %v", err)
	}

	if got := mustReadStringField(t, decision, "ConversationMode"); got != "confirm" {
		t.Fatalf("expected conversation mode %q, got %q", "confirm", got)
	}
	if !mustReadBoolField(t, decision, "ProposalReady") {
		t.Fatal("expected proposal_ready to be true")
	}
	if !mustReadBoolField(t, decision, "AwaitingAuthorization") {
		t.Fatal("expected awaiting_authorization to be true")
	}
}

// TestDeliberationAgentReturnsExecuteIntentOnlyForStrictAuthorization 验证只有明确授权时才会返回 execute 模式语义。
func TestDeliberationAgentReturnsExecuteIntentOnlyForStrictAuthorization(t *testing.T) {
	agent := newDeliberationAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"request_kind":"workflow_command",
				"response_mode":"answer_then_task_card",
				"conversation_mode":"execute",
				"requested_next_step":"promote_to_workflow",
				"proposal_ready":true,
				"proposed_instruction":"把第三个项目改成问题-动作-结果结构",
				"proposed_plan_goal":"产出可执行的简历改写任务",
				"awaiting_authorization":false,
				"chat_fulfillable":false,
				"workflow_commitment":true,
				"needs_clarification":false,
				"evidence_sufficiency":"sufficient",
				"confidence":0.9,
				"reasons":["用户明确要求直接开始执行"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Deliberate(context.Background(), RuntimeState{
		Message: "直接修改吧",
	})
	if err != nil {
		t.Fatalf("deliberate: %v", err)
	}

	if got := mustReadStringField(t, decision, "ConversationMode"); got != "execute" {
		t.Fatalf("expected conversation mode %q, got %q", "execute", got)
	}
	if mustReadBoolField(t, decision, "AwaitingAuthorization") {
		t.Fatal("expected execute mode to clear awaiting_authorization")
	}
}
