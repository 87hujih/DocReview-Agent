package assistant

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"agent_project/apps/server/internal/agent/llmclient"
	"agent_project/apps/server/internal/storage/postgres"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const conversationSummarySystemPrompt = `你负责把一段已经结束的助手对话压缩成稳定摘要。

要求：
1. 只基于提供的 transcript 和稳定上下文总结。
2. 输出固定三段：
当前目标：
关键结论：
待继续事项：
3. 当前快照状态只作为背景，不要机械重复所有结构化字段。`

// SummaryInput 描述滚动摘要生成所需的输入。
type SummaryInput struct {
	PreviousSummary *string
	Transcript      []postgres.AssistantMessage
	Snapshot        *SessionContextSnapshot
}

// SummaryResult 表示一轮摘要生成结果。
type SummaryResult struct {
	Summary string
}

// ConversationSummarizer 定义会话摘要器接口。
type ConversationSummarizer interface {
	Summarize(ctx context.Context, input SummaryInput) (*SummaryResult, error)
}

type llmConversationSummarizer struct {
	client      assistantLLMClient
	retryConfig llmclient.Config
	timeout     time.Duration
}

// NewConversationSummarizer 使用当前 assistant LLM 配置构造会话摘要器。
func NewConversationSummarizer(
	ctx context.Context,
	baseURL string,
	apiKey string,
	model string,
	cfg llmclient.Config,
) (ConversationSummarizer, error) {
	temperature := float32(0.2)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		HTTPClient:  &http.Client{},
	}
	if strings.TrimSpace(baseURL) != "" {
		config.BaseURL = baseURL
	}

	client, err := openaiacl.NewClient(ctx, config)
	if err != nil {
		return nil, err
	}

	return newConversationSummarizerWithClient(&openAIClientAdapter{client: client}, cfg), nil
}

func newConversationSummarizerWithClient(client assistantLLMClient, cfg llmclient.Config) ConversationSummarizer {
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 90000
	}

	return &llmConversationSummarizer{
		client: client,
		retryConfig: llmclient.Config{
			TimeoutMS: timeoutMS,
			RetryMax:  cfg.RetryMax,
			BackoffMS: cfg.BackoffMS,
		},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

func (s *llmConversationSummarizer) Summarize(ctx context.Context, input SummaryInput) (*SummaryResult, error) {
	if s == nil || s.client == nil {
		return nil, nil
	}

	transcriptLines := buildSummaryTranscriptLines(input.Transcript)
	if len(transcriptLines) == 0 {
		return nil, nil
	}

	response, err := s.generateWithRetry(ctx, buildConversationSummaryMessages(input, transcriptLines))
	if err != nil {
		return nil, err
	}

	summary := strings.TrimSpace(response.Content)
	if summary == "" {
		return nil, nil
	}

	return &SummaryResult{Summary: summary}, nil
}

func (s *llmConversationSummarizer) generateWithRetry(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	var response *schema.Message

	err := llmclient.CallWithRetry(ctx, s.retryConfig, func() error {
		callCtx := ctx
		cancel := func() {}
		if s.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, s.timeout)
		}
		defer cancel()

		var err error
		response, err = s.client.Generate(callCtx, messages)
		return err
	}, nil)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func buildConversationSummaryMessages(input SummaryInput, transcriptLines []string) []*schema.Message {
	return []*schema.Message{
		{
			Role:    schema.System,
			Content: conversationSummarySystemPrompt,
		},
		{
			Role: schema.User,
			Content: strings.Join([]string{
				buildSummaryPreviousSummarySection(input.PreviousSummary),
				buildSummarySnapshotContextSection(input.Snapshot),
				"待压缩 transcript：\n" + strings.Join(transcriptLines, "\n"),
			}, "\n\n"),
		},
	}
}

func buildSummaryPreviousSummarySection(previousSummary *string) string {
	if summary := strings.TrimSpace(optionalStringValue(previousSummary)); summary != "" {
		return "已有摘要：\n" + summary
	}

	return "已有摘要：\n无"
}

func buildSummarySnapshotContextSection(snapshot *SessionContextSnapshot) string {
	lines := []string{"稳定上下文："}

	if snapshot == nil {
		lines = append(lines, "- 当前活跃资源：无")
		return strings.Join(lines, "\n")
	}

	if snapshot.ActiveResource != nil {
		lines = append(lines, fmt.Sprintf(
			"- 当前活跃资源：%s（来源=%s，资源ID=%s）",
			strings.TrimSpace(snapshot.ActiveResource.Title),
			strings.TrimSpace(snapshot.ActiveResource.SourceType),
			strings.TrimSpace(snapshot.ActiveResource.ID),
		))
	} else {
		lines = append(lines, "- 当前活跃资源：无")
	}

	if snapshot.PendingTaskSuggestion != nil && strings.TrimSpace(snapshot.PendingTaskSuggestion.Instruction) != "" {
		lines = append(lines, "- 待确认任务："+strings.TrimSpace(snapshot.PendingTaskSuggestion.Instruction))
	}

	if snapshot.LatestTask != nil {
		lines = append(lines, fmt.Sprintf(
			"- 最近任务状态：ID=%s，状态=%s",
			strings.TrimSpace(snapshot.LatestTask.ID),
			strings.TrimSpace(snapshot.LatestTask.Status),
		))
	}

	return strings.Join(lines, "\n")
}

func buildSummaryTranscriptLines(history []postgres.AssistantMessage) []string {
	if len(history) == 0 {
		return nil
	}

	lines := make([]string, 0, len(history))
	for _, message := range history {
		if message.Kind != KindText {
			continue
		}

		payload, err := unmarshalTextPayload(message.Payload)
		if err != nil {
			continue
		}

		content := strings.TrimSpace(payload.Content)
		if content == "" {
			continue
		}

		prefix := "助手"
		if message.Role == RoleUser {
			prefix = "用户"
		}
		lines = append(lines, prefix+"："+content)
	}

	return lines
}

func historyMessageContentLength(message *schema.Message) int {
	if message == nil {
		return 0
	}

	return utf8.RuneCountInString(strings.TrimSpace(message.Content))
}
