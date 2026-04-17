package postgres

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAssistantRepoSessionLifecycle(t *testing.T) {
	pool := newAssistantTestPool(t)
	repo := NewAssistantRepo(pool)
	ctx := assistantTestContext(t)

	session, messages, err := repo.CreateSessionWithMessages(ctx, "学生守则会话", []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"请先看看第二章"}`),
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"好的，我先帮你梳理。 "}`),
	})
	if err != nil {
		t.Fatalf("create session with messages: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	listedSessions, err := repo.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}

	if len(listedSessions) == 0 {
		t.Fatal("expected at least one session")
	}

	gotSession, err := repo.GetSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session by id: %v", err)
	}
	if gotSession == nil {
		t.Fatal("expected created session")
	}

	appended, err := repo.AppendMessages(ctx, session.ID, []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"继续整理成任务"}`),
		mustAssistantMessageInput(t, "assistant", "task_suggestion", `{"title":"建议创建任务","instruction":"继续整理成任务","can_create":false,"action_label":"确认创建任务","resource_label":"资源未明确","status_message":"还没有明确可执行的资源，请先上传文件或继续明确材料。"}`),
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

	if allMessages[3].Kind != "task_suggestion" {
		t.Fatalf("expected last message kind %q, got %q", "task_suggestion", allMessages[3].Kind)
	}

	messageByID, err := repo.GetMessageByID(ctx, allMessages[3].ID)
	if err != nil {
		t.Fatalf("get message by id: %v", err)
	}
	if messageByID == nil {
		t.Fatal("expected fetched message")
	}

	deleted, err := repo.DeleteSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete to return true")
	}

	gotDeletedSession, err := repo.GetSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get deleted session: %v", err)
	}
	if gotDeletedSession != nil {
		t.Fatalf("expected deleted session to be nil, got %#v", gotDeletedSession)
	}
}

func TestAssistantRepoListMessagesAfterSequence(t *testing.T) {
	pool := newAssistantTestPool(t)
	repo := NewAssistantRepo(pool)
	ctx := assistantTestContext(t)

	session, _, err := repo.CreateSessionWithMessages(ctx, "摘要窗口", []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"第一条"}`),
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"第二条"}`),
	})
	if err != nil {
		t.Fatalf("create session with messages: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})

	appended, err := repo.AppendMessages(ctx, session.ID, []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"第三条"}`),
		mustAssistantMessageInput(t, "assistant", "task_suggestion", `{"instruction":"第四条"}`),
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"第五条"}`),
	})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}

	window, err := repo.ListMessagesAfterSequence(ctx, session.ID, 2)
	if err != nil {
		t.Fatalf("list messages after sequence: %v", err)
	}

	if len(window) != 3 {
		t.Fatalf("expected 3 messages after sequence 2, got %d", len(window))
	}
	if window[0].SequenceNo != 3 || window[0].ID != appended[0].ID {
		t.Fatalf("expected first window message to be sequence 3 (%s), got sequence %d (%s)", appended[0].ID, window[0].SequenceNo, window[0].ID)
	}
	if window[1].SequenceNo != 4 || window[1].ID != appended[1].ID {
		t.Fatalf("expected second window message to be sequence 4 (%s), got sequence %d (%s)", appended[1].ID, window[1].SequenceNo, window[1].ID)
	}
	if window[2].SequenceNo != 5 || window[2].ID != appended[2].ID {
		t.Fatalf("expected third window message to be sequence 5 (%s), got sequence %d (%s)", appended[2].ID, window[2].SequenceNo, window[2].ID)
	}
}

func TestAssistantRepoListMessagesAfterSequenceReturnsAscendingWindow(t *testing.T) {
	pool := newAssistantTestPool(t)
	repo := NewAssistantRepo(pool)
	ctx := assistantTestContext(t)

	session, _, err := repo.CreateSessionWithMessages(ctx, "摘要窗口顺序", []AssistantMessageInput{
		mustAssistantMessageInput(t, "user", "text", `{"content":"第零条"}`),
	})
	if err != nil {
		t.Fatalf("create session with messages: %v", err)
	}
	t.Cleanup(func() {
		if _, err := repo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})

	_, err = repo.AppendMessages(ctx, session.ID, []AssistantMessageInput{
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"第一条"}`),
		mustAssistantMessageInput(t, "user", "session_file", `{"status":"ready"}`),
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"第三条"}`),
	})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}

	window, err := repo.ListMessagesAfterSequence(ctx, session.ID, 1)
	if err != nil {
		t.Fatalf("list messages after sequence: %v", err)
	}

	if len(window) != 3 {
		t.Fatalf("expected 3 messages after sequence 1, got %d", len(window))
	}
	for index, message := range window {
		expectedSequence := index + 2
		if message.SequenceNo != expectedSequence {
			t.Fatalf("expected ascending sequence %d at index %d, got %d", expectedSequence, index, message.SequenceNo)
		}
	}
}

func TestAssistantRepoDatabaseHostGateOnlyAllowsLoopback(t *testing.T) {
	allowed := []string{
		"postgres://user:pass@127.0.0.1:5432/app",
		"postgres://user:pass@localhost:5432/app",
		"postgres://user:pass@[::1]:5432/app",
	}
	for _, databaseURL := range allowed {
		if !isLocalDatabaseHost(databaseURL) {
			t.Fatalf("expected %s to be treated as local", databaseURL)
		}
	}

	blocked := []string{
		"postgres://user:pass@10.0.0.2:5432/app",
		"postgres://user:pass@192.168.1.20:5432/app",
		"postgres://user:pass@106.52.42.194:5432/app",
		"postgres://user:pass@db.internal:5432/app",
	}
	for _, databaseURL := range blocked {
		if isLocalDatabaseHost(databaseURL) {
			t.Fatalf("expected %s to require explicit nonlocal opt-in", databaseURL)
		}
	}
}

func newAssistantTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	cfg := appconfig.Load()
	if !isLocalDatabaseHost(cfg.DatabaseURL) {
		t.Skip("assistant repo integration test only runs against local database hosts")
	}

	ctx := assistantTestContext(t)
	return postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "storage_postgres_assistant", NewPool, RunMigrations)
}

func assistantTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func mustAssistantMessageInput(t *testing.T, role string, kind string, payload string) AssistantMessageInput {
	t.Helper()
	return AssistantMessageInput{
		Role:    role,
		Kind:    kind,
		Payload: []byte(payload),
	}
}

func isLocalDatabaseHost(databaseURL string) bool {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return false
	}

	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
