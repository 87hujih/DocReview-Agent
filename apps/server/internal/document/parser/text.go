package parser

import "context"

type textParser struct{}

// Parse 对纯文本文件做最小处理，直接返回原始正文。
func (p *textParser) Parse(_ context.Context, input Input) (*Result, error) {
	return &Result{
		Text: string(input.Content),
	}, nil
}
