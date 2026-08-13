package outbox_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/storage/postgres/outbox"
	"agent_project/apps/server/internal/testsupport/postgrestest"
)

// TestRepositoryEnqueueClaimAndPublishRoundTrip 验证对应场景下的正常路径与失败路径。
func TestRepositoryEnqueueClaimAndPublishRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "outbox_repository", postgres.NewPool, postgres.RunMigrations)
	repo := outbox.NewRepository(pool)
	idempotencyKey := "agent-run-created-1"

	event, created, err := repo.Enqueue(ctx, nil, outbox.EnqueueParams{
		AggregateType:  "agent_run",
		AggregateID:    "run-1",
		EventType:      "agent.run.created",
		IdempotencyKey: &idempotencyKey,
		PayloadJSON:    json.RawMessage(`{"run_id":"run-1"}`),
	})
	if err != nil || !created {
		t.Fatalf("enqueue event: created=%v err=%v", created, err)
	}
	duplicate, created, err := repo.Enqueue(ctx, nil, outbox.EnqueueParams{
		AggregateType:  "agent_run",
		AggregateID:    "run-1",
		EventType:      "agent.run.created",
		IdempotencyKey: &idempotencyKey,
		PayloadJSON:    json.RawMessage(`{"run_id":"run-1"}`),
	})
	if err != nil || created || duplicate.ID != event.ID {
		t.Fatalf("idempotent enqueue: created=%v duplicate=%#v err=%v", created, duplicate, err)
	}

	now := time.Now().UTC()
	events, err := repo.Claim(ctx, outbox.ClaimParams{
		Now:           now,
		WorkerID:      "projection-worker-test",
		LeaseDuration: time.Minute,
		Limit:         10,
	})
	if err != nil || len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("claim events: events=%#v err=%v", events, err)
	}
	if err := repo.MarkPublished(ctx, outbox.PublishParams{
		EventID:         events[0].ID,
		WorkerID:        "projection-worker-test",
		LeaseGeneration: events[0].LeaseGeneration,
		PublishedAt:     now.Add(time.Second),
	}); err != nil {
		t.Fatalf("mark published: %v", err)
	}
}
