package reviewer

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

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const systemPrompt = `你是一个文档审阅代理。你的任务是基于原文内容、检索到的引用和修订意图，输出一段精炼的审阅摘要。

要求：
1. 只输出纯文本摘要，不要输出 JSON、Markdown 代码块或列表标号。
2. 摘要要指出需要修改的章节、存在的问题、建议修订方向。
3. 如果引用不足，也要明确指出证据不足。`

// Agent 封装 Reviewer 所需的 LLM 客户端和重试配置。
type Agent struct {
	client   *openaiacl.Client
	retryCfg llmclient.Config
}

// New 构造 Reviewer Agent。cfg 控制单次 HTTP 超时和重试行为。
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

// Review 输出面向编辑步骤的审阅摘要。
func (a *Agent) Review(ctx context.Context, resourceContent string, citations []citation.Citation, intent string) (string, error) {
	citationsJSON, err := json.Marshal(citations)
	if err != nil {
		return "", err
	}

	messages := []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{
			Role: schema.User,
			Content: fmt.Sprintf(
				"修订意图：%s\n\n文档原文：\n%s\n\n引用证据：\n%s\n\n请输出纯文本审阅摘要。",
				intent,
				resourceContent,
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
		log.Printf("reviewer: 第 %d 次重试，原因：%v，退避：%v", attempt, cause, backoff)
	})
	if err != nil {
		return "", err
	}

	summary := strings.TrimSpace(trimCodeFence(response.Content))
	if summary == "" {
		return "", fmt.Errorf("审阅代理返回了空摘要")
	}

	return summary, nil
}

// trimCodeFence 去掉模型输出外层的 Markdown 代码围栏，保留内部可解析正文。
func trimCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	trimmed = strings.TrimPrefix(trimmed, "```text")
	trimmed = strings.TrimPrefix(trimmed, "```markdown")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	return strings.TrimSpace(trimmed)
}
