// Package 轮次 owns 请求 idempotency 和 the shared ingestion 流水线 用于
// streaming 和 non-streaming Agent turns. 持久化的 状态 remains 位于 存储; the
// 协调器 deliberately keeps 没有 位于-处理流程 运行 状态.
package turn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidRequest      = errors.New("无效的 agent 轮次请求")
	ErrIdempotencyConflict = errors.New("轮次请求 idempotency conflict")
)

type Status string

const (
	StatusAccepted        Status = "accepted"
	StatusRunning         Status = "running"
	StatusWaitingInput    Status = "waiting_input"
	StatusWaitingApproval Status = "waiting_approval"
	StatusSucceeded       Status = "succeeded"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
)

type Request struct {
	RequestID      string
	TraceID        string
	OrganizationID string
	WorkspaceID    string
	ResourceID     string
	SessionID      string
	Message        string
	PrincipalType  string
	PrincipalID    string
	TrustSource    string
	RuntimeMode    string
}

type Turn struct {
	ID        string
	SessionID string
	RunID     string
	RequestID string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Event struct {
	ID        string
	TurnID    string
	Sequence  int
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type AcceptInput struct {
	Request   Request
	InputJSON json.RawMessage
	InputHash string
}

type Store interface {
	// Accept atomically creates 或 returns the 轮次, user 消息, 持久化的 运行,
	// initial 类型化的 步骤, 和 outbox/事件 facts 用于 一个 request_id.
	Accept(ctx context.Context, input AcceptInput) (turn Turn, created bool, err error)
	Commit(ctx context.Context, input CommitInput) (turn Turn, created bool, err error)
	Events(ctx context.Context, turnID string, afterSequence int) ([]Event, error)
}

type Result struct {
	Turn    Turn
	Created bool
	Events  []Event
}

type Message struct {
	Role    string          `json:"role"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type Outcome struct {
	TurnID         string
	IdempotencyKey string
	Status         Status
	OutputJSON     json.RawMessage
	ErrorJSON      json.RawMessage
	Messages       []Message
}

type CommitInput struct {
	Outcome     Outcome
	OutcomeHash string
}

type OutcomeResult struct {
	Turn    Turn
	Created bool
}

type Coordinator struct {
	store Store
}

// NewCoordinator 校验依赖并创建对应实例。
func NewCoordinator(store Store) *Coordinator {
	return &Coordinator{store: store}
}

// Submit 执行该函数负责的核心处理逻辑。
func (c *Coordinator) Submit(ctx context.Context, request Request) (Result, error) {
	input, err := normalize(request)
	if err != nil {
		return Result{}, err
	}
	if c == nil || c.store == nil {
		return Result{}, fmt.Errorf("%w：轮次存储不能为空", ErrInvalidRequest)
	}

	turn, created, err := c.store.Accept(ctx, input)
	if err != nil {
		return Result{}, err
	}
	events, err := c.store.Events(ctx, turn.ID, 0)
	if err != nil {
		return Result{}, fmt.Errorf("加载持久化ed 轮次事件：%w", err)
	}
	return Result{Turn: turn, Created: created, Events: events}, nil
}

// CommitOutcome stores the 响应 envelope through the same deterministic
// 协调器 boundary used 由 every transport. 存储 owns transition checks
// 和 the 事务 containing messages, 事件, 和 outbox facts.
func (c *Coordinator) CommitOutcome(ctx context.Context, outcome Outcome) (OutcomeResult, error) {
	if c == nil || c.store == nil {
		return OutcomeResult{}, fmt.Errorf("%w：轮次存储不能为空", ErrInvalidRequest)
	}
	input, err := PrepareOutcome(outcome)
	if err != nil {
		return OutcomeResult{}, err
	}
	committed, created, err := c.store.Commit(ctx, input)
	if err != nil {
		return OutcomeResult{}, err
	}
	return OutcomeResult{Turn: committed, Created: created}, nil
}

// Stream projects persisted 事件 来自 Submit. Observer failures never roll
// back 或 mutate the accepted 轮次, so the same request_id can safely replay.
func (c *Coordinator) Stream(ctx context.Context, request Request, afterSequence int, observe func(Event) error) error {
	if afterSequence < 0 || observe == nil {
		return fmt.Errorf("%w：stream curs或和 observer 无效", ErrInvalidRequest)
	}
	result, err := c.Submit(ctx, request)
	if err != nil {
		return err
	}
	for _, event := range result.Events {
		if event.Sequence <= afterSequence {
			continue
		}
		if err := observe(event); err != nil {
			return fmt.Errorf("投影轮次事件 %d：%w", event.Sequence, err)
		}
	}
	return nil
}

// normalize 执行该函数负责的核心处理逻辑。
func normalize(request Request) (AcceptInput, error) {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.TraceID = strings.TrimSpace(request.TraceID)
	request.OrganizationID = strings.TrimSpace(request.OrganizationID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.PrincipalType = strings.ToLower(strings.TrimSpace(request.PrincipalType))
	request.PrincipalID = strings.TrimSpace(request.PrincipalID)
	request.TrustSource = strings.TrimSpace(request.TrustSource)
	request.RuntimeMode = strings.ToLower(strings.TrimSpace(request.RuntimeMode))
	if request.RequestID == "" || strings.TrimSpace(request.Message) == "" {
		return AcceptInput{}, fmt.Errorf("%w：request_id 和消息不能为空", ErrInvalidRequest)
	}

	payload := struct {
		Message        string `json:"message"`
		OrganizationID string `json:"organization_id,omitempty"`
		WorkspaceID    string `json:"workspace_id,omitempty"`
		ResourceID     string `json:"resource_id,omitempty"`
		SessionID      string `json:"session_id,omitempty"`
		PrincipalType  string `json:"principal_type,omitempty"`
		PrincipalID    string `json:"principal_id,omitempty"`
		TrustSource    string `json:"trust_source,omitempty"`
		RuntimeMode    string `json:"runtime_mode,omitempty"`
	}{
		Message: request.Message, OrganizationID: request.OrganizationID,
		WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID, SessionID: request.SessionID,
		PrincipalType: request.PrincipalType, PrincipalID: request.PrincipalID,
		TrustSource: request.TrustSource, RuntimeMode: request.RuntimeMode,
	}
	inputJSON, err := json.Marshal(payload)
	if err != nil {
		return AcceptInput{}, fmt.Errorf("%w：编码输入：%v", ErrInvalidRequest, err)
	}
	digest := sha256.Sum256(inputJSON)
	return AcceptInput{
		Request:   request,
		InputJSON: inputJSON,
		InputHash: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

// PrepareOutcome canonicalizes 和 hashes 一个 结果. Stores call it again 位于
// their trust boundary so direct callers 不能 supply 一个 forged 结果 哈希.
func PrepareOutcome(outcome Outcome) (CommitInput, error) {
	outcome.TurnID = strings.TrimSpace(outcome.TurnID)
	outcome.IdempotencyKey = strings.TrimSpace(outcome.IdempotencyKey)
	if outcome.TurnID == "" || outcome.IdempotencyKey == "" || !outcome.Status.Valid() || outcome.Status == StatusAccepted {
		return CommitInput{}, fmt.Errorf("%w：turn_id、idempotency_键、和结果状态不能为空", ErrInvalidRequest)
	}
	var err error
	if outcome.OutputJSON, err = canonicalObject(outcome.OutputJSON, "output_json"); err != nil {
		return CommitInput{}, err
	}
	if len(outcome.ErrorJSON) > 0 {
		if outcome.ErrorJSON, err = canonicalObject(outcome.ErrorJSON, "error_json"); err != nil {
			return CommitInput{}, err
		}
	}
	for index := range outcome.Messages {
		message := &outcome.Messages[index]
		message.Role = strings.TrimSpace(message.Role)
		message.Kind = strings.TrimSpace(message.Kind)
		if (message.Role != "assistant" && message.Role != "system") || message.Kind == "" {
			return CommitInput{}, fmt.Errorf("处理失败：%w：结果 messages 必须为类型化的助手/system messages", ErrInvalidRequest)
		}
		message.Payload, err = canonicalObject(message.Payload, "message payload")
		if err != nil {
			return CommitInput{}, err
		}
	}
	envelope, err := json.Marshal(struct {
		Status     Status          `json:"status"`
		OutputJSON json.RawMessage `json:"output_json"`
		ErrorJSON  json.RawMessage `json:"error_json,omitempty"`
		Messages   []Message       `json:"messages"`
	}{outcome.Status, outcome.OutputJSON, outcome.ErrorJSON, outcome.Messages})
	if err != nil {
		return CommitInput{}, fmt.Errorf("%w：编码结果", ErrInvalidRequest)
	}
	digest := sha256.Sum256(envelope)
	return CommitInput{Outcome: outcome, OutcomeHash: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

// canonicalObject 执行该函数负责的核心处理逻辑。
func canonicalObject(value json.RawMessage, name string) (json.RawMessage, error) {
	if len(value) == 0 {
		value = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w：%s 必须是 JSON 对象", ErrInvalidRequest, name)
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w：编码 %s", ErrInvalidRequest, name)
	}
	return canonical, nil
}

// 有效的 执行该函数负责的核心处理逻辑。
func (status Status) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch status {
	case StatusAccepted, StatusRunning, StatusWaitingInput, StatusWaitingApproval,
		StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransition 执行该函数负责的核心处理逻辑。
func CanTransition(from, to Status) bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch from {
	case StatusAccepted, StatusRunning:
		return to == StatusRunning || to == StatusWaitingInput || to == StatusWaitingApproval ||
			to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	case StatusWaitingInput, StatusWaitingApproval:
		return to == StatusRunning || to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	default:
		return false
	}
}
