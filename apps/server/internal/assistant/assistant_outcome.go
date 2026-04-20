package assistant

import (
	"errors"
	"strings"
)

// AssistantOutcomeKind 表示 assistant 当前轮对外暴露的结果阶段。
type AssistantOutcomeKind string

const (
	// AssistantOutcomeChatAnswer 表示普通聊天回复。
	AssistantOutcomeChatAnswer AssistantOutcomeKind = "chat_answer"
	// AssistantOutcomeClarification 表示澄清问题。
	AssistantOutcomeClarification AssistantOutcomeKind = "clarification"
	// AssistantOutcomeWorkflowProposal 表示提案已形成，但尚未真正创建任务或修改资源。
	AssistantOutcomeWorkflowProposal AssistantOutcomeKind = "workflow_proposal"
	// AssistantOutcomeTaskCreatedNotice 预留给已创建任务通知。
	AssistantOutcomeTaskCreatedNotice AssistantOutcomeKind = "task_created_notice"
	// AssistantOutcomeTaskExecutionStatus 预留给任务执行状态消息。
	AssistantOutcomeTaskExecutionStatus AssistantOutcomeKind = "task_execution_status"
)

// AssistantEffectState 表示当前轮真实副作用状态。
type AssistantEffectState struct {
	TaskCreated        bool
	ResourceMutated    bool
	ExecutionCommitted bool
}

// AssistantOutcome 表示一轮 assistant 对外结果的结构化表达。
type AssistantOutcome struct {
	Kind                  AssistantOutcomeKind
	ReplyText             string
	ProposalPreviewText   string
	SuggestionInstruction string
	Resource              *resourceContext
	EffectState           AssistantEffectState
}

// BuildAssistantOutcome 根据 deliberation / gate 结果构造当前轮 outcome。
func BuildAssistantOutcome(
	reply *ChatCompletionResult,
	decision *DeliberationDecision,
	gate ActionGateDecision,
	resource *resourceContext,
) (*AssistantOutcome, error) {
	if reply == nil || strings.TrimSpace(reply.Reply) == "" {
		return nil, errors.New("助手模型返回了空回复")
	}

	outcome := &AssistantOutcome{
		Kind:      AssistantOutcomeChatAnswer,
		ReplyText: strings.TrimSpace(reply.Reply),
	}
	if decision != nil && decision.ResponseMode == ResponseModeClarifyFirst {
		outcome.Kind = AssistantOutcomeClarification
	}

	if !gate.AllowTaskSuggestion || gate.PendingProposal == nil || gate.AuthorizationState == nil {
		return outcome, nil
	}
	if decision == nil || decision.ResponseMode != ResponseModeAnswerThenTaskCard {
		return outcome, nil
	}
	if strings.TrimSpace(gate.AuthorizationState.Status) != "granted" {
		return outcome, nil
	}

	instruction := strings.TrimSpace(gate.PendingProposal.Instruction)
	if decision != nil && decision.CandidateTaskInstruction != nil {
		if revised := strings.TrimSpace(*decision.CandidateTaskInstruction); revised != "" {
			instruction = revised
		}
	}
	if instruction == "" {
		return outcome, nil
	}

	outcome.Kind = AssistantOutcomeWorkflowProposal
	outcome.ProposalPreviewText = strings.TrimSpace(reply.Reply)
	outcome.SuggestionInstruction = instruction
	outcome.Resource = resource
	outcome.EffectState = AssistantEffectState{}
	return outcome, nil
}
