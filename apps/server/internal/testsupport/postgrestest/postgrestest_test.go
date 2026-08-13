package postgrestest

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestValidateDatabaseTestEnvironmentAcceptsExplicitSafeConfig 验证对应场景下的正常路径与失败路径。
func TestValidateDatabaseTestEnvironmentAcceptsExplicitSafeConfig(t *testing.T) {
	env := map[string]string{
		allowDBTestsEnv:              "1",
		testDatabaseURLEnv:           "postgres://user:secret@localhost:5432/agent_project_test?sslmode=disable",
		testDatabaseHostAllowlistEnv: "127.0.0.1, localhost, ::1",
	}

	databaseURL, err := validateDatabaseTestEnvironment(mapLookup(env))
	if err != nil {
		t.Fatalf("expected safe test configuration, got %v", err)
	}
	if databaseURL != env[testDatabaseURLEnv] {
		t.Fatal("expected validated TEST_DATABASE_URL to be returned unchanged")
	}
}

// TestValidateDatabaseTestEnvironmentAcceptsAllowlistedIPv6 验证对应场景下的正常路径与失败路径。
func TestValidateDatabaseTestEnvironmentAcceptsAllowlistedIPv6(t *testing.T) {
	env := map[string]string{
		allowDBTestsEnv:              "1",
		testDatabaseURLEnv:           "postgres://user:secret@[::1]:5432/agent_project_test",
		testDatabaseHostAllowlistEnv: "[::1]",
	}

	if _, err := validateDatabaseTestEnvironment(mapLookup(env)); err != nil {
		t.Fatalf("expected allowlisted IPv6 host, got %v", err)
	}
}

// TestValidateDatabaseTestEnvironmentNeverReadsProductionDatabaseURL 验证对应场景下的正常路径与失败路径。
func TestValidateDatabaseTestEnvironmentNeverReadsProductionDatabaseURL(t *testing.T) {
	env := map[string]string{
		allowDBTestsEnv:              "1",
		testDatabaseURLEnv:           "postgres://user:secret@localhost:5432/agent_project_test",
		testDatabaseHostAllowlistEnv: "localhost",
		"DATABASE_URL":               "postgres://user:secret@production.example/agent_project",
	}
	requested := make(map[string]bool)
	lookup := func(key string) (string, bool) {
		requested[key] = true
		value, ok := env[key]
		return value, ok
	}

	if _, err := validateDatabaseTestEnvironment(lookup); err != nil {
		t.Fatalf("expected safe test configuration, got %v", err)
	}
	if requested["DATABASE_URL"] {
		t.Fatal("database test fuse must never read DATABASE_URL")
	}
}

// TestValidateDatabaseTestEnvironmentRejectsUnsafeConfig 验证对应场景下的正常路径与失败路径。
func TestValidateDatabaseTestEnvironmentRejectsUnsafeConfig(t *testing.T) {
	base := map[string]string{
		allowDBTestsEnv:              "1",
		testDatabaseURLEnv:           "postgres://user:secret@localhost:5432/agent_project_test",
		testDatabaseHostAllowlistEnv: "localhost",
	}

	tests := []struct {
		name       string
		overrides  map[string]string
		wantReason string
	}{
		{name: "missing opt in", overrides: map[string]string{allowDBTestsEnv: ""}, wantReason: "ALLOW_DB_TESTS"},
		{name: "invalid opt in", overrides: map[string]string{allowDBTestsEnv: "true"}, wantReason: "ALLOW_DB_TESTS"},
		{name: "missing url", overrides: map[string]string{testDatabaseURLEnv: ""}, wantReason: "TEST_DATABASE_URL"},
		{name: "missing allowlist", overrides: map[string]string{testDatabaseHostAllowlistEnv: ""}, wantReason: "TEST_DATABASE_HOST_ALLOWLIST"},
		{name: "invalid scheme", overrides: map[string]string{testDatabaseURLEnv: "mysql://localhost/agent_project_test"}, wantReason: "PostgreSQL URL"},
		{name: "missing host", overrides: map[string]string{testDatabaseURLEnv: "postgres:///agent_project_test"}, wantReason: "明确的 host"},
		{name: "production database name", overrides: map[string]string{testDatabaseURLEnv: "postgres://localhost/agent_project"}, wantReason: "_test"},
		{name: "nested database path", overrides: map[string]string{testDatabaseURLEnv: "postgres://localhost/team/agent_project_test"}, wantReason: "_test"},
		{name: "host not allowlisted", overrides: map[string]string{testDatabaseURLEnv: "postgres://db.internal/agent_project_test"}, wantReason: "TEST_DATABASE_HOST_ALLOWLIST"},
		{name: "host query override", overrides: map[string]string{testDatabaseURLEnv: "postgres://localhost/agent_project_test?host=db.internal"}, wantReason: "override"},
		{name: "database query override", overrides: map[string]string{testDatabaseURLEnv: "postgres://localhost/agent_project_test?dbname=agent_project"}, wantReason: "override"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := cloneMap(base)
			for key, value := range test.overrides {
				env[key] = value
			}

			if _, err := validateDatabaseTestEnvironment(mapLookup(env)); err == nil {
				t.Fatal("expected unsafe configuration to be rejected")
			} else if !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("expected error containing %q, got %v", test.wantReason, err)
			}
		})
	}
}

