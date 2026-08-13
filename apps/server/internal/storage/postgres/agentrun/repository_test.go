package agentrun

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentruntime "agent_project/apps/server/internal/agent/runtime"
)

// TestCreateRunRejectsInvalidInputBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateRunRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, _, err := repo.CreateOrGetRun(context.Background(), CreateRunParams{
		Objective: " ",
		MaxSteps:  10,
	})
	if err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected objective validation error, got %v", err)
	}
}

// TestCreateStepRejectsInvalidJSONBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateStepRejectsInvalidJSONBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, _, err := repo.CreateOrGetStep(context.Background(), CreateStepParams{
		RunID:     "run-1",
		StepKey:   "understand_goal",
		StepType:  "UnderstandGoal",
		InputJSON: json.RawMessage(`{`),
	})
	if err == nil || !strings.Contains(err.Error(), "input_json") {
		t.Fatalf("expected input_json validation error, got %v", err)
	}
}

// TestCreateRunWithInitialStepRejectsInvalidStepBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateRunWithInitialStepRejectsInvalidStepBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	_, _, _, err := repo.CreateOrGetRunWithInitialStep(context.Background(), CreateRunParams{
		Objective: "shadow legacy task",
	}, CreateStepParams{
		StepKey: "understand_goal:1", StepType: "UnderstandGoal", InputJSON: json.RawMessage(`[]`),
	})
	if err == nil || !strings.Contains(err.Error(), "input_json") {
		t.Fatalf("expected initial step validation before transaction, got %v", err)
	}
}

// TestClaimStepSQLUsesSkipLockedLeaseGenerationAndRunGuards 验证对应场景下的正常路径与失败路径。
func TestClaimStepSQLUsesSkipLockedLeaseGenerationAndRunGuards(t *testing.T) {
	for _, fragment := range []string{
		"FOR UPDATE OF step, run SKIP LOCKED",
		"lease_generation = step.lease_generation + 1",
		"run.cancel_requested_at IS NOT NULL",
		"run.deadline_at <= $1",
		"step.next_retry_at IS NULL OR step.next_retry_at <= $1",
		"run.runtime_mode = 'durable'",
		"run.principal_type IS NOT NULL",
		"run.principal_id IS NOT NULL",
		"run.trust_source IS NOT NULL",
		"run.workspace_id IS NOT NULL",
	} {
		if !strings.Contains(claimStepSQL, fragment) {
			t.Fatalf("claim SQL must contain %q", fragment)
		}
	}
}

// TestSameRunRequestRejectsChangedExecutionBudgets 验证对应场景下的正常路径与失败路径。
func TestSameRunRequestRejectsChangedExecutionBudgets(t *testing.T) {
	run := Run{
		Objective:    "same objective",
		MaxSteps:     8,
		MaxToolCalls: 4,
	}
	params := CreateRunParams{
		Objective:    "same objective",
		MaxSteps:     9,
		MaxToolCalls: 4,
	}
	if sameRunRequest(run, params) {
		t.Fatal("same request_id with different budgets must be an idempotency conflict")
	}
}

// TestTransitionRunRejectsInvalidStateChangeBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestTransitionRunRejectsInvalidStateChangeBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	err := repo.TransitionRun(context.Background(), TransitionRunParams{
		RunID:           "run-1",
		From:            "succeeded",
		To:              "running",
		ExpectedVersion: 1,
		At:              time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "transition") {
		t.Fatalf("expected transition validation error, got %v", err)
	}
}

// TestResumeWaitingStepRejectsMismatchedWaitingStatesBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestResumeWaitingStepRejectsMismatchedWaitingStatesBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	err := repo.ResumeWaitingStep(context.Background(), ResumeWaitingParams{
		RunID:              "run-1",
		StepID:             "step-1",
		ExpectedRunStatus:  agentruntime.RunStatusWaitingApproval,
		ExpectedStepStatus: agentruntime.StepStatusWaitingInput,
		At:                 time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "matching waiting states") {
		t.Fatalf("expected matching waiting state validation error, got %v", err)
	}
}

// TestStepLeaseMutationsGuardGenerationOwnerAndExpiry 验证对应场景下的正常路径与失败路径。
func TestStepLeaseMutationsGuardGenerationOwnerAndExpiry(t *testing.T) {
	for name, statement := range map[string]string{
		"heartbeat": heartbeatStepSQL,
		"complete":  completeStepSQL,
		"retry":     retryStepSQL,
	} {
		for _, fragment := range []string{"claimed_by = $2", "lease_generation = $3", "lease_expires_at >"} {
			if !strings.Contains(statement, fragment) {
				t.Fatalf("%s SQL must contain %q", name, fragment)
			}
		}
	}
}

