// Package operations defines the authenticated、工作区-scoped 操作员
// boundary 用于 inspecting 和 safely recovering the 持久化的 Agent 运行时.
package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type DiagnosticRequest struct {
	WorkspaceID string `json:"workspace_id"`
	RunID       string `json:"run_id"`
}

type RunListRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"status,omitempty"`
	ResourceID  string `json:"resource_id,omitempty"`
	Limit       int    `json:"limit"`
}

type RunSummary struct {
	ID                 string    `json:"id"`
	WorkspaceID        string    `json:"workspace_id"`
	ResourceID         string    `json:"resource_id,omitempty"`
	SessionID          string    `json:"session_id,omitempty"`
	RequestID          string    `json:"request_id,omitempty"`
	Status             string    `json:"status"`
	Objective          string    `json:"objective"`
	CurrentStep        string    `json:"current_step,omitempty"`
	StepCount          int       `json:"step_count"`
	CompletedStepCount int       `json:"completed_step_count"`
	FailedStepCount    int       `json:"failed_step_count"`
	PendingApprovalID  string    `json:"pending_approval_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ApprovalListRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"status,omitempty"`
	Limit       int    `json:"limit"`
}

type ApprovalSummary struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	RunID          string          `json:"run_id"`
	StepID         string          `json:"step_id"`
	ResourceID     string          `json:"resource_id,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	Objective      string          `json:"objective"`
	ToolName       string          `json:"tool_name"`
	ToolVersion    string          `json:"tool_version"`
	Reason         string          `json:"reason"`
	Status         string          `json:"status"`
	ResourcesJSON  json.RawMessage `json:"resources"`
	PayloadJSON    json.RawMessage `json:"payload"`
	DecisionReason string          `json:"decision_reason,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	DecidedAt      *time.Time      `json:"decided_at,omitempty"`
}

type MetricsRequest struct {
	WorkspaceID string        `json:"workspace_id"`
	ResourceID  string        `json:"resource_id,omitempty"`
	Window      time.Duration `json:"-"`
}

type ComparisonListRequest struct {
	WorkspaceID string        `json:"workspace_id"`
	ResourceID  string        `json:"resource_id"`
	Window      time.Duration `json:"-"`
	Limit       int           `json:"limit"`
}

type ComparisonView struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	ResourceID       string          `json:"resource_id"`
	RunID            string          `json:"run_id,omitempty"`
	RequestID        string          `json:"request_id"`
	ComparisonKind   string          `json:"comparison_kind"`
	Status           string          `json:"status"`
	LegacyResultHash string          `json:"legacy_result_hash,omitempty"`
	TypedResultHash  string          `json:"typed_result_hash,omitempty"`
	LegacyEventHash  string          `json:"legacy_event_hash,omitempty"`
	TypedEventHash   string          `json:"typed_event_hash,omitempty"`
	LegacyDTOHash    string          `json:"legacy_dto_hash,omitempty"`
	TypedDTOHash     string          `json:"typed_dto_hash,omitempty"`
	DetailsJSON      json.RawMessage `json:"details_json"`
	CreatedAt        time.Time       `json:"created_at"`
}

type ComparisonList struct {
	SchemaVersion string           `json:"schema_version"`
	WorkspaceID   string           `json:"workspace_id"`
	ResourceID    string           `json:"resource_id"`
	WindowSeconds int64            `json:"window_seconds"`
	CollectedAt   time.Time        `json:"collected_at"`
	Limit         int              `json:"limit"`
	Comparisons   []ComparisonView `json:"comparisons"`
}

