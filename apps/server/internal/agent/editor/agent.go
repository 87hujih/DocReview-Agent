package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const systemPrompt = `你是一个文档修订编辑代理。你必须只输出 JSON 对象，不要输出 Markdown 代码块，也不要输出额外说明。

输出 JSON 结构：
{
  "sections": [
    {
      "section_title": "章节标题",
      "original": "原文片段",
      "revised": "修订后片段",
      "reason": "修改原因",
      "citation_ids": ["cite_1"]
    }
  ]
}

要求：
1. 只返回需要修改的章节，未发现问题时返回 {"sections": []}。
2. 每个 section 的 citation_ids 至少包含 1 个引用 ID。
3. revised 必须是可直接替换到文档中的文本。
4. reason 解释修改原因，不要做程序化校验说明。`

// Agent 封装 Editor 所需的 LLM 客户端。
type Agent struct {
	client *openaiacl.Client
}

// DiffPreview 是 Editor 输出的结构化预览。
type DiffPreview struct {
	Sections []DiffSection `json:"sections"`
}

// DiffSection 表示一个待修订章节。
type DiffSection struct {
	SectionTitle string   `json:"section_title"`
	Original     string   `json:"original"`
	Revised      string   `json:"revised"`
	Reason       string   `json:"reason"`
	CitationIDs  []string `json:"citation_ids"`
}

// New 构造 Editor Agent。
func New(ctx context.Context, baseURL string, apiKey string, model string) (*Agent, error) {
	temperature := float32(0)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		ResponseFormat: &openaiacl.ChatCompletionResponseFormat{
			Type: openaiacl.ChatCompletionResponseFormatTypeJSONObject,
		},
	}
	if strings.TrimSpace(baseURL) != "" {
		config.BaseURL = baseURL
	}

	client, err := openaiacl.NewClient(ctx, config)
	if err != nil {
		return nil, err
	}

	return &Agent{client: client}, nil
}

// Edit 输出结构化 diff preview。
func (a *Agent) Edit(ctx context.Context, resourceContent string, reviewSummary string, citations []citation.Citation) (*DiffPreview, error) {
	citationsJSON, err := json.Marshal(citations)
	if err != nil {
		return nil, err
	}

	response, err := a.client.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{
			Role: schema.User,
			Content: fmt.Sprintf(
				"文档原文：\n%s\n\n审阅摘要：\n%s\n\n引用证据：\n%s\n\n请输出 JSON。",
				resourceContent,
				reviewSummary,
				string(citationsJSON),
			),
		},
	})
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(response.Content)
	normalized := trimCodeFence(raw)

	var preview DiffPreview
	if err := json.Unmarshal([]byte(normalized), &preview); err != nil {
		return nil, fmt.Errorf("解析编辑代理结果失败：%w；原始输出：%s", err, raw)
	}

	for index := range preview.Sections {
		preview.Sections[index].SectionTitle = strings.TrimSpace(preview.Sections[index].SectionTitle)
		preview.Sections[index].Original = strings.TrimSpace(preview.Sections[index].Original)
		preview.Sections[index].Revised = strings.TrimSpace(preview.Sections[index].Revised)
		preview.Sections[index].Reason = strings.TrimSpace(preview.Sections[index].Reason)
		preview.Sections[index].CitationIDs = normalizeStringSlice(preview.Sections[index].CitationIDs)
	}

	return &preview, nil
}

func trimCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	return strings.TrimSpace(trimmed)
}

func normalizeStringSlice(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		normalized = append(normalized, trimmed)
	}

	return normalized
}
