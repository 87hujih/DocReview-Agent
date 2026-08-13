package context

import "encoding/json"

// JSONTokenCounter lets ContextAssembler 和 ToolRuntime share the exact same
// 模型分词器配置档. ToolRuntime 数量 the serialized JSON that would
// otherwise 为 sent 用于 the 模型 when enforcing MaxResultTokens.
type JSONTokenCounter struct {
	Tokenizer Tokenizer
}

// CountJSON 执行该函数负责的核心处理逻辑。
func (counter JSONTokenCounter) CountJSON(value json.RawMessage) int {
	if counter.Tokenizer == nil {
		return -1
	}
	return counter.Tokenizer.Count(string(value))
}
