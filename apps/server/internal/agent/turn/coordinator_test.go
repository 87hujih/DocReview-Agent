package turn_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentturn "agent_project/apps/server/internal/agent/turn"
)

// TestSubmitRequiresRequestIDBeforeStoreAccess 验证对应场景下的正常路径与失败路径。
func TestSubmitRequiresRequestIDBeforeStoreAccess(t *testing.T) {
	store := &fakeStore{}
	coordinator := agentturn.NewCoordinator(store)

	_, err := coordinator.Submit(context.Background(), agentturn.Request{Message: "review this"})
	if !errors.Is(err, agentturn.ErrInvalidRequest) {
		t.Fatalf("expected invalid request, got %v", err)
	}
	if store.acceptCalls != 0 {
		t.Fatalf("unsafe request reached store %d times", store.acceptCalls)
	}
}

// TestDuplicateRequestReusesPersistedTurnAndEvents 验证对应场景下的正常路径与失败路径。
func TestDuplicateRequestReusesPersistedTurnAndEvents(t *testing.T) {
	store := &fakeStore{
		turn:   agentturn.Turn{ID: "turn-1", SessionID: "session-1", RunID: "run-1", Status: agentturn.StatusAccepted},
		events: []agentturn.Event{{TurnID: "turn-1", Sequence: 1, Type: "turn.accepted"}},
	}
	coordinator := agentturn.NewCoordinator(store)
	req := agentturn.Request{RequestID: "req-1", SessionID: "session-1", Message: "review this"}

	first, err := coordinator.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	second, err := coordinator.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("duplicate submit: %v", err)
	}
	if !first.Created || second.Created {
		t.Fatalf("created flags = first %v, second %v", first.Created, second.Created)
	}
	if first.Turn.ID != second.Turn.ID || len(second.Events) != 1 {
		t.Fatalf("duplicate did not replay persisted turn: %#v", second)
	}
	if store.createdFacts != 1 {
		t.Fatalf("request retry created %d durable facts, want 1", store.createdFacts)
	}
}

