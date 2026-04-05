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

func TestLoadAIConfigDefaults(t *testing.T) {
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("EMBEDDING_DIM", "")

	cfg := Load()

	if cfg.SiliconFlowBaseURL != "https://api.siliconflow.cn/v1" {
		t.Fatalf("expected default base url, got %q", cfg.SiliconFlowBaseURL)
	}

	if cfg.EmbeddingDim != 1024 {
		t.Fatalf("expected default embedding dim 1024, got %d", cfg.EmbeddingDim)
	}
}

func TestLoadEmbeddingDimOverride(t *testing.T) {
	t.Setenv("EMBEDDING_DIM", "2048")

	cfg := Load()

	if cfg.EmbeddingDim != 2048 {
		t.Fatalf("expected embedding dim 2048, got %d", cfg.EmbeddingDim)
	}
}
