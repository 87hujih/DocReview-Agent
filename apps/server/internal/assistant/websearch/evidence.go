package websearch

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// maxSnippetRunes 是净化后 snippet 的最大字符数。
const maxSnippetRunes = 300

// injectionPatterns 用于过滤外部 snippet 中的 prompt injection 攻击尝试。
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior)\s+instructions?`),
	regexp.MustCompile(`(?i)forget\s+(everything|all\s+your)\s+`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+`),
	regexp.MustCompile(`(?i)system\s*:\s*`),
	regexp.MustCompile(`(?i)\bact\s+as\b`),
}

// highReliabilityDomains 高可信度域名片段（政府、权威机构、官方媒体）。
var highReliabilityDomains = []string{
	".gov.cn", ".gov", ".edu.cn", ".edu",
	"xinhuanet.com", "news.cn", "people.com.cn",
	"moj.gov.cn", "npc.gov.cn",
}

// lowReliabilityDomains 低可信度域名片段（论坛、问答社区）。
var lowReliabilityDomains = []string{
	"zhihu.com", "tieba.baidu.com", "v2ex.com",
	"reddit.com", "quora.com", "answers.com",
	"bbs.",
}

// SanitizeSnippet 清理外部 snippet：去除 prompt injection 攻击模式，截断至 maxSnippetRunes。
func SanitizeSnippet(text string) string {
	cleaned := text
	for _, re := range injectionPatterns {
		cleaned = re.ReplaceAllString(cleaned, "")
	}
	cleaned = strings.TrimSpace(cleaned)
	runes := []rune(cleaned)
	if len(runes) > maxSnippetRunes {
		return string(runes[:maxSnippetRunes]) + "…"
	}
	return cleaned
}

// DomainReliabilityHint 根据 URL 域名返回可信度提示："high"、"medium" 或 "low"。
func DomainReliabilityHint(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "medium"
	}
	host := strings.ToLower(u.Host)
	for _, fragment := range highReliabilityDomains {
		if strings.Contains(host, fragment) {
			return "high"
		}
	}
	for _, fragment := range lowReliabilityDomains {
		if strings.Contains(host, fragment) {
			return "low"
		}
	}
	return "medium"
}

// RankAndDedup 对搜索结果按 Score 降序（Score 相同时按 Rank 升序）排列，去除重复 URL。
func RankAndDedup(results []SearchResult) []SearchResult {
	if len(results) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(results))
	deduped := make([]SearchResult, 0, len(results))
	for _, r := range results {
		key := normalizeURL(r.URL)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, r)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Score != deduped[j].Score {
			return deduped[i].Score > deduped[j].Score
		}
		return deduped[i].Rank < deduped[j].Rank
	})
	return deduped
}

func normalizeURL(raw string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "/"))
}
