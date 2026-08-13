package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestProjectionWorkerRecoversExpiredLeasesBeforeClaiming 验证对应场景下的正常路径与失败路径。
func TestProjectionWorkerRecoversExpiredLeasesBeforeClaiming(t *testing.T) {
	store := &fakeWorkerStore{}
	worker := mustProjectionWorker(t, store, &idempotentProjector{})
	ctx, cancel := context.WithCancel(context.Background())
	store.onClaim = cancel

	err := worker.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled worker, got %v", err)
	}
	if store.recoverCalls != 1 || store.claimCalls != 1 || store.recoveredAfterClaim {
		t.Fatalf("startup recovery must precede claim: %#v", store)
	}
}

// TestProjectionWorkerUsesEventIdentityForIdempotentReplay 验证对应场景下的正常路径与失败路径。
func TestProjectionWorkerUsesEventIdentityForIdempotentReplay(t *testing.T) {
	event := testOutboxEvent(1)
	store := &fakeWorkerStore{claims: [][]Event{{event}, {event}}}
	projector := &idempotentProjector{}
	worker := mustProjectionWorker(t, store, projector)

	if worked, err := worker.ProcessOne(context.Background()); err != nil || !worked {
		t.Fatalf("first projection: worked=%v err=%v", worked, err)
	}
	if worked, err := worker.ProcessOne(context.Background()); err != nil || !worked {
		t.Fatalf("replayed projection: worked=%v err=%v", worked, err)
	}
	if projector.sideEffects != 1 || store.published != 2 {
		t.Fatalf("replay must not duplicate projection side effect, effects=%d published=%d", projector.sideEffects, store.published)
	}
}

// TestProjectionWorkerRetriesThenDeadLettersAtBound 验证对应场景下的正常路径与失败路径。
func TestProjectionWorkerRetriesThenDeadLettersAtBound(t *testing.T) {
	retryEvent := testOutboxEvent(1)
	deadEvent := testOutboxEvent(3)
	store := &fakeWorkerStore{claims: [][]Event{{retryEvent}, {deadEvent}}}
	projector := &idempotentProjector{err: errors.New("处理失败：投影不可用")}
	worker := mustProjectionWorker(t, store, projector)

	if _, err := worker.ProcessOne(context.Background()); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	if len(store.retries) != 1 || store.retries[0].DeadLetter || !store.retries[0].NextAttemptAt.After(store.retries[0].Now) {
		t.Fatalf("expected bounded retry, got %#v", store.retries)
	}
	if _, err := worker.ProcessOne(context.Background()); err != nil {
		t.Fatalf("dead letter: %v", err)
	}
	if len(store.retries) != 2 || !store.retries[1].DeadLetter {
		t.Fatalf("expected dead-letter at max attempts, got %#v", store.retries)
	}
}

// mustProjectionWorker 执行该函数负责的核心处理逻辑。
func mustProjectionWorker(t *testing.T, store ProjectionStore, projector Projector) *ProjectionWorker {
	t.Helper()
	worker, err := NewProjectionWorker(ProjectionWorkerConfig{
		WorkerID: "projection-1", LeaseDuration: time.Minute, PollInterval: time.Millisecond,
		ErrorBackoff: time.Millisecond, RecoveryInterval: time.Hour, BatchSize: 1,
		MaxAttempts: 3, RetryBase: time.Second, RetryMax: time.Minute,
	}, store, projector, fixedWorkerClock{now: time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("new projection worker: %v", err)
	}
	return worker
}

// testOutboxEvent 执行该函数负责的核心处理逻辑。
func testOutboxEvent(attempt int) Event {
	key := "projection:event-1"
	return Event{
		ID: "event-1", AggregateType: "agent_run", AggregateID: "run-1",
		EventType: "agent.step.outcome_committed", IdempotencyKey: &key,
		PayloadJSON: json.RawMessage(`{"run_id":"run-1"}`), Status: "publishing",
		AttemptCount: attempt, ClaimedBy: stringPointer("projection-1"), LeaseGeneration: int64(attempt),
	}
}

// stringPointer 执行该函数负责的核心处理逻辑。
func stringPointer(value string) *string { return &value }

type fakeWorkerStore struct {
	claims              [][]Event
	recoverCalls        int
	claimCalls          int
	published           int
	retries             []RetryParams
	recoveredAfterClaim bool
	onClaim             func()
}

// RecoverExpiredLeases 执行该函数负责的核心处理逻辑。
func (store *fakeWorkerStore) RecoverExpiredLeases(context.Context, time.Time) (int64, error) {
	store.recoverCalls++
	if store.claimCalls > 0 {
		store.recoveredAfterClaim = true
	}
	return 0, nil
}

// 声明 执行该函数负责的核心处理逻辑。
func (store *fakeWorkerStore) Claim(_ context.Context, _ ClaimParams) ([]Event, error) {
	store.claimCalls++
	if store.onClaim != nil {
		store.onClaim()
	}
	if len(store.claims) == 0 {
		return nil, nil
	}
	result := store.claims[0]
	store.claims = store.claims[1:]
	return result, nil
}

// MarkPublished 执行该函数负责的核心处理逻辑。
func (store *fakeWorkerStore) MarkPublished(context.Context, PublishParams) error {
	store.published++
	return nil
}

// ScheduleRetry 执行该函数负责的核心处理逻辑。
func (store *fakeWorkerStore) ScheduleRetry(_ context.Context, params RetryParams) error {
	store.retries = append(store.retries, params)
	return nil
}

type idempotentProjector struct {
	seen        map[string]struct{}
	sideEffects int
	err         error
}

// 投影 执行该函数负责的核心处理逻辑。
func (projector *idempotentProjector) Project(_ context.Context, event Event) error {
	if projector.err != nil {
		return projector.err
	}
	if projector.seen == nil {
		projector.seen = make(map[string]struct{})
	}
	if _, exists := projector.seen[event.ID]; exists {
		return nil
	}
	projector.seen[event.ID] = struct{}{}
	projector.sideEffects++
	return nil
}

type fixedWorkerClock struct{ now time.Time }

// Now 执行该函数负责的核心处理逻辑。
func (clock fixedWorkerClock) Now() time.Time { return clock.now }
