package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// TestEngineCompletesSuccessfulStepAndAttempt 验证对应场景下的正常路径与失败路径。
func TestEngineCompletesSuccessfulStepAndAttempt(t *testing.T) {
	store := &fakeEngineStore{work: testWork()}
	executor := fakeExecutor{result: ExecutionResult{
		Outcome: OutcomeSucceed, OutputJSON: json.RawMessage(`{"ok":true}`),
		Provider: "test-provider", Model: "test-model", PromptVersion: "prompt-v3",
		Temperature: float64Pointer(0.2), ContextManifestID: "manifest-1", RetryCount: 2,
		InputTokens: 21, OutputTokens: 8, Cost: 0.001, FinishReason: "stop",
	}}
	engine := mustTestEngine(t, store, executor)

	worked, err := engine.ProcessOne(context.Background())
	if err != nil || !worked {
		t.Fatalf("process one: worked=%v err=%v", worked, err)
	}
	if len(store.attemptStarts) != 1 || len(store.attemptFinishes) != 1 {
		t.Fatalf("expected one persisted attempt, starts=%d finishes=%d", len(store.attemptStarts), len(store.attemptFinishes))
	}
	finished := store.attemptFinishes[0]
	if finished.Provider != "test-provider" || finished.Model != "test-model" || finished.PromptVersion != "prompt-v3" ||
		finished.Temperature == nil || *finished.Temperature != 0.2 || finished.ContextManifestID != "manifest-1" || finished.RetryCount != 2 ||
		finished.InputTokens != 21 || finished.OutputTokens != 8 {
		t.Fatalf("attempt observability fields were not persisted: %#v", finished)
	}
	if len(store.commits) != 1 || store.commits[0].RunStatus != RunStatusSucceeded || store.commits[0].StepStatus != StepStatusSucceeded {
		t.Fatalf("unexpected commit: %#v", store.commits)
	}
}

// TestEngineAttemptTimeoutIsClassifiedAndRetried 验证对应场景下的正常路径与失败路径。
func TestEngineAttemptTimeoutIsClassifiedAndRetried(t *testing.T) {
	store := &fakeEngineStore{work: testWork()}
	engine := mustTestEngine(t, store, blockingExecutor{})
	engine.cfg.AttemptTimeout = 8 * time.Millisecond
	engine.cfg.HeartbeatInterval = 2 * time.Millisecond

	worked, err := engine.ProcessOne(context.Background())
	if err != nil || !worked {
		t.Fatalf("process one: worked=%v err=%v", worked, err)
	}
	if len(store.retries) != 1 || store.retries[0].Error.Category != ErrorCategoryTimeout || store.retries[0].Error.TimeoutScope != TimeoutScopeAttempt {
		t.Fatalf("expected classified attempt timeout retry, got %#v", store.retries)
	}
	if len(store.heartbeats) == 0 {
		t.Fatal("long-running attempt must heartbeat before timing out")
	}
}

// TestEngineStepTimeoutStopsBeforeExecutor 验证对应场景下的正常路径与失败路径。
func TestEngineStepTimeoutStopsBeforeExecutor(t *testing.T) {
	work := testWork()
	started := time.Date(2026, 8, 9, 23, 0, 0, 0, time.UTC)
	work.StepStartedAt = &started
	store := &fakeEngineStore{work: work}
	executor := &countingExecutor{}
	engine := mustTestEngine(t, store, executor)

	_, err := engine.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if executor.calls != 0 || len(store.commits) != 1 || store.commits[0].Error.TimeoutScope != TimeoutScopeStep {
		t.Fatalf("expected step timeout before execution, calls=%d commits=%#v", executor.calls, store.commits)
	}
}

