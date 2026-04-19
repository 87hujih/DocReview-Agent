package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"agent_project/apps/server/internal/agent/llmclient"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const (
	recentRawTurnLimit      = 8
	recentRawTurnCharBudget = 1600
)

const assistantSystemPrompt = `你是中文个人助手。

要求：
1. 基于会话历史继续自然对话，回答直接、简洁、有帮助。
2. 不要声称已经创建任务、上传文件或修改资源。
3. 如果提供了资源片段，只能基于片段回答；证据不足时明确说明。`

type chatResponder interface {
	Reply(ctx context.Context, input ChatCompletionInput) (*ChatCompletionResult, error)
	Stream(ctx context.Context, input ChatCompletionInput) (chatStream, error)
}

type assistantLLMClient interface {
	Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
	Stream(ctx context.Context, messages []*schema.Message) (assistantLLMStream, error)
}

type assistantLLMStream interface {
	Close()
	Recv() (*schema.Message, error)
}

type chatStream interface {
	Close() error
	Recv() (string, error)
}

type chatStreamResultProvider interface {
	Result() (*ChatCompletionResult, error)
}

// ChatCompletionInput 描述一次助手回复所需的会话上下文。
type ChatCompletionInput struct {
	RuntimeState             RuntimeState
	Snapshot                 *SessionContextSnapshot
	Citations                []citation.Citation
	GroundedTarget           *ResolvedReference
	CanonicalAnalysisContext string
	History                  []postgres.AssistantMessage
	Message                  string
	Resource                 *resourceContext
	Decision                 *DeliberationDecision
	TaskSuggestionDecision   *TaskSuggestionDecision
}

// ChatCompletionResult 表示模型返回的自然语言回复与可选任务建议。
type ChatCompletionResult struct {
	Reply           string
	TaskInstruction *string
}

// chatResponsePayload 承载聊天响应载荷相关状态，明确助手链路中的数据边界。
type chatResponsePayload struct {
	Reply           string  `json:"reply"`
	TaskInstruction *string `json:"task_instruction"`
}

// ChatResponder 封装 assistant 会话所需的模型客户端。
type ChatResponder struct {
	client      assistantLLMClient
	retryConfig llmclient.Config
	timeout     time.Duration
}

// NewChatResponder 构造助手对话模型客户端。
func NewChatResponder(
	ctx context.Context,
	baseURL string,
	apiKey string,
	model string,
	cfg llmclient.Config,
) (*ChatResponder, error) {
	temperature := float32(0.3)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		// 流式回复可能持续较长时间，不能在 HTTP client 层绑定固定总时长。
		HTTPClient: &http.Client{},
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

	return newChatResponderWithClient(&openAIClientAdapter{client: client}, cfg), nil
}

// newChatResponderWithClient 创建带客户端的聊天Responder，并补齐助手链路需要的默认依赖和缺省行为。
func newChatResponderWithClient(client assistantLLMClient, cfg llmclient.Config) *ChatResponder {
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 90000
	}

	return &ChatResponder{
		client: client,
		retryConfig: llmclient.Config{
			TimeoutMS: timeoutMS,
			RetryMax:  cfg.RetryMax,
			BackoffMS: cfg.BackoffMS,
		},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// Reply 基于历史、当前资源上下文和最新消息生成一轮助手回复。
func (r *ChatResponder) Reply(ctx context.Context, input ChatCompletionInput) (*ChatCompletionResult, error) {
	response, err := r.generateWithRetry(ctx, buildChatMessages(input))
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(response.Content)
	normalized := trimJSONCodeFence(raw)

	var payload chatResponsePayload
	if err := json.Unmarshal([]byte(normalized), &payload); err != nil {
		return nil, fmt.Errorf("解析助手模型结果失败：%w；原始输出：%s", err, raw)
	}

	result := &ChatCompletionResult{
		Reply: strings.TrimSpace(payload.Reply),
	}
	if payload.TaskInstruction != nil {
		trimmed := strings.TrimSpace(*payload.TaskInstruction)
		if trimmed != "" {
			result.TaskInstruction = &trimmed
		}
	}

	return result, nil
}

// Stream 基于历史、资源上下文和最新消息流式返回 reply 文本增量。
func (r *ChatResponder) Stream(ctx context.Context, input ChatCompletionInput) (chatStream, error) {
	stream, err := r.openStreamWithRetry(ctx, buildChatMessages(input))
	if err != nil {
		return nil, err
	}

	return &responseChunkStream{
		extractor: &replyJSONStreamExtractor{},
		stream:    stream,
	}, nil
}

// generateWithRetry 生成 `Retry`，把模型调用和重试策略收口在接收者内部。
func (r *ChatResponder) generateWithRetry(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	var response *schema.Message

	err := llmclient.CallWithRetry(ctx, r.retryConfig, func() error {
		callCtx := ctx
		cancel := func() {}
		if r.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, r.timeout)
		}
		defer cancel()

		var err error
		response, err = r.client.Generate(callCtx, messages)
		return err
	}, nil)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// openStreamWithRetry 从助手打开目标内容，屏蔽底层路径组织细节。
