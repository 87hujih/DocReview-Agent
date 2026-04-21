package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/llmclient"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/sections"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const systemPrompt = `你是一个文档修订编辑代理。你必须只输出 JSON 对象，不要输出 Markdown 代码块，也不要输出额外说明。

输出 JSON 结构：
{
  "no_change": false,
  "sections": [
    {
      "section_title": "章节标题",
      "section_occurrence": 1,
      "original": "原文片段",
      "revised": "修订后片段",
      "reason": "修改原因",
      "citation_ids": ["cite_1"]
    }
  ]
}

要求：
1. 若文档无需任何修改，返回 {"no_change": true, "sections": []}。
2. 否则只返回需要修改的章节，no_change 设为 false。
3. 每个 section 必须输出 section_occurrence，表示同名章节第几次出现；若全文没有任何 ## 二级标题，则 section_title 固定写“全文”，section_occurrence 固定写 1。
4. 每个 section 的 citation_ids 至少包含 1 个引用 ID。
5. original 必须与该章节当前正文逐字一致，不得只截取局部片段；若文档存在 ## 章节标题，original 不得包含标题行本身。
6. revised 必须是替换该章节正文后的完整正文，且与 original 不同；若文档存在 ## 章节标题，revised 不得包含标题行本身。
7. reason 解释修改原因，不要做程序化校验说明。`

// Agent 封装 Editor 所需的 LLM 客户端和重试配置。
type Agent struct {
	client   *openaiacl.Client
	retryCfg llmclient.Config
}

// DiffPreview 是编辑代理输出的结构化预览。
type DiffPreview struct {
	NoChange bool          `json:"no_change"`
	Sections []DiffSection `json:"sections"`
}

// DiffSection 表示一个待修订章节。
type DiffSection struct {
	SectionTitle      string   `json:"section_title"`
	SectionOccurrence int      `json:"section_occurrence"`
	Original          string   `json:"original"`
	Revised           string   `json:"revised"`
	Reason            string   `json:"reason"`
	CitationIDs       []string `json:"citation_ids"`
}

// New 构造 Editor Agent。cfg 控制单次 HTTP 超时和重试行为。
func New(ctx context.Context, baseURL string, apiKey string, model string, cfg llmclient.Config) (*Agent, error) {
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 90000
	}
	temperature := float32(0)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		HTTPClient:  &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
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

	return &Agent{client: client, retryCfg: cfg}, nil
}

// Edit 输出结构化 diff preview。
func (a *Agent) Edit(ctx context.Context, resourceContent string, reviewSummary string, citations []citation.Citation) (*DiffPreview, error) {
	citationsJSON, err := json.Marshal(citations)
	if err != nil {
		return nil, err
	}

	messages := []*schema.Message{
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
	}

	var response *schema.Message
	err = llmclient.CallWithRetry(ctx, a.retryCfg, func() error {
		var genErr error
		response, genErr = a.client.Generate(ctx, messages)
		return genErr
	}, func(attempt int, cause error, backoff time.Duration) {
		log.Printf("editor: 第 %d 次重试，原因：%v，退避：%v", attempt, cause, backoff)
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
	normalizePreviewForResourceContent(&preview, resourceContent)

	return &preview, nil
}

// trimCodeFence 去掉模型输出外层的 Markdown 代码围栏，保留内部可解析正文。
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

// normalizeStringSlice 归一化 `StringSlice`，避免后续流程重复处理边界输入。
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

// normalizePreviewForResourceContent 在无二级标题正文上兜底修正 section 标识，避免模型返回伪造章节标题。
func normalizePreviewForResourceContent(preview *DiffPreview, resourceContent string) {
	if preview == nil || preview.NoChange || len(preview.Sections) == 0 {
		return
	}

	parsedSections := sections.ParseMarkdown(resourceContent)
	if len(parsedSections) != 1 || parsedSections[0].Heading != "" {
		for index := range preview.Sections {
			matchedSection, ok := matchPreviewSection(parsedSections, preview.Sections[index])
			if !ok || strings.TrimSpace(matchedSection.Heading) == "" {
				continue
			}

			preview.Sections[index].Original = stripRepeatedSectionHeading(preview.Sections[index].Original, matchedSection.Heading)
			preview.Sections[index].Revised = stripRepeatedSectionHeading(preview.Sections[index].Revised, matchedSection.Heading)
		}
		return
	}

	for index := range preview.Sections {
		preview.Sections[index].SectionTitle = sections.WholeDocumentTitle
		preview.Sections[index].SectionOccurrence = 1
	}
}

// matchPreviewSection 用 editor 侧已有章节信息定位资源 scope 中的真实 section，便于做最小归一化修正。
func matchPreviewSection(parsedSections []sections.Section, diff DiffSection) (sections.Section, bool) {
	title := strings.TrimSpace(diff.SectionTitle)
	if title == "" {
		return sections.Section{}, false
	}

	for _, section := range parsedSections {
		if section.Title != title {
			continue
		}
		if diff.SectionOccurrence > 0 && section.Occurrence != diff.SectionOccurrence {
			continue
		}

		return section, true
	}

	return sections.Section{}, false
}

// stripRepeatedSectionHeading 去掉模型重复写进正文字段里的 heading，收口 editor 输出与 workflow section body 的契约。
func stripRepeatedSectionHeading(content string, heading string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if normalized == "" || strings.TrimSpace(heading) == "" {
		return normalized
	}

	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 {
		return normalized
	}
	if strings.TrimSpace(lines[0]) == strings.TrimSpace(heading) {
		lines = lines[1:]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}
