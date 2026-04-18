package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/llmclient"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const (
	// ResponseModeAnswerOnly 表示当前请求可以直接在聊天通道回答。
	ResponseModeAnswerOnly = "answer_only"
	// ResponseModeAnswerWithGrounding 表示当前请求需要基于 grounding 证据回答。
	ResponseModeAnswerWithGrounding = "answer_with_grounding"
	// ResponseModeClarifyFirst 表示当前请求应先澄清。
	ResponseModeClarifyFirst = "clarify_first"
	// ResponseModeAnswerThenTaskCard 表示当前请求应先回答，再附带任务建议卡。
	ResponseModeAnswerThenTaskCard = "answer_then_task_card"
	// ResponseModePlanThenAnswer 表示当前请求需要先规划，再向用户说明。
	ResponseModePlanThenAnswer = "plan_then_answer"
)

const assistantDeliberationSystemPrompt = `你是 assistant runtime 的 deliberation agent。

职责：
1. 只判断当前请求的语义类型与产品下一步动作。
2. 只输出结构化 JSON 决策，绝不能输出 reply、话术或面向用户的正文。
3. 优先判断当前请求属于直接回答、基于证据回答、先澄清，还是回答后给任务卡。
4. 候选 task instruction 只是候选动作，不代表系统已经创建任务。

只输出 JSON：
{"request_kind":"readback|analysis|workflow_command|capability_query|other","response_mode":"answer_only|answer_with_grounding|clarify_first|answer_then_task_card|plan_then_answer","chat_fulfillable":true,"workflow_commitment":false,"needs_clarification":false,"clarification_question":"可选","evidence_sufficiency":"sufficient|partial|insufficient","candidate_task_instruction":"可选","candidate_plan_goal":"可选","confidence":0.0,"reasons":["至少一条理由"]}`

type deliberationAgent interface {
	Deliberate(ctx context.Context, state RuntimeState) (*DeliberationDecision, error)
}

// DeliberationDecision 表示 assistant 在回复前产出的结构化决策。
type DeliberationDecision struct {
	RequestKind              string   `json:"request_kind"`
	ResponseMode             string   `json:"response_mode"`
	ChatFulfillable          bool     `json:"chat_fulfillable"`
	WorkflowCommitment       bool     `json:"workflow_commitment"`
	NeedsClarification       bool     `json:"needs_clarification"`
	ClarificationQuestion    *string  `json:"clarification_question,omitempty"`
	EvidenceSufficiency      string   `json:"evidence_sufficiency"`
	CandidateTaskInstruction *string  `json:"candidate_task_instruction,omitempty"`
	CandidatePlanGoal        *string  `json:"candidate_plan_goal,omitempty"`
	Confidence               float64  `json:"confidence"`
	Reasons                  []string `json:"reasons"`
}

// DeliberationAgent 负责把 RuntimeState 转成结构化 deliberation 决策。
type DeliberationAgent struct {
	client      assistantLLMClient
	retryConfig llmclient.Config
	timeout     time.Duration
}

// NewDeliberationAgent 构造 deliberation agent。
func NewDeliberationAgent(
	ctx context.Context,
	baseURL string,
	apiKey string,
	model string,
	cfg llmclient.Config,
) (*DeliberationAgent, error) {
	temperature := float32(0)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		HTTPClient:  &http.Client{},
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

	return newDeliberationAgentWithClient(&openAIClientAdapter{client: client}, cfg), nil
}

// newDeliberationAgentWithClient 创建带客户端的 deliberation agent，并补齐默认依赖。
func newDeliberationAgentWithClient(client assistantLLMClient, cfg llmclient.Config) *DeliberationAgent {
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 90000
	}

	return &DeliberationAgent{
		client: client,
		retryConfig: llmclient.Config{
			TimeoutMS: timeoutMS,
			RetryMax:  cfg.RetryMax,
			BackoffMS: cfg.BackoffMS,
		},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// Deliberate 基于 RuntimeState 生成结构化决策。
func (a *DeliberationAgent) Deliberate(ctx context.Context, state RuntimeState) (*DeliberationDecision, error) {
	response, err := a.generateWithRetry(ctx, buildDeliberationMessages(state))
	if err != nil {
		return nil, err
	}

	decision, err := decodeDeliberationDecision(response.Content)
	if err != nil {
		return nil, err
	}

	return decision, nil
}

// generateWithRetry 生成 `Retry`，把模型调用和重试策略收口在接收者内部。
func (a *DeliberationAgent) generateWithRetry(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	var response *schema.Message

	err := llmclient.CallWithRetry(ctx, a.retryConfig, func() error {
		callCtx := ctx
		cancel := func() {}
		if a.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, a.timeout)
		}
		defer cancel()

		var err error
		response, err = a.client.Generate(callCtx, messages)
		return err
	}, nil)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// buildDeliberationMessages 组装 deliberation agent 所需的消息序列。
