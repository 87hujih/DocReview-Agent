package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadUsesYAMLDefaults 验证未被覆盖时会读取 YAML 默认配置。
func TestLoadUsesYAMLDefaults(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  siliconflow_base_url: \"https://yaml.example/v1\"",
		"  llm_model: \"yaml-llm\"",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1536",
	}, "\n"))

	cfg := Load()

	if cfg.ServerPort != "8181" {
		t.Fatalf("expected yaml server port %q, got %q", "8181", cfg.ServerPort)
	}

	if cfg.DatabaseURL != "" {
		t.Fatalf("expected empty database url by default, got %q", cfg.DatabaseURL)
	}

	if cfg.SiliconFlowBaseURL != "https://yaml.example/v1" {
		t.Fatalf("expected yaml base url, got %q", cfg.SiliconFlowBaseURL)
	}

	if cfg.LLMModel != "yaml-llm" {
		t.Fatalf("expected yaml llm model, got %q", cfg.LLMModel)
	}

	if cfg.EmbeddingModel != "yaml-embedding" {
		t.Fatalf("expected yaml embedding model, got %q", cfg.EmbeddingModel)
	}

	if cfg.EmbeddingDim != 1536 {
		t.Fatalf("expected yaml embedding dim 1536, got %d", cfg.EmbeddingDim)
	}
}

// TestLoadUsesDotEnvOverrides 验证 .env 会覆盖 YAML 中的默认值。
func TestLoadUsesDotEnvOverrides(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1024",
	}, "\n"))
	writeTestFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"SERVER_PORT=8282",
		"DATABASE_URL=postgres://dotenv-db",
		"SILICONFLOW_API_KEY=dotenv-key",
		"EMBEDDING_MODEL=dotenv-embedding",
		"EMBEDDING_DIM=2048",
	}, "\n"))

	cfg := Load()

	if cfg.ServerPort != "8282" {
		t.Fatalf("expected dotenv server port %q, got %q", "8282", cfg.ServerPort)
	}

	if cfg.DatabaseURL != "postgres://dotenv-db" {
		t.Fatalf("expected dotenv database url, got %q", cfg.DatabaseURL)
	}

	if cfg.SiliconFlowAPIKey != "dotenv-key" {
		t.Fatalf("expected dotenv api key, got %q", cfg.SiliconFlowAPIKey)
	}

	if cfg.EmbeddingModel != "dotenv-embedding" {
		t.Fatalf("expected dotenv embedding model, got %q", cfg.EmbeddingModel)
	}

	if cfg.EmbeddingDim != 2048 {
		t.Fatalf("expected dotenv embedding dim 2048, got %d", cfg.EmbeddingDim)
	}
}

// TestLoadUsesEnvironmentOverridesOverDotEnv 验证进程环境变量优先级高于 .env。
func TestLoadUsesEnvironmentOverridesOverDotEnv(t *testing.T) {
	t.Setenv("SERVER_PORT", "8383")
	t.Setenv("DATABASE_URL", "postgres://env-db")
	t.Setenv("EMBEDDING_MODEL", "env-embedding")
	t.Setenv("EMBEDDING_DIM", "3072")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1024",
	}, "\n"))
	writeTestFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"SERVER_PORT=8282",
		"DATABASE_URL=postgres://dotenv-db",
		"EMBEDDING_MODEL=dotenv-embedding",
		"EMBEDDING_DIM=2048",
	}, "\n"))

	cfg := Load()

	if cfg.ServerPort != "8383" {
		t.Fatalf("expected env server port %q, got %q", "8383", cfg.ServerPort)
	}

	if cfg.DatabaseURL != "postgres://env-db" {
		t.Fatalf("expected env database url, got %q", cfg.DatabaseURL)
	}

	if cfg.EmbeddingModel != "env-embedding" {
		t.Fatalf("expected env embedding model, got %q", cfg.EmbeddingModel)
	}

	if cfg.EmbeddingDim != 3072 {
		t.Fatalf("expected env embedding dim 3072, got %d", cfg.EmbeddingDim)
	}
}

// TestValidateForServerRequiresDatabaseURL 验证缺少数据库地址时会返回校验错误。
func TestValidateForServerRequiresDatabaseURL(t *testing.T) {
	cfg := Config{
		ServerPort:     "8080",
		DatabaseURL:    "",
		EmbeddingModel: "embedding-model",
		EmbeddingDim:   1024,
	}

	err := cfg.ValidateForServer()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL validation error, got %v", err)
	}
}

// TestValidateForServerAcceptsValidConfig 验证完整配置可以通过服务启动前校验。
func TestValidateForServerAcceptsValidConfig(t *testing.T) {
	cfg := Config{
		ServerPort:     "8080",
		DatabaseURL:    "postgres://example",
		EmbeddingModel: "embedding-model",
		EmbeddingDim:   1024,
	}

	if err := cfg.ValidateForServer(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
}

// writeTestFile 为配置测试写入临时文件树。
func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
