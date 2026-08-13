package projection_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agent_project/apps/server/internal/agent/projection"
	agentturn "agent_project/apps/server/internal/agent/turn"
	"agent_project/apps/server/internal/storage/postgres/outbox"
)

// TestRuntimeProjectorCommitsRenderOutcomeAsTypedAssistantMessage 验证对应场景下的正常路径与失败路径。
func TestRuntimeProjectorCommitsRenderOutcomeAsTypedAssistantMessage(t *testing.T) {
	reader := &fakeSnapshotReader{snapshot: projection.RuntimeSnapshot{
		TurnID: "turn-1", RunID: "run-1", RunStatus: "succeeded", StepType: "RenderOutcome",
		OutputJSON: json.RawMessage(`{"message":"revision complete"}`),
	}}
	committer := &fakeOutcomeCommitter{}
	receipts := &fakeReceiptStore{}
	projector := mustRuntimeProjector(t, reader, committer, receipts)
	event := runtimeEvent("event-1", "agent.step.outcome_committed")

	if err := projector.Project(context.Background(), event); err != nil {
		t.Fatalf("project terminal outcome: %v", err)
	}
	if committer.calls != 1 || committer.last.Status != agentturn.StatusSucceeded || len(committer.last.Messages) != 1 {
		t.Fatalf("unexpected projected outcome: calls=%d outcome=%#v", committer.calls, committer.last)
	}
	if string(committer.last.Messages[0].Payload) != `{"content":"revision complete"}` {
		t.Fatalf("unexpected public message: %s", committer.last.Messages[0].Payload)
	}
}

// TestRuntimeProjectorReplaysSafelyAfterOutcomeBeforeReceiptCrash 验证对应场景下的正常路径与失败路径。
func TestRuntimeProjectorReplaysSafelyAfterOutcomeBeforeReceiptCrash(t *testing.T) {
	reader := &fakeSnapshotReader{snapshot: projection.RuntimeSnapshot{
		TurnID: "turn-1", RunID: "run-1", RunStatus: "waiting_approval", StepType: "RequestApproval",
		OutputJSON: json.RawMessage(`{"approval_id":"approval-1","status":"pending"}`),
	}}
	committer := &fakeOutcomeCommitter{}
	receipts := &fakeReceiptStore{failFirstRecord: true}
	projector := mustRuntimeProjector(t, reader, committer, receipts)
	event := runtimeEvent("event-1", "agent.step.outcome_committed")

	if err := projector.Project(context.Background(), event); err == nil {
		t.Fatal("expected injected receipt failure")
	}
	if err := projector.Project(context.Background(), event); err != nil {
		t.Fatalf("replay projection: %v", err)
	}
	if committer.sideEffects != 1 || receipts.records != 1 {
		t.Fatalf("replay duplicated outcome or missed receipt: effects=%d receipts=%d", committer.sideEffects, receipts.records)
	}
}

// TestRuntimeProjectorMapsExternalApprovalRejectionToDeterministicFailure 验证对应场景下的正常路径与失败路径。
func TestRuntimeProjectorMapsExternalApprovalRejectionToDeterministicFailure(t *testing.T) {
	reader := &fakeSnapshotReader{snapshot: projection.RuntimeSnapshot{
		TurnID: "turn-1", RunID: "run-1", RunStatus: "failed", StepType: "RequestApproval",
		ErrorJSON: json.RawMessage(`{"category":"policy_blocked","message":"external approval was rejected"}`),
	}}
	committer := &fakeOutcomeCommitter{}
	projector := mustRuntimeProjector(t, reader, committer, &fakeReceiptStore{})

	if err := projector.Project(context.Background(), runtimeEvent("event-rejected", "agent.tool_approval.rejected")); err != nil {
		t.Fatalf("project approval rejection: %v", err)
	}
	if committer.last.Status != agentturn.StatusFailed || len(committer.last.Messages) != 0 {
		t.Fatalf("rejected approval must be deterministic failure without model message: %#v", committer.last)
	}
}

// mustRuntimeProjector 执行该函数负责的核心处理逻辑。
func mustRuntimeProjector(t *testing.T, reader projection.RuntimeSnapshotReader, committer projection.OutcomeCommitter, receipts projection.ReceiptStore) *projection.RuntimeProjector {
	t.Helper()
	projector, err := projection.NewRuntimeProjector(reader, committer, receipts)
	if err != nil {
		t.Fatalf("new runtime projector: %v", err)
	}
	return projector
}

// runtimeEvent 执行该函数负责的核心处理逻辑。
func runtimeEvent(id, eventType string) outbox.Event {
	key := "key-" + id
	return outbox.Event{
		ID: id, AggregateType: "agent_run", AggregateID: "run-1", EventType: eventType,
		IdempotencyKey: &key, PayloadJSON: json.RawMessage(`{"run_id":"run-1","step_id":"step-1"}`),
	}
}

type fakeSnapshotReader struct {
	snapshot projection.RuntimeSnapshot
	err      error
}

// 加载按作用域读取并返回所需数据。
func (reader *fakeSnapshotReader) Load(context.Context, outbox.Event) (projection.RuntimeSnapshot, error) {
	return reader.snapshot, reader.err
}

type fakeOutcomeCommitter struct {
	calls       int
	sideEffects int
	seen        map[string]struct{}
	last        agentturn.Outcome
}

// CommitOutcome 执行该函数负责的核心处理逻辑。
func (committer *fakeOutcomeCommitter) CommitOutcome(_ context.Context, outcome agentturn.Outcome) (agentturn.OutcomeResult, error) {
	committer.calls++
	committer.last = outcome
	if committer.seen == nil {
		committer.seen = make(map[string]struct{})
	}
	if _, exists := committer.seen[outcome.IdempotencyKey]; exists {
		return agentturn.OutcomeResult{}, nil
	}
	committer.seen[outcome.IdempotencyKey] = struct{}{}
	committer.sideEffects++
	return agentturn.OutcomeResult{Created: true}, nil
}

type fakeReceiptStore struct {
	seen            map[string]string
	records         int
	failFirstRecord bool
}

// Exists 执行该函数负责的核心处理逻辑。
func (store *fakeReceiptStore) Exists(_ context.Context, eventID, name string) (bool, error) {
	_, exists := store.seen[eventID+":"+name]
	return exists, nil
}

// 记录按领域约束持久化数据。
func (store *fakeReceiptStore) Record(_ context.Context, eventID, name, hash string) error {
	if store.failFirstRecord {
		store.failFirstRecord = false
		return errors.New("注入的回执失败")
	}
	if store.seen == nil {
		store.seen = make(map[string]string)
	}
	key := eventID + ":" + name
	if previous, exists := store.seen[key]; exists && previous != hash {
		return errors.New("处理失败：回执 conflict")
	}
	if _, exists := store.seen[key]; !exists {
		store.records++
	}
	store.seen[key] = hash
	return nil
}
