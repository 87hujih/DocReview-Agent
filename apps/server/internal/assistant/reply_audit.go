package assistant

import "strings"

// ReplyAuditInput 归拢回复审计所需输入，避免调用方散落判断条件。
type ReplyAuditInput struct {
	CurrentDocumentReady bool
	CanonicalAccessOK    bool
	OutcomeKind          string
	TaskCreated          bool
	ResourceMutated      bool
	ExecutionCommitted   bool
	Reply                string
}

// AuditReply 在 file-aware 模式下拦截旧的 snippet-only 话术，避免模型漂回旧世界观。
func AuditReply(input ReplyAuditInput) (string, bool) {
	reply := strings.TrimSpace(input.Reply)
	if reply == "" {
		return "", false
	}

	if shouldRewriteFalseCompletionClaim(input, reply) {
		if strings.TrimSpace(input.OutcomeKind) == string(AssistantOutcomeWorkflowProposal) {
			return renderWorkflowProposalReply("我先整理了一版建议改法。"), true
		}

		return "当前还没有真正修改文档、创建任务或写入结果。我可以先继续分析，或先给你一版建议方案。", true
	}

	if !input.CurrentDocumentReady || !input.CanonicalAccessOK {
		return reply, false
	}

	sanitized := reply
	replacements := []struct {
		old string
		new string
	}{
		{"我只能基于你提供的文档片段", "我会直接基于当前文件内容"},
		{"只能基于你提供的文档片段", "会直接基于当前文件内容"},
		{"只能基于文档片段", "会直接基于当前文件内容"},
		{"基于提供的文档片段", "基于当前文件内容"},
		{"如果你有完整文档，我可以", "基于当前文件，我可以"},
		{"如果你有完整文档我可以", "基于当前文件我可以"},
		{"如果您有完整的文档，我可以", "基于当前文件，我可以"},
		{"如果有完整文档，我可以", "基于当前文件，我可以"},
		{"如果有完整文档我可以", "基于当前文件我可以"},
		{"我无法看到原文", "我已经可以直接读取当前文件原文"},
		{"无法看到原文", "已经可以直接读取当前文件原文"},
		{"只看到了片段", "已经可以直接读取当前文件内容"},
	}

	rewritten := false
	for _, item := range replacements {
		if !strings.Contains(sanitized, item.old) {
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, item.old, item.new)
		rewritten = true
	}

	if containsSnippetOnlyWording(sanitized) {
		return "我已经可以直接读取当前文件内容。请继续指出希望我分析的项目名、章节名，或具体角度。", true
	}

	return strings.TrimSpace(sanitized), rewritten
}

// shouldRewriteFalseCompletionClaim 判断当前回复是否在没有真实副作用时伪造了完成态。
func shouldRewriteFalseCompletionClaim(input ReplyAuditInput, reply string) bool {
	if input.TaskCreated || input.ResourceMutated || input.ExecutionCommitted {
		return false
	}

	return containsFalseCompletionClaim(reply)
}

// containsSnippetOnlyWording 判断回复里是否还残留旧的 snippet-only 语义。
func containsSnippetOnlyWording(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	for _, marker := range []string{
		"文档片段",
		"只看到了片段",
		"如果有完整文档",
		"如果您有完整的文档",
		"无法看到原文",
	} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}

	return false
}
