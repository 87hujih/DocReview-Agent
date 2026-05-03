package assistant

import (
	"errors"

	"agent_project/apps/server/internal/storage/postgres"
)

// RenderAssistantOutcome 把结构化 outcome 渲染为最终持久化消息输入。
func RenderAssistantOutcome(
	outcome *AssistantOutcome,
	currentDocumentReady bool,
) ([]postgres.AssistantMessageInput, error) {
	if outcome == nil {
		return nil, errors.New("assistant outcome 不能为空")
	}

	switch outcome.Kind {
	case AssistantOutcomeWorkflowProposal:
		return renderWorkflowProposalOutcome(outcome, currentDocumentReady)
	default:
		sanitizedReply, _ := AuditReply(ReplyAuditInput{
			CurrentDocumentReady: currentDocumentReady,
			CanonicalAccessOK:    currentDocumentReady,
			OutcomeKind:          string(outcome.Kind),
			TaskCreated:          outcome.EffectState.TaskCreated,
			ResourceMutated:      outcome.EffectState.ResourceMutated,
			ExecutionCommitted:   outcome.EffectState.ExecutionCommitted,
			Reply:                outcome.ReplyText,
		})
		input, err := buildMessageInput(RoleAssistant, KindText, TextPayload{Content: sanitizedReply})
		if err != nil {
			return nil, err
		}

		return []postgres.AssistantMessageInput{input}, nil
	}
}
