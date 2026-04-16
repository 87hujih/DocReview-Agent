package retriever

import "strings"

// IntentKind 表示 query analyzer 识别出的检索意图。
type IntentKind string

const (
	IntentListProjects  IntentKind = "list_projects"
	IntentProjectDetail IntentKind = "project_detail"
	IntentTechStack     IntentKind = "tech_stack"
	IntentGeneralSearch IntentKind = "general_search"
)

// QueryIntent 描述一次检索请求的规则意图判断结果。
type QueryIntent struct {
	Kind IntentKind
}

// AnalyzeQuery 使用规则优先的方式识别结构化文档检索意图。
func AnalyzeQuery(query string) QueryIntent {
	normalized := strings.TrimSpace(strings.ToLower(query))

	switch {
	case strings.Contains(normalized, "技术栈"),
		strings.Contains(normalized, "用了哪些技术"),
		strings.Contains(normalized, "用到了哪些技术"):
		return QueryIntent{Kind: IntentTechStack}
	case strings.Contains(normalized, "哪些项目"),
		strings.Contains(normalized, "项目有哪些"),
		strings.Contains(normalized, "项目内容，都有哪些"),
		strings.Contains(normalized, "项目内容 都有哪些"):
		return QueryIntent{Kind: IntentListProjects}
	case strings.Contains(normalized, "做了什么"),
		strings.Contains(normalized, "项目详情"),
		strings.Contains(normalized, "项目内容"):
		return QueryIntent{Kind: IntentProjectDetail}
	default:
		return QueryIntent{Kind: IntentGeneralSearch}
	}
}