func (r *ChatResponder) openStreamWithRetry(ctx context.Context, messages []*schema.Message) (assistantLLMStream, error) {
	var stream assistantLLMStream

	err := llmclient.CallWithRetry(ctx, llmclient.Config{
		RetryMax:  r.retryConfig.RetryMax,
		BackoffMS: r.retryConfig.BackoffMS,
	}, func() error {
		var err error
		stream, err = r.client.Stream(ctx, messages)
		return err
	}, nil)
	if err != nil {
		return nil, err
	}

	return stream, nil
}

// buildChatMessages 组装 `聊天消息` 序列，统一角色顺序和上下文裁剪规则。
func buildChatMessages(input ChatCompletionInput) []*schema.Message {
	history := chatInputHistory(input)
	message := chatInputMessage(input)

	messages := []*schema.Message{
		{Role: schema.System, Content: buildSystemPrompt(input)},
	}

	messages = append(messages, buildHistoryMessages(history)...)
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: message,
	})

	return messages
}

// buildSystemPrompt 组装 `系统提示词`，统一模型输入文案和约束说明。
func buildSystemPrompt(input ChatCompletionInput) string {
	parts := []string{assistantSystemPrompt, buildAssistantResponseSchemaPrompt()}
	if decisionHint := buildReplyDecisionHint(input.Decision); decisionHint != "" {
		parts = append(parts, decisionHint)
	}
	if runtimeContext := buildRuntimeContext(input); runtimeContext != "" {
		parts = append(parts, runtimeContext)
	}

	return strings.Join(parts, "\n\n")
}

// buildAssistantResponseSchemaPrompt 组装 `助手响应Schema提示词`，统一模型输入文案和约束说明。
func buildAssistantResponseSchemaPrompt() string {
	return "只输出 JSON：\n{\"reply\":\"给用户的回复\"}"
}

// buildReplyDecisionHint 组装 `回复决策提示`，把上层判定作为只读线索传给 responder。
func buildReplyDecisionHint(decision *DeliberationDecision) string {
	if decision == nil {
		return ""
	}

	lines := []string{"上层决策提示："}
	if decision.RequestKind != "" {
		lines = append(lines, "request_kind="+strings.TrimSpace(decision.RequestKind))
	}
	if decision.ResponseMode != "" {
		lines = append(lines, "response_mode="+strings.TrimSpace(decision.ResponseMode))
	}
	if decision.ChatFulfillable {
		lines = append(lines, "chat_fulfillable=true")
	}
	if decision.NeedsClarification {
		lines = append(lines, "needs_clarification=true")
	}
	lines = append(lines, "你只负责生成自然语言 reply，不输出额外动作字段。")

	return strings.Join(lines, "\n")
}

