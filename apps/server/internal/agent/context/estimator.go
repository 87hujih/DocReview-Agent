package context

import (
	"strings"
	"unicode"
)

// ModelEstimator 为一个 versioned、conservative 分词器 substitute 用于模型
// profiles whose 提供方分词器为 not linked into the 服务. CJK runes
// 和 punctuation 数量 as 一个 token; alphanumeric runs use four bytes/token.
// The 配置档 name 为 persisted 位于 every 清单 so estimates 为 auditable
// 和 can 为 rebuilt 包含一个 exact 分词器不包含 changing the 组装器.
type ModelEstimator struct {
	Profile string
}

// Name 执行该函数负责的核心处理逻辑。
func (e ModelEstimator) Name() string {
	return strings.TrimSpace(e.Profile)
}

// 数量执行该函数负责的核心处理逻辑。
func (e ModelEstimator) Count(text string) int {
	tokens := 0
	runBytes := 0
	flush := func() {
		if runBytes > 0 {
			tokens += (runBytes + 3) / 4
			runBytes = 0
		}
	}
	for _, current := range text {
		// 根据当前状态或类型选择对应的处理分支。
		switch {
		case unicode.IsSpace(current):
			flush()
		case unicode.In(current, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul), unicode.IsPunct(current), unicode.IsSymbol(current):
			flush()
			tokens++
		default:
			runBytes += len(string(current))
		}
	}
	flush()
	return tokens
}