type RunView struct {
	ID                string          `json:"id"`
	WorkspaceID       string          `json:"workspace_id"`
	ResourceID        string          `json:"resource_id,omitempty"`
	SessionID         string          `json:"session_id,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	TraceID           string          `json:"trace_id,omitempty"`
	RuntimeMode       string          `json:"runtime_mode,omitempty"`
	Status            string          `json:"status"`
	Objective         string          `json:"objective"`
	CurrentStep       string          `json:"current_step,omitempty"`
	Version           int64           `json:"version"`
	StateJSON         json.RawMessage `json:"state_json"`
	DeadlineAt        *time.Time      `json:"deadline_at,omitempty"`
	CancelRequestedAt *time.Time      `json:"cancel_requested_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type StepView struct {
	ID              string          `json:"id"`
	RunID           string          `json:"run_id"`
	StepKey         string          `json:"step_key"`
	StepType        string          `json:"step_type"`
	Status          string          `json:"status"`
	InputJSON       json.RawMessage `json:"input_json"`
	OutputJSON      json.RawMessage `json:"output_json,omitempty"`
	ErrorJSON       json.RawMessage `json:"error_json,omitempty"`
	ClaimedBy       string          `json:"claimed_by,omitempty"`
	LeaseExpiresAt  *time.Time      `json:"lease_expires_at,omitempty"`
	LeaseGeneration int64           `json:"lease_generation"`
	AttemptCount    int             `json:"attempt_count"`
	MaxAttempts     int             `json:"max_attempts"`
	NextRetryAt     *time.Time      `json:"next_retry_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AttemptView struct {
	ID                string     `json:"id"`
	StepID            string     `json:"step_id"`
	AttemptNumber     int        `json:"attempt_number"`
	ContextManifestID string     `json:"context_manifest_id,omitempty"`
	TraceID           string     `json:"trace_id,omitempty"`
	Provider          string     `json:"provider,omitempty"`
	Model             string     `json:"model,omitempty"`
	InputTokens       int64      `json:"input_tokens"`
	OutputTokens      int64      `json:"output_tokens"`
	Cost              float64    `json:"cost"`
	LatencyMS         int64      `json:"latency_ms"`
	ErrorCategory     string     `json:"error_category,omitempty"`
	StartedAt         time.Time  `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

type ToolCallView struct {
	ID              string          `json:"id"`
	RunID           string          `json:"run_id"`
	StepID          string          `json:"step_id"`
	ToolName        string          `json:"tool_name"`
	ToolVersion     string          `json:"tool_version"`
	Status          string          `json:"status"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	InputJSON       json.RawMessage `json:"input_json"`
	OutputJSON      json.RawMessage `json:"output_json,omitempty"`
	ErrorJSON       json.RawMessage `json:"error_json,omitempty"`
	ErrorCategory   string          `json:"error_category,omitempty"`
	LeaseGeneration int64           `json:"lease_generation"`
	EvidenceSetID   string          `json:"evidence_set_id,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
}

type ContextManifestView struct {
	ID                   string          `json:"id"`
	RunID                string          `json:"run_id"`
	StepID               string          `json:"step_id"`
	Tokenizer            string          `json:"tokenizer"`
	TokenBudget          int64           `json:"token_budget"`
	ReservedOutputTokens int64           `json:"reserved_output_tokens"`
	TotalTokens          int64           `json:"total_tokens"`
	ContentHash          string          `json:"content_hash"`
	ItemsJSON            json.RawMessage `json:"items_json"`
	CreatedAt            time.Time       `json:"created_at"`
}

type OutboxView struct {
	ID              string          `json:"id"`
	AggregateType   string          `json:"aggregate_type"`
	AggregateID     string          `json:"aggregate_id"`
	EventType       string          `json:"event_type"`
	Status          string          `json:"status"`
	AttemptCount    int             `json:"attempt_count"`
	LeaseGeneration int64           `json:"lease_generation"`
	PayloadJSON     json.RawMessage `json:"payload_json"`
	ErrorJSON       json.RawMessage `json:"error_json,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	PublishedAt     *time.Time      `json:"published_at,omitempty"`
}

type ApprovalView struct {
	ID        string     `json:"id"`
	RunID     string     `json:"run_id"`
	StepID    string     `json:"step_id"`
	ToolName  string     `json:"tool_name"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

type TraceLink struct {
	RunID             string   `json:"run_id"`
	StepID            string   `json:"step_id"`
	AttemptID         string   `json:"attempt_id,omitempty"`
	ToolCallIDs       []string `json:"tool_call_ids,omitempty"`
	ContextManifestID string   `json:"context_manifest_id,omitempty"`
	EvidenceSetIDs    []string `json:"evidence_set_ids,omitempty"`
}

type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type Diagnostic struct {
	Run              RunView               `json:"run"`
	Steps            []StepView            `json:"steps"`
	Attempts         []AttemptView         `json:"attempts"`
	ToolCalls        []ToolCallView        `json:"tool_calls"`
	ContextManifests []ContextManifestView `json:"context_manifests"`
	Approvals        []ApprovalView        `json:"approvals"`
	OutboxEvents     []OutboxView          `json:"outbox_events"`
	TraceIndex       []TraceLink           `json:"trace_index"`
	Findings         []Finding             `json:"findings"`
}

type QueueMetrics struct {
	QueuedSteps              int64   `json:"queued_steps"`
	RunningSteps             int64   `json:"running_steps"`
	ExpiredStepLeases        int64   `json:"expired_step_leases"`
	OldestQueuedAgeSeconds   float64 `json:"oldest_queued_age_seconds"`
	WaitingApprovals         int64   `json:"waiting_approvals"`
	OldestApprovalAgeSeconds float64 `json:"oldest_approval_age_seconds"`
}

type UsageMetrics struct {
	Attempts     int64   `json:"attempts"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"error_rate"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

type OutboxMetrics struct {
	Pending                 int64   `json:"pending"`
	Publishing              int64   `json:"publishing"`
	DeadLetters             int64   `json:"dead_letters"`
	ExpiredLeases           int64   `json:"expired_leases"`
	OldestPendingAgeSeconds float64 `json:"oldest_pending_age_seconds"`
}

type RetrievalMetrics struct {
	Calls                int64   `json:"calls"`
	Succeeded            int64   `json:"succeeded"`
	LexicalDegraded      int64   `json:"lexical_degraded"`
	SemanticDegraded     int64   `json:"semantic_degraded"`
	WebDegraded          int64   `json:"web_degraded"`
	ProfileMismatches    int64   `json:"profile_mismatches"`
	MissingNodeCitations int64   `json:"missing_node_citations"`
	AverageEvidenceCount float64 `json:"average_evidence_count"`
}

type ReconciliationMetrics struct {
	Matched     int64 `json:"matched"`
	Diverged    int64 `json:"diverged"`
	Unavailable int64 `json:"unavailable"`
}

type MetricsSnapshot struct {
	SchemaVersion  string                `json:"schema_version"`
	WorkspaceID    string                `json:"workspace_id"`
	ResourceID     string                `json:"resource_id,omitempty"`
	WindowSeconds  int64                 `json:"window_seconds"`
	CollectedAt    time.Time             `json:"collected_at"`
	RunStatus      map[string]int64      `json:"run_status"`
	StepStatus     map[string]int64      `json:"step_status"`
	Queue          QueueMetrics          `json:"queue"`
	Usage          UsageMetrics          `json:"usage"`
	Outbox         OutboxMetrics         `json:"outbox"`
	Retrieval      RetrievalMetrics      `json:"retrieval"`
	Reconciliation ReconciliationMetrics `json:"reconciliation"`
}

type ActionRequest struct {
	WorkspaceID string    `json:"workspace_id"`
	RequestID   string    `json:"request_id"`
	OperatorID  string    `json:"operator_id"`
	Reason      string    `json:"reason"`
	RunID       string    `json:"run_id,omitempty"`
	EventID     string    `json:"event_id,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
}

type ActionResult struct {
	ActionID   string          `json:"action_id"`
	Action     string          `json:"action"`
	TargetID   string          `json:"target_id"`
	Status     string          `json:"status"`
	Replayed   bool            `json:"replayed"`
	ResultJSON json.RawMessage `json:"result_json,omitempty"`
}

type Store interface {
	Diagnose(context.Context, DiagnosticRequest) (Diagnostic, error)
	Metrics(context.Context, MetricsRequest) (MetricsSnapshot, error)
	Comparisons(context.Context, ComparisonListRequest) (ComparisonList, error)
	Cancel(context.Context, ActionRequest) (ActionResult, error)
	Retry(context.Context, ActionRequest) (ActionResult, error)
	ReplayDeadLetter(context.Context, ActionRequest) (ActionResult, error)
}

type Service struct{ store Store }

// NewService 校验依赖并创建对应实例。
func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("operations 存储不能为空")
	}
	return &Service{store: store}, nil
}

