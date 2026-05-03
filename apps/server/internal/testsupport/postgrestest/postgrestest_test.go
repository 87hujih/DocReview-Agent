package postgrestest

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

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
