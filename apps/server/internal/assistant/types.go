package assistant

import (
	"context"
	"errors"

	"agent_project/apps/server/internal/storage/postgres"
)

const (
	RoleAssistant = "assistant"
	RoleUser      = "user"

	KindSessionFile    = "session_file"
	KindSystem         = "system"
	KindTaskCreated    = "task_created"
	KindTaskStatus     = "task_status"
	KindTaskSuggestion = "task_suggestion"
	KindText           = "text"
)

const (
	StreamEventSessionCreated   = "session_created"
	StreamEventSessionFile      = "session_file"
	StreamEventMessageStarted   = "message_started"
	StreamEventMessageDelta     = "message_delta"
	StreamEventMessageCompleted = "message_completed"
	StreamEventTaskSuggestion   = "task_suggestion"
	StreamEventDone             = "done"
	StreamEventError            = "error"
)

const (
	StreamErrorCodeTimeout      = "assistant_timeout"
	StreamErrorCodeStreamFailed = "assistant_stream_failed"
	StreamErrorCodeEmptyReply   = "assistant_empty_reply"
	StreamErrorCodeInternal     = "assistant_internal_error"
)

const (
	RuntimeEventTypeDeliberationDecided       = "deliberation.decided"
	RuntimeEventTypePolicyApplied             = "policy.applied"
	RuntimeEventTypePlannerUsed               = "planner.used"
	RuntimeEventTypeVerifierUsed              = "verifier.used"
	RuntimeEventTypeClarificationPrompted     = "clarification.prompted"
	RuntimeEventTypeClarificationResolvedChat = "clarification.resolved_to_chat"
	RuntimeEventTypeClarificationResolvedFlow = "clarification.resolved_to_workflow"
	RuntimeEventTypeTaskSuggestionCreated     = "task_suggestion.created"
	RuntimeEventTypeTaskSuggestionConfirmed   = "task_suggestion.confirmed"
	RuntimeEventTypeTaskSuggestionIgnored     = "task_suggestion.ignored"
	RuntimeEventTypeUserCorrected             = "user.corrected"
	RuntimeEventTypeWorkflowPromoted          = "workflow.promoted"
	RuntimeEventTypeWorkflowDowngraded        = "workflow.downgraded"
)

const (
	RuntimeFinalOutcomeTaskSuggestionCreated   = "task_suggestion_created"
	RuntimeFinalOutcomeTaskSuggestionConfirmed = "task_suggestion_confirmed"
	RuntimeFinalOutcomeTaskSuggestionIgnored   = "task_suggestion_ignored"
	RuntimeFinalOutcomeClarificationToChat     = "clarification_resolved_to_chat"
	RuntimeFinalOutcomeClarificationToFlow     = "clarification_resolved_to_workflow"
	RuntimeFinalOutcomeWorkflowDowngraded      = "workflow_downgraded"
	RuntimeFinalOutcomeUserCorrected           = "user_corrected"
)

const (
	RuntimeCorrectionReasonNotThisIntent       = "not_this_intent"
	RuntimeCorrectionReasonDeclineTaskCreation = "decline_task_creation"
	RuntimeCorrectionReasonReadbackOnly        = "readback_only"
)

// ConversationResult 表示创建会话、加载会话后的标准结果。
type ConversationResult struct {
	Session  postgres.AssistantSession
	Messages []postgres.AssistantMessage
}

// StreamEvent 表示 assistant 流式回复阶段发送给上层的事件。
type StreamEvent struct {
	Type    string                     `json:"type"`
	Delta   string                     `json:"delta,omitempty"`
	Message *postgres.AssistantMessage `json:"message,omitempty"`
	Session *postgres.AssistantSession `json:"session,omitempty"`
}

// StreamError 表示流式回复阶段对外暴露的结构化错误。
type StreamError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