// TestEngineStopsBeforeExecutionWhenRunBudgetIsExhausted 验证对应场景下的正常路径与失败路径。
func TestEngineStopsBeforeExecutionWhenRunBudgetIsExhausted(t *testing.T) {
	tokenBudget := int64(100)
	costBudget := 1.5
	tests := []struct {
		name   string
		mutate func(*WorkItem)
	}{
		{name: "steps", mutate: func(work *WorkItem) { work.MaxSteps, work.StepCount = 1, 2 }},
		{name: "tools", mutate: func(work *WorkItem) { work.MaxToolCalls, work.ToolCallCount = 1, 1 }},
		{name: "tokens", mutate: func(work *WorkItem) { work.TokenBudget, work.TokensUsed = &tokenBudget, 100 }},
		{name: "cost", mutate: func(work *WorkItem) { work.CostBudget, work.CostUsed = &costBudget, 1.5 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			work := testWork()
			test.mutate(work)
			store := &fakeEngineStore{work: work}
			executor := &countingExecutor{}
			engine := mustTestEngine(t, store, executor)

			if _, err := engine.ProcessOne(context.Background()); err != nil {
				t.Fatalf("process one: %v", err)
			}
			if executor.calls != 0 || len(store.commits) != 1 || store.commits[0].Error == nil || store.commits[0].Error.Category != ErrorCategoryPolicyBlocked {
				t.Fatalf("expected deterministic budget stop, calls=%d commits=%#v", executor.calls, store.commits)
			}
		})
	}
}

// TestEngineDoesNotQueueContinuationBeyondRunBudgets 验证对应场景下的正常路径与失败路径。
func TestEngineDoesNotQueueContinuationBeyondRunBudgets(t *testing.T) {
	tokenBudget := int64(100)
	work := testWork()
	work.MaxSteps, work.StepCount = 1, 1
	work.TokenBudget, work.TokensUsed = &tokenBudget, 90
	store := &fakeEngineStore{work: work}
	executor := fakeExecutor{result: ExecutionResult{
		Outcome: OutcomeContinue, InputTokens: 8, OutputTokens: 5,
		NextSteps: []StepSpec{{StepKey: "retrieve:1", StepType: "RetrieveEvidence", InputJSON: json.RawMessage(`{}`)}},
	}}
	engine := mustTestEngine(t, store, executor)

	if _, err := engine.ProcessOne(context.Background()); err != nil {
		t.Fatalf("process one: %v", err)
	}
	if len(store.commits) != 1 || store.commits[0].RunStatus != RunStatusFailed || store.commits[0].Error == nil || store.commits[0].Error.Category != ErrorCategoryPolicyBlocked {
		t.Fatalf("continuation beyond budgets must fail, got %#v", store.commits)
	}
}

// TestEngineRejectsMalformedContinuation 验证对应场景下的正常路径与失败路径。
func TestEngineRejectsMalformedContinuation(t *testing.T) {
	store := &fakeEngineStore{work: testWork()}
	executor := fakeExecutor{result: ExecutionResult{
		Outcome:   OutcomeContinue,
		NextSteps: []StepSpec{{StepKey: " ", StepType: "RetrieveEvidence", InputJSON: json.RawMessage(`{}`)}},
	}}
	engine := mustTestEngine(t, store, executor)

	_, err := engine.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if len(store.commits) != 1 || store.commits[0].Error == nil || store.commits[0].Error.Category != ErrorCategoryInvalidInput {
		t.Fatalf("malformed continuation must fail deterministically, got %#v", store.commits)
	}
}