// buildRuntimeContext 组装 `运行时上下文`，为后续助手流程提供可直接消费的上下文。
func buildRuntimeContext(input ChatCompletionInput) string {
	state := runtimeStateFromChatInput(input)
	var sections []string

	if snapshotProjection := buildSnapshotProjection(state.Snapshot); snapshotProjection != "" {
		sections = append(sections, snapshotProjection)
	}
	if rollingSummary := buildRollingSummaryProjection(state.Snapshot); rollingSummary != "" {
		sections = append(sections, rollingSummary)
	}

	if state.ActiveResource != nil {
		sections = append(sections, fmt.Sprintf(
			"本轮检索所用资源：标题=%s；来源=%s；资源ID=%s。",
			state.ActiveResource.Title,
			state.ActiveResource.Source,
			state.ActiveResource.ID,
		))
	}
	if state.GroundedTarget != nil {
		lines := []string{
			fmt.Sprintf("本轮 grounding 目标：section_id=%s；section_type=%s。",
				strings.TrimSpace(state.GroundedTarget.SectionID),
				strings.TrimSpace(state.GroundedTarget.SectionType),
			),
		}
		if entity := strings.TrimSpace(state.GroundedTarget.EntityName); entity != "" {
			lines = append(lines, "目标实体："+entity)
		}
		if reason := strings.TrimSpace(state.GroundedTarget.Reason); reason != "" {
			lines = append(lines, "定位原因："+reason)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if analysisContext := strings.TrimSpace(input.CanonicalAnalysisContext); analysisContext != "" {
		sections = append(sections, analysisContext)
	}

	if len(state.Citations) > 0 {
		lines := make([]string, 0, len(state.Citations)+1)
		lines = append(lines, "与本轮用户问题最相关的资源片段：")
		for index, item := range state.Citations {
			labelParts := []string{fallbackSectionTitle(item.SectionTitle)}
			if strings.TrimSpace(item.SectionType) != "" || strings.TrimSpace(item.SectionID) != "" {
				labelParts = append(labelParts, fmt.Sprintf("section=%s/%s", strings.TrimSpace(item.SectionType), strings.TrimSpace(item.SectionID)))
			}
			if item.Window != nil && strings.TrimSpace(item.Window.GroupID) != "" {
				labelParts = append(labelParts, "window="+strings.TrimSpace(item.Window.GroupID))
			}
			lines = append(lines, fmt.Sprintf(
				"%d. [%s] %s",
				index+1,
				strings.Join(labelParts, "；"),
				strings.TrimSpace(item.Snippet),
			))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	} else if state.ActiveResource != nil {
		sections = append(sections, "当前已有资源，但本轮没有命中直接证据片段；若用户追问资源细节，请明确说明信息不足。")
	}

	return strings.Join(sections, "\n\n")
}

// runtimeStateFromChatInput 把旧回复输入归一化成 runtime state，便于 responder 与 deliberation 共用上下文投影逻辑。
func runtimeStateFromChatInput(input ChatCompletionInput) RuntimeState {
	state := input.RuntimeState
	if strings.TrimSpace(state.Message) == "" {
		state.Message = strings.TrimSpace(input.Message)
	}
	if state.Snapshot == nil {
		state.Snapshot = input.Snapshot
	}
	if state.ActiveResource == nil {
		state.ActiveResource = input.Resource
	}
	if state.GroundedTarget == nil {
		state.GroundedTarget = input.GroundedTarget
	}
	if len(state.Citations) == 0 {
		state.Citations = input.Citations
	}
	if len(state.History) == 0 {
		state.History = input.History
	}

	return state
}

// chatInputHistory 返回 responder 本轮应消费的 recent history，兼容旧输入与 runtime state 输入。
func chatInputHistory(input ChatCompletionInput) []postgres.AssistantMessage {
	if len(input.RuntimeState.History) > 0 {
		return input.RuntimeState.History
	}

	return input.History
}

// chatInputMessage 返回 responder 本轮应消费的当前消息，兼容旧输入与 runtime state 输入。
func chatInputMessage(input ChatCompletionInput) string {
	if strings.TrimSpace(input.RuntimeState.Message) != "" {
		return strings.TrimSpace(input.RuntimeState.Message)
	}

	return strings.TrimSpace(input.Message)
}

// buildSnapshotProjection 组装 `快照Projection`，保持状态投影结果在不同调用点口径一致。
func buildSnapshotProjection(snapshot *SessionContextSnapshot) string {
	if snapshot == nil {
		return ""
	}

	lines := []string{"当前会话快照："}
	if snapshot.ActiveResource != nil {
		lines = append(lines, fmt.Sprintf(
			"- 当前活跃资源：%s（来源=%s，资源ID=%s）。",
			snapshot.ActiveResource.Title,
			snapshot.ActiveResource.SourceType,
			snapshot.ActiveResource.ID,
		))
	} else {
		lines = append(lines, "- 当前活跃资源：未记录。")
	}
	if snapshot.ActiveSection != nil {
		entity := strings.TrimSpace(optionalStringValue(snapshot.ActiveEntityName))
		if entity != "" {
			lines = append(lines, fmt.Sprintf("- 当前 grounding：section=%s（type=%s，entity=%s）。", snapshot.ActiveSection.ID, snapshot.ActiveSection.Type, entity))
		} else {
			lines = append(lines, fmt.Sprintf("- 当前 grounding：section=%s（type=%s）。", snapshot.ActiveSection.ID, snapshot.ActiveSection.Type))
		}
	}

	if snapshot.PendingTaskSuggestion != nil {
		lines = append(lines, fmt.Sprintf(
			"- 待确认任务建议：%s（消息ID=%s）。",
			snapshot.PendingTaskSuggestion.Instruction,
			snapshot.PendingTaskSuggestion.MessageID,
		))
	} else {
		lines = append(lines, "- 待确认任务建议：无。")
	}

	if snapshot.LatestTask != nil {
		lines = append(lines, fmt.Sprintf(
			"- 最近真实任务：ID=%s；状态=%s。",
			snapshot.LatestTask.ID,
			snapshot.LatestTask.Status,
		))
	} else {
		lines = append(lines, "- 最近真实任务：无。")
	}

	if len(snapshot.ConfirmedConstraints) > 0 {
		parts := make([]string, 0, len(snapshot.ConfirmedConstraints))
		for _, constraint := range snapshot.ConfirmedConstraints {
			label := strings.TrimSpace(constraint.Label)
			value := strings.TrimSpace(constraint.Value)
			if label == "" || value == "" {
				continue
			}
			parts = append(parts, label+"="+value)
		}
		if len(parts) > 0 {
			lines = append(lines, "- 已确认约束："+strings.Join(parts, "；"))
		}
	}

	return strings.Join(lines, "\n")
}

// buildRollingSummaryProjection 组装 `滚动摘要Projection`，保持状态投影结果在不同调用点口径一致。
func buildRollingSummaryProjection(snapshot *SessionContextSnapshot) string {
	if snapshot == nil || snapshot.RollingSummary == nil {
		return ""
	}

	summary := strings.TrimSpace(*snapshot.RollingSummary)
	if summary == "" {
		return ""
	}

	return "当前会话滚动摘要：\n" + summary
}

// buildHistoryMessages 组装 `历史消息` 序列，统一角色顺序和上下文裁剪规则。
func buildHistoryMessages(history []postgres.AssistantMessage) []*schema.Message {
	if len(history) == 0 {
		return nil
	}

	selected := make([]*schema.Message, 0, recentRawTurnLimit)
	totalChars := 0
	for index := len(history) - 1; index >= 0; index-- {
		item := history[index]
		if item.Kind != KindText {
			continue
		}

		message := toSchemaMessage(item)
		if message == nil {
			continue
		}
		charCount := utf8.RuneCountInString(strings.TrimSpace(message.Content))
		if len(selected) >= recentRawTurnLimit {
			break
		}
		if len(selected) > 0 && totalChars+charCount > recentRawTurnCharBudget {
			break
		}

		selected = append(selected, message)
		totalChars += charCount
	}
	if len(selected) == 0 {
		return nil
	}

	messages := make([]*schema.Message, 0, len(selected))
	for index := len(selected) - 1; index >= 0; index-- {
		messages = append(messages, selected[index])
	}

	return messages
}

// toSchemaMessage 把 `Schema消息` 转换成助手接口需要的结构，避免上层直接感知内部模型。
func toSchemaMessage(message postgres.AssistantMessage) *schema.Message {
	switch message.Kind {
	case KindText:
		payload, err := unmarshalTextPayload(message.Payload)
		if err != nil {
			return nil
		}

		return &schema.Message{
			Role:    toSchemaRole(message.Role),
			Content: payload.Content,
		}
	case KindSessionFile:
		payload, err := decodeSessionFile(message.Payload)
		if err != nil {
			return nil
		}

		return &schema.Message{
			Role: schema.Assistant,
			Content: fmt.Sprintf(
				"系统记录：用户上传了文件《%s》，已自动入库为资源《%s》（%s）。",
				payload.FileName,
				payload.ResourceTitle,
				payload.SourceType,
			),
		}
	case KindTaskSuggestion:
		payload, err := decodeTaskSuggestion(message.Payload)
		if err != nil {
			return nil
		}

		return &schema.Message{
			Role: schema.Assistant,
			Content: fmt.Sprintf(
				"系统记录：此前建议创建任务，建议指令为“%s”，当前建议状态：%s。",
				payload.Instruction,
				payload.StatusMessage,
			),
		}
	case KindTaskCreated:
		payload, err := unmarshalTaskCreatedPayload(message.Payload)
		if err != nil {
			return nil
		}

		return &schema.Message{
			Role: schema.Assistant,
			Content: fmt.Sprintf(
				"系统记录：此前已经创建任务，任务指令为“%s”，任务状态为 %s。",
				payload.Instruction,
				payload.Status,
			),
		}
	case KindSystem:
		payload, err := unmarshalSystemPayload(message.Payload)
		if err != nil {
			return nil
		}

		return &schema.Message{
			Role:    schema.Assistant,
			Content: "系统提示：" + payload.Content,
		}
	default:
		return nil
	}
}

// toSchemaRole 把 `Schema角色` 转换成助手接口需要的结构，避免上层直接感知内部模型。
func toSchemaRole(role string) schema.RoleType {
	if role == RoleUser {
		return schema.User
	}

	return schema.Assistant
}

// fallbackSectionTitle 为缺失标题的引用 section 生成可读兜底标题，避免前端出现空标题。
func fallbackSectionTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return "未命名片段"
	}

	return trimmed
}

// trimJSONCodeFence 去掉 JSON 结果外层的代码围栏，避免后续解码被 Markdown 包裹干扰。
func trimJSONCodeFence(content string) string {
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

// unmarshalTextPayload 解码 `文本载荷` 载荷，避免上层直接处理原始 JSON。
func unmarshalTextPayload(payload []byte) (TextPayload, error) {
	var value TextPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TextPayload{}, err
	}

	return value, nil
}

// unmarshalTaskCreatedPayload 解码 `任务Created载荷` 载荷，避免上层直接处理原始 JSON。
func unmarshalTaskCreatedPayload(payload []byte) (TaskCreatedPayload, error) {
	var value TaskCreatedPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TaskCreatedPayload{}, err
	}

	return value, nil
}

