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
