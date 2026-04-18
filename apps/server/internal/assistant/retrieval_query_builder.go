package assistant

import (
	"strings"
	"unicode/utf8"
)

var retrievalFollowUpTokens = []string{
	"继续",
	"刚才",
	"上一个",
	"第二个",
	"那个",
	"这个",
	"再",
	"还是",
	"上一版",
}

// RetrievalQueryInput 描述检索 query 改写所需的最小上下文。
type RetrievalQueryInput struct {
	CurrentMessage        string
	RollingSummary        *string
	PendingTaskSuggestion *SnapshotPendingTaskSuggestion
	ActiveResource        *SnapshotActiveResource
	ResolvedReference     *ResolvedReference
}

// RetrievalQueryBuilder 负责在承接式短句场景下构造更稳定的检索 query。
type RetrievalQueryBuilder struct{}

// Build 返回最终传给检索器的 query。
func (b *RetrievalQueryBuilder) Build(input RetrievalQueryInput) string {
	currentMessage := strings.TrimSpace(input.CurrentMessage)
	if currentMessage == "" {
		return ""
	}
	if input.ResolvedReference != nil && strings.TrimSpace(input.ResolvedReference.SectionID) != "" {
		return currentMessage
	}
	if !shouldExpandRetrievalQuery(currentMessage) {
		return currentMessage
	}

	lines := []string{"当前问题：" + currentMessage}
	if summary := strings.TrimSpace(optionalStringValue(input.RollingSummary)); summary != "" {
		lines = append(lines, "会话摘要："+summary)
	}
	if input.PendingTaskSuggestion != nil {
		if instruction := strings.TrimSpace(input.PendingTaskSuggestion.Instruction); instruction != "" {
			lines = append(lines, "待确认任务："+instruction)
		}
	}

	return strings.Join(lines, "\n")
}

// shouldExpandRetrievalQuery 判断 `ExpandRetrieval查询` 是否值得进入扩展分支，避免策略条件散落。
func shouldExpandRetrievalQuery(query string) bool {
	compact := strings.Join(strings.Fields(strings.TrimSpace(query)), "")
	if compact == "" {
		return false
	}
	if utf8.RuneCountInString(compact) >= 10 {
		return false
	}

	for _, token := range retrievalFollowUpTokens {
		if strings.Contains(compact, token) {
			return true
		}
	}

	return false
}

// optionalStringValue 把 `StringValue` 归一化为可选值表示，统一 nil 和空值边界。
func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