func buildDeliberationMessages(state RuntimeState) []*schema.Message {
	messages := []*schema.Message{
		{Role: schema.System, Content: buildDeliberationSystemPrompt(state)},
	}

	messages = append(messages, buildHistoryMessages(state.History)...)
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: strings.TrimSpace(state.Message),
	})

	return messages
}

// buildDeliberationSystemPrompt 组装 deliberation agent 的系统提示词。
func buildDeliberationSystemPrompt(state RuntimeState) string {
	parts := []string{assistantDeliberationSystemPrompt}
	if runtimeContext := buildRuntimeContext(ChatCompletionInput{
		RuntimeState: state,
	}); runtimeContext != "" {
		parts = append(parts, runtimeContext)
	}

	return strings.Join(parts, "\n\n")
}

// decodeDeliberationDecision 解码并校验 deliberation 决策。
func decodeDeliberationDecision(raw string) (*DeliberationDecision, error) {
	normalized := trimJSONCodeFence(strings.TrimSpace(raw))
	decoder := json.NewDecoder(bytes.NewBufferString(normalized))
	decoder.DisallowUnknownFields()

	var decision DeliberationDecision
	if err := decoder.Decode(&decision); err != nil {
		return nil, fmt.Errorf("解析 deliberation 结果失败：%w；原始输出：%s", err, raw)
	}

	decision.RequestKind = strings.TrimSpace(decision.RequestKind)
	decision.ResponseMode = strings.TrimSpace(decision.ResponseMode)
	decision.EvidenceSufficiency = strings.TrimSpace(decision.EvidenceSufficiency)
	decision.ClarificationQuestion = normalizeOptionalText(decision.ClarificationQuestion)
	decision.CandidateTaskInstruction = normalizeOptionalText(decision.CandidateTaskInstruction)
	decision.CandidatePlanGoal = normalizeOptionalText(decision.CandidatePlanGoal)
	decision.Reasons = normalizeDecisionReasons(decision.Reasons)

	if err := validateDeliberationDecision(&decision); err != nil {
		return nil, err
	}

	return &decision, nil
}

// validateDeliberationDecision 校验 deliberation 决策的关键字段。
func validateDeliberationDecision(decision *DeliberationDecision) error {
	if decision == nil {
		return fmt.Errorf("deliberation 结果不能为空")
	}
	if decision.RequestKind == "" {
		return fmt.Errorf("deliberation 结果缺少 request_kind")
	}
	if !isSupportedResponseMode(decision.ResponseMode) {
		return fmt.Errorf("deliberation 结果包含非法 response_mode: %s", decision.ResponseMode)
	}
	if decision.EvidenceSufficiency == "" {
		return fmt.Errorf("deliberation 结果缺少 evidence_sufficiency")
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return fmt.Errorf("deliberation 结果 confidence 超出范围: %v", decision.Confidence)
	}
	if len(decision.Reasons) == 0 {
		return fmt.Errorf("deliberation 结果缺少 reasons")
	}

	return nil
}

// isSupportedResponseMode 判断响应模式是否属于当前 runtime 允许的枚举。
func isSupportedResponseMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case ResponseModeAnswerOnly,
		ResponseModeAnswerWithGrounding,
		ResponseModeClarifyFirst,
		ResponseModeAnswerThenTaskCard,
		ResponseModePlanThenAnswer:
		return true
	default:
		return false
	}
}

// normalizeOptionalText 归一化可选文本字段，避免空字符串继续向后传播。
func normalizeOptionalText(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

// normalizeDecisionReasons 清理 deliberation reasons，避免空白理由进入后续链路。
func normalizeDecisionReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}

	if len(normalized) == 0 {
		return nil
	}

	return normalized
}
