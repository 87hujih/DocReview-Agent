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
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("UPLOAD_STORAGE_DIR", "")
	t.Setenv("UPLOAD_MAX_BYTES", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

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
		"  reranker_model: \"yaml-reranker\"",
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

	if cfg.RerankerModel != "yaml-reranker" {
		t.Fatalf("expected yaml reranker model, got %q", cfg.RerankerModel)
	}

	if cfg.UploadStorageDir != "data/uploads" {
		t.Fatalf("expected default upload storage dir %q, got %q", "data/uploads", cfg.UploadStorageDir)
	}

	if cfg.UploadMaxBytes != 20*1024*1024 {
		t.Fatalf("expected default upload max bytes %d, got %d", 20*1024*1024, cfg.UploadMaxBytes)
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
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1024",
		"  reranker_model: \"yaml-reranker\"",
	}, "\n"))
	writeTestFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"SERVER_PORT=8282",
		"DATABASE_URL=postgres://dotenv-db",
		"SILICONFLOW_API_KEY=dotenv-key",
		"EMBEDDING_MODEL=dotenv-embedding",
		"EMBEDDING_DIM=2048",
		"RERANKER_MODEL=dotenv-reranker",
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

	if cfg.RerankerModel != "dotenv-reranker" {
		t.Fatalf("expected dotenv reranker model, got %q", cfg.RerankerModel)
	}
}

// TestLoadUsesEnvironmentOverridesOverDotEnv 验证进程环境变量优先级高于 .env。
func TestLoadUsesEnvironmentOverridesOverDotEnv(t *testing.T) {
	t.Setenv("SERVER_PORT", "8383")
	t.Setenv("DATABASE_URL", "postgres://env-db")
	t.Setenv("EMBEDDING_MODEL", "env-embedding")
	t.Setenv("EMBEDDING_DIM", "3072")
	t.Setenv("RERANKER_MODEL", "env-reranker")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1024",
		"  reranker_model: \"yaml-reranker\"",
	}, "\n"))
	writeTestFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"SERVER_PORT=8282",
		"DATABASE_URL=postgres://dotenv-db",
		"EMBEDDING_MODEL=dotenv-embedding",
		"EMBEDDING_DIM=2048",
		"RERANKER_MODEL=dotenv-reranker",
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

	if cfg.RerankerModel != "env-reranker" {
		t.Fatalf("expected env reranker model, got %q", cfg.RerankerModel)
	}
}

func TestLoadUsesHardcodedLLMDefault(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	cfg := Load()

	if cfg.LLMModel != "Qwen/Qwen2.5-7B-Instruct" {
		t.Fatalf("expected hardcoded llm model %q, got %q", "Qwen/Qwen2.5-7B-Instruct", cfg.LLMModel)
	}

	if cfg.LogLevel != "info" {
		t.Fatalf("expected hardcoded log level %q, got %q", "info", cfg.LogLevel)
	}

	if cfg.LogFormat != "json" {
		t.Fatalf("expected hardcoded log format %q, got %q", "json", cfg.LogFormat)
	}

	if cfg.LogAddSource {
		t.Fatalf("expected hardcoded log add_source %v, got %v", false, cfg.LogAddSource)
	}
}

func TestLoadStopsDotEnvSearchAtWorktreeRoot(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

	tempDir := t.TempDir()
	parentRepoRoot := filepath.Join(tempDir, "repo")
	worktreeRoot := filepath.Join(parentRepoRoot, ".worktrees", "task3-agent-workflow")
	worktreeAppDir := filepath.Join(worktreeRoot, "apps", "server")

	writeTestFile(t, filepath.Join(parentRepoRoot, ".env"), strings.Join([]string{
		"LLM_MODEL=parent-dotenv-llm",
		"DATABASE_URL=postgres://parent-dotenv-db",
	}, "\n"))
	writeTestFile(t, filepath.Join(worktreeRoot, ".git"), "gitdir: ../.git/worktrees/task3-agent-workflow")
	writeTestFile(t, filepath.Join(worktreeRoot, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  llm_model: \"worktree-yaml-llm\"",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1024",
		"  reranker_model: \"yaml-reranker\"",
	}, "\n"))
	if err := os.MkdirAll(worktreeAppDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", worktreeAppDir, err)
	}
	t.Chdir(worktreeAppDir)

	cfg := Load()

	if cfg.LLMModel != "worktree-yaml-llm" {
		t.Fatalf("expected worktree yaml llm model %q, got %q", "worktree-yaml-llm", cfg.LLMModel)
	}

	if cfg.DatabaseURL != "" {
		t.Fatalf("expected parent dotenv DATABASE_URL to be ignored at worktree root, got %q", cfg.DatabaseURL)
	}
}