// Diagnose 执行该函数负责的核心处理逻辑。
func (service *Service) Diagnose(ctx context.Context, request DiagnosticRequest) (Diagnostic, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RunID = strings.TrimSpace(request.RunID)
	if request.WorkspaceID == "" || request.RunID == "" {
		return Diagnostic{}, fmt.Errorf("工作区_id 和 run_id 不能为空")
	}
	return service.store.Diagnose(ctx, request)
}

// 指标执行该函数负责的核心处理逻辑。
func (service *Service) Metrics(ctx context.Context, request MetricsRequest) (MetricsSnapshot, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	if request.WorkspaceID == "" {
		return MetricsSnapshot{}, fmt.Errorf("工作区_id 不能为空")
	}
	if request.Window == 0 {
		request.Window = time.Hour
	}
	if request.Window < time.Minute || request.Window > 30*24*time.Hour {
		return MetricsSnapshot{}, fmt.Errorf("指标时间窗口必须介于一分钟和 30 天之间")
	}
	return service.store.Metrics(ctx, request)
}

// Comparisons returns immutable cutover facts for a bounded, exact Resource cohort.
func (service *Service) Comparisons(ctx context.Context, request ComparisonListRequest) (ComparisonList, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	if request.WorkspaceID == "" || request.ResourceID == "" {
		return ComparisonList{}, fmt.Errorf("workspace_id 和 resource_id 不能为空")
	}
	if request.Window == 0 {
		request.Window = time.Hour
	}
	if request.Window < time.Minute || request.Window > 30*24*time.Hour {
		return ComparisonList{}, fmt.Errorf("comparison 时间窗口必须介于一分钟和 30 天之间")
	}
	if request.Limit == 0 {
		request.Limit = 200
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return ComparisonList{}, fmt.Errorf("comparison limit 必须介于 1 和 1000 之间")
	}
	return service.store.Comparisons(ctx, request)
}