// TestOpenValidatedPoolRejectsUnsafeConfigBeforeConnectionFactory 验证对应场景下的正常路径与失败路径。
func TestOpenValidatedPoolRejectsUnsafeConfigBeforeConnectionFactory(t *testing.T) {
	env := map[string]string{
		allowDBTestsEnv:              "1",
		testDatabaseURLEnv:           "postgres://user:secret@production.example/agent_project",
		testDatabaseHostAllowlistEnv: "production.example",
	}
	connectionFactoryCalled := false

	pool, _, err := openValidatedPool(
		context.Background(),
		mapLookup(env),
		func(context.Context, string) (*pgxpool.Pool, error) {
			connectionFactoryCalled = true
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected unsafe database configuration to fail")
	}
	if pool != nil {
		t.Fatal("expected no pool for unsafe database configuration")
	}
	if connectionFactoryCalled {
		t.Fatal("connection factory must not be called before the database test fuse passes")
	}
}

// mapLookup 执行该函数负责的核心处理逻辑。
func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// cloneMap 执行该函数负责的核心处理逻辑。
func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// TestWithSearchPathPreservesExistingQueryAndAppendsPublic 验证`withSearchPath`在状态保持路径下的行为，防止同类回归。
func TestWithSearchPathPreservesExistingQueryAndAppendsPublic(t *testing.T) {
	databaseURL := "postgres://user:pass@localhost:5432/agent_project?sslmode=disable"

	isolatedURL, err := withSearchPath(databaseURL, "ci_pkg_123", "public", "public")
	if err != nil {
		t.Fatalf("with search path: %v", err)
	}

	parsed, err := url.Parse(isolatedURL)
	if err != nil {
		t.Fatalf("parse isolated url: %v", err)
	}

	if parsed.Path != "/agent_project" {
		t.Fatalf("expected database path %q, got %q", "/agent_project", parsed.Path)
	}

	query := parsed.Query()
	if query.Get("sslmode") != "disable" {
		t.Fatalf("expected sslmode to be preserved, got %q", query.Get("sslmode"))
	}
	if query.Get("search_path") != "ci_pkg_123,public" {
		t.Fatalf("expected search_path %q, got %q", "ci_pkg_123,public", query.Get("search_path"))
	}
}

// TestWithSearchPathKeepsCustomVectorSchemaAheadOfPublic 验证`withSearchPath`在状态保持路径下的行为，防止同类回归。
func TestWithSearchPathKeepsCustomVectorSchemaAheadOfPublic(t *testing.T) {
	databaseURL := "postgres://user:pass@localhost:5432/agent_project"

	isolatedURL, err := withSearchPath(databaseURL, "ci_pkg_123", "vector_ext", "public")
	if err != nil {
		t.Fatalf("with search path: %v", err)
	}

	parsed, err := url.Parse(isolatedURL)
	if err != nil {
		t.Fatalf("parse isolated url: %v", err)
	}

	if parsed.Query().Get("search_path") != "ci_pkg_123,vector_ext,public" {
		t.Fatalf("expected custom vector schema to be preserved, got %q", parsed.Query().Get("search_path"))
	}
}

// TestNewSchemaNameSanitizesNamespaceAndCapsLength 验证`newSchemaNameSanitizesNamespaceAndCapsLength`在特定边界条件下的行为，防止同类回归。
func TestNewSchemaNameSanitizesNamespaceAndCapsLength(t *testing.T) {
	schemaName := newSchemaName(strings.Repeat("server/handlers-", 8))

	if len(schemaName) == 0 {
		t.Fatal("expected schema name to be generated")
	}
	if len(schemaName) > 63 {
		t.Fatalf("expected schema name length <= 63, got %d (%q)", len(schemaName), schemaName)
	}

	validName := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	if !validName.MatchString(schemaName) {
		t.Fatalf("expected schema name %q to contain only lowercase letters, digits, and underscores", schemaName)
	}
	if !strings.Contains(schemaName, "server_handlers") {
		t.Fatalf("expected schema name %q to include sanitized namespace", schemaName)
	}
}
