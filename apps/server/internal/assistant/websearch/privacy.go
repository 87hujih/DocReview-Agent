package websearch

import (
	"regexp"
	"strings"
)

// PrivacyCheckResult 描述隐私检查结果。
type PrivacyCheckResult struct {
	// Safe 为 true 表示 queries 均通过隐私检查，可以发送给外部搜索服务。
	Safe    bool
	Queries []string
	Blocked bool
	Reason  string
}

// 敏感信息正则：身份证号、手机号、邮箱。
var (
	reChineseID = regexp.MustCompile(`\b\d{17}[\dXx]\b`)
	reMobile    = regexp.MustCompile(`\b1[3-9]\d{9}\b`)
	reEmail     = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	reToken     = regexp.MustCompile(`(?i)(token|key|secret|password|passwd|pwd)\s*[:=]\s*\S+`)
	reIntranet  = regexp.MustCompile(`(?i)(192\.168\.|10\.\d+\.\d+\.|172\.(1[6-9]|2\d|3[01])\.)`)
)

// maxSingleQueryRunes 超过该长度的单条 query 视为包含大段原文，直接拦截。
const maxSingleQueryRunes = 200

// CheckQueryPrivacy 对候选 queries 进行隐私检查。
// 只要有一条 query 命中敏感规则就整体拦截，返回 Blocked=true。
func CheckQueryPrivacy(queries []string) PrivacyCheckResult {
	if len(queries) == 0 {
		return PrivacyCheckResult{Safe: true, Queries: queries}
	}

	for _, q := range queries {
		trimmed := strings.TrimSpace(q)

		// 超长 query 视为含大段原文
		if len([]rune(trimmed)) > maxSingleQueryRunes {
			return PrivacyCheckResult{
				Safe:    false,
				Blocked: true,
				Reason:  "query_too_long",
			}
		}

		if reChineseID.MatchString(trimmed) {
			return PrivacyCheckResult{Safe: false, Blocked: true, Reason: "chinese_id"}
		}
		if reMobile.MatchString(trimmed) {
			return PrivacyCheckResult{Safe: false, Blocked: true, Reason: "mobile_number"}
		}
		if reEmail.MatchString(trimmed) {
			return PrivacyCheckResult{Safe: false, Blocked: true, Reason: "email_address"}
		}
		if reToken.MatchString(trimmed) {
			return PrivacyCheckResult{Safe: false, Blocked: true, Reason: "token_or_key"}
		}
		if reIntranet.MatchString(trimmed) {
			return PrivacyCheckResult{Safe: false, Blocked: true, Reason: "intranet_address"}
		}
	}

	return PrivacyCheckResult{Safe: true, Queries: queries}
}