// Cancel 执行该函数负责的核心处理逻辑。
func (service *Service) Cancel(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := validateAction(request, true, false); err != nil {
		return ActionResult{}, err
	}
	return service.store.Cancel(ctx, normalizeAction(request))
}

// Retry 执行该函数负责的核心处理逻辑。
func (service *Service) Retry(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := validateAction(request, true, false); err != nil {
		return ActionResult{}, err
	}
	return service.store.Retry(ctx, normalizeAction(request))
}

// ReplayDeadLetter 执行该函数负责的核心处理逻辑。
func (service *Service) ReplayDeadLetter(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := validateAction(request, false, true); err != nil {
		return ActionResult{}, err
	}
	return service.store.ReplayDeadLetter(ctx, normalizeAction(request))
}

// validateAction 校验输入及领域约束。
func validateAction(request ActionRequest, requireRun, requireEvent bool) error {
	request = normalizeAction(request)
	if request.WorkspaceID == "" || request.RequestID == "" || request.OperatorID == "" || request.Reason == "" || request.RequestedAt.IsZero() {
		return fmt.Errorf("工作区_id、request_id、operator_id、原因、和 requested_at 不能为空")
	}
	if requireRun != (request.RunID != "") || requireEvent != (request.EventID != "") || (request.RunID != "" && request.EventID != "") {
		return fmt.Errorf("操作目标无效")
	}
	return nil
}

// normalizeAction 执行该函数负责的核心处理逻辑。
func normalizeAction(request ActionRequest) ActionRequest {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.OperatorID = strings.TrimSpace(request.OperatorID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.RunID = strings.TrimSpace(request.RunID)
	request.EventID = strings.TrimSpace(request.EventID)
	return request
}
