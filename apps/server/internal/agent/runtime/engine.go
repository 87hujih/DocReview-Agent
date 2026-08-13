package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Outcome string

const (
	OutcomeContinue     Outcome = "continue"
	OutcomeSucceed      Outcome = "succeed"
	OutcomeWaitInput    Outcome = "wait_input"
	OutcomeWaitApproval Outcome = "wait_approval"
)

type TimeoutScope string

const (
	TimeoutScopeAttempt TimeoutScope = "attempt"
	TimeoutScopeStep    TimeoutScope = "step"
	TimeoutScopeRun     TimeoutScope = "run"
)

type Config struct {
	WorkerID          string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	AttemptTimeout    time.Duration
	StepTimeout       time.Duration
	RetryBase         time.Duration
	RetryMax          time.Duration
}

type WorkItem struct {
	RunID             string
	RunVersion        int64
	RunDeadlineAt     *time.Time
	CancelRequestedAt *time.Time
	StepID            string
	StepKey           string
	StepType          string
	InputJSON         json.RawMessage
	AttemptNumber     int
	MaxAttempts       int
	LeaseGeneration   int64
	ClaimedBy         string
	StepStartedAt     *time.Time
	MaxSteps          int
	StepCount         int
	MaxToolCalls      int
	ToolCallCount     int
	TokenBudget       *int64
	TokensUsed        int64
	CostBudget        *float64
	CostUsed          float64
}

type StepSpec struct {
	StepKey     string
	StepType    string
	InputJSON   json.RawMessage
	MaxAttempts int
}

type ExecutionInput struct {
	RunID          string
	StepID         string
	TraceID        string
	StepKey        string
	StepType       string
	InputJSON      json.RawMessage
	AttemptNumber  int
	IdempotencyKey string
}

type ExecutionError struct {
	Category     ErrorCategory `json:"category"`
	Message      string        `json:"message"`
	TimeoutScope TimeoutScope  `json:"timeout_scope,omitempty"`
}

type ExecutionResult struct {
	Outcome           Outcome
	OutputJSON        json.RawMessage
	NextSteps         []StepSpec
	Err               *ExecutionError
	Provider          string
	Model             string
	PromptVersion     string
	Temperature       *float64
	ContextManifestID string
	RetryCount        int
	InputTokens       int64
	OutputTokens      int64
	Cost              float64
	FinishReason      string
	Observations      []ObservationSpec
}

