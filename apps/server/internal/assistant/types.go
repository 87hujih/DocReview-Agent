package assistant

import "agent_project/apps/server/internal/storage/postgres"

const (
	RoleAssistant = "assistant"
	RoleUser      = "user"

	KindSessionFile    = "session_file"
	KindSystem         = "system"
	KindTaskCreated    = "task_created"
	KindTaskSuggestion = "task_suggestion"
	KindText           = "text"
)

// ConversationResult 表示创建会话、加载会话后的标准结果。
type ConversationResult struct {
	Session  postgres.AssistantSession
	Messages []postgres.AssistantMessage
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
	FileName string
	Content  []byte
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

// SessionFilePayload 表示当前会话中一个已导入资源库的文件。
type SessionFilePayload struct {
	FileName      string `json:"file_name"`
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
