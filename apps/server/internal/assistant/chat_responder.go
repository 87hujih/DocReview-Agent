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

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

const assistantSystemPrompt = `你是中文个人助手。

要求：
1. 基于会话历史继续自然对话，回答直接、简洁、有帮助。
2. 不要声称已经创建任务、上传文件或修改资源。
3. 如果提供了资源片段，只能基于片段回答；证据不足时明确说明。

只输出 JSON：
{"reply":"给用户的回复"}`

type chatResponder interface {
	Reply(ctx context.Context, input ChatCompletionInput) (*ChatCompletionResult, error)
	Stream(ctx context.Context, input ChatCompletionInput) (chatStream, error)
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
	Citations []citation.Citation
	History   []postgres.AssistantMessage
	Message   string
	Resource  *resourceContext
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
	client *openaiacl.Client
}

// NewChatResponder 构造助手对话模型客户端。
func NewChatResponder(ctx context.Context, baseURL string, apiKey string, model string) (*ChatResponder, error) {
	temperature := float32(0.3)
	config := &openaiacl.Config{
		APIKey:      apiKey,
		Model:       model,
		Temperature: &temperature,
		HTTPClient:  &http.Client{Timeout: 120 * time.Second},
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

	return &ChatResponder{client: client}, nil
}

// Reply 基于历史、当前资源上下文和最新消息生成一轮助手回复。
func (r *ChatResponder) Reply(ctx context.Context, input ChatCompletionInput) (*ChatCompletionResult, error) {
	response, err := r.client.Generate(ctx, buildChatMessages(input))
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
	stream, err := r.client.Stream(ctx, buildChatMessages(input))
	if err != nil {
		return nil, err
	}

	return &responseChunkStream{
		extractor: &replyJSONStreamExtractor{},
		stream:    stream,
	}, nil
}

func buildChatMessages(input ChatCompletionInput) []*schema.Message {
	messages := []*schema.Message{
		{Role: schema.System, Content: assistantSystemPrompt},
	}

	if runtimeContext := buildRuntimeContext(input); runtimeContext != "" {
		messages = append(messages, &schema.Message{
			Role:    schema.System,
			Content: runtimeContext,
		})
	}

	messages = append(messages, buildHistoryMessages(input.History)...)
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: strings.TrimSpace(input.Message),
	})

	return messages
}

func buildRuntimeContext(input ChatCompletionInput) string {
	var sections []string

	if input.Resource != nil {
		sections = append(sections, fmt.Sprintf(
			"当前最近可用资源：标题=%s；来源=%s；资源ID=%s。",
			input.Resource.Title,
			input.Resource.Source,
			input.Resource.ID,
		))
	}

	if len(input.Citations) > 0 {
		lines := make([]string, 0, len(input.Citations)+1)
		lines = append(lines, "与本轮用户问题最相关的资源片段：")
		for index, item := range input.Citations {
			lines = append(lines, fmt.Sprintf(
				"%d. [%s] %s",
				index+1,
				fallbackSectionTitle(item.SectionTitle),
				strings.TrimSpace(item.Snippet),
			))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	} else if input.Resource != nil {
		sections = append(sections, "当前已有资源，但本轮没有命中直接证据片段；若用户追问资源细节，请明确说明信息不足。")
	}

	return strings.Join(sections, "\n\n")
}

func buildHistoryMessages(history []postgres.AssistantMessage) []*schema.Message {
	if len(history) == 0 {
		return nil
	}

	start := 0
	if len(history) > 16 {
		start = len(history) - 16
	}

	messages := make([]*schema.Message, 0, len(history)-start)
	for _, item := range history[start:] {
		message := toSchemaMessage(item)
		if message == nil {
			continue
		}

		messages = append(messages, message)
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
	stream       *schema.StreamReader[*schema.Message]
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
	keyBuffer     strings.Builder
	replyBuffer   strings.Builder
	state         replyJSONStreamState
	unicodeBuffer strings.Builder
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
				output.WriteRune(r)
				e.replyBuffer.WriteRune(r)
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
			output.WriteRune(decoded)
			e.replyBuffer.WriteRune(decoded)
			e.state = replyJSONStreamStateInReply
		case replyJSONStreamStateInReplyUnicode:
			if !isHexDigit(r) {
				return "", fmt.Errorf("解析助手流式结果失败：非法 unicode 转义 %q", string(r))
			}

			e.unicodeBuffer.WriteRune(r)
			if e.unicodeBuffer.Len() < 4 {
				continue
			}

			value, err := strconv.ParseInt(e.unicodeBuffer.String(), 16, 32)
			if err != nil {
				return "", fmt.Errorf("解析助手流式结果失败：%w", err)
			}

			decoded := rune(value)
			output.WriteRune(decoded)
			e.replyBuffer.WriteRune(decoded)
			e.state = replyJSONStreamStateInReply
		}
	}

	return output.String(), nil
}

func (e *replyJSONStreamExtractor) Text() string {
	return e.replyBuffer.String()
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
