package assistant

import "strings"

// ReadIntentKind 描述当前消息在活跃文件阅读模式下的意图类别。
type ReadIntentKind string

const (
	ReadIntentListSections       ReadIntentKind = "list_sections"
	ReadIntentLocateOrdinal      ReadIntentKind = "locate_ordinal"
	ReadIntentExcerptSection     ReadIntentKind = "excerpt_section"
	ReadIntentAggregateAttribute ReadIntentKind = "aggregate_attribute"
	ReadIntentAnalyzeSection     ReadIntentKind = "analyze_section"
	ReadIntentTransformSection   ReadIntentKind = "transform_section"
	ReadIntentExecutionRequest   ReadIntentKind = "execution_request"
)

// ReadIntent 收口阅读模式需要的稳定判断，避免后续流程重复猜测。
type ReadIntent struct {
	Kind                  ReadIntentKind
	RequiresLLM           bool
	ShouldTriggerTaskFlow bool
}

// ClassifyReadIntent 对当前消息做最小阅读意图分类。
func ClassifyReadIntent(message string) ReadIntent {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ReadIntent{}
	}

	switch {
	case matchesExecutionRequest(trimmed):
		return ReadIntent{Kind: ReadIntentExecutionRequest, ShouldTriggerTaskFlow: true}
	case matchesAggregateAttributeRequest(trimmed):
		return ReadIntent{Kind: ReadIntentAggregateAttribute}
	case matchesListSectionsRequest(trimmed):
		return ReadIntent{Kind: ReadIntentListSections}
	case matchesExcerptRequest(trimmed):
		return ReadIntent{Kind: ReadIntentExcerptSection}
	case matchesTransformRequest(trimmed):
		return ReadIntent{Kind: ReadIntentTransformSection, RequiresLLM: true}
	case matchesAnalyzeRequest(trimmed):
		return ReadIntent{Kind: ReadIntentAnalyzeSection, RequiresLLM: true}
	case matchesLocateOrdinalRequest(trimmed):
		return ReadIntent{Kind: ReadIntentLocateOrdinal}
	default:
		return ReadIntent{}
	}
}

// matchesExecutionRequest 只在用户明确要求直接修改、开始执行或生成新版本时返回 true。
func matchesExecutionRequest(message string) bool {
	if containsAny(message, []string{
		"如果要改",
		"有什么需要优化",
		"哪里要改",
		"问题在哪",
		"怎么改",
		"先帮我看看",
		"你觉得",
		"怎么看",
	}) {
		return false
	}

	if containsAny(message, []string{
		"输出",
		"列出来",
		"列一下",
		"复述",
		"摘录",
		"摘出来",
		"提取",
	}) {
		return false
	}

	if containsAny(message, []string{
		"生成新版本",
		"产出新版本",
		"直接修改",
		"现在开始执行",
		"开始执行",
		"整理成任务",
		"整理为任务",
		"整理成后续事项",
		"做成后续事项",
	}) {
		return true
	}

	if containsAny(message, []string{"整理成", "整理为", "做成"}) &&
		containsAny(message, []string{"任务", "后续事项"}) &&
		containsAny(message, []string{"请直接", "直接", "请帮我", "帮我", "请把", "把"}) {
		return true
	}

	if !containsAny(message, []string{
		"改成",
		"改写",
		"重写",
		"润色",
		"修订",
		"写成",
		"改为",
		"做成",
	}) {
		return false
	}

	return containsAny(message, []string{
		"请直接",
		"直接",
		"马上",
		"现在",
		"开始",
		"请帮我",
		"帮我",
		"请把",
		"把这",
	})
}

// matchesListSectionsRequest 判断消息是否在要求列出当前文件里的 section 集合。
func matchesListSectionsRequest(message string) bool {
	return containsAny(message, []string{
		"有哪些项目",
		"都有哪些项目",
		"项目有哪些",
		"列出项目",
		"列一下项目",
		"有哪些经历",
		"列出经历",
		"都有哪些章节",
		"有哪些章节",
	})
}

// matchesAggregateAttributeRequest 判断消息是否在要求聚合 section 属性。
func matchesAggregateAttributeRequest(message string) bool {
	if !containsAny(message, []string{
		"技术栈",
		"技能",
		"哪些技术",
		"用到了哪些技术",
	}) {
		return false
	}

	return containsAny(message, []string{
		"有哪些",
		"列出来",
		"列一下",
		"总结",
		"提取",
	})
}

// matchesExcerptRequest 判断消息是否在要求忠实输出原文或摘录。
func matchesExcerptRequest(message string) bool {
	if matchesListSectionsRequest(message) {
		return false
	}

	return containsAny(message, []string{
		"输出一遍",
		"输出一下",
		"先输出",
		"输出",
		"复述",
		"摘录",
		"摘出来",
		"提取",
		"贴出来",
		"原文",
	})
}

// matchesTransformRequest 判断消息是否在要求对当前 section 做改写或优化。
func matchesTransformRequest(message string) bool {
	if matchesExecutionRequest(message) {
		return false
	}

	return containsAny(message, []string{
		"怎么优化",
		"优化一下",
		"改写一下",
		"重写一下",
		"润色一下",
		"改成",
		"改为",
	})
}

// matchesAnalyzeRequest 判断消息是否在要求对当前 section 做分析或点评。
func matchesAnalyzeRequest(message string) bool {
	return containsAny(message, []string{
		"帮我分析",
		"分析一下",
		"评价一下",
		"看看有什么问题",
		"问题在哪",
		"怎么看",
		"先帮我看看",
	})
}

// matchesLocateOrdinalRequest 判断消息是否只是在定位序号 section。
func matchesLocateOrdinalRequest(message string) bool {
	return strings.Contains(message, "第") && containsAny(message, []string{"项目", "经历", "部分", "章节", "小节"})
}