// unmarshalSystemPayload 解码 `系统载荷` 载荷，避免上层直接处理原始 JSON。
func unmarshalSystemPayload(payload []byte) (SystemPayload, error) {
	var value SystemPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return SystemPayload{}, err
	}

	return value, nil
}

// responseChunkStream 承载响应chunk流式消息相关状态，明确助手链路中的数据边界。
type responseChunkStream struct {
	done         bool
	extractor    *replyJSONStreamExtractor
	result       *ChatCompletionResult
	resultErr    error
	resultLoaded bool
	raw          strings.Builder
	stream       assistantLLMStream
}

// Recv 从流式结果中读取下一条增量消息，保持消费协议封装在接收者内部。
func (s *responseChunkStream) Recv() (string, error) {
	for {
		message, err := s.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.done = true
				return "", io.EOF
			}

			return "", err
		}

		if message == nil || message.Content == "" {
			continue
		}

		s.raw.WriteString(message.Content)

		delta, extractErr := s.extractor.Feed(message.Content)
		if extractErr != nil {
			return "", extractErr
		}

		if delta == "" {
			continue
		}

		return delta, nil
	}
}

// Close 关闭接收者持有的流或资源，释放后续读取不再需要的底层句柄。
func (s *responseChunkStream) Close() error {
	s.stream.Close()
	return nil
}

