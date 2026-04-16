package postgres

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAssistantRepoSessionLifecycleIntegration(t *testing.T) {
	pool := newAssistantIntegrationPool(t)
	repo := NewAssistantRepo(pool)
	ctx := assistantTestContext(t)

	title := fmt.Sprintf("assistant-integration-%d", time.Now().UnixNano())
	session, messages, err := repo.CreateSessionWithMessages(ctx, title, []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"请验证真实数据库会话生命周期"}`),
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"已收到验证请求"}`),
	})
	if err != nil {
		t.Fatalf("create session with messages: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session %q: %v", session.ID, err)
		}
	})

	if len(messages) != 2 {
		t.Fatalf("expected 2 initial messages, got %d", len(messages))
	}

	gotSession, err := repo.GetSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session by id: %v", err)
	}
	if gotSession == nil || gotSession.Title != title {
		t.Fatalf("expected created session title %q, got %#v", title, gotSession)
	}

	appended, err := repo.AppendMessages(ctx, session.ID, []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"继续追加真实消息"}`),
		mustAssistantMessageInput(t, "assistant", "task_suggestion", `{"title":"真实库验证","instruction":"继续追加真实消息","can_create":false,"action_label":"确认创建任务","resource_label":"无资源","status_message":"验证消息持久化。"}`),
	})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}
	if len(appended) != 2 {
		t.Fatalf("expected 2 appended messages, got %d", len(appended))
	}

	allMessages, err := repo.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(allMessages) != 4 {
		t.Fatalf("expected 4 stored messages, got %d", len(allMessages))
	}
	if allMessages[0].SequenceNo != 1 || allMessages[3].SequenceNo != 4 {
		t.Fatalf("expected contiguous sequence numbers, got first=%d last=%d", allMessages[0].SequenceNo, allMessages[3].SequenceNo)
	}

	deleted, err := repo.DeleteSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete to report true")
	}

	gotDeletedSession, err := repo.GetSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get deleted session: %v", err)
	}
	if gotDeletedSession != nil {
		t.Fatalf("expected deleted session to be nil, got %#v", gotDeletedSession)
	}

	var remainingMessages int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM assistant_messages WHERE session_id = $1`, session.ID).Scan(&remainingMessages); err != nil {
		t.Fatalf("count remaining messages: %v", err)
	}
	if remainingMessages != 0 {
		t.Fatalf("expected cascade cleanup to remove messages, got %d", remainingMessages)
	}
}

func newAssistantIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("ASSISTANT_DB_INTEGRATION")) != "1" {
		t.Skip("assistant repo integration test requires ASSISTANT_DB_INTEGRATION=1")
	}

	cfg := appconfig.Load()
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		t.Skip("database not available")
	}

	if !isLocalDatabaseHost(cfg.DatabaseURL) && strings.TrimSpace(os.Getenv("ALLOW_NONLOCAL_DB")) != "1" {
		t.Skip("nonlocal database requires ALLOW_NONLOCAL_DB=1")
	}

	ctx := assistantTestContext(t)
	return postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "storage_postgres_assistant_integration", NewPool, RunMigrations)
}
