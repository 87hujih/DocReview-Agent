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
	Snapshot               *SessionContextSnapshot
	Citations              []citation.Citation
	GroundedTarget         *ResolvedReference
	GroundedSection        *GroundedAnalysisInput
	History                []postgres.AssistantMessage
	Message                string
	Resource               *resourceContext
	TaskSuggestionDecision *TaskSuggestionDecision
}

// ChatCompletionResult 表示模型返回的自然语言回复与可选任务建议。
type ChatCompletionResult struct {
	Reply           string
	TaskInstruction *string
}

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

func buildChatMessages(input ChatCompletionInput) []*schema.Message {
	messages := []*schema.Message{
		{Role: schema.System, Content: buildSystemPrompt(input)},
	}

	messages = append(messages, buildHistoryMessages(input.History)...)
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: strings.TrimSpace(input.Message),
	})

	return messages
}

func buildSystemPrompt(input ChatCompletionInput) string {
	parts := []string{assistantSystemPrompt, buildAssistantResponseSchemaPrompt(input.TaskSuggestionDecision)}
	if runtimeContext := buildRuntimeContext(input); runtimeContext != "" {
		parts = append(parts, runtimeContext)
	}

	return strings.Join(parts, "\n\n")
}

func buildAssistantResponseSchemaPrompt(decision *TaskSuggestionDecision) string {
	if decision != nil && decision.ReadinessState == ReadinessStateReadyForTask {
		return "只输出 JSON：\n{\"reply\":\"给用户的回复\",\"task_instruction\":\"仅当用户要求立即开始执行且材料已明确时才填写，可选\"}"
	}

	return "只输出 JSON：\n{\"reply\":\"给用户的回复\"}"
}

func buildRuntimeContext(input ChatCompletionInput) string {
	var sections []string

	if snapshotProjection := buildSnapshotProjection(input.Snapshot); snapshotProjection != "" {
		sections = append(sections, snapshotProjection)
	}
	if rollingSummary := buildRollingSummaryProjection(input.Snapshot); rollingSummary != "" {
		sections = append(sections, rollingSummary)
	}

	if input.Resource != nil {
		sections = append(sections, fmt.Sprintf(
			"本轮检索所用资源：标题=%s；来源=%s；资源ID=%s。",
			input.Resource.Title,
			input.Resource.Source,
			input.Resource.ID,
		))
	}
	if input.GroundedTarget != nil {
		lines := []string{
			fmt.Sprintf("本轮 grounding 目标：section_id=%s；section_type=%s。",
				strings.TrimSpace(input.GroundedTarget.SectionID),
				strings.TrimSpace(input.GroundedTarget.SectionType),
			),
		}
		if entity := strings.TrimSpace(input.GroundedTarget.EntityName); entity != "" {
			lines = append(lines, "目标实体："+entity)
		}
		if reason := strings.TrimSpace(input.GroundedTarget.Reason); reason != "" {
			lines = append(lines, "定位原因："+reason)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if input.GroundedSection != nil {
		lines := []string{
			"当前需要分析的 section 原文：",
			fmt.Sprintf(
				"- section_id=%s；section_type=%s；section_order=%d；section_title=%s。",
				strings.TrimSpace(input.GroundedSection.SectionID),
				strings.TrimSpace(input.GroundedSection.SectionType),
				input.GroundedSection.SectionOrder,
				strings.TrimSpace(input.GroundedSection.SectionTitle),
			),
		}
		if instruction := strings.TrimSpace(input.GroundedSection.UserInstruction); instruction != "" {
			lines = append(lines, "- 用户要求："+instruction)
		}
		lines = append(lines, strings.TrimSpace(input.GroundedSection.SectionText))
		sections = append(sections, strings.Join(lines, "\n"))
	}

	if len(input.Citations) > 0 {
		lines := make([]string, 0, len(input.Citations)+1)
		lines = append(lines, "与本轮用户问题最相关的资源片段：")
		for index, item := range input.Citations {
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
	} else if input.Resource != nil {
		sections = append(sections, "当前已有资源，但本轮没有命中直接证据片段；若用户追问资源细节，请明确说明信息不足。")
	}

	return strings.Join(sections, "\n\n")
}

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

func toSchemaRole(role string) schema.RoleType {
	if role == RoleUser {
		return schema.User
	}

	return schema.Assistant
}

func fallbackSectionTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return "未命名片段"
	}

	return trimmed
}

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

func unmarshalTextPayload(payload []byte) (TextPayload, error) {
	var value TextPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TextPayload{}, err
	}

	return value, nil
}

func unmarshalTaskCreatedPayload(payload []byte) (TaskCreatedPayload, error) {
	var value TaskCreatedPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TaskCreatedPayload{}, err
	}

	return value, nil
}

func unmarshalSystemPayload(payload []byte) (SystemPayload, error) {
	var value SystemPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return SystemPayload{}, err
	}

	return value, nil
}

type responseChunkStream struct {
	done         bool
	extractor    *replyJSONStreamExtractor
	result       *ChatCompletionResult
	resultErr    error
	resultLoaded bool
	raw          strings.Builder
	stream       assistantLLMStream
}

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

func (s *responseChunkStream) Close() error {
	s.stream.Close()
	return nil
}

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

func (e *replyJSONStreamExtractor) Text() string {
	return e.replyBuffer.String()
}

func (e *replyJSONStreamExtractor) writeReplyRune(output *strings.Builder, r rune) {
	output.WriteRune(r)
	e.replyBuffer.WriteRune(r)
}

func parseJSONUnicodeEscape(buffer string) (rune, error) {
	value, err := strconv.ParseInt(buffer, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("解析助手流式结果失败：%w", err)
	}

	return rune(value), nil
}

func isHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func isLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

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

func isJSONWhitespace(r rune) bool {
	switch r {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}

type openAIClientAdapter struct {
	client *openaiacl.Client
}

func (c *openAIClientAdapter) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	return c.client.Generate(ctx, messages)
}

func (c *openAIClientAdapter) Stream(ctx context.Context, messages []*schema.Message) (assistantLLMStream, error) {
	stream, err := c.client.Stream(ctx, messages)
	if err != nil {
		return nil, err
	}

	return &schemaMessageStream{stream: stream}, nil
}

type schemaMessageStream struct {
	stream *schema.StreamReader[*schema.Message]
}

func (s *schemaMessageStream) Recv() (*schema.Message, error) {
	return s.stream.Recv()
}

func (s *schemaMessageStream) Close() {
	s.stream.Close()
}