// Result 返回当前累计结果，供调用方在流式处理结束后统一取用。
func (s *responseChunkStream) Result() (*ChatCompletionResult, error) {
	if s.resultLoaded {
		return s.result, s.resultErr
	}

	s.resultLoaded = true

	replyText := strings.TrimSpace(s.extractor.Text())
	raw := strings.TrimSpace(s.raw.String())
	if raw == "" {
		s.result = &ChatCompletionResult{Reply: replyText}
		return s.result, nil
	}

	var payload chatResponsePayload
	if err := json.Unmarshal([]byte(trimJSONCodeFence(raw)), &payload); err != nil {
		if replyText != "" {
			s.result = &ChatCompletionResult{Reply: replyText}
			return s.result, nil
		}

		s.resultErr = fmt.Errorf("解析助手流式结果失败：%w", err)
		return nil, s.resultErr
	}

	s.result = &ChatCompletionResult{
		Reply: strings.TrimSpace(payload.Reply),
	}
	if payload.TaskInstruction != nil {
		trimmed := strings.TrimSpace(*payload.TaskInstruction)
		if trimmed != "" {
			s.result.TaskInstruction = &trimmed
		}
	}

	return s.result, nil
}

// replyJSONStreamExtractor 承载replyJSON流式消息Extractor相关状态，明确助手链路中的数据边界。
type replyJSONStreamExtractor struct {
	keyBuffer            strings.Builder
	replyBuffer          strings.Builder
	state                replyJSONStreamState
	unicodeBuffer        strings.Builder
	pendingHighSurrogate rune
}

