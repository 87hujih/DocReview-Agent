package assistant

import "strings"

const (
	// PendingResolutionOptionWorkflow 表示当前澄清被解析为“进入执行/修改分支”。
	PendingResolutionOptionWorkflow = "workflow"
	// PendingResolutionOptionDraft 表示当前澄清被解析为“先看草案/继续顾问”分支。
	PendingResolutionOptionDraft = "draft"
)

// PendingResolution 表示 runtime 对上一轮待确认状态的确定性解析结果。
type PendingResolution struct {
	ResolvesClarification bool
	ResolvesProposal      bool
	ExplicitAuthorization bool
	DowngradeToAdvice     bool
	NeedsFollowupQuestion bool
	SelectedOption        string
}

// ResolvePendingState 只在存在 pending clarification / proposal 时运行确定性解析。
func ResolvePendingState(state RuntimeState) PendingResolution {
	message := normalizeResolverText(state.Message)
	if message == "" {
		return PendingResolution{}
	}

	if state.PendingClarification != nil {
		return resolvePendingClarification(message, state.PendingClarification)
	}
	if state.PendingProposal != nil {
		return resolvePendingProposal(message, state.PendingProposal)
	}

	return PendingResolution{}
}

// resolvePendingClarification 解析上一轮澄清问题的回答，不承担全局意图识别。
func resolvePendingClarification(message string, pending *SnapshotPendingClarification) PendingResolution {
	if pending == nil {
		return PendingResolution{}
	}
	if isDraftRequestText(message) {
		return PendingResolution{
			ResolvesClarification: true,
			DowngradeToAdvice:     true,
			SelectedOption:        PendingResolutionOptionDraft,
		}
	}

	if strings.TrimSpace(pending.Kind) == "proposal_selection" && isAmbiguousProposalAuthorization(message) {
		return PendingResolution{
			NeedsFollowupQuestion: true,
		}
	}

	if matchesClarificationWorkflowBranch(message, pending) {
		return PendingResolution{
			ResolvesClarification: true,
			SelectedOption:        PendingResolutionOptionWorkflow,
		}
	}

	if matchesClarificationOption(message, pending, "草案") {
		return PendingResolution{
			ResolvesClarification: true,
			DowngradeToAdvice:     true,
			SelectedOption:        PendingResolutionOptionDraft,
		}
	}

	return PendingResolution{}
}

// resolvePendingProposal 解析上一轮 proposal 的授权结果。
func resolvePendingProposal(message string, pending *SnapshotPendingProposal) PendingResolution {
	if pending == nil {
		return PendingResolution{}
	}
	if isDraftRequestText(message) {
		return PendingResolution{
			DowngradeToAdvice: true,
			SelectedOption:    PendingResolutionOptionDraft,
		}
	}
	if isExplicitProposalAuthorization(message) {
		return PendingResolution{
			ResolvesProposal:      true,
			ExplicitAuthorization: true,
			SelectedOption:        PendingResolutionOptionWorkflow,
		}
	}

	return PendingResolution{}
}

// matchesClarificationWorkflowBranch 判断当前回答是否明确选择了澄清问题里的执行分支。
func matchesClarificationWorkflowBranch(message string, pending *SnapshotPendingClarification) bool {
	if pending == nil {
		return false
	}
	if !containsAny(message, []string{
		"直接修改",
		"直接改",
		"开始修改",
		"开始执行",
		"创建任务",
		"按这个方案改",
		"按这个方向改",
	}) {
		return false
	}

	return containsAny(strings.TrimSpace(pending.Question), []string{"修改", "执行", "任务"}) ||
		matchesClarificationOption(message, pending, "修改") ||
		matchesClarificationOption(message, pending, "执行") ||
		matchesClarificationOption(message, pending, "任务")
}

// matchesClarificationOption 判断澄清选项里是否存在与当前回答对齐的候选项。
func matchesClarificationOption(message string, pending *SnapshotPendingClarification, keyword string) bool {
	if pending == nil {
		return false
	}

	normalizedKeyword := normalizeResolverText(keyword)
	for _, option := range pending.Options {
		normalizedOption := normalizeResolverText(option)
		if normalizedOption == "" || !strings.Contains(normalizedOption, normalizedKeyword) {
			continue
		}
		if strings.Contains(message, normalizedOption) || strings.Contains(message, normalizedKeyword) {
			return true
		}
	}

	return false
}

// isDraftRequestText 判断用户是否明确要求先看草案 / 初稿。
func isDraftRequestText(message string) bool {
	return containsAny(message, []string{
		"先给我草案看看",
		"先给我个草案看看",
		"先给我草稿看看",
		"先给我初稿看看",
		"先看草案",
		"先看草稿",
		"先出个草案",
		"先出个版本",
	})
}

// isExplicitProposalAuthorization 判断用户是否对唯一 proposal 给出明确授权。
func isExplicitProposalAuthorization(message string) bool {
	if containsAny(message, []string{"可以", "好的", "好", "行", "嗯"}) &&
		!containsAny(message, []string{"方案", "方向", "修改", "执行", "创建任务", "开始"}) {
		return false
	}

	return containsAny(message, []string{
		"按这个方案改",
		"按这个方向改",
		"按这个方案来",
		"按这个来改",
		"按你的建议改",
		"按你的方案改",
		"就按这个来",
		"直接修改",
		"开始修改",
		"开始执行",
		"创建任务",
	})
}

// isAmbiguousProposalAuthorization 判断当前回答是否只是笼统地“按你的建议”，无法唯一指向多建议中的一条。
func isAmbiguousProposalAuthorization(message string) bool {
	return containsAny(message, []string{
		"按你的建议改",
		"按你的建议",
		"按这个建议改",
		"按这个来改",
		"就按这个来",
	})
}

// normalizeResolverText 统一清理解析输入，避免大小写和空白差异干扰匹配。
func normalizeResolverText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
