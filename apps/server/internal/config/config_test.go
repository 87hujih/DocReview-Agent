package config

import "testing"

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")

	cfg := Load()

	if cfg.ServerPort != defaultServerPort {
		t.Fatalf("expected default server port %q, got %q", defaultServerPort, cfg.ServerPort)
	}

	if cfg.DatabaseURL != defaultDatabaseURL {
		t.Fatalf("expected default database url %q, got %q", defaultDatabaseURL, cfg.DatabaseURL)
	}
}

func TestLoadUsesEnvironmentOverrides(t *testing.T) {
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://example")

	cfg := Load()

	if cfg.ServerPort != "9090" {
		t.Fatalf("expected overridden server port %q, got %q", "9090", cfg.ServerPort)
	}

	if cfg.DatabaseURL != "postgres://example" {
		t.Fatalf("expected overridden database url %q, got %q", "postgres://example", cfg.DatabaseURL)
	}
}
