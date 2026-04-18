package retriever

import "strings"

// QueryIntent 描述 grounded retrieval 当前要走的检索意图。
type QueryIntent string

const (
	QueryIntentGeneralSearch      QueryIntent = "general_search"
	QueryIntentListSections       QueryIntent = "list_sections"
	QueryIntentDetailByEntity     QueryIntent = "detail_by_entity"
	QueryIntentDetailByOrdinal    QueryIntent = "detail_by_ordinal"
	QueryIntentAggregateAttribute QueryIntent = "aggregate_attribute"
)

// QueryAnalysis 是规则分析器产出的最小检索计划。
type QueryAnalysis struct {
	Intent         QueryIntent
	Query          string
	SectionType    string
	EntityName     string
	AggregateField string
	Ordinal        int
}

// QueryAnalyzer 负责在不依赖 LLM 的前提下识别 grounded 检索意图。
type QueryAnalyzer struct{}

// Analyze 把用户查询映射到稳定的 retrieval intent。
func (a QueryAnalyzer) Analyze(query string) QueryAnalysis {
	focus := extractPrimaryQuestion(query)
	analysis := QueryAnalysis{
		Intent: QueryIntentGeneralSearch,
		Query:  focus,
	}

	if isProjectScopedQuestion(focus) {
		analysis.SectionType = "project"
	}

	switch {
	case isAggregateTechStackQuestion(focus):
		analysis.Intent = QueryIntentAggregateAttribute
		analysis.SectionType = "project"
		analysis.AggregateField = "tech_stack"
		return analysis
	case isListSectionsQuestion(focus) && analysis.SectionType != "":
		analysis.Intent = QueryIntentListSections
		return analysis
	}

	if ordinal := extractQueryOrdinal(focus); ordinal > 0 && analysis.SectionType != "" {
		analysis.Intent = QueryIntentDetailByOrdinal
		analysis.Ordinal = ordinal
		return analysis
	}

	if entityName := extractEntityName(focus); entityName != "" && containsDetailQuestionMarker(focus) {
		analysis.Intent = QueryIntentDetailByEntity
		analysis.SectionType = "project"
		analysis.EntityName = entityName
	}

	return analysis
}

// extractPrimaryQuestion 从现有内容里提取 `PrimaryQuestion`，避免调用方重复解析同一份数据。
func extractPrimaryQuestion(query string) string {
	for _, line := range strings.Split(strings.TrimSpace(query), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "当前问题：") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "当前问题："))
		}
	}

	return strings.TrimSpace(query)
}

// isProjectScopedQuestion 判断 `项目ScopedQuestion` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
func isProjectScopedQuestion(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return false
	}

	for _, marker := range []string{"项目", "经历", "技术栈"} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}

	return false
}

// isAggregateTechStackQuestion 判断 `Aggregate技术栈Question` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
func isAggregateTechStackQuestion(query string) bool {
	return strings.Contains(query, "技术栈") ||
		(strings.Contains(query, "用了哪些技术") && !strings.Contains(query, "项目"))
}

// isListSectionsQuestion 判断 `ListsectionQuestion` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
func isListSectionsQuestion(query string) bool {
	if !isProjectScopedQuestion(query) {
		return false
	}

	for _, marker := range []string{"有哪些", "哪些", "列出", "都有什么", "分别是什么"} {
		if strings.Contains(query, marker) {
			return true
		}
	}

	return false
}

// extractQueryOrdinal 从现有内容里提取 `查询序号`，避免调用方重复解析同一份数据。
func extractQueryOrdinal(query string) int {
	switch {
	case strings.Contains(query, "第一个"), strings.Contains(query, "第1个"):
		return 1
	case strings.Contains(query, "第二个"), strings.Contains(query, "第2个"):
		return 2
	case strings.Contains(query, "第三个"), strings.Contains(query, "第3个"):
		return 3
	default:
		return 0
	}
}

// containsDetailQuestionMarker 判断当前集合里是否包含 `详情QuestionMarker`，把匹配规则收口在单点。
func containsDetailQuestionMarker(query string) bool {
	for _, marker := range []string{"做了什么", "负责什么", "讲讲", "介绍", "看下", "看看", "怎么做", "给出修改示例"} {
		if strings.Contains(query, marker) {
			return true
		}
	}

	return false
}

// extractEntityName 从现有内容里提取 `EntityName`，避免调用方重复解析同一份数据。
func extractEntityName(query string) string {
	for _, marker := range []string{"做了什么", "负责什么", "讲讲", "介绍", "看下", "看看", "怎么做", "给出修改示例"} {
		index := strings.Index(query, marker)
		if index <= 0 {
			continue
		}

		prefix := strings.TrimSpace(query[:index])
		prefix = strings.Trim(prefix, "，,。！？!?：:")
		for _, drop := range []string{"针对", "关于", "请", "帮我", "帮忙", "看下", "看看", "说说", "讲讲"} {
			prefix = strings.TrimSpace(strings.TrimPrefix(prefix, drop))
		}
		prefix = strings.TrimSuffix(prefix, "项目")
		prefix = strings.TrimSuffix(prefix, "经历")
		prefix = strings.TrimSpace(prefix)
		if prefix != "" {
			return prefix
		}
	}

	return ""
}
