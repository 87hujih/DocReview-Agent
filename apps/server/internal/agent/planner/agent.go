package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/llmclient"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const systemPrompt = `你是一个文档修订任务的规划代理。你必须只输出 JSON 对象，不要输出 Markdown 代码块，也不要输出额外说明。

输出 JSON 结构：
{
  "intent": "一句话总结本次修订目标",
  "search_queries": ["用于检索证据的查询语句"],
  "focus_sections": ["优先关注的章节标题或主题"]
}

要求：
1. search_queries 至少返回 1 条，最多 5 条。
2. focus_sections 可为空数组，但不能为 null。
3. intent 必须简洁明确。`

// Agent 封装 Planner 所需的 LLM 客户端和重试配置。
type Agent struct {
	client   *openaiacl.Client
	retryCfg llmclient.Config
}

// PlanResult 是 Planner 输出的结构化结果。
type PlanResult struct {
	Intent        string   `json:"intent"`
	SearchQueries []string `json:"search_queries"`
	FocusSections []string `json:"focus_sections"`
}

// New 构造 Planner Agent。cfg 控制单次 HTTP 超时和重试行为。
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

// Plan 根据任务指令和资源摘要生成检索计划。
func (a *Agent) Plan(ctx context.Context, instruction string, resourceTitle string, resourceSummary string) (*PlanResult, error) {
	messages := []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{
			Role: schema.User,
			Content: fmt.Sprintf(
				"任务指令：%s\n\n资源标题：%s\n\n资源摘要：%s\n\n请输出 JSON。",
				instruction,
				resourceTitle,
				resourceSummary,
			),
		},
	}

	var response *schema.Message
	err := llmclient.CallWithRetry(ctx, a.retryCfg, func() error {
		var genErr error
		response, genErr = a.client.Generate(ctx, messages)
		return genErr
	}, func(attempt int, cause error, backoff time.Duration) {
		log.Printf("planner: 第 %d 次重试，原因：%v，退避：%v", attempt, cause, backoff)
	})
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(response.Content)
	normalized := trimCodeFence(raw)

	var result PlanResult
	if err := json.Unmarshal([]byte(normalized), &result); err != nil {
		return nil, fmt.Errorf("解析规划代理结果失败：%w；原始输出：%s", err, raw)
	}

	result.Intent = strings.TrimSpace(result.Intent)
	result.SearchQueries = normalizeStringSlice(result.SearchQueries)
	result.FocusSections = normalizeStringSlice(result.FocusSections)

	return &result, nil
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
