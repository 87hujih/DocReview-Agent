package parser

import "context"

// textParser 承载文本解析器相关状态，明确文档解析链路中的数据边界。
type textParser struct{}

// Parse 对纯文本文件做最小处理，直接返回原始正文。
func (p *textParser) Parse(_ context.Context, input Input) (*Result, error) {
	return &Result{
		Text:     string(input.Content),
		Document: buildTextDocument(input.FileName, string(input.Content)),
	}, nil
}