type replyJSONStreamState int

const (
	replyJSONStreamStateSeekString replyJSONStreamState = iota
	replyJSONStreamStateReadString
	replyJSONStreamStateReadStringEscape
	replyJSONStreamStateAfterReplyKey
	replyJSONStreamStateBeforeReplyValue
	replyJSONStreamStateInReply
	replyJSONStreamStateInReplyEscape
	replyJSONStreamStateInReplyUnicode
	replyJSONStreamStateInReplyUnicodeLowEscape
	replyJSONStreamStateInReplyUnicodeLowMarker
	replyJSONStreamStateInReplyUnicodeLow
	replyJSONStreamStateDone
)

// Feed 向接收者写入新的增量片段，推进当前解析状态。
func (e *replyJSONStreamExtractor) Feed(chunk string) (string, error) {
	if e.state == replyJSONStreamStateDone {
		return "", nil
	}

	var output strings.Builder
	for _, r := range chunk {
		switch e.state {
		case replyJSONStreamStateSeekString:
			if r == '"' {
				e.keyBuffer.Reset()
				e.state = replyJSONStreamStateReadString
			}
		case replyJSONStreamStateReadString:
			switch r {
			case '\\':
				e.state = replyJSONStreamStateReadStringEscape
			case '"':
				if e.keyBuffer.String() == "reply" {
					e.state = replyJSONStreamStateAfterReplyKey
					continue
				}
				e.state = replyJSONStreamStateSeekString
			default:
				e.keyBuffer.WriteRune(r)
			}
		case replyJSONStreamStateReadStringEscape:
			e.keyBuffer.WriteRune(r)
			e.state = replyJSONStreamStateReadString
		case replyJSONStreamStateAfterReplyKey:
			if isJSONWhitespace(r) {
				continue
			}
			if r == ':' {
				e.state = replyJSONStreamStateBeforeReplyValue
				continue
			}
			e.state = replyJSONStreamStateSeekString
		case replyJSONStreamStateBeforeReplyValue:
			if isJSONWhitespace(r) {
				continue
			}
			if r == '"' {
				e.state = replyJSONStreamStateInReply
				continue
			}
			e.state = replyJSONStreamStateSeekString
		case replyJSONStreamStateInReply:
			switch r {
			case '\\':
				e.state = replyJSONStreamStateInReplyEscape
			case '"':
				e.state = replyJSONStreamStateDone
			default:
				e.writeReplyRune(&output, r)
			}
		case replyJSONStreamStateInReplyEscape:
			if r == 'u' {
				e.unicodeBuffer.Reset()
				e.state = replyJSONStreamStateInReplyUnicode
				continue
			}

			decoded, err := decodeJSONStringEscape(r)
			if err != nil {
				return "", err
			}
			e.writeReplyRune(&output, decoded)
			e.state = replyJSONStreamStateInReply
		case replyJSONStreamStateInReplyUnicode:
			if !isHexDigit(r) {
				return "", fmt.Errorf("解析助手流式结果失败：非法 unicode 转义 %q", string(r))
			}

			e.unicodeBuffer.WriteRune(r)
			if e.unicodeBuffer.Len() < 4 {
				continue
			}

			decoded, err := parseJSONUnicodeEscape(e.unicodeBuffer.String())
			if err != nil {
				return "", err
			}

			switch {
			case isHighSurrogate(decoded):
				e.pendingHighSurrogate = decoded
				e.unicodeBuffer.Reset()
				e.state = replyJSONStreamStateInReplyUnicodeLowEscape
			case isLowSurrogate(decoded):
				return "", fmt.Errorf("解析助手流式结果失败：非法 Unicode 代理项组合")
			default:
				e.writeReplyRune(&output, decoded)
				e.state = replyJSONStreamStateInReply
			}
		case replyJSONStreamStateInReplyUnicodeLowEscape:
			if r != '\\' {
				return "", fmt.Errorf("解析助手流式结果失败：非法 Unicode 代理项组合")
			}
			e.state = replyJSONStreamStateInReplyUnicodeLowMarker
		case replyJSONStreamStateInReplyUnicodeLowMarker:
			if r != 'u' {
				return "", fmt.Errorf("解析助手流式结果失败：非法 Unicode 代理项组合")
			}
			e.unicodeBuffer.Reset()
			e.state = replyJSONStreamStateInReplyUnicodeLow
		case replyJSONStreamStateInReplyUnicodeLow:
			if !isHexDigit(r) {
				return "", fmt.Errorf("解析助手流式结果失败：非法 unicode 转义 %q", string(r))
			}

			e.unicodeBuffer.WriteRune(r)
			if e.unicodeBuffer.Len() < 4 {
				continue
			}

			low, err := parseJSONUnicodeEscape(e.unicodeBuffer.String())
			if err != nil {
				return "", err
			}
			if !isLowSurrogate(low) {
				return "", fmt.Errorf("解析助手流式结果失败：非法 Unicode 代理项组合")
			}

			combined := utf16.DecodeRune(e.pendingHighSurrogate, low)
			e.pendingHighSurrogate = 0
			e.unicodeBuffer.Reset()
			e.writeReplyRune(&output, combined)
			e.state = replyJSONStreamStateInReply
		}
	}

	return output.String(), nil
}

