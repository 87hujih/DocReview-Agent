package cutover_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/cutover"
	agentturn "agent_project/apps/server/internal/agent/turn"
)

// TestDurableRunnerResumesInterruptedStreamFromPersistedCursor 验证对应场景下的正常路径与失败路径。
func TestDurableRunnerResumesInterruptedStreamFromPersistedCursor(t *testing.T) {
	coordinator := newFakeTurnCoordinator()
	projector := &fakePublicProjector{}
	runner := mustDurableRunner(t, coordinator, projector)
	request := validRequest()

	interrupted := errors.New("处理失败：客户端已断开连接")
	err := func() error {
		_, err := runner.Execute(context.Background(), request, func(event cutover.Event) error {
			if event.Sequence == 3 {
				return interrupted
			}
			return nil
		})
		return err
	}()
	if !errors.Is(err, interrupted) {
		t.Fatalf("expected observer interruption, got %v", err)
	}

	request.AfterSequence = 2
	var resumed []int
	result, err := runner.Execute(context.Background(), request, func(event cutover.Event) error {
		resumed = append(resumed, event.Sequence)
		return nil
	})
	if err != nil {
		t.Fatalf("resume durable stream: %v", err)
	}
	if len(resumed) != 2 || resumed[0] != 3 || resumed[1] != 4 {
		t.Fatalf("expected persisted cursor replay [3 4], got %v", resumed)
	}
	if string(result.DTO) != `{"status":"succeeded","turn_id":"turn-1"}` {
		t.Fatalf("unexpected projected DTO: %s", result.DTO)
	}
	if coordinator.createdFacts != 1 {
		t.Fatalf("same request retry must not create duplicate message/turn/run, facts=%d", coordinator.createdFacts)
	}
}

// TestDurableRunnerNonStreamWaitsForSamePersistedTerminalEvent 验证对应场景下的正常路径与失败路径。
func TestDurableRunnerNonStreamWaitsForSamePersistedTerminalEvent(t *testing.T) {
	coordinator := newFakeTurnCoordinator()
	projector := &fakePublicProjector{}
	runner := mustDurableRunner(t, coordinator, projector)

	result, err := runner.Execute(context.Background(), validRequest(), nil)
	if err != nil {
		t.Fatalf("execute durable non-stream: %v", err)
	}
	if projector.calls != 1 || len(result.Events) != 4 || result.Events[3].Type != "turn.succeeded" {
		t.Fatalf("expected deterministic persisted outcome, calls=%d events=%#v", projector.calls, result.Events)
	}
}

// TestDurableRunnerDoesNotExposeAmbiguousAcceptedState 验证对应场景下的正常路径与失败路径。
func TestDurableRunnerDoesNotExposeAmbiguousAcceptedState(t *testing.T) {
	coordinator := newFakeTurnCoordinator()
	coordinator.terminal = nil
	runner, err := cutover.NewDurableRunner(cutover.DurableRunnerConfig{
		PollInterval: time.Millisecond, MaxWait: 2 * time.Millisecond,
	}, coordinator, &fakePublicProjector{})
	if err != nil {
		t.Fatalf("new durable runner: %v", err)
	}

	_, err = runner.Execute(context.Background(), validRequest(), nil)
	if !errors.Is(err, cutover.ErrTurnNotDeterministic) {
		t.Fatalf("expected deterministic-state timeout, got %v", err)
	}
}

// mustDurableRunner 执行该函数负责的核心处理逻辑。
func mustDurableRunner(t *testing.T, coordinator cutover.TurnCoordinator, projector cutover.PublicProjector) *cutover.DurableRunner {
	t.Helper()
	runner, err := cutover.NewDurableRunner(cutover.DurableRunnerConfig{
		PollInterval: time.Millisecond, MaxWait: 100 * time.Millisecond,
	}, coordinator, projector)
	if err != nil {
		t.Fatalf("new durable runner: %v", err)
	}
	return runner
}

type fakeTurnCoordinator struct {
	created      bool
	createdFacts int
	streamCalls  int
	initial      []agentturn.Event
	terminal     []agentturn.Event
}

// newFakeTurnCoordinator 执行该函数负责的核心处理逻辑。
func newFakeTurnCoordinator() *fakeTurnCoordinator {
	now := time.Now().UTC()
	return &fakeTurnCoordinator{
		initial: []agentturn.Event{
			{ID: "event-1", TurnID: "turn-1", Sequence: 1, Type: "turn.accepted", Payload: json.RawMessage(`{}`), CreatedAt: now},
			{ID: "event-2", TurnID: "turn-1", Sequence: 2, Type: "run.queued", Payload: json.RawMessage(`{}`), CreatedAt: now},
		},
		terminal: []agentturn.Event{
			{ID: "event-3", TurnID: "turn-1", Sequence: 3, Type: "assistant.message", Payload: json.RawMessage(`{"role":"assistant","kind":"text","payload":{"content":"done"}}`), CreatedAt: now},
			{ID: "event-4", TurnID: "turn-1", Sequence: 4, Type: "turn.succeeded", Payload: json.RawMessage(`{"status":"succeeded"}`), CreatedAt: now},
		},
	}
}

// Submit 执行该函数负责的核心处理逻辑。
func (coordinator *fakeTurnCoordinator) Submit(_ context.Context, request agentturn.Request) (agentturn.Result, error) {
	created := !coordinator.created
	if created {
		coordinator.created = true
		coordinator.createdFacts++
	}
	events := append([]agentturn.Event(nil), coordinator.initial...)
	if coordinator.streamCalls > 0 {
		events = append(events, coordinator.terminal...)
	}
	return agentturn.Result{Turn: agentturn.Turn{ID: "turn-1", RunID: "run-1", SessionID: "session-1", RequestID: request.RequestID}, Created: created, Events: events}, nil
}

// Stream 执行该函数负责的核心处理逻辑。
func (coordinator *fakeTurnCoordinator) Stream(_ context.Context, _ agentturn.Request, after int, observe func(agentturn.Event) error) error {
	coordinator.streamCalls++
	all := append(append([]agentturn.Event(nil), coordinator.initial...), coordinator.terminal...)
	for _, event := range all {
		if event.Sequence > after {
			if err := observe(event); err != nil {
				return err
			}
		}
	}
	return nil
}

type fakePublicProjector struct{ calls int }

// 投影执行该函数负责的核心处理逻辑。
func (projector *fakePublicProjector) Project(_ context.Context, turn agentturn.Turn, status agentturn.Status, _ []agentturn.Event) (json.RawMessage, error) {
	projector.calls++
	return json.Marshal(map[string]string{"status": string(status), "turn_id": turn.ID})
}