func TestLoadUsesLogDefaultsFromYAML(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"server:",
		"  port: \"8181\"",
		"ai:",
		"  embedding_model: \"yaml-embedding\"",
		"  embedding_dim: 1024",
		"  reranker_model: \"yaml-reranker\"",
		"log:",
		"  level: \"debug\"",
		"  format: \"text\"",
		"  add_source: true",
	}, "\n"))

	cfg := Load()

	if cfg.LogLevel != "debug" {
		t.Fatalf("expected yaml log level %q, got %q", "debug", cfg.LogLevel)
	}

	if cfg.LogFormat != "text" {
		t.Fatalf("expected yaml log format %q, got %q", "text", cfg.LogFormat)
	}

	if !cfg.LogAddSource {
		t.Fatalf("expected yaml log add_source %v, got %v", true, cfg.LogAddSource)
	}
}

func TestLoadUsesEnvironmentOverridesForLogging(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("LOG_ADD_SOURCE", "true")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, "config", "default.yaml"), strings.Join([]string{
		"log:",
		"  level: \"debug\"",
		"  format: \"json\"",
		"  add_source: false",
	}, "\n"))

	cfg := Load()

	if cfg.LogLevel != "warn" {
		t.Fatalf("expected env log level %q, got %q", "warn", cfg.LogLevel)
	}

	if cfg.LogFormat != "text" {
		t.Fatalf("expected env log format %q, got %q", "text", cfg.LogFormat)
	}

	if !cfg.LogAddSource {
		t.Fatalf("expected env log add_source %v, got %v", true, cfg.LogAddSource)
	}
}

// TestValidateForServerRequiresDatabaseURL 验证缺少数据库地址时会返回校验错误。
func TestValidateForServerRequiresDatabaseURL(t *testing.T) {
	cfg := Config{
		ServerPort:     "8080",
		DatabaseURL:    "",
		EmbeddingModel: "embedding-model",
		EmbeddingDim:   1024,
		RerankerModel:  "reranker-model",
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
		ServerPort:        "8080",
		DatabaseURL:       "postgres://example",
		SiliconFlowAPIKey: "api-key",
		LLMModel:          "llm-model",
		EmbeddingModel:    "embedding-model",
		EmbeddingDim:      1024,
		RerankerModel:     "reranker-model",
	}

	if err := cfg.ValidateForServer(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
}

func TestValidateForServerRequiresRerankerModel(t *testing.T) {
	cfg := Config{
		ServerPort:     "8080",
		DatabaseURL:    "postgres://example",
		EmbeddingModel: "embedding-model",
		EmbeddingDim:   1024,
	}

	err := cfg.ValidateForServer()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "RERANKER_MODEL") {
		t.Fatalf("expected RERANKER_MODEL validation error, got %v", err)
	}
}

func TestValidateForServerRequiresSiliconFlowAPIKey(t *testing.T) {
	cfg := Config{
		ServerPort:         "8080",
		DatabaseURL:        "postgres://example",
		SiliconFlowAPIKey:  "",
		LLMModel:           "llm-model",
		EmbeddingModel:     "embedding-model",
		EmbeddingDim:       1024,
		RerankerModel:      "reranker-model",
		SiliconFlowBaseURL: "https://example.invalid/v1",
	}

	err := cfg.ValidateForServer()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "SILICONFLOW_API_KEY") {
		t.Fatalf("expected SILICONFLOW_API_KEY validation error, got %v", err)
	}
}

func TestValidateForServerRequiresLLMModel(t *testing.T) {
	cfg := Config{
		ServerPort:         "8080",
		DatabaseURL:        "postgres://example",
		SiliconFlowAPIKey:  "api-key",
		LLMModel:           "",
		EmbeddingModel:     "embedding-model",
		EmbeddingDim:       1024,
		RerankerModel:      "reranker-model",
		SiliconFlowBaseURL: "https://example.invalid/v1",
	}

	err := cfg.ValidateForServer()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "LLM_MODEL") {
		t.Fatalf("expected LLM_MODEL validation error, got %v", err)
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
