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

const assistantWorkflowVerifierSystemPrompt = `你是 assistant runtime 的 workflow critic / verifier。

职责：
1. 你不是 planner，不要重新做完整规划，也不要假设任何数据库、任务或审批副作用。
2. 你只检查当前 promotion 是否过度，判断这次 workflow promotion 应该被放行、降回 chat，还是先澄清。
3. 你优先寻找更低打扰的替代路径；如果聊天里能完成，就不要放行 workflow。
4. 只有在当前 workflow promotion 合理时，才返回 approve_workflow=true。
5. 如果候选 instruction 过度推断，可以通过 revised_instruction 收紧；若不需要改写则留空。

只输出 JSON：
{"approve_workflow":false,"downgrade_to_chat":true,"needs_clarification":false,"clarification_question":"可选","revised_instruction":"可选","confidence":0.0,"reasons":["至少一条理由"]}`

// WorkflowVerifierAgent 负责为 workflow promotion 做 assistant 侧反对式复核。
type WorkflowVerifierAgent struct {
	client      assistantLLMClient
	retryConfig llmclient.Config
	timeout     time.Duration
}

// NewWorkflowVerifierAgent 构造 workflow verifier agent。
func NewWorkflowVerifierAgent(
	ctx context.Context,
	baseURL string,
	apiKey string,
	model string,
	cfg llmclient.Config,
) (*WorkflowVerifierAgent, error) {
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

	return newWorkflowVerifierAgentWithClient(&openAIClientAdapter{client: client}, cfg), nil
}

// newWorkflowVerifierAgentWithClient 创建带客户端的 workflow verifier agent，并补齐默认依赖。
func newWorkflowVerifierAgentWithClient(client assistantLLMClient, cfg llmclient.Config) *WorkflowVerifierAgent {
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 90000
	}

	return &WorkflowVerifierAgent{
		client: client,
		retryConfig: llmclient.Config{
			TimeoutMS: timeoutMS,
			RetryMax:  cfg.RetryMax,
			BackoffMS: cfg.BackoffMS,
		},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// Verify 基于 runtime state、deliberation 与 planner 结果生成 verifier 结论。
func (a *WorkflowVerifierAgent) Verify(
	ctx context.Context,
	state RuntimeState,
	decision *DeliberationDecision,
	plan *WorkflowPlanDecision,
) (*WorkflowVerificationDecision, error) {
	response, err := a.generateWithRetry(ctx, buildWorkflowVerifierMessages(state, decision, plan))
	if err != nil {
		return nil, err
	}

	return decodeWorkflowVerificationDecision(response.Content)
}

// generateWithRetry 生成 `Retry`，把模型调用和重试策略收口在接收者内部。
func (a *WorkflowVerifierAgent) generateWithRetry(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
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

// buildWorkflowVerifierMessages 组装 workflow verifier 所需的消息序列。
func buildWorkflowVerifierMessages(state RuntimeState, decision *DeliberationDecision, plan *WorkflowPlanDecision) []*schema.Message {
	messages := []*schema.Message{
		{Role: schema.System, Content: buildWorkflowVerifierSystemPrompt(state, decision, plan)},
	}

	messages = append(messages, buildHistoryMessages(state.History)...)
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: strings.TrimSpace(state.Message),
	})

	return messages
}

// buildWorkflowVerifierSystemPrompt 组装 workflow verifier 的系统提示词。
func buildWorkflowVerifierSystemPrompt(state RuntimeState, decision *DeliberationDecision, plan *WorkflowPlanDecision) string {
	parts := []string{assistantWorkflowVerifierSystemPrompt}
	if decisionProjection := buildWorkflowPlannerDecisionProjection(decision); decisionProjection != "" {
		parts = append(parts, decisionProjection)
	}
	if planProjection := buildWorkflowVerifierPlanProjection(plan); planProjection != "" {
		parts = append(parts, planProjection)
	}
	if runtimeContext := buildRuntimeContext(ChatCompletionInput{
		RuntimeState: state,
		Decision:     decision,
	}); runtimeContext != "" {
		parts = append(parts, runtimeContext)
	}

	return strings.Join(parts, "\n\n")
}

// buildWorkflowVerifierPlanProjection 组装当前 planner 结果的摘要，避免 verifier 脱离 promotion 语境。
func buildWorkflowVerifierPlanProjection(plan *WorkflowPlanDecision) string {
	if plan == nil {
		return "当前 planner 结论：缺失。"
	}

	lines := []string{"当前 planner 结论："}
	if plan.ShouldEnterWorkflow {
		lines = append(lines, "should_enter_workflow=true")
	}
	if plan.ChatFulfillable {
		lines = append(lines, "chat_fulfillable=true")
	}
	if plan.NeedsClarification {
		lines = append(lines, "needs_clarification=true")
	}
	if plan.ClarificationQuestion != nil {
		lines = append(lines, "clarification_question="+strings.TrimSpace(*plan.ClarificationQuestion))
	}
	if plan.CandidateInstruction != nil {
		lines = append(lines, "candidate_instruction="+strings.TrimSpace(*plan.CandidateInstruction))
	}
	if plan.CandidatePlanGoal != nil {
		lines = append(lines, "candidate_plan_goal="+strings.TrimSpace(*plan.CandidatePlanGoal))
	}
	if len(plan.MissingMaterials) > 0 {
		lines = append(lines, "missing_materials="+strings.Join(normalizeWorkflowPlanMissingMaterials(plan.MissingMaterials), "；"))
	}
	if len(plan.Reasons) > 0 {
		lines = append(lines, "reasons="+strings.Join(normalizeDecisionReasons(plan.Reasons), "；"))
	}

	return strings.Join(lines, "\n")
}

// decodeWorkflowVerificationDecision 解码并校验 workflow verifier 的输出。
func decodeWorkflowVerificationDecision(raw string) (*WorkflowVerificationDecision, error) {
	normalized := trimJSONCodeFence(strings.TrimSpace(raw))
	decoder := json.NewDecoder(bytes.NewBufferString(normalized))
	decoder.DisallowUnknownFields()

	var decision WorkflowVerificationDecision
	if err := decoder.Decode(&decision); err != nil {
		return nil, fmt.Errorf("解析 workflow verifier 结果失败：%w；原始输出：%s", err, raw)
	}

	return normalizeWorkflowVerificationDecision(&decision)
}
