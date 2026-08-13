package documentcommit

import (
	"errors"
	"testing"

	canonicalcommit "agent_project/apps/server/internal/document/commit"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestSerializationAndDeadlockFailuresRemainRetryable 验证对应场景下的正常路径与失败路径。
func TestSerializationAndDeadlockFailuresRemainRetryable(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		err := classifyTransactionError(&pgconn.PgError{Code: code, Message: "retry"})
		if !errors.Is(err, canonicalcommit.ErrRetryableCommit) {
			t.Fatalf("code %s was not retryable: %v", code, err)
		}
	}
}

// TestAtomicStoreRejectsRequestsNotPreparedByCommitter 验证对应场景下的正常路径与失败路径。
func TestAtomicStoreRejectsRequestsNotPreparedByCommitter(t *testing.T) {
	if err := validateAtomicRequest(canonicalcommit.AtomicRequest{}); err == nil {
		t.Fatal("forged atomic request was accepted")
	}
}
