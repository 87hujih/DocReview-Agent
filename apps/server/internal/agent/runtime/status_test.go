package runtime

import "testing"

// TestRunStatusTransitionsAreExplicitAndTerminalStatusesStayTerminal 验证对应场景下的正常路径与失败路径。
func TestRunStatusTransitionsAreExplicitAndTerminalStatusesStayTerminal(t *testing.T) {
	tests := []struct {
		from RunStatus
		to   RunStatus
		want bool
	}{
		{RunStatusQueued, RunStatusRunning, true},
		{RunStatusQueued, RunStatusCancelled, true},
		{RunStatusRunning, RunStatusQueued, true},
		{RunStatusRunning, RunStatusWaitingInput, true},
		{RunStatusRunning, RunStatusWaitingApproval, true},
		{RunStatusRunning, RunStatusSucceeded, true},
		{RunStatusWaitingInput, RunStatusQueued, true},
		{RunStatusWaitingApproval, RunStatusQueued, true},
		{RunStatusSucceeded, RunStatusRunning, false},
		{RunStatusFailed, RunStatusQueued, false},
		{RunStatusCancelled, RunStatusRunning, false},
		{RunStatusQueued, RunStatusSucceeded, false},
	}

	for _, test := range tests {
		if got := CanTransitionRun(test.from, test.to); got != test.want {
			t.Fatalf("transition %s -> %s: expected %v, got %v", test.from, test.to, test.want, got)
		}
	}
}

// TestStepStatusTransitionsRequireClaimedExecution 验证对应场景下的正常路径与失败路径。
func TestStepStatusTransitionsRequireClaimedExecution(t *testing.T) {
	if !CanTransitionStep(StepStatusQueued, StepStatusRunning) {
		t.Fatal("queued step must be claimable")
	}
	if !CanTransitionStep(StepStatusRunning, StepStatusQueued) {
		t.Fatal("running step must be retryable")
	}
	if CanTransitionStep(StepStatusQueued, StepStatusSucceeded) {
		t.Fatal("queued step must not bypass execution")
	}
	if CanTransitionStep(StepStatusSucceeded, StepStatusRunning) {
		t.Fatal("terminal step must not be reclaimed")
	}
}

// TestErrorCategoryRetryabilityIsDeterministic 验证对应场景下的正常路径与失败路径。
func TestErrorCategoryRetryabilityIsDeterministic(t *testing.T) {
	for _, category := range []ErrorCategory{
		ErrorCategoryRateLimited,
		ErrorCategoryTimeout,
		ErrorCategoryRetryableUpstream,
		ErrorCategoryLeaseExpired,
	} {
		if !category.Retryable() {
			t.Fatalf("expected %s to be retryable", category)
		}
	}

	for _, category := range []ErrorCategory{
		ErrorCategoryInvalidInput,
		ErrorCategoryPermissionDenied,
		ErrorCategoryConflict,
		ErrorCategoryTerminalUpstream,
		ErrorCategoryPolicyBlocked,
		ErrorCategoryCancelled,
	} {
		if category.Retryable() {
			t.Fatalf("expected %s to be terminal", category)
		}
	}
}
