package agentturn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	turn "agent_project/apps/server/internal/agent/turn"
)

// TestAcceptRejectsHashMismatchBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestAcceptRejectsHashMismatchBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	_, _, err := repo.Accept(context.Background(), turn.AcceptInput{
		Request:   turn.Request{RequestID: "req-1", Message: "review"},
		InputJSON: json.RawMessage(`{"message":"review"}`),
		InputHash: "sha256:not-the-content-hash",
	})
	if err == nil || !strings.Contains(err.Error(), "input_hash") {
		t.Fatalf("expected input hash validation, got %v", err)
	}
}

// TestAcceptRejectsInvalidScopeIDBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestAcceptRejectsInvalidScopeIDBeforeDatabaseAccess(t *testing.T) {
	payload := json.RawMessage(`{"message":"review"}`)
	sum := sha256.Sum256(payload)
	repo := NewRepository(nil)
	_, _, err := repo.Accept(context.Background(), turn.AcceptInput{
		Request:   turn.Request{RequestID: "req-1", WorkspaceID: "not-a-uuid", Message: "review"},
		InputJSON: payload,
		InputHash: "sha256:" + hex.EncodeToString(sum[:]),
	})
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("expected workspace validation, got %v", err)
	}
}

// TestTurnInsertUsesImmutableIdempotencyScope 验证对应场景下的正常路径与失败路径。
func TestTurnInsertUsesImmutableIdempotencyScope(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO agent_turns",
		"idempotency_scope",
		"ON CONFLICT (idempotency_scope, request_id) DO NOTHING",
		"input_hash",
	} {
		if !strings.Contains(createTurnSQL, fragment) {
			t.Fatalf("turn insert must contain %q", fragment)
		}
	}
}

// TestNewTurnFactsAreCreatedWithinOneTransaction 验证对应场景下的正常路径与失败路径。
func TestNewTurnFactsAreCreatedWithinOneTransaction(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO assistant_messages",
		"INSERT INTO agent_runs",
		"INSERT INTO agent_steps",
		"UnderstandGoal",
		"INSERT INTO agent_turn_events",
		"INSERT INTO outbox_events",
	} {
		if !strings.Contains(createFactsSQL, fragment) {
			t.Fatalf("atomic turn facts SQL must contain %q", fragment)
		}
	}
}

// TestInitialTypedStepContainsOnlyNodeContractFields 验证对应场景下的正常路径与失败路径。
func TestInitialTypedStepContainsOnlyNodeContractFields(t *testing.T) {
	fragment := "jsonb_build_object('message', $4::jsonb ->> 'message', 'resource_id', $9::text)"
	if !strings.Contains(createFactsSQL, fragment) {
		t.Fatalf("initial typed step must strip transport and trusted identity fields; missing %q", fragment)
	}
}

// TestDurableTurnPersistsTrustedScopeAndResourceWithRunFacts 验证对应场景下的正常路径与失败路径。
func TestDurableTurnPersistsTrustedScopeAndResourceWithRunFacts(t *testing.T) {
	for _, fragment := range []string{
		"resource_id", "principal_type", "principal_id", "trust_source", "runtime_mode",
	} {
		if !strings.Contains(createTurnSQL, fragment) {
			t.Fatalf("turn insert must persist %q", fragment)
		}
		if !strings.Contains(createFactsSQL, fragment) {
			t.Fatalf("run facts must persist %q", fragment)
		}
	}
}

// TestAcceptRejectsInvalidDurableTrustBindingBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestAcceptRejectsInvalidDurableTrustBindingBeforeDatabaseAccess(t *testing.T) {
	payload := json.RawMessage(`{"message":"review","resource_id":"00000000-0000-4000-8000-000000000020"}`)
	sum := sha256.Sum256(payload)
	_, _, err := NewRepository(nil).Accept(context.Background(), turn.AcceptInput{
		Request: turn.Request{
			RequestID: "req-1", Message: "review", RuntimeMode: "durable",
			WorkspaceID:   "00000000-0000-4000-8000-000000000010",
			ResourceID:    "00000000-0000-4000-8000-000000000020",
			PrincipalType: "user", PrincipalID: "", TrustSource: "edge-hmac-v1",
		},
		InputJSON: payload, InputHash: "sha256:" + hex.EncodeToString(sum[:]),
	})
	if err == nil || !strings.Contains(err.Error(), "principal") {
		t.Fatalf("expected invalid durable trust binding, got %v", err)
	}
}

// TestOutcomeCommitUsesPersistedIdempotencyAndTransactionalFacts 验证对应场景下的正常路径与失败路径。
func TestOutcomeCommitUsesPersistedIdempotencyAndTransactionalFacts(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO agent_turn_outcomes",
		"ON CONFLICT (turn_id, idempotency_key) DO NOTHING",
		"outcome_hash",
	} {
		if !strings.Contains(createOutcomeSQL, fragment) {
			t.Fatalf("outcome insert must contain %q", fragment)
		}
	}
	for _, fragment := range []string{
		"INSERT INTO assistant_messages",
		"INSERT INTO agent_turn_events",
		"INSERT INTO outbox_events",
		"INSERT INTO agent_turn_public_projections",
		"ON CONFLICT (turn_id) DO UPDATE",
		"last_event_sequence",
		"UPDATE agent_turns",
		"'id', inserted_messages.id",
		"'sequence_no', inserted_messages.sequence_no",
		"'created_at', inserted_messages.created_at",
	} {
		if !strings.Contains(createOutcomeFactsSQL, fragment) {
			t.Fatalf("outcome facts must contain %q", fragment)
		}
	}
}

// TestCommitRejectsForgedOutcomeHashBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCommitRejectsForgedOutcomeHashBeforeDatabaseAccess(t *testing.T) {
	prepared, err := turn.PrepareOutcome(turn.Outcome{
		TurnID: "00000000-0000-0000-0000-000000000001", IdempotencyKey: "render:1",
		Status: turn.StatusSucceeded, OutputJSON: json.RawMessage(`{"answer":"done"}`),
	})
	if err != nil {
		t.Fatalf("prepare outcome: %v", err)
	}
	prepared.OutcomeHash = "sha256:forged"
	_, _, err = NewRepository(nil).Commit(context.Background(), prepared)
	if err == nil || !strings.Contains(err.Error(), "outcome_hash") {
		t.Fatalf("expected forged hash rejection, got %v", err)
	}
}

// TestOrganizationScopeRemainsStableWhenRetryIncludesCreatedSession 验证对应场景下的正常路径与失败路径。
func TestOrganizationScopeRemainsStableWhenRetryIncludesCreatedSession(t *testing.T) {
	organizationID := "00000000-0000-0000-0000-000000000010"
	sessionID := "00000000-0000-0000-0000-000000000011"
	payload := json.RawMessage(`{"message":"review"}`)
	sum := sha256.Sum256(payload)
	base := turn.AcceptInput{
		Request:   turn.Request{RequestID: "req-1", OrganizationID: organizationID, Message: "review"},
		InputJSON: payload, InputHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
	_, firstScope, err := prepare(base)
	if err != nil {
		t.Fatalf("prepare first request: %v", err)
	}
	base.Request.SessionID = sessionID
	_, retryScope, err := prepare(base)
	if err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if retryScope != firstScope {
		t.Fatalf("idempotency scope changed from %q to %q", firstScope, retryScope)
	}
}
