package websearch

import (
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// NeedGateResult 描述本轮是否需要外部搜索及原因。
type NeedGateResult struct {
	Needed bool
	Reason string
}

// 触发外部搜索的事实性关键词（第一版启发式规则，后续可升级为 LLM 判断）。
var factualKeywords = []string{
	"最新", "现在", "当前", "目前", "今年", "今天",
	"什么是", "是什么", "怎么", "如何", "怎样",
	"政策", "法规", "标准", "规范", "文件",
	"API", "SDK", "文档", "官方",
	"价格", "费用", "收费",
	"版本", "更新", "发布",
	"参数", "指标", "数据",
}

// 纯寒暄/确认类短语，命中则不触发搜索。
var smallTalkPhrases = []string{
	"谢谢", "好的", "明白", "了解", "知道了",
	"继续", "下一步", "然后呢", "再说",
	"嗯", "哦", "啊", "哈哈",
	"没事", "没关系", "算了",
}

// AssessWebSearchNeed 判断本轮消息是否需要外部事实支撑。
// 第一版使用关键词启发式，不依赖 LLM 调用。
func AssessWebSearchNeed(content string, _ []postgres.AssistantMessage) NeedGateResult {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return NeedGateResult{Needed: false, Reason: "empty_message"}
	}

	// 短寒暄消息（20 字以内且命中寒暄词）直接跳过
	if len([]rune(trimmed)) <= 20 {
		for _, phrase := range smallTalkPhrases {
			if strings.Contains(trimmed, phrase) {
				return NeedGateResult{Needed: false, Reason: "small_talk"}
			}
		}
	}

	// 命中事实性关键词则触发搜索
	lower := strings.ToLower(trimmed)
	for _, kw := range factualKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return NeedGateResult{Needed: true, Reason: "factual_keyword:" + kw}
		}
	}

	return NeedGateResult{Needed: false, Reason: "no_factual_signal"}
}
