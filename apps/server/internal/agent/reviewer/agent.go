package reviewer

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

const systemPrompt = `你是一个文档审阅代理。你的任务是基于原文内容、检索到的引用和修订意图，输出一段精炼的审阅摘要。

要求：
1. 只输出纯文本摘要，不要输出 JSON、Markdown 代码块或列表标号。
2. 摘要要指出需要修改的章节、存在的问题、建议修订方向。
3. 如果引用不足，也要明确指出证据不足。`

// Agent 封装 Reviewer 所需的 LLM 客户端。
type Agent struct {
	client *openaiacl.Client
}

// New 构造 Reviewer Agent。
func New(ctx context.Context, baseURL string, apiKey string, model string) (*Agent, error) {
	temperature := float32(0)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
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

// Review 输出面向编辑步骤的审阅摘要。
func (a *Agent) Review(ctx context.Context, resourceContent string, citations []citation.Citation, intent string) (string, error) {
	citationsJSON, err := json.Marshal(citations)
	if err != nil {
		return "", err
	}

	response, err := a.client.Generate(ctx, []*schema.Message{
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
