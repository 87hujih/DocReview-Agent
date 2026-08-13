package agentprojection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent_project/apps/server/internal/storage/postgres/outbox"
)

// TestSnapshotReaderRejectsInvalidEventBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestSnapshotReaderRejectsInvalidEventBeforeDatabaseAccess(t *testing.T) {
	_, err := NewRepository(nil).Load(context.Background(), outbox.Event{
		ID: "not-a-uuid", EventType: "agent.step.outcome_committed", PayloadJSON: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "event") {
		t.Fatalf("expected event validation, got %v", err)
	}
}

// TestRuntimeProjectionQueriesFenceTrustedDurableRunAndExactStepOrApproval 验证对应场景下的正常路径与失败路径。
func TestRuntimeProjectionQueriesFenceTrustedDurableRunAndExactStepOrApproval(t *testing.T) {
	for _, query := range []string{loadStepSnapshotSQL, loadRejectedApprovalSnapshotSQL} {
		for _, fragment := range []string{"run.runtime_mode = 'durable'", "run.turn_id IS NOT NULL", "run.id = $1"} {
			if !strings.Contains(query, fragment) {
				t.Fatalf("runtime snapshot query must contain %q", fragment)
			}
		}
	}
	if !strings.Contains(loadStepSnapshotSQL, "step.id = $2") || !strings.Contains(loadRejectedApprovalSnapshotSQL, "approval.id = $2") {
		t.Fatal("runtime projection must bind the exact persisted step/approval fact")
	}
}

// TestStepProjectionUsesOutcomeEventStatusInsteadOfMutableCurrentRunStatus 验证对应场景下的正常路径与失败路径。
func TestStepProjectionUsesOutcomeEventStatusInsteadOfMutableCurrentRunStatus(t *testing.T) {
	if strings.Contains(loadStepSnapshotSQL, "run.status") {
		t.Fatal("lagged step projection must not reinterpret an old event using mutable current run status")
	}
	if !strings.Contains(loadRejectedApprovalSnapshotSQL, "run.status") {
		t.Fatal("approval rejection snapshot must read its atomic terminal run state")
	}
}

// TestReceiptStoreRejectsInvalidIdentityAndHashBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestReceiptStoreRejectsInvalidIdentityAndHashBeforeDatabaseAccess(t *testing.T) {
	err := NewRepository(nil).Record(context.Background(), "not-a-uuid", "runtime", "sha256:bad")
	if err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatalf("expected receipt validation, got %v", err)
	}
}
