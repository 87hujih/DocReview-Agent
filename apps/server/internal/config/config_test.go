package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestLoadReadsExplicitCORSAllowedOrigins 验证对应场景下的正常路径与失败路径。
func TestLoadReadsExplicitCORSAllowedOrigins(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://127.0.0.1:3000, https://app.example.com")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	cfg := Load()
	want := []string{"http://127.0.0.1:3000", "https://app.example.com"}
	if !slices.Equal(cfg.CORSAllowedOrigins, want) {
		t.Fatalf("expected CORS origins %#v, got %#v", want, cfg.CORSAllowedOrigins)
	}
	if cfg.Environment != "development" {
		t.Fatalf("expected environment %q, got %q", "development", cfg.Environment)
	}
}

// TestValidateForServerRejectsProductionWithoutCORSOrigins 验证对应场景下的正常路径与失败路径。
func TestValidateForServerRejectsProductionWithoutCORSOrigins(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.Environment = "production"
	cfg.CORSAllowedOrigins = nil

	err := cfg.ValidateForServer()
	if err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
		t.Fatalf("生产环境缺少 CORS 允许列表时应返回错误，实际错误：%v", err)
	}
}

// TestValidateForServerRejectsWildcardCORSOrigin 验证对应场景下的正常路径与失败路径。
func TestValidateForServerRejectsWildcardCORSOrigin(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.CORSAllowedOrigins = []string{"*"}

	err := cfg.ValidateForServer()
	if err == nil || !strings.Contains(err.Error(), "通配符") {
		t.Fatalf("CORS 来源包含通配符时应返回错误，实际错误：%v", err)
	}
}

// TestValidateForServerRejectsCORSOriginWithPath 验证对应场景下的正常路径与失败路径。
func TestValidateForServerRejectsCORSOriginWithPath(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.CORSAllowedOrigins = []string{"https://app.example.com/path"}

	err := cfg.ValidateForServer()
	if err == nil || !strings.Contains(err.Error(), "不能带路径") {
		t.Fatalf("CORS 来源包含路径时应返回错误，实际错误：%v", err)
	}
}

// TestValidateForServerAcceptsProductionCORSAllowlist 验证对应场景下的正常路径与失败路径。
func TestValidateForServerAcceptsProductionCORSAllowlist(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.Environment = "production"
	cfg.CORSAllowedOrigins = []string{"https://app.example.com"}

	if err := cfg.ValidateForServer(); err != nil {
		t.Fatalf("有效的生产 CORS 允许列表应通过校验，实际错误：%v", err)
	}
}

// TestValidateForServerRejectsDurableRuntimeWithoutExplicitDoubleCohort 验证对应场景下的正常路径与失败路径。
func TestValidateForServerAcceptsGlobalDurableRuntimeWithoutCohort(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.AgentRuntimeMode = "durable"
	cfg.AgentRuntimeTrustedIngressSecret = "0123456789abcdef0123456789abcdef"
	cfg.AgentRuntimeTrustedIngressSource = "edge-hmac-v1"
	cfg.AgentRuntimeTrustedIngressMaxAgeMS = 300000

	if err := cfg.ValidateForServer(); err != nil {
		t.Fatalf("全量持久化模式不应再要求 cohort，实际错误：%v", err)
	}
}

// TestValidateForServerRejectsDurableRuntimeWithoutTrustedIngress 验证对应场景下的正常路径与失败路径。
func TestValidateForServerRejectsDurableRuntimeWithoutTrustedIngress(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.AgentRuntimeMode = "durable"
	cfg.AgentRuntimeTrustedIngressSecret = ""
	cfg.AgentRuntimeTrustedIngressSource = ""

	err := cfg.ValidateForServer()
	if err == nil || !strings.Contains(err.Error(), "TRUSTED_INGRESS") {
		t.Fatalf("持久化模式缺少可信入口时应闭锁失败，实际错误：%v", err)
	}
}

// TestValidateForServerAcceptsExplicitDurableCohortWithTrustedIngress 验证对应场景下的正常路径与失败路径。
func TestValidateForServerRejectsLegacyAndShadowAfterGlobalCutover(t *testing.T) {
	for _, mode := range []string{"legacy", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			cfg := validServerConfigForCORSTest()
			cfg.AgentRuntimeMode = mode
			err := cfg.ValidateForServer()
			if err == nil || !strings.Contains(err.Error(), "durable") {
				t.Fatalf("模式 %q 应在全量切流后被拒绝，实际错误：%v", mode, err)
			}
		})
	}
}

func TestLoadDefaultsToDurableRuntime(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	cfg := Load()
	if cfg.AgentRuntimeMode != "durable" {
		t.Fatalf("全量切流后默认模式应为 durable，实际为 %q", cfg.AgentRuntimeMode)
	}
}

