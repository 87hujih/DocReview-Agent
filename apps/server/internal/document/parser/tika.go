package parser

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type tikaParser struct {
	baseURL string
	client  *http.Client
}

// Parse 把原始文件字节转发给 Tika，并读取返回的纯文本正文。
func (p *tikaParser) Parse(ctx context.Context, input Input) (*Result, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, p.baseURL+"/tika", bytes.NewReader(input.Content))
	if err != nil {
		return nil, fmt.Errorf("构造 Tika 请求失败：%w", err)
	}
	request.Header.Set("Accept", "text/plain")

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("调用 Tika 失败：%w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Tika 响应失败：%w", err)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Tika 解析失败：状态码 %d，响应 %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	return &Result{
		Text:     string(body),
		Document: buildTikaDocument(sourceFormatFromFileName(input.FileName), string(body)),
	}, nil
}