// TestEngineClassifiesInvalidExecutorTelemetryAndErrors 验证对应场景下的正常路径与失败路径。
func TestEngineClassifiesInvalidExecutorTelemetryAndErrors(t *testing.T) {
	tests := []struct {
		name   string
		result ExecutionResult
	}{
		{name: "unknown error category", result: ExecutionResult{Err: &ExecutionError{Category: ErrorCategory("mystery"), Message: "bad"}}},
		{name: "negative token usage", result: ExecutionResult{Outcome: OutcomeSucceed, InputTokens: -1}},
		{name: "negative cost", result: ExecutionResult{Outcome: OutcomeSucceed, Cost: -0.5}},
		{name: "forged observation hash", result: ExecutionResult{Outcome: OutcomeSucceed, Observations: []ObservationSpec{{
			ObservationKey: "observation-1", Kind: "tool", Action: "read", PayloadJSON: json.RawMessage(`{"ok":true}`), ContentHash: "sha256:forged",
		}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeEngineStore{work: testWork()}
			engine := mustTestEngine(t, store, fakeExecutor{result: test.result})
			if _, err := engine.ProcessOne(context.Background()); err != nil {
				t.Fatalf("process one: %v", err)
			}
			if len(store.commits) != 1 || store.commits[0].Error == nil || store.commits[0].Error.Category != ErrorCategoryInvalidInput {
				t.Fatalf("invalid executor result must be classified before storage, got %#v", store.commits)
			}
		})
	}
}

// TestEngineSchedulesDeterministicExponentialRetry 验证对应场景下的正常路径与失败路径。
func TestEngineSchedulesDeterministicExponentialRetry(t *testing.T) {
	work := testWork()
	work.AttemptNumber = 3
	store := &fakeEngineStore{work: work}
	executor := fakeExecutor{result: ExecutionResult{Err: &ExecutionError{Category: ErrorCategoryRetryableUpstream, Message: "upstream unavailable"}}}
	engine := mustTestEngine(t, store, executor)
	engine.cfg.RetryBase = time.Second
	engine.cfg.RetryMax = 30 * time.Second
	now := engine.clock.Now()

	worked, err := engine.ProcessOne(context.Background())
	if err != nil || !worked {
		t.Fatalf("process one: worked=%v err=%v", worked, err)
	}
	if len(store.retries) != 1 {
		t.Fatalf("expected one retry, got %#v", store.retries)
	}
	if got := store.retries[0].NextRetryAt.Sub(now); got != 4*time.Second {
		t.Fatalf("expected third-attempt backoff 4s, got %v", got)
	}
}

// TestEngineUsesStableStepIdempotencyKeyAcrossAttempts 验证对应场景下的正常路径与失败路径。
func TestEngineUsesStableStepIdempotencyKeyAcrossAttempts(t *testing.T) {
	firstWork := testWork()
	firstWork.AttemptNumber = 1
	firstExecutor := &capturingExecutor{result: ExecutionResult{Outcome: OutcomeSucceed}}
	firstEngine := mustTestEngine(t, &fakeEngineStore{work: firstWork}, firstExecutor)
	if _, err := firstEngine.ProcessOne(context.Background()); err != nil {
		t.Fatalf("first attempt: %v", err)
	}

	secondWork := testWork()
	secondWork.AttemptNumber = 2
	secondExecutor := &capturingExecutor{result: ExecutionResult{Outcome: OutcomeSucceed}}
	secondEngine := mustTestEngine(t, &fakeEngineStore{work: secondWork}, secondExecutor)
	if _, err := secondEngine.ProcessOne(context.Background()); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if firstExecutor.input.IdempotencyKey == "" || firstExecutor.input.IdempotencyKey != secondExecutor.input.IdempotencyKey {
		t.Fatalf("step retries require one stable idempotency key, first=%q second=%q", firstExecutor.input.IdempotencyKey, secondExecutor.input.IdempotencyKey)
	}
}

// TestEngineUsesOneTraceIDForAttemptAndExecutor 验证对应场景下的正常路径与失败路径。
func TestEngineUsesOneTraceIDForAttemptAndExecutor(t *testing.T) {
	store := &fakeEngineStore{work: testWork()}
	executor := &capturingExecutor{result: ExecutionResult{Outcome: OutcomeSucceed}}
	engine := mustTestEngine(t, store, executor)

	if _, err := engine.ProcessOne(context.Background()); err != nil {
		t.Fatalf("process one: %v", err)
	}
	if len(store.attemptStarts) != 1 || store.attemptStarts[0].TraceID == "" {
		t.Fatalf("attempt trace was not persisted: %#v", store.attemptStarts)
	}
	if executor.input.TraceID != store.attemptStarts[0].TraceID {
		t.Fatalf("executor trace %q differs from persisted attempt trace %q", executor.input.TraceID, store.attemptStarts[0].TraceID)
	}
}

// TestEnginePersistsTerminalFailureWithoutRetry 验证对应场景下的正常路径与失败路径。
func TestEnginePersistsTerminalFailureWithoutRetry(t *testing.T) {
	store := &fakeEngineStore{work: testWork()}
	executor := fakeExecutor{result: ExecutionResult{Err: &ExecutionError{Category: ErrorCategoryPermissionDenied, Message: "denied"}}}
	engine := mustTestEngine(t, store, executor)

	_, err := engine.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if len(store.retries) != 0 || len(store.commits) != 1 || store.commits[0].RunStatus != RunStatusFailed {
		t.Fatalf("expected terminal failure, retries=%#v commits=%#v", store.retries, store.commits)
	}
}

// TestEngineCancellationSkipsExecutorAndAttempt 验证对应场景下的正常路径与失败路径。
func TestEngineCancellationSkipsExecutorAndAttempt(t *testing.T) {
	work := testWork()
	cancelledAt := time.Now().UTC()
	work.CancelRequestedAt = &cancelledAt
	store := &fakeEngineStore{work: work}
	executor := &countingExecutor{}
	engine := mustTestEngine(t, store, executor)

	_, err := engine.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if executor.calls != 0 || len(store.attemptStarts) != 0 {
		t.Fatalf("cancelled work must not execute, calls=%d attempts=%d", executor.calls, len(store.attemptStarts))
	}
	if len(store.commits) != 1 || store.commits[0].RunStatus != RunStatusCancelled {
		t.Fatalf("expected cancelled commit, got %#v", store.commits)
	}
}

// TestEnginePersistsWaitingApproval 验证对应场景下的正常路径与失败路径。
func TestEnginePersistsWaitingApproval(t *testing.T) {
	store := &fakeEngineStore{work: testWork()}
	executor := fakeExecutor{result: ExecutionResult{Outcome: OutcomeWaitApproval, OutputJSON: json.RawMessage(`{"risk":"high"}`)}}
	engine := mustTestEngine(t, store, executor)

	_, err := engine.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if len(store.commits) != 1 || store.commits[0].StepStatus != StepStatusWaitingApproval || store.commits[0].RunStatus != RunStatusWaitingApproval {
		t.Fatalf("expected waiting approval commit, got %#v", store.commits)
	}
}

// TestEngineRunDeadlineStopsBeforeExecutor 验证对应场景下的正常路径与失败路径。
func TestEngineRunDeadlineStopsBeforeExecutor(t *testing.T) {
	work := testWork()
	deadline := time.Date(2026, 8, 10, 0, 59, 59, 0, time.UTC)
	work.RunDeadlineAt = &deadline
	store := &fakeEngineStore{work: work}
	executor := &countingExecutor{}
	engine := mustTestEngine(t, store, executor)

	_, err := engine.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if executor.calls != 0 || len(store.commits) != 1 || store.commits[0].Error.Category != ErrorCategoryTimeout || store.commits[0].Error.TimeoutScope != TimeoutScopeRun {
		t.Fatalf("expected run deadline commit, calls=%d commits=%#v", executor.calls, store.commits)
	}
}

// TestEngineRecoverDelegatesExpiredLeaseRecovery 验证对应场景下的正常路径与失败路径。
func TestEngineRecoverDelegatesExpiredLeaseRecovery(t *testing.T) {
	store := &fakeEngineStore{}
	engine := mustTestEngine(t, store, fakeExecutor{})
	if err := engine.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if store.recoverCalls != 1 {
		t.Fatalf("expected recovery call, got %d", store.recoverCalls)
	}
}

// mustTestEngine 执行该函数负责的核心处理逻辑。
func mustTestEngine(t *testing.T, store *fakeEngineStore, executor Executor) *Engine {
	t.Helper()
	clock := fixedClock{now: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)}
	engine, err := NewEngine(Config{
		WorkerID:          "worker-test",
		LeaseDuration:     time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AttemptTimeout:    time.Minute,
		StepTimeout:       time.Hour,
		RetryBase:         time.Second,
		RetryMax:          time.Minute,
	}, store, executor, clock)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return engine
}

