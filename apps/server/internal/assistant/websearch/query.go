package websearch

import (
	"strings"
	"unicode"
)

const (
	// maxQueryLength 单条 query 最大字符数，防止把大段正文整体发送。
	maxQueryLength = 120
	// maxQueries 每轮最多生成的搜索 query 数量。
	maxQueries = 2
)

// PlanSearchQuery 从用户消息中提取搜索关键词，返回不超过 maxQueries 条 query。
// 第一版实现：截断长消息、拆分问句、去除明显私密片段。
func PlanSearchQuery(content string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	// 按句号、问号、感叹号拆分，取前两句作为候选 query
	candidates := splitIntoSentences(trimmed)

	var queries []string
	for _, candidate := range candidates {
		q := cleanQuery(candidate)
		if q == "" {
			continue
		}
		queries = append(queries, q)
		if len(queries) >= maxQueries {
			break
		}
	}

	// 若拆分后为空，直接截断原始消息
	if len(queries) == 0 {
		q := cleanQuery(trimmed)
		if q != "" {
			queries = append(queries, q)
		}
	}

	return queries
}

// splitIntoSentences 按中英文句末标点拆分文本为句子列表。
func splitIntoSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	for _, r := range text {
		current.WriteRune(r)
		if r == '。' || r == '？' || r == '！' || r == '.' || r == '?' || r == '!' || r == '\n' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}

	// 末尾没有句末标点的部分
	if remainder := strings.TrimSpace(current.String()); remainder != "" {
		sentences = append(sentences, remainder)
	}

	return sentences
}

// cleanQuery 清理单条 query：去除多余空白，截断过长文本。
func cleanQuery(text string) string {
	// 去除首尾标点和空白
	trimmed := strings.TrimFunc(strings.TrimSpace(text), func(r rune) bool {
		return unicode.IsPunct(r) && r != '(' && r != ')' && r != '“' && r != '”'
	})
	trimmed = strings.TrimSpace(trimmed)

	if trimmed == "" {
		return ""
	}

	// 截断过长 query
	runes := []rune(trimmed)
	if len(runes) > maxQueryLength {
		trimmed = string(runes[:maxQueryLength])
	}

	return trimmed
}