// TestExpiredLeaseRecoveryClosesAbandonedAttempt 验证对应场景下的正常路径与失败路径。
func TestExpiredLeaseRecoveryClosesAbandonedAttempt(t *testing.T) {
	for _, fragment := range []string{
		"UPDATE agent_attempts AS attempt",
		"attempt.completed_at IS NULL",
		"error_category = 'lease_expired'",
	} {
		if !strings.Contains(recoverExpiredStepsSQL, fragment) {
			t.Fatalf("expired lease recovery SQL must contain %q", fragment)
		}
	}
}

// TestCancellationWakesPersistedWaitingStep 验证对应场景下的正常路径与失败路径。
func TestCancellationWakesPersistedWaitingStep(t *testing.T) {
	for _, fragment := range []string{
		"status = CASE WHEN status IN ('waiting_input', 'waiting_approval') THEN 'queued' ELSE status END",
		"UPDATE agent_steps",
		"status IN ('waiting_input', 'waiting_approval')",
		"next_retry_at = $2",
		"cancel_requested_at IS NULL",
		"EXISTS (SELECT 1 FROM existing)",
	} {
		if !strings.Contains(requestCancelSQL, fragment) {
			t.Fatalf("cancellation SQL must contain %q", fragment)
		}
	}
}

// TestHeartbeatRejectsInvalidLeaseBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestHeartbeatRejectsInvalidLeaseBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	err := repo.HeartbeatStep(context.Background(), HeartbeatParams{
		StepID:          "step-1",
		WorkerID:        "worker-1",
		LeaseGeneration: 0,
		Now:             time.Now(),
		LeaseDuration:   time.Minute,
	})
	if err == nil || !strings.Contains(err.Error(), "lease_generation") {
		t.Fatalf("expected lease generation validation error, got %v", err)
	}
}

// TestCreateAttemptRejectsInvalidAttemptNumberBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateAttemptRejectsInvalidAttemptNumberBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, err := repo.CreateAttempt(context.Background(), CreateAttemptParams{
		StepID:        "step-1",
		AttemptNumber: 0,
		StartedAt:     time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "attempt_number") {
		t.Fatalf("expected attempt number validation error, got %v", err)
	}
}

// TestCreateContextManifestRejectsNonArrayItemsBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateContextManifestRejectsNonArrayItemsBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, err := repo.CreateContextManifest(context.Background(), CreateContextManifestParams{
		RunID:                "run-1",
		StepID:               "step-1",
		TokenBudget:          1000,
		ReservedOutputTokens: 200,
		Tokenizer:            "test-tokenizer-v1",
		ItemsJSON:            json.RawMessage(`{"item":"not-an-array"}`),
		TotalTokens:          100,
		ContentHash:          "sha256:test",
	})
	if err == nil || !strings.Contains(err.Error(), "items_json") {
		t.Fatalf("expected manifest items validation error, got %v", err)
	}
}

// TestGetContextManifestRejectsEmptyIDBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestGetContextManifestRejectsEmptyIDBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	_, err := repo.GetContextManifest(context.Background(), " ")
	if err == nil || !strings.Contains(err.Error(), "manifest_id") {
		t.Fatalf("expected manifest identity validation, got %v", err)
	}
}

// TestListObservationsRejectsMissingRunBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestListObservationsRejectsMissingRunBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	if _, err := repo.ListObservations(context.Background(), " "); err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("expected run identity validation before database access, got %v", err)
	}
}

// TestCompareShadowOutputsCanonicalizesObjects 验证对应场景下的正常路径与失败路径。
func TestCompareShadowOutputsCanonicalizesObjects(t *testing.T) {
	comparison, err := CompareShadowOutputs(json.RawMessage(`{"b":2,"a":1}`), json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("compare outputs: %v", err)
	}
	if comparison.Status != ShadowMatched || comparison.LegacyOutputHash == nil || comparison.TypedOutputHash == nil ||
		*comparison.LegacyOutputHash != *comparison.TypedOutputHash {
		t.Fatalf("canonical comparison=%#v", comparison)
	}
}

// TestRecordShadowComparisonRejectsMissingRunBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestRecordShadowComparisonRejectsMissingRunBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	_, err := repo.RecordShadowComparison(context.Background(), ShadowComparisonParams{Status: ShadowUnavailable})
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("expected run identity validation before database access, got %v", err)
	}
}

// TestCreateToolCallRejectsInvalidInputBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateToolCallRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)

	_, _, err := repo.CreateOrGetToolCall(context.Background(), CreateToolCallParams{
		RunID:       "run-1",
		StepID:      "step-1",
		ToolName:    "document.read_nodes",
		ToolVersion: "1.0.0",
		InputJSON:   json.RawMessage(`[]`),
	})
	if err == nil || !strings.Contains(err.Error(), "input_json") {
		t.Fatalf("expected tool input validation error, got %v", err)
	}
}