// testWork 执行该函数负责的核心处理逻辑。
func testWork() *WorkItem {
	started := time.Date(2026, 8, 10, 0, 59, 0, 0, time.UTC)
	return &WorkItem{
		RunID:           "run-1",
		RunVersion:      2,
		StepID:          "step-1",
		StepKey:         "understand_goal:1",
		StepType:        "UnderstandGoal",
		InputJSON:       json.RawMessage(`{"objective":"test"}`),
		AttemptNumber:   1,
		MaxAttempts:     5,
		LeaseGeneration: 1,
		StepStartedAt:   &started,
		MaxSteps:        64,
		MaxToolCalls:    32,
	}
}

type fixedClock struct{ now time.Time }

// Now 执行该函数负责的核心处理逻辑。
func (c fixedClock) Now() time.Time { return c.now }

// float64Pointer 执行该函数负责的核心处理逻辑。
func float64Pointer(value float64) *float64 { return &value }

type fakeExecutor struct{ result ExecutionResult }

// Execute 执行该函数负责的核心处理逻辑。
func (e fakeExecutor) Execute(context.Context, ExecutionInput) ExecutionResult { return e.result }

type countingExecutor struct{ calls int }

// Execute 执行该函数负责的核心处理逻辑。
func (e *countingExecutor) Execute(context.Context, ExecutionInput) ExecutionResult {
	e.calls++
	return ExecutionResult{Outcome: OutcomeSucceed}
}