// Text 返回接收者当前缓冲的纯文本结果，供上层直接消费。
func (e *replyJSONStreamExtractor) Text() string {
	return e.replyBuffer.String()
}

// writeReplyRune 把 `ReplyRune` 写入接收者管理的目标位置，统一输出边界。
func (e *replyJSONStreamExtractor) writeReplyRune(output *strings.Builder, r rune) {
	output.WriteRune(r)
	e.replyBuffer.WriteRune(r)
}

// parseJSONUnicodeEscape 解析 `JSONUnicodeEscape`，把格式和参数错误收口到助手边界。
func parseJSONUnicodeEscape(buffer string) (rune, error) {
	value, err := strconv.ParseInt(buffer, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("解析助手流式结果失败：%w", err)
	}

	return rune(value), nil
}

// isHighSurrogate 判断 rune 是否为 UTF-16 高位代理项，供流式 JSON 解码处理代理对。
func isHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// isLowSurrogate 判断 rune 是否为 UTF-16 低位代理项，供流式 JSON 解码处理代理对。
func isLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// decodeJSONStringEscape 解码 `JSONStringEscape` 载荷，避免上层直接处理原始 JSON。
func decodeJSONStringEscape(r rune) (rune, error) {
	switch r {
	case '"', '\\', '/':
		return r, nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	default:
		return 0, fmt.Errorf("解析助手流式结果失败：非法转义字符 %q", string(r))
	}
}

// isHexDigit 判断字节是否是十六进制字符，供 Unicode 转义解析复用。
func isHexDigit(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case r >= 'a' && r <= 'f':
		return true
	case r >= 'A' && r <= 'F':
		return true
	default:
		return false
	}
}

// isJSONWhitespace 判断字节是否属于 JSON 允许的空白字符，供流式提取器跳过无效间隔。
func isJSONWhitespace(r rune) bool {
	switch r {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}

// openAIClientAdapter 承载openAI客户端Adapter相关状态，明确助手链路中的数据边界。
type openAIClientAdapter struct {
	client *openaiacl.Client
}

// Generate 生成 `Generate`，把模型调用和重试策略收口在接收者内部。
func (c *openAIClientAdapter) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	return c.client.Generate(ctx, messages)
}

// Stream 启动 `流式消息` 的流式处理，统一增量输出协议。
func (c *openAIClientAdapter) Stream(ctx context.Context, messages []*schema.Message) (assistantLLMStream, error) {
	stream, err := c.client.Stream(ctx, messages)
	if err != nil {
		return nil, err
	}

	return &schemaMessageStream{stream: stream}, nil
}

// schemaMessageStream 承载schema消息流式消息相关状态，明确助手链路中的数据边界。
type schemaMessageStream struct {
	stream *schema.StreamReader[*schema.Message]
}

// Recv 从流式结果中读取下一条增量消息，保持消费协议封装在接收者内部。
func (s *schemaMessageStream) Recv() (*schema.Message, error) {
	return s.stream.Recv()
}

// Close 关闭接收者持有的流或资源，释放后续读取不再需要的底层句柄。
func (s *schemaMessageStream) Close() {
	s.stream.Close()
}