// TestSameRequestIDWithDifferentInputIsConflict 验证对应场景下的正常路径与失败路径。
func TestSameRequestIDWithDifferentInputIsConflict(t *testing.T) {
	store := &fakeStore{turn: agentturn.Turn{ID: "turn-1", Status: agentturn.StatusAccepted}}
	coordinator := agentturn.NewCoordinator(store)

	if _, err := coordinator.Submit(context.Background(), agentturn.Request{RequestID: "req-1", Message: "first"}); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	_, err := coordinator.Submit(context.Background(), agentturn.Request{RequestID: "req-1", Message: "changed"})
	if !errors.Is(err, agentturn.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

// TestRequestIdentityResourceAndModeArePartOfImmutableTurnInput 验证对应场景下的正常路径与失败路径。
func TestRequestIdentityResourceAndModeArePartOfImmutableTurnInput(t *testing.T) {
	store := &fakeStore{turn: agentturn.Turn{ID: "turn-1", Status: agentturn.StatusAccepted}}
	coordinator := agentturn.NewCoordinator(store)
	request := agentturn.Request{
		RequestID: "req-1", Message: "revise this", RuntimeMode: "durable",
		OrganizationID: "11111111-1111-4111-8111-111111111111",
		WorkspaceID:    "22222222-2222-4222-8222-222222222222",
		ResourceID:     "33333333-3333-4333-8333-333333333333",
		PrincipalType:  "user", PrincipalID: "44444444-4444-4444-8444-444444444444",
		TrustSource: "edge-hmac-v1",
	}
	if _, err := coordinator.Submit(context.Background(), request); err != nil {
		t.Fatalf("submit trusted turn: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(store.lastAccept.InputJSON, &persisted); err != nil {
		t.Fatalf("decode accepted input: %v", err)
	}
	for _, field := range []string{"message", "resource_id", "principal_type", "principal_id", "trust_source", "runtime_mode"} {
		if persisted[field] == nil || persisted[field] == "" {
			t.Fatalf("expected immutable %s in accepted input, got %s", field, store.lastAccept.InputJSON)
		}
	}

	request.ResourceID = "55555555-5555-4555-8555-555555555555"
	if _, err := coordinator.Submit(context.Background(), request); !errors.Is(err, agentturn.ErrIdempotencyConflict) {
		t.Fatalf("same request_id with changed resource must conflict, got %v", err)
	}
}

// TestStreamInterruptionLeavesReplayableDurableTurn 验证对应场景下的正常路径与失败路径。
func TestStreamInterruptionLeavesReplayableDurableTurn(t *testing.T) {
	store := &fakeStore{
		turn: agentturn.Turn{ID: "turn-1", RunID: "run-1", Status: agentturn.StatusAccepted},
		events: []agentturn.Event{
			{TurnID: "turn-1", Sequence: 1, Type: "turn.accepted"},
			{TurnID: "turn-1", Sequence: 2, Type: "run.queued"},
		},
	}
	coordinator := agentturn.NewCoordinator(store)
	req := agentturn.Request{RequestID: "req-1", Message: "review this"}

	stop := errors.New("处理失败：客户端已断开连接")
	err := coordinator.Stream(context.Background(), req, 0, func(event agentturn.Event) error {
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("expected observer interruption, got %v", err)
	}

	var replayed []agentturn.Event
	if err := coordinator.Stream(context.Background(), req, 0, func(event agentturn.Event) error {
		replayed = append(replayed, event)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replayed) != 2 || store.createdFacts != 1 {
		t.Fatalf("replay=%#v created facts=%d", replayed, store.createdFacts)
	}
}

// TestOutcomeCommitIsIdempotentAndRejectsChangedReplay 验证对应场景下的正常路径与失败路径。
func TestOutcomeCommitIsIdempotentAndRejectsChangedReplay(t *testing.T) {
	store := &fakeStore{turn: agentturn.Turn{ID: "turn-1", Status: agentturn.StatusAccepted}}
	coordinator := agentturn.NewCoordinator(store)
	outcome := agentturn.Outcome{
		TurnID: "turn-1", IdempotencyKey: "render:run-1",
		Status:     agentturn.StatusSucceeded,
		OutputJSON: []byte(`{"answer":"done"}`),
		Messages:   []agentturn.Message{{Role: "assistant", Kind: "text", Payload: []byte(`{"content":"done"}`)}},
	}

	first, err := coordinator.CommitOutcome(context.Background(), outcome)
	if err != nil || !first.Created {
		t.Fatalf("first commit: result=%#v err=%v", first, err)
	}
	second, err := coordinator.CommitOutcome(context.Background(), outcome)
	if err != nil || second.Created || store.committedFacts != 1 {
		t.Fatalf("idempotent commit: result=%#v facts=%d err=%v", second, store.committedFacts, err)
	}
	outcome.OutputJSON = []byte(`{"answer":"changed"}`)
	_, err = coordinator.CommitOutcome(context.Background(), outcome)
	if !errors.Is(err, agentturn.ErrIdempotencyConflict) {
		t.Fatalf("changed outcome replay must conflict, got %v", err)
	}
}

type fakeStore struct {
	turn           agentturn.Turn
	events         []agentturn.Event
	acceptedHash   string
	acceptCalls    int
	createdFacts   int
	outcomeHash    string
	committedFacts int
	lastAccept     agentturn.AcceptInput
}

// Commit 执行该函数负责的核心处理逻辑。
func (s *fakeStore) Commit(_ context.Context, input agentturn.CommitInput) (agentturn.Turn, bool, error) {
	if s.outcomeHash != "" {
		if s.outcomeHash != input.OutcomeHash {
			return agentturn.Turn{}, false, agentturn.ErrIdempotencyConflict
		}
		return s.turn, false, nil
	}
	s.outcomeHash = input.OutcomeHash
	s.committedFacts++
	s.turn.Status = input.Outcome.Status
	return s.turn, true, nil
}

// Accept 执行该函数负责的核心处理逻辑。
func (s *fakeStore) Accept(_ context.Context, input agentturn.AcceptInput) (agentturn.Turn, bool, error) {
	s.acceptCalls++
	s.lastAccept = input
	if s.acceptedHash != "" {
		if s.acceptedHash != input.InputHash {
			return agentturn.Turn{}, false, agentturn.ErrIdempotencyConflict
		}
		return s.turn, false, nil
	}
	s.acceptedHash = input.InputHash
	s.createdFacts++
	return s.turn, true, nil
}

// 事件 执行该函数负责的核心处理逻辑。
func (s *fakeStore) Events(_ context.Context, _ string, afterSequence int) ([]agentturn.Event, error) {
	var result []agentturn.Event
	for _, event := range s.events {
		if event.Sequence > afterSequence {
			result = append(result, event)
		}
	}
	return result, nil
}