type blockingExecutor struct{}

// Execute 执行该函数负责的核心处理逻辑。
func (blockingExecutor) Execute(ctx context.Context, _ ExecutionInput) ExecutionResult {
	<-ctx.Done()
	return ExecutionResult{}
}

type capturingExecutor struct {
	input  ExecutionInput
	result ExecutionResult
}

// Execute 执行该函数负责的核心处理逻辑。
func (e *capturingExecutor) Execute(_ context.Context, input ExecutionInput) ExecutionResult {
	e.input = input
	return e.result
}

type fakeEngineStore struct {
	mu              sync.Mutex
	work            *WorkItem
	recoverCalls    int
	attemptStarts   []AttemptStart
	attemptFinishes []AttemptFinish
	heartbeats      []LeaseHeartbeat
	commits         []OutcomeCommit
	retries         []RetryCommit
}

// RecoverExpired 执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) RecoverExpired(context.Context, time.Time) error {
	s.recoverCalls++
	return nil
}

// 声明执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) Claim(context.Context, ClaimRequest) (*WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.work
	s.work = nil
	return work, nil
}

// 心跳执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) Heartbeat(_ context.Context, heartbeat LeaseHeartbeat) error {
	s.heartbeats = append(s.heartbeats, heartbeat)
	return nil
}

// StartAttempt 执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) StartAttempt(_ context.Context, input AttemptStart) (string, error) {
	s.attemptStarts = append(s.attemptStarts, input)
	return "attempt-1", nil
}

// FinishAttempt 执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) FinishAttempt(_ context.Context, input AttemptFinish) error {
	s.attemptFinishes = append(s.attemptFinishes, input)
	return nil
}

// CommitOutcome 执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) CommitOutcome(_ context.Context, input OutcomeCommit) error {
	s.commits = append(s.commits, input)
	return nil
}

// ScheduleRetry 执行该函数负责的核心处理逻辑。
func (s *fakeEngineStore) ScheduleRetry(_ context.Context, input RetryCommit) error {
	s.retries = append(s.retries, input)
	return nil
}