type ObservationSpec struct {
	ObservationKey string          `json:"observation_key"`
	Kind           string          `json:"kind"`
	Action         string          `json:"action"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	PayloadJSON    json.RawMessage `json:"payload_json"`
	ContentHash    string          `json:"content_hash"`
	Novel          bool            `json:"novel"`
}

type ClaimRequest struct {
	Now           time.Time
	WorkerID      string
	LeaseDuration time.Duration
}

type LeaseHeartbeat struct {
	StepID          string
	WorkerID        string
	LeaseGeneration int64
	Now             time.Time
	LeaseDuration   time.Duration
}

type AttemptStart struct {
	StepID        string
	AttemptNumber int
	StartedAt     time.Time
	TraceID       string
}

type AttemptFinish struct {
	AttemptID         string
	Provider          string
	Model             string
	PromptVersion     string
	Temperature       *float64
	ContextManifestID string
	RetryCount        int
	InputTokens       int64
	OutputTokens      int64
	Cost              float64
	LatencyMS         int64
	FinishReason      string
	ErrorCategory     *ErrorCategory
	CompletedAt       time.Time
}

type OutcomeCommit struct {
	Work         WorkItem
	StepStatus   StepStatus
	RunStatus    RunStatus
	OutputJSON   json.RawMessage
	Error        *ExecutionError
	NextSteps    []StepSpec
	Observations []ObservationSpec
	CommittedAt  time.Time
}

type RetryCommit struct {
	Work        WorkItem
	Error       ExecutionError
	NextRetryAt time.Time
	CommittedAt time.Time
}

type Store interface {
	RecoverExpired(ctx context.Context, now time.Time) error
	Claim(ctx context.Context, request ClaimRequest) (*WorkItem, error)
	Heartbeat(ctx context.Context, heartbeat LeaseHeartbeat) error
	StartAttempt(ctx context.Context, attempt AttemptStart) (string, error)
	FinishAttempt(ctx context.Context, attempt AttemptFinish) error
	CommitOutcome(ctx context.Context, outcome OutcomeCommit) error
	ScheduleRetry(ctx context.Context, retry RetryCommit) error
}

type Executor interface {
	Execute(ctx context.Context, input ExecutionInput) ExecutionResult
}

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

// Now 执行该函数负责的核心处理逻辑。
func (systemClock) Now() time.Time { return time.Now().UTC() }

type Engine struct {
	cfg      Config
	store    Store
	executor Executor
	clock    Clock
}

// NewEngine 校验依赖并创建对应实例。
func NewEngine(cfg Config, store Store, executor Executor, clock Clock) (*Engine, error) {
	cfg.WorkerID = strings.TrimSpace(cfg.WorkerID)
	if cfg.WorkerID == "" || cfg.LeaseDuration <= 0 || cfg.HeartbeatInterval <= 0 ||
		cfg.AttemptTimeout <= 0 || cfg.StepTimeout <= 0 || cfg.RetryBase <= 0 || cfg.RetryMax < cfg.RetryBase {
		return nil, fmt.Errorf("持久化的引擎配置无效")
	}
	if cfg.HeartbeatInterval >= cfg.LeaseDuration {
		return nil, fmt.Errorf("心跳间隔必须为更短于租约时长")
	}
	if store == nil || executor == nil {
		return nil, fmt.Errorf("持久化的引擎存储和执行器不能为空")
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Engine{cfg: cfg, store: store, executor: executor, clock: clock}, nil
}

// Recover 执行该函数负责的核心处理逻辑。
func (e *Engine) Recover(ctx context.Context) error {
	return e.store.RecoverExpired(ctx, e.clock.Now())
}

// ProcessOne 执行该函数负责的核心处理逻辑。
func (e *Engine) ProcessOne(ctx context.Context) (bool, error) {
	now := e.clock.Now()
	work, err := e.store.Claim(ctx, ClaimRequest{Now: now, WorkerID: e.cfg.WorkerID, LeaseDuration: e.cfg.LeaseDuration})
	if err != nil || work == nil {
		return false, err
	}
	if work.CancelRequestedAt != nil {
		return true, e.commitTerminal(ctx, *work, StepStatusCancelled, RunStatusCancelled, nil, &ExecutionError{Category: ErrorCategoryCancelled, Message: "运行收到取消请求"})
	}
	if work.RunDeadlineAt != nil && !work.RunDeadlineAt.After(now) {
		return true, e.commitTerminal(ctx, *work, StepStatusFailed, RunStatusFailed, nil, timeoutError(TimeoutScopeRun))
	}
	if work.StepStartedAt != nil && !work.StepStartedAt.Add(e.cfg.StepTimeout).After(now) {
		return true, e.commitTerminal(ctx, *work, StepStatusFailed, RunStatusFailed, nil, timeoutError(TimeoutScopeStep))
	}
	if limitErr := workLimitError(*work); limitErr != nil {
		return true, e.commitTerminal(ctx, *work, StepStatusFailed, RunStatusFailed, nil, limitErr)
	}

	attemptStarted := e.clock.Now()
	traceID := fmt.Sprintf("%s:%s:%d", work.RunID, work.StepID, work.AttemptNumber)
	attemptID, err := e.store.StartAttempt(ctx, AttemptStart{
		StepID: work.StepID, AttemptNumber: work.AttemptNumber, StartedAt: attemptStarted,
		TraceID: traceID,
	})
	if err != nil {
		return true, err
	}

	attemptDuration, timeoutScope := e.attemptBudget(*work, attemptStarted)
	attemptCtx, cancel := context.WithTimeout(ctx, attemptDuration)
	result, heartbeatErr := e.executeWithHeartbeat(attemptCtx, *work, traceID)
	cancel()
	completedAt := e.clock.Now()
	if heartbeatErr != nil {
		return true, heartbeatErr
	}
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		result.Err = timeoutError(timeoutScope)
	}
	if err := validateExecutionResult(result); err != nil {
		result.Err = &ExecutionError{Category: ErrorCategoryInvalidInput, Message: err.Error()}
	}

	var category *ErrorCategory
	if result.Err != nil {
		value := result.Err.Category
		category = &value
	}
	latency := completedAt.Sub(attemptStarted)
	if latency < 0 {
		latency = 0
	}
	if err := e.store.FinishAttempt(ctx, AttemptFinish{
		AttemptID: attemptID, Provider: result.Provider, Model: result.Model, PromptVersion: result.PromptVersion,
		Temperature: result.Temperature, ContextManifestID: result.ContextManifestID, RetryCount: result.RetryCount,
		InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		Cost: result.Cost, LatencyMS: latency.Milliseconds(), FinishReason: result.FinishReason,
		ErrorCategory: category, CompletedAt: completedAt,
	}); err != nil {
		return true, err
	}

	if result.Err != nil {
		if result.Err.Category.Retryable() && work.AttemptNumber < work.MaxAttempts && timeoutScope != TimeoutScopeRun && timeoutScope != TimeoutScopeStep {
			nextRetryAt := completedAt.Add(exponentialBackoff(e.cfg.RetryBase, e.cfg.RetryMax, work.AttemptNumber))
			if work.RunDeadlineAt == nil || nextRetryAt.Before(*work.RunDeadlineAt) {
				return true, e.store.ScheduleRetry(ctx, RetryCommit{Work: *work, Error: *result.Err, NextRetryAt: nextRetryAt, CommittedAt: completedAt})
			}
		}
		return true, e.commitTerminal(ctx, *work, StepStatusFailed, RunStatusFailed, nil, result.Err)
	}
	if result.Outcome == OutcomeContinue {
		projected := *work
		projected.StepCount += len(result.NextSteps)
		projected.TokensUsed += result.InputTokens + result.OutputTokens
		projected.CostUsed += result.Cost
		if limitErr := workLimitError(projected); limitErr != nil {
			return true, e.commitTerminal(ctx, *work, StepStatusFailed, RunStatusFailed, result.OutputJSON, limitErr)
		}
	}

	stepStatus, runStatus := statusesForOutcome(result.Outcome)
	return true, e.store.CommitOutcome(ctx, OutcomeCommit{
		Work: *work, StepStatus: stepStatus, RunStatus: runStatus, OutputJSON: normalizeObject(result.OutputJSON),
		NextSteps: result.NextSteps, Observations: result.Observations, CommittedAt: completedAt,
	})
}

// executeWithHeartbeat 执行该函数负责的核心处理逻辑。
func (e *Engine) executeWithHeartbeat(ctx context.Context, work WorkItem, traceID string) (ExecutionResult, error) {
	resultCh := make(chan ExecutionResult, 1)
	// 启动并发任务，并由外围同步机制负责回收。
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				resultCh <- ExecutionResult{Err: &ExecutionError{Category: ErrorCategoryTerminalUpstream, Message: fmt.Sprintf("executor panic: %v", recovered)}}
			}
		}()
		resultCh <- e.executor.Execute(ctx, ExecutionInput{
			RunID: work.RunID, StepID: work.StepID, TraceID: traceID, StepKey: work.StepKey, StepType: work.StepType,
			InputJSON: work.InputJSON, AttemptNumber: work.AttemptNumber,
			IdempotencyKey: fmt.Sprintf("agent-step:%s", work.StepID),
		})
	}()
	ticker := time.NewTicker(e.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		// 等待并发事件、取消信号或超时结果。
		select {
		case result := <-resultCh:
			return result, nil
		case <-ticker.C:
			now := e.clock.Now()
			if err := e.store.Heartbeat(ctx, LeaseHeartbeat{StepID: work.StepID, WorkerID: e.cfg.WorkerID, LeaseGeneration: work.LeaseGeneration, Now: now, LeaseDuration: e.cfg.LeaseDuration}); err != nil {
				return ExecutionResult{}, err
			}
		case <-ctx.Done():
			// 等待并发事件、取消信号或超时结果。
			select {
			case result := <-resultCh:
				return result, nil
			default:
				return ExecutionResult{}, nil
			}
		}
	}
}

// attemptBudget 执行该函数负责的核心处理逻辑。
func (e *Engine) attemptBudget(work WorkItem, now time.Time) (time.Duration, TimeoutScope) {
	duration := e.cfg.AttemptTimeout
	scope := TimeoutScopeAttempt
	if work.StepStartedAt != nil {
		remaining := work.StepStartedAt.Add(e.cfg.StepTimeout).Sub(now)
		if remaining < duration {
			duration, scope = remaining, TimeoutScopeStep
		}
	}
	if work.RunDeadlineAt != nil {
		remaining := work.RunDeadlineAt.Sub(now)
		if remaining < duration {
			duration, scope = remaining, TimeoutScopeRun
		}
	}
	if duration <= 0 {
		duration = time.Nanosecond
	}
	return duration, scope
}

// commitTerminal 执行该函数负责的核心处理逻辑。
func (e *Engine) commitTerminal(ctx context.Context, work WorkItem, stepStatus StepStatus, runStatus RunStatus, output json.RawMessage, executionErr *ExecutionError) error {
	return e.store.CommitOutcome(ctx, OutcomeCommit{Work: work, StepStatus: stepStatus, RunStatus: runStatus, OutputJSON: normalizeObject(output), Error: executionErr, CommittedAt: e.clock.Now()})
}

// validateExecutionResult 校验输入及领域约束。
func validateExecutionResult(result ExecutionResult) error {
	if result.InputTokens < 0 || result.OutputTokens < 0 || result.Cost < 0 || result.RetryCount < 0 ||
		(result.Temperature != nil && *result.Temperature < 0) {
		return fmt.Errorf("执行 usage 值必须 not 为负数")
	}
	if result.Err != nil {
		if !result.Err.Category.Valid() || strings.TrimSpace(result.Err.Message) == "" {
			return fmt.Errorf("执行错误需要类别和消息")
		}
		return nil
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch result.Outcome {
	case OutcomeSucceed, OutcomeWaitInput, OutcomeWaitApproval:
		if len(result.NextSteps) != 0 {
			return fmt.Errorf("终止或等待结果必须 not add 下一个 steps")
		}
	case OutcomeContinue:
		if len(result.NextSteps) == 0 {
			return fmt.Errorf("继续结果需要至少一个下一个步骤")
		}
	default:
		return fmt.Errorf("处理失败：unknown 执行结果 %q", result.Outcome)
	}
	if err := validateJSONObject(result.OutputJSON, "output_json"); err != nil {
		return err
	}
	seenStepKeys := make(map[string]struct{}, len(result.NextSteps))
	for index, next := range result.NextSteps {
		stepKey := strings.TrimSpace(next.StepKey)
		stepType := strings.TrimSpace(next.StepType)
		if stepKey == "" || stepType == "" {
			return fmt.Errorf("next_steps[%d] 需要 step_键和 step_type", index)
		}
		if next.MaxAttempts < 0 {
			return fmt.Errorf("next_steps[%d].max_attempts 必须 not 为负数", index)
		}
		if _, duplicate := seenStepKeys[stepKey]; duplicate {
			return fmt.Errorf("next_steps 包含重复的 step_键 %q", stepKey)
		}
		seenStepKeys[stepKey] = struct{}{}
		if err := validateJSONObject(next.InputJSON, fmt.Sprintf("next_steps[%d].input_json", index)); err != nil {
			return err
		}
	}
	seenObservationKeys := make(map[string]struct{}, len(result.Observations))
	for index, observation := range result.Observations {
		if strings.TrimSpace(observation.ObservationKey) == "" || strings.TrimSpace(observation.Kind) == "" ||
			strings.TrimSpace(observation.Action) == "" || len(observation.PayloadJSON) == 0 || !strings.HasPrefix(observation.ContentHash, "sha256:") {
			return fmt.Errorf("观察结果[%d] 标识和内容哈希不能为空", index)
		}
		if _, duplicate := seenObservationKeys[observation.ObservationKey]; duplicate {
			return fmt.Errorf("观察结果包含重复的 observation_键 %q", observation.ObservationKey)
		}
		seenObservationKeys[observation.ObservationKey] = struct{}{}
		if err := validateJSONObject(observation.PayloadJSON, fmt.Sprintf("observations[%d].payload_json", index)); err != nil {
			return err
		}
		var payload map[string]any
		_ = json.Unmarshal(observation.PayloadJSON, &payload)
		canonical, _ := json.Marshal(payload)
		digest := sha256.Sum256(canonical)
		if observation.ContentHash != fmt.Sprintf("sha256:%x", digest[:]) {
			return fmt.Errorf("观察结果[%d].content_hash 不匹配 payload_json", index)
		}
	}
	return nil
}

// validateJSONObject 校验输入及领域约束。
func validateJSONObject(value json.RawMessage, field string) error {
	if len(value) == 0 {
		return nil
	}
	if !json.Valid(value) {
		return fmt.Errorf("处理失败：%s 必须为有效的 JSON", field)
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return fmt.Errorf("%s 必须是 JSON 对象", field)
	}
	return nil
}

// statusesForOutcome 执行该函数负责的核心处理逻辑。
func statusesForOutcome(outcome Outcome) (StepStatus, RunStatus) {
	// 根据当前状态或类型选择对应的处理分支。
	switch outcome {
	case OutcomeContinue:
		return StepStatusSucceeded, RunStatusQueued
	case OutcomeWaitInput:
		return StepStatusWaitingInput, RunStatusWaitingInput
	case OutcomeWaitApproval:
		return StepStatusWaitingApproval, RunStatusWaitingApproval
	default:
		return StepStatusSucceeded, RunStatusSucceeded
	}
}

// exponentialBackoff 执行该函数负责的核心处理逻辑。
func exponentialBackoff(base, maximum time.Duration, attemptNumber int) time.Duration {
	value := base
	for i := 1; i < attemptNumber && value < maximum; i++ {
		if value > maximum/2 {
			return maximum
		}
		value *= 2
	}
	if value > maximum {
		return maximum
	}
	return value
}

// timeoutError 执行该函数负责的核心处理逻辑。
func timeoutError(scope TimeoutScope) *ExecutionError {
	return &ExecutionError{Category: ErrorCategoryTimeout, Message: fmt.Sprintf("%s timeout exceeded", scope), TimeoutScope: scope}
}

// workLimitError 执行该函数负责的核心处理逻辑。
func workLimitError(work WorkItem) *ExecutionError {
	// 根据当前状态或类型选择对应的处理分支。
	switch {
	case work.MaxSteps > 0 && work.StepCount > work.MaxSteps:
		return &ExecutionError{Category: ErrorCategoryPolicyBlocked, Message: "已超过最大步骤数"}
	case work.MaxToolCalls >= 0 && work.ToolCallCount >= work.MaxToolCalls && work.MaxToolCalls > 0:
		return &ExecutionError{Category: ErrorCategoryPolicyBlocked, Message: "已达到最大工具调用次数"}
	case work.TokenBudget != nil && work.TokensUsed >= *work.TokenBudget:
		return &ExecutionError{Category: ErrorCategoryPolicyBlocked, Message: "令牌预算已耗尽"}
	case work.CostBudget != nil && work.CostUsed >= *work.CostBudget:
		return &ExecutionError{Category: ErrorCategoryPolicyBlocked, Message: "成本预算已耗尽"}
	default:
		return nil
	}
}

// normalizeObject 执行该函数负责的核心处理逻辑。
func normalizeObject(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}