// TestValidateForMigrationsRequiresDatabaseURL 验证对应场景下的正常路径与失败路径。
func TestValidateForMigrationsRequiresDatabaseURL(t *testing.T) {
	err := (Config{}).ValidateForMigrations()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

// TestValidateForMigrationsDoesNotRequireServerOrAIConfig 验证对应场景下的正常路径与失败路径。
func TestValidateForMigrationsDoesNotRequireServerOrAIConfig(t *testing.T) {
	cfg := Config{DatabaseURL: "postgres://database.example/agent_project"}
	if err := cfg.ValidateForMigrations(); err != nil {
		t.Fatalf("expected database-only config to pass, got %v", err)
	}
}

// validServerConfigForCORSTest 执行该函数负责的核心处理逻辑。
func validServerConfigForCORSTest() Config {
	return Config{
		ServerPort:                         "8080",
		DatabaseURL:                        "postgres://example",
		SiliconFlowAPIKey:                  "test-key",
		LLMModel:                           "llm-model",
		EmbeddingModel:                     "embedding-model",
		EmbeddingDim:                       1024,
		RerankerModel:                      "reranker-model",
		DocumentParser:                     "text",
		CORSAllowedOrigins:                 []string{"http://127.0.0.1:3000"},
		AgentRuntimeMode:                   "durable",
		AgentRuntimeTrustedIngressSecret:   "0123456789abcdef0123456789abcdef",
		AgentRuntimeTrustedIngressSource:   "edge-hmac-v1",
		AgentRuntimeTrustedIngressMaxAgeMS: 300000,
	}
}

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

// TestLoadUsesHardcodedLLMDefault 验证`加载`在依赖选择路径下的行为，防止同类回归。
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

// TestLoadUsesDocumentParserOverrides 验证`加载`在依赖选择路径下的行为，防止同类回归。
func TestLoadUsesDocumentParserOverrides(t *testing.T) {
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("SILICONFLOW_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("EMBEDDING_DIM", "")
	t.Setenv("RERANKER_MODEL", "")
	t.Setenv("DOCUMENT_PARSER", "")
	t.Setenv("TIKA_URL", "")
	t.Setenv("TIKA_TIMEOUT_MS", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")
	t.Setenv("LOG_ADD_SOURCE", "")

	tempDir := t.TempDir()
	t.Chdir(tempDir)

	writeTestFile(t, filepath.Join(tempDir, ".env"), strings.Join([]string{
		"DOCUMENT_PARSER=tika",
		"TIKA_URL=http://127.0.0.1:9998",
		"TIKA_TIMEOUT_MS=45000",
	}, "\n"))

	cfg := Load()

	if cfg.DocumentParser != "tika" {
		t.Fatalf("expected document parser %q, got %q", "tika", cfg.DocumentParser)
	}

	if cfg.TikaURL != "http://127.0.0.1:9998" {
		t.Fatalf("expected tika url %q, got %q", "http://127.0.0.1:9998", cfg.TikaURL)
	}

	if cfg.TikaTimeoutMS != 45000 {
		t.Fatalf("expected tika timeout %d, got %d", 45000, cfg.TikaTimeoutMS)
	}
}

// TestLoadStopsDotEnvSearchAtWorktreeRoot 验证`加载`在流程控制路径下的行为，防止同类回归。
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

// TestLoadUsesLogDefaultsFromYAML 验证`加载`在依赖选择路径下的行为，防止同类回归。
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

// TestLoadUsesEnvironmentOverridesForLogging 验证`加载`在依赖选择路径下的行为，防止同类回归。
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
	cfg := validServerConfigForCORSTest()

	if err := cfg.ValidateForServer(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
}

// TestValidateForServerRequiresRerankerModel 验证`validateForServer`在约束校验路径下的行为，防止同类回归。
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

// TestValidateForServerRequiresSiliconFlowAPIKey 验证`validateForServer`在约束校验路径下的行为，防止同类回归。
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

// TestValidateForServerRequiresLLMModel 验证`validateForServer`在约束校验路径下的行为，防止同类回归。
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

// TestValidateForServerAllowsTextParserWithoutTika 验证`validateForServer`在合法输入或兼容路径下的行为，防止同类回归。
func TestValidateForServerAllowsTextParserWithoutTika(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.DocumentParser = "text"

	if err := cfg.ValidateForServer(); err != nil {
		t.Fatalf("expected valid text parser config, got %v", err)
	}
}

// TestValidateForServerRequiresTikaURLWhenTikaParserEnabled 验证`validateForServer`在约束校验路径下的行为，防止同类回归。
func TestValidateForServerRequiresTikaURLWhenTikaParserEnabled(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.DocumentParser = "tika"
	cfg.TikaURL = ""
	cfg.TikaTimeoutMS = 30000

	err := cfg.ValidateForServer()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "TIKA_URL") {
		t.Fatalf("expected TIKA_URL validation error, got %v", err)
	}
}

// TestValidateForServerRequiresPositiveTikaTimeout 验证`validateForServer`在约束校验路径下的行为，防止同类回归。
func TestValidateForServerRequiresPositiveTikaTimeout(t *testing.T) {
	cfg := validServerConfigForCORSTest()
	cfg.DocumentParser = "tika"
	cfg.TikaURL = "http://127.0.0.1:9998"
	cfg.TikaTimeoutMS = 0

	err := cfg.ValidateForServer()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "TIKA_TIMEOUT_MS") {
		t.Fatalf("expected TIKA_TIMEOUT_MS validation error, got %v", err)
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
