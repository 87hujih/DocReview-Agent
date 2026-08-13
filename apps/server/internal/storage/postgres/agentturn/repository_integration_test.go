package agentturn_test

import (
	"context"
	"errors"
	"testing"
	"time"

	turn "agent_project/apps/server/internal/agent/turn"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/storage/postgres/agentturn"
	"agent_project/apps/server/internal/testsupport/postgrestest"
)

// TestAcceptAtomicallyCreatesOneMessageRunStepAndReplayableEvents 验证对应场景下的正常路径与失败路径。
func TestAcceptAtomicallyCreatesOneMessageRunStepAndReplayableEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "agent_turn_accept", postgres.NewPool, postgres.RunMigrations)
	coordinator := turn.NewCoordinator(agentturn.NewRepository(pool))
	request := turn.Request{RequestID: "request-turn-atomic-1", Message: "review the selected section"}

	first, err := coordinator.Submit(ctx, request)
	if err != nil || !first.Created || first.Turn.ID == "" || first.Turn.SessionID == "" || first.Turn.RunID == "" {
		t.Fatalf("first submit: result=%#v err=%v", first, err)
	}
	second, err := coordinator.Submit(ctx, request)
	if err != nil || second.Created || second.Turn.ID != first.Turn.ID {
		t.Fatalf("idempotent submit: result=%#v err=%v", second, err)
	}
	if len(second.Events) != 2 || second.Events[0].Sequence != 1 || second.Events[1].Sequence != 2 {
		t.Fatalf("persisted events = %#v", second.Events)
	}

	for table, query := range map[string]string{
		"turn":    `SELECT count(*) FROM agent_turns WHERE id = $1`,
		"message": `SELECT count(*) FROM assistant_messages WHERE turn_id = $1`,
		"run":     `SELECT count(*) FROM agent_runs WHERE turn_id = $1`,
		"step":    `SELECT count(*) FROM agent_steps AS step JOIN agent_runs AS run ON run.id = step.run_id WHERE run.turn_id = $1`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query, first.Turn.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s facts: count=%d err=%v", table, count, err)
		}
	}

	outcome := turn.Outcome{
		TurnID: first.Turn.ID, IdempotencyKey: "render:" + first.Turn.RunID,
		Status: turn.StatusSucceeded, OutputJSON: []byte(`{"answer":"done"}`),
		Messages: []turn.Message{{Role: "assistant", Kind: "text", Payload: []byte(`{"content":"done"}`)}},
	}
	committed, err := coordinator.CommitOutcome(ctx, outcome)
	if err != nil || !committed.Created || committed.Turn.Status != turn.StatusSucceeded {
		t.Fatalf("commit outcome: result=%#v err=%v", committed, err)
	}
	replayed, err := coordinator.CommitOutcome(ctx, outcome)
	if err != nil || replayed.Created || replayed.Turn.ID != first.Turn.ID {
		t.Fatalf("replay outcome: result=%#v err=%v", replayed, err)
	}
	var outcomeCount, assistantMessageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_turn_outcomes WHERE turn_id = $1`, first.Turn.ID).Scan(&outcomeCount); err != nil {
		t.Fatalf("count outcomes: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assistant_messages WHERE outcome_id IS NOT NULL AND turn_id = $1`, first.Turn.ID).Scan(&assistantMessageCount); err != nil {
		t.Fatalf("count outcome messages: %v", err)
	}
	if outcomeCount != 1 || assistantMessageCount != 1 {
		t.Fatalf("outcome facts = outcomes %d messages %d", outcomeCount, assistantMessageCount)
	}

	_, err = coordinator.Submit(ctx, turn.Request{RequestID: request.RequestID, Message: "changed input"})
	if !errors.Is(err, turn.ErrIdempotencyConflict) {
		t.Fatalf("changed retry must conflict, got %v", err)
	}
}
