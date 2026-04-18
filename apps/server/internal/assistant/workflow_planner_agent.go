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

const assistantWorkflowPlannerSystemPrompt = `你是 assistant runtime 的 workflow planner。

职责：
1. 只规划当前请求是否真的需要进入 workflow，不要创建任务、写数据库或假设任何副作用。
2. 你只能判断是否进入 workflow、是否仍可在聊天里完成、是否需要先澄清。
3. 如果聊天里能完成，必须明确返回 chat_fulfillable=true 且 should_enter_workflow=false。
4. 如果需要澄清，必须明确返回 needs_clarification=true，并提供 clarification_question。
5. 只有在应该进入 workflow 时，才返回 should_enter_workflow=true，并提供 candidate_instruction 与 reasons。

只输出 JSON：
{"should_enter_workflow":true,"chat_fulfillable":false,"needs_clarification":false,"clarification_question":"可选","candidate_instruction":"可选","candidate_plan_goal":"可选","missing_materials":["可选"],"confidence":0.0,"reasons":["至少一条理由"]}`

// WorkflowPlannerAgent 负责为 workflow 候选请求做 assistant 侧二次规划。
type WorkflowPlannerAgent struct {
	client      assistantLLMClient
	retryConfig llmclient.Config
	timeout     time.Duration
}

// NewWorkflowPlannerAgent 构造 workflow planner agent。
func NewWorkflowPlannerAgent(
	ctx context.Context,
	baseURL string,
	apiKey string,
	model string,
	cfg llmclient.Config,
) (*WorkflowPlannerAgent, error) {
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

	return newWorkflowPlannerAgentWithClient(&openAIClientAdapter{client: client}, cfg), nil
}

// newWorkflowPlannerAgentWithClient 创建带客户端的 workflow planner agent，并补齐默认依赖。
func newWorkflowPlannerAgentWithClient(client assistantLLMClient, cfg llmclient.Config) *WorkflowPlannerAgent {
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 90000
	}

	return &WorkflowPlannerAgent{
		client: client,
		retryConfig: llmclient.Config{
			TimeoutMS: timeoutMS,
			RetryMax:  cfg.RetryMax,
			BackoffMS: cfg.BackoffMS,
		},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// Plan 基于 runtime state 与 deliberation 候选结果生成 workflow 规划结论。
func (a *WorkflowPlannerAgent) Plan(ctx context.Context, state RuntimeState, decision *DeliberationDecision) (*WorkflowPlanDecision, error) {
	response, err := a.generateWithRetry(ctx, buildWorkflowPlannerMessages(state, decision))
	if err != nil {
		return nil, err
	}

	return decodeWorkflowPlanDecision(response.Content)
}

// generateWithRetry 生成 `Retry`，把模型调用和重试策略收口在接收者内部。
func (a *WorkflowPlannerAgent) generateWithRetry(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
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

// buildWorkflowPlannerMessages 组装 workflow planner 所需的消息序列。
func buildWorkflowPlannerMessages(state RuntimeState, decision *DeliberationDecision) []*schema.Message {
	messages := []*schema.Message{
		{Role: schema.System, Content: buildWorkflowPlannerSystemPrompt(state, decision)},
	}

	messages = append(messages, buildHistoryMessages(state.History)...)
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: strings.TrimSpace(state.Message),
	})

	return messages
}

// buildWorkflowPlannerSystemPrompt 组装 workflow planner 的系统提示词。
func buildWorkflowPlannerSystemPrompt(state RuntimeState, decision *DeliberationDecision) string {
	parts := []string{assistantWorkflowPlannerSystemPrompt}
	if decisionProjection := buildWorkflowPlannerDecisionProjection(decision); decisionProjection != "" {
		parts = append(parts, decisionProjection)
	}
	if runtimeContext := buildRuntimeContext(ChatCompletionInput{
		RuntimeState: state,
		Decision:     decision,
	}); runtimeContext != "" {
		parts = append(parts, runtimeContext)
	}

	return strings.Join(parts, "\n\n")
}

// buildWorkflowPlannerDecisionProjection 组装上游 deliberation 的摘要，避免 planner 脱离主链语境。
func buildWorkflowPlannerDecisionProjection(decision *DeliberationDecision) string {
	if decision == nil {
		return "上游 deliberation 候选：缺失。"
	}

	lines := []string{"上游 deliberation 候选："}
	if decision.RequestKind != "" {
		lines = append(lines, "request_kind="+strings.TrimSpace(decision.RequestKind))
	}
	if decision.ResponseMode != "" {
		lines = append(lines, "response_mode="+strings.TrimSpace(decision.ResponseMode))
	}
	if decision.ChatFulfillable {
		lines = append(lines, "chat_fulfillable=true")
	}
	if decision.WorkflowCommitment {
		lines = append(lines, "workflow_commitment=true")
	}
	if decision.NeedsClarification {
		lines = append(lines, "needs_clarification=true")
	}
	if decision.EvidenceSufficiency != "" {
		lines = append(lines, "evidence_sufficiency="+strings.TrimSpace(decision.EvidenceSufficiency))
	}
	if decision.CandidateTaskInstruction != nil {
		lines = append(lines, "candidate_task_instruction="+strings.TrimSpace(*decision.CandidateTaskInstruction))
	}
	if decision.CandidatePlanGoal != nil {
		lines = append(lines, "candidate_plan_goal="+strings.TrimSpace(*decision.CandidatePlanGoal))
	}
	if len(decision.Reasons) > 0 {
		lines = append(lines, "reasons="+strings.Join(normalizeDecisionReasons(decision.Reasons), "；"))
	}

	return strings.Join(lines, "\n")
}

// decodeWorkflowPlanDecision 解码并校验 workflow planner 的输出。
func decodeWorkflowPlanDecision(raw string) (*WorkflowPlanDecision, error) {
	normalized := trimJSONCodeFence(strings.TrimSpace(raw))
	decoder := json.NewDecoder(bytes.NewBufferString(normalized))
	decoder.DisallowUnknownFields()

	var decision WorkflowPlanDecision
	if err := decoder.Decode(&decision); err != nil {
		return nil, fmt.Errorf("解析 workflow planner 结果失败：%w；原始输出：%s", err, raw)
	}

	return normalizeWorkflowPlanDecision(&decision)
}
