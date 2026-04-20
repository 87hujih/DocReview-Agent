package assistant

import (
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

func renderWorkflowProposalOutcome(
	outcome *AssistantOutcome,
	currentDocumentReady bool,
) ([]postgres.AssistantMessageInput, error) {
	replyText := renderWorkflowProposalReply(strings.TrimSpace(outcome.ProposalPreviewText))
	replyText, _ = AuditReply(ReplyAuditInput{
		CurrentDocumentReady: currentDocumentReady,
		CanonicalAccessOK:    currentDocumentReady,
		OutcomeKind:          string(AssistantOutcomeWorkflowProposal),
		TaskCreated:          outcome.EffectState.TaskCreated,
		ResourceMutated:      outcome.EffectState.ResourceMutated,
		ExecutionCommitted:   outcome.EffectState.ExecutionCommitted,
		Reply:                replyText,
	})

	replyInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{Content: replyText})
	if err != nil {
		return nil, err
	}

	inputs := []postgres.AssistantMessageInput{replyInput}
	if strings.TrimSpace(outcome.SuggestionInstruction) == "" || outcome.Resource == nil {
		return inputs, nil
	}

	suggestionInput, err := buildMessageInput(
		RoleAssistant,
		KindTaskSuggestion,
		buildTaskSuggestion(strings.TrimSpace(outcome.SuggestionInstruction), outcome.Resource),
	)
	if err != nil {
		return nil, err
	}

	return append(inputs, suggestionInput), nil
}

func renderWorkflowProposalReply(preview string) string {
	body := normalizeWorkflowProposalPreview(preview)
	lines := []string{"下面是建议改法，尚未写回原文件。"}
	if body != "" {
		lines = append(lines, body)
	}
	lines = append(lines, "如果你确认，我会基于这份方案创建任务。")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func normalizeWorkflowProposalPreview(preview string) string {
	trimmed := strings.TrimSpace(preview)
	if trimmed == "" {
		return "我先整理了一版建议改法。"
	}
	if containsFalseCompletionClaim(trimmed) {
		return "我先整理了一版建议改法。"
	}

	return trimmed
}

// containsFalseCompletionClaim 判断回复里是否伪造了“已经修改/已经创建任务”等未发生的完成态。
func containsFalseCompletionClaim(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	for _, marker := range []string{
		"我已经修改好了",
		"我已经完成了修改",
		"我已经创建任务",
		"已经创建任务了",
		"已经完成了修改",
		"已为你修改",
		"已经帮你修改",
		"已帮你修改",
		"修改已完成",
		"已完成修改",
		"已更新到文件中",
		"更新到文件中了",
		"已经更新到文件中",
		"已经替你改好",
		"我已经帮你改好",
	} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}

	return false
}