// WorkflowVerificationDecision 表示 verifier 对 workflow promotion 的最终复核结论。
type WorkflowVerificationDecision struct {
	ApproveWorkflow       bool     `json:"approve_workflow"`
	DowngradeToChat       bool     `json:"downgrade_to_chat"`
	NeedsClarification    bool     `json:"needs_clarification"`
	ClarificationQuestion *string  `json:"clarification_question,omitempty"`
	RevisedInstruction    *string  `json:"revised_instruction,omitempty"`
	Confidence            float64  `json:"confidence"`
	Reasons               []string `json:"reasons"`
}

// Error 返回接收者封装后的错误文本，便于上层直接对外暴露。
func (e *StreamError) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}

// Unwrap 返回接收者内部持有的底层错误，支持上层使用 errors.Is 或 errors.As。
func (e *StreamError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.cause
}

// NewStreamError 构造一个带错误码的流式错误。
func NewStreamError(code string, message string, cause error) *StreamError {
	return &StreamError{
		Code:    code,
		Message: message,
		cause:   cause,
	}
}

// NormalizeStreamError 把任意错误归一成可直接返回给前端的流式错误。
func NormalizeStreamError(err error) *StreamError {
	if err == nil {
		return nil
	}

	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		return streamErr
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return NewStreamError(StreamErrorCodeTimeout, "助手响应超时，请重试。", err)
	}

	return NewStreamError(StreamErrorCodeInternal, "助手暂时不可用，请稍后重试。", err)
}

// UploadFileResult 表示文件上传并写入会话后的结果。
type UploadFileResult struct {
	Session      postgres.AssistantSession
	Resource     *postgres.Resource
	Messages     []postgres.AssistantMessage
	ErrorMessage *string
}

// ConfirmTaskResult 表示任务建议确认后的结果。
type ConfirmTaskResult struct {
	Session      postgres.AssistantSession
	Task         *postgres.Task
	Messages     []postgres.AssistantMessage
	ErrorMessage *string
}

// ImportDocumentInput 描述导入一份会话文件所需的输入。
type ImportDocumentInput struct {
	FileName      string
	Content       []byte
	SourceType    string
	SourceRef     *string
	VersionSource string
}

// ImportDocumentResult 描述导入资源库后的结果。
type ImportDocumentResult struct {
	Resource *postgres.Resource
	Version  *postgres.ResourceVersion
}

// TextPayload 表示普通文本消息内容。
type TextPayload struct {
	Content string `json:"content"`
}

// TaskSuggestionPayload 表示任务建议卡片的结构化内容。
type TaskSuggestionPayload struct {
	ActionLabel   string  `json:"action_label"`
	CanCreate     bool    `json:"can_create"`
	Instruction   string  `json:"instruction"`
	ResourceID    *string `json:"resource_id,omitempty"`
	ResourceLabel string  `json:"resource_label"`
	StatusMessage string  `json:"status_message"`
	Title         string  `json:"title"`
}

// TaskCreatedPayload 表示任务已创建后的结构化消息。
type TaskCreatedPayload struct {
	DetailURL           string `json:"detail_url"`
	Instruction         string `json:"instruction"`
	ResourceID          string `json:"resource_id"`
	Status              string `json:"status"`
	SuggestionMessageID string `json:"suggestion_message_id"`
	TaskID              string `json:"task_id"`
}

// TaskStatusPayload 表示任务终态回写到会话后的结构化消息。
type TaskStatusPayload struct {
	DetailURL     string `json:"detail_url"`
	Instruction   string `json:"instruction"`
	ResourceID    string `json:"resource_id"`
	ResultURL     string `json:"result_url,omitempty"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
	TaskID        string `json:"task_id"`
	Title         string `json:"title"`
}

// SessionFilePayload 表示当前会话中一个已导入资源库的文件。
type SessionFilePayload struct {
	FileName      string `json:"file_name"`
	FileID        string `json:"file_id,omitempty"`
	ResourceID    string `json:"resource_id"`
	ResourceTitle string `json:"resource_title"`
	SourceType    string `json:"source_type"`
	Status        string `json:"status"`
}

// SystemPayload 表示系统状态或失败消息。
type SystemPayload struct {
	Content string `json:"content"`
	Level   string `json:"level"`
}
