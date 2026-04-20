package assistant

import "strings"

type ReadIntentKind string

const (
	ReadIntentDiscussion         ReadIntentKind = "discussion"
	ReadIntentListSections       ReadIntentKind = "list_sections"
	ReadIntentLocateOrdinal      ReadIntentKind = "locate_ordinal"
	ReadIntentExcerptSection     ReadIntentKind = "excerpt_section"
	ReadIntentAggregateAttribute ReadIntentKind = "aggregate_attribute"
	ReadIntentAnalyzeSection     ReadIntentKind = "analyze_section"
	ReadIntentTransformSection   ReadIntentKind = "transform_section"
	ReadIntentExecutionRequest   ReadIntentKind = "execution_request"
)

// ReadIntent 承载当前文件直接访问链所需的最小分类结果，供任务建议和后续 direct access 主链复用。
type ReadIntent struct {
	Kind                  ReadIntentKind
	NeedsLLM              bool
	ShouldEnterTaskFlow   bool
	RequiresSectionTarget bool
}

// ClassifyReadIntent 对当前消息做“读 / 分析 / 执行”最小分类，避免任务建议继续依赖动作词兜底。
func ClassifyReadIntent(message string) ReadIntent {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ReadIntent{Kind: ReadIntentDiscussion}
	}

	if isExplicitExecutionRequest(trimmed) {
		return ReadIntent{
			Kind:                ReadIntentExecutionRequest,
			NeedsLLM:            true,
			ShouldEnterTaskFlow: true,
		}
	}

	if isExcerptSectionRequest(trimmed) {
		return ReadIntent{
			Kind:                  ReadIntentExcerptSection,
			RequiresSectionTarget: true,
		}
	}

	if isListSectionsRequest(trimmed) {
		return ReadIntent{Kind: ReadIntentListSections}
	}

	if isAggregateAttributeRequest(trimmed) {
		return ReadIntent{Kind: ReadIntentAggregateAttribute}
	}

	if isAnalyzeSectionRequest(trimmed) {
		return ReadIntent{
			Kind:                  ReadIntentAnalyzeSection,
			NeedsLLM:              true,
			RequiresSectionTarget: true,
		}
	}

	if isTransformSectionRequest(trimmed) {
		return ReadIntent{
			Kind:                  ReadIntentTransformSection,
			NeedsLLM:              true,
			RequiresSectionTarget: true,
		}
	}

	if referencesOrdinalTarget(trimmed) {
		return ReadIntent{
			Kind:                  ReadIntentLocateOrdinal,
			RequiresSectionTarget: true,
		}
	}

	return ReadIntent{
		Kind:     ReadIntentDiscussion,
		NeedsLLM: true,
	}
}

// isExplicitExecutionRequest 判断消息是否明确要求进入修改 / 执行链，而不是聊天内阅读或分析。
func isExplicitExecutionRequest(message string) bool {
	if containsAny(message, []string{
		"整理成任务",
		"生成任务",
		"创建任务",
		"任务建议",
		"任务卡",
		"开始执行",
	}) {
		return true
	}

	if !containsAny(message, []string{
		"请直接",
		"直接帮我",
		"直接把",
		"请帮我",
		"帮我",
		"请把",
		"开始处理",
		"开始修改",
		"开始执行",
		"执行吧",
		"生成新版本",
		"按这个方向开始修改",
		"按这个方向开始处理",
		"现在就改",
		"马上改",
	}) {
		return false
	}

	return containsAny(message, []string{
		"改成",
		"改写",
		"重写",
		"润色",
		"检查并修订",
		"修订",
		"改为",
		"修改",
		"处理",
		"新版本",
	})
}

// isExcerptSectionRequest 判断消息是否要求对当前文件目标内容做原文保真输出。
func isExcerptSectionRequest(message string) bool {
	if !containsAny(message, []string{
		"输出一遍",
		"输出一下",
		"先输出",
		"列出来",
		"摘录",
		"提取",
		"复述",
	}) {
		return false
	}

	return referencesCurrentFileTarget(message)
}

// isListSectionsRequest 判断消息是否要求列举当前文件内的 section / 项目列表。
func isListSectionsRequest(message string) bool {
	if containsAny(message, []string{
		"有哪些项目",
		"都有哪些项目",
		"有哪些章节",
		"都有哪些章节",
	}) {
		return true
	}

	if !containsAny(message, []string{"列一下", "列出"}) {
		return false
	}

	return containsAny(message, []string{"项目", "章节", "部分", "小节"})
}

// isAggregateAttributeRequest 判断消息是否要求读取当前文件里的聚合属性，例如技术栈。
func isAggregateAttributeRequest(message string) bool {
	if !containsAny(message, []string{"哪些技术栈", "用了哪些技术", "用到了哪些技术", "技术栈"}) {
		return false
	}

	return referencesCurrentFileTarget(message)
}

// isAnalyzeSectionRequest 判断消息是否要求基于当前文件目标内容做分析，而不是直接修改。
func isAnalyzeSectionRequest(message string) bool {
	if !containsAny(message, []string{
		"问题是什么",
		"问题在哪",
		"有什么问题",
		"为什么显得弱",
		"为什么比较弱",
	}) {
		return false
	}

	return referencesCurrentFileTarget(message)
}

// isTransformSectionRequest 判断消息是否要求在聊天里讨论如何强化当前文件某个局部内容。
func isTransformSectionRequest(message string) bool {
	if !containsAny(message, []string{
		"改强",
		"优化一下",
		"怎么优化",
		"怎么改",
		"怎么强化",
	}) {
		return false
	}

	if !containsAny(message, []string{"把", "将", "这个项目", "这一段", "这段", "这一节", "第三个项目"}) {
		return false
	}

	return referencesCurrentFileTarget(message)
}

// referencesCurrentFileTarget 判断消息是否在引用当前文件里的具体目标，而不是泛泛而谈。
func referencesCurrentFileTarget(message string) bool {
	return referencesOrdinalTarget(message) || containsAny(message, []string{
		"项目",
		"章节",
		"这一段",
		"这段",
		"这一节",
		"这节",
		"这个项目",
		"这份简历",
		"这份文件",
	})
}

// referencesOrdinalTarget 判断消息是否带有“第几个 section / 项目”这类顺序目标。
func referencesOrdinalTarget(message string) bool {
	if !strings.Contains(message, "第") {
		return false
	}

	return containsAny(message, []string{
		"个项目",
		"个章节",
		"部分",
		"节",
	})
}
