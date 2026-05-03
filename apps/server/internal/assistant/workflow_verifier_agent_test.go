package assistant

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/llmclient"
	"github.com/cloudwego/eino/schema"
)

// TestWorkflowVerifierAgentApprovesExplicitWorkflowPromotion 验证 verifier agent 会放行明确的 workflow promotion。
func TestWorkflowVerifierAgentApprovesExplicitWorkflowPromotion(t *testing.T) {
	agent := newWorkflowVerifierAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"approve_workflow": true,
				"downgrade_to_chat": false,
				"needs_clarification": false,
				"revised_instruction": "把第三个项目改成更聚焦产品岗位的版本",
				"confidence": 0.94,
				"reasons": ["用户明确要求开始执行", "候选 instruction 只需要轻微收紧"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Verify(context.Background(), RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "sufficient",
		Confidence:          0.88,
		Reasons:             []string{"用户明确要求开始执行"},
	}, &WorkflowPlanDecision{
		ShouldEnterWorkflow:  true,
		CandidateInstruction: stringPointer("把第三个项目改成产品经理版本"),
		Confidence:           0.9,
		Reasons:              []string{"planner 认为材料充足"},
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if !decision.ApproveWorkflow || decision.DowngradeToChat || decision.NeedsClarification {
		t.Fatalf("unexpected verification decision: %#v", decision)
	}
	if decision.RevisedInstruction == nil || *decision.RevisedInstruction != "把第三个项目改成更聚焦产品岗位的版本" {
		t.Fatalf("expected revised instruction, got %#v", decision.RevisedInstruction)
	}
}

// TestWorkflowVerifierAgentDowngradesReadbackLikePromotion 验证 verifier agent 会拦住仍可聊天完成的 promotion。
func TestWorkflowVerifierAgentDowngradesReadbackLikePromotion(t *testing.T) {
	agent := newWorkflowVerifierAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"approve_workflow": false,
				"downgrade_to_chat": true,
				"needs_clarification": false,
				"confidence": 0.86,
				"reasons": ["当前请求仍可在聊天里直接完成", "planner 对 workflow 的升级过度"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Verify(context.Background(), RuntimeState{
		Message: "把第三个项目先输出一遍",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "sufficient",
		Confidence:          0.76,
		Reasons:             []string{"上游先把它当成 workflow 候选"},
	}, &WorkflowPlanDecision{
		ShouldEnterWorkflow:  true,
		CandidateInstruction: stringPointer("把第三个项目输出并开始优化"),
		Confidence:           0.81,
		Reasons:              []string{"planner 误判成需要 workflow"},
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if decision.ApproveWorkflow || !decision.DowngradeToChat || decision.NeedsClarification {
		t.Fatalf("expected downgrade decision, got %#v", decision)
	}
}

// TestWorkflowVerifierAgentRequestsClarificationForAmbiguousTransform 验证 verifier agent 会为歧义改写请求要求澄清。
func TestWorkflowVerifierAgentRequestsClarificationForAmbiguousTransform(t *testing.T) {
	agent := newWorkflowVerifierAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{
				"approve_workflow": false,
				"downgrade_to_chat": false,
				"needs_clarification": true,
				"clarification_question": "你是想先让我输出原文，还是直接开始改写成产品经理版本？",
				"confidence": 0.83,
				"reasons": ["用户目标仍有歧义"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	decision, err := agent.Verify(context.Background(), RuntimeState{
		Message: "整理一下第三个项目",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "partial",
		Confidence:          0.71,
		Reasons:             []string{"需要进一步确定用户意图"},
	}, &WorkflowPlanDecision{
		ShouldEnterWorkflow:  true,
		CandidateInstruction: stringPointer("整理第三个项目"),
		Confidence:           0.8,
		Reasons:              []string{"planner 倾向进入 workflow"},
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if decision.ApproveWorkflow || decision.DowngradeToChat || !decision.NeedsClarification {
		t.Fatalf("expected clarification decision, got %#v", decision)
	}
	if decision.ClarificationQuestion == nil || *decision.ClarificationQuestion != "你是想先让我输出原文，还是直接开始改写成产品经理版本？" {
		t.Fatalf("expected clarification question, got %#v", decision.ClarificationQuestion)
	}
}

// TestWorkflowVerifierPromptMentionsChallengeInsteadOfReplanning 验证 verifier prompt 强调反对式复核，而不是再做一次 planner。
func TestWorkflowVerifierPromptMentionsChallengeInsteadOfReplanning(t *testing.T) {
	var captured []*schema.Message
	agent := newWorkflowVerifierAgentWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
			captured = messages
			return &schema.Message{Content: `{
				"approve_workflow": false,
				"downgrade_to_chat": true,
				"needs_clarification": false,
				"confidence": 0.8,
				"reasons": ["当前请求仍可在聊天里完成"]
			}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1200})

	_, err := agent.Verify(context.Background(), RuntimeState{
		Message: "把第三个项目先输出一遍",
	}, &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		WorkflowCommitment:  true,
		EvidenceSufficiency: "sufficient",
		Confidence:          0.77,
		Reasons:             []string{"deliberation 认为它接近 workflow 入口"},
	}, &WorkflowPlanDecision{
		ShouldEnterWorkflow:  true,
		CandidateInstruction: stringPointer("输出第三个项目并开始优化"),
		Confidence:           0.82,
		Reasons:              []string{"planner 倾向进入 workflow"},
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("expected captured workflow verifier prompt")
	}

	systemPrompt := captured[0].Content
	if !strings.Contains(systemPrompt, "你不是 planner") {
		t.Fatalf("expected verifier prompt to reject replanning role, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "只检查当前 promotion 是否过度") {
		t.Fatalf("expected verifier prompt to focus on promotion challenge, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "优先寻找更低打扰的替代路径") {
		t.Fatalf("expected verifier prompt to prefer lower-disruption alternatives, got %q", systemPrompt)
	}
}
