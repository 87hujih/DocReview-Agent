package outbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestEnqueueRejectsInvalidPayloadBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestEnqueueRejectsInvalidPayloadBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, _, err := repo.Enqueue(context.Background(), nil, EnqueueParams{
		AggregateType: "agent_run",
		AggregateID:   "run-1",
		EventType:     "agent.run.created",
		PayloadJSON:   json.RawMessage(`{`),
	})
	if err == nil || !strings.Contains(err.Error(), "payload_json") {
		t.Fatalf("expected payload validation error, got %v", err)
	}
}

// TestEnqueueRequiresIdempotencyKeyBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestEnqueueRequiresIdempotencyKeyBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, _, err := repo.Enqueue(context.Background(), nil, EnqueueParams{
		AggregateType: "agent_run",
		AggregateID:   "run-1",
		EventType:     "agent.run.created",
		PayloadJSON:   json.RawMessage(`{"run_id":"run-1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "idempotency_key") {
		t.Fatalf("expected idempotency key validation error, got %v", err)
	}
}

// TestClaimOutboxSQLUsesSkipLockedAndLease 验证对应场景下的正常路径与失败路径。
func TestClaimOutboxSQLUsesSkipLockedAndLease(t *testing.T) {
	for _, fragment := range []string{
		"FOR UPDATE OF event SKIP LOCKED",
		"event.next_attempt_at IS NULL OR event.next_attempt_at <= $1",
		"lease_expires_at = $3",
		"status = 'publishing'",
		"event.event_type = ANY($5::text[])",
	} {
		if !strings.Contains(claimEventsSQL, fragment) {
			t.Fatalf("outbox claim SQL must contain %q", fragment)
		}
	}
}

// TestClaimRejectsBlankProjectionEventFilterBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestClaimRejectsBlankProjectionEventFilterBeforeDatabaseAccess(t *testing.T) {
	_, err := NewRepository(nil).Claim(context.Background(), ClaimParams{
		Now: time.Now().UTC(), WorkerID: "projection-1", LeaseDuration: time.Minute,
		Limit: 1, EventTypes: []string{"agent.step.outcome_committed", " "},
	})
	if err == nil || !strings.Contains(err.Error(), "event type") {
		t.Fatalf("expected event filter validation, got %v", err)
	}
}

// TestOutboxCompletionSQLGuardsGenerationOwnerAndExpiry 验证对应场景下的正常路径与失败路径。
func TestOutboxCompletionSQLGuardsGenerationOwnerAndExpiry(t *testing.T) {
	for name, statement := range map[string]string{
		"publish": markPublishedSQL,
		"retry":   scheduleRetrySQL,
	} {
		for _, fragment := range []string{"claimed_by = $2", "lease_generation = $3", "lease_expires_at >"} {
			if !strings.Contains(statement, fragment) {
				t.Fatalf("%s SQL must contain %q", name, fragment)
			}
		}
	}
}
