package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultServerPort         = "8080"
	defaultSiliconFlowBaseURL = "https://api.siliconflow.cn/v1"
	defaultLLMModel           = "Qwen/Qwen2.5-7B-Instruct"
	defaultEmbeddingModel     = "Qwen/Qwen3-Embedding-8B"
	defaultEmbeddingDim       = 1024
	defaultRerankerModel      = "Qwen/Qwen3-Reranker-8B"
	defaultUploadStorageDir   = "data/uploads"
	defaultUploadMaxBytes     = 20 * 1024 * 1024
	defaultLogLevel           = "info"
	defaultLogFormat          = "json"
)

// Config 保存从环境变量、.env 和默认 YAML 配置解析出的运行时参数。
type Config struct {
	ServerPort  string
	DatabaseURL string

	SiliconFlowAPIKey  string
	SiliconFlowBaseURL string
	LLMModel           string
	EmbeddingModel     string
	EmbeddingDim       int
	RerankerModel      string
	UploadStorageDir   string
	UploadMaxBytes     int

	LogLevel     string
	LogFormat    string
	LogAddSource bool
}

// fileConfig 对应默认 YAML 配置文件的顶层结构。
type fileConfig struct {
	Server serverConfig `yaml:"server"`
	AI     aiConfig     `yaml:"ai"`
	Log    logConfig    `yaml:"log"`
}

// serverConfig 描述服务端口等服务级默认配置。
type serverConfig struct {
	Port string `yaml:"port"`
}

// aiConfig 描述模型、维度和上游地址等 AI 相关默认配置。
type aiConfig struct {
	SiliconFlowBaseURL string `yaml:"siliconflow_base_url"`
	LLMModel           string `yaml:"llm_model"`
	EmbeddingModel     string `yaml:"embedding_model"`
	EmbeddingDim       int    `yaml:"embedding_dim"`
	RerankerModel      string `yaml:"reranker_model"`
}

// logConfig 描述日志级别、输出格式和是否附带源码位置。
type logConfig struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	AddSource bool   `yaml:"add_source"`
}

// Load 按“进程环境变量 -> .env -> config/default.yaml -> 硬编码默认值”的优先级解析配置。
func Load() Config {
	defaults := loadDefaultFileConfig()
	dotenvValues := loadDotEnvValues()

	return Config{
		ServerPort:         resolveString("SERVER_PORT", dotenvValues, defaults.Server.Port, defaultServerPort),
		DatabaseURL:        resolveString("DATABASE_URL", dotenvValues, "", ""),
		SiliconFlowAPIKey:  resolveString("SILICONFLOW_API_KEY", dotenvValues, "", ""),
		SiliconFlowBaseURL: resolveString("SILICONFLOW_BASE_URL", dotenvValues, defaults.AI.SiliconFlowBaseURL, defaultSiliconFlowBaseURL),
		LLMModel:           resolveString("LLM_MODEL", dotenvValues, defaults.AI.LLMModel, defaultLLMModel),
		EmbeddingModel:     resolveString("EMBEDDING_MODEL", dotenvValues, defaults.AI.EmbeddingModel, defaultEmbeddingModel),
		EmbeddingDim:       resolveInt("EMBEDDING_DIM", dotenvValues, defaults.AI.EmbeddingDim, defaultEmbeddingDim),
		RerankerModel:      resolveString("RERANKER_MODEL", dotenvValues, defaults.AI.RerankerModel, defaultRerankerModel),
		UploadStorageDir:   resolveString("UPLOAD_STORAGE_DIR", dotenvValues, "", defaultUploadStorageDir),
		UploadMaxBytes:     resolveInt("UPLOAD_MAX_BYTES", dotenvValues, 0, defaultUploadMaxBytes),
		LogLevel:           resolveString("LOG_LEVEL", dotenvValues, defaults.Log.Level, defaultLogLevel),
		LogFormat:          resolveString("LOG_FORMAT", dotenvValues, defaults.Log.Format, defaultLogFormat),
		LogAddSource:       resolveBool("LOG_ADD_SOURCE", dotenvValues, defaults.Log.AddSource, false),
	}
}

// ValidateForServer 校验启动 HTTP 服务和 embedding / reranker 客户端所需的最小配置。
func (c Config) ValidateForServer() error {
	var missing []string

	if strings.TrimSpace(c.ServerPort) == "" {
		missing = append(missing, "SERVER_PORT")
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}

	if strings.TrimSpace(c.SiliconFlowAPIKey) == "" {
		missing = append(missing, "SILICONFLOW_API_KEY")
	}

	if strings.TrimSpace(c.LLMModel) == "" {
		missing = append(missing, "LLM_MODEL")
	}

	if strings.TrimSpace(c.EmbeddingModel) == "" {
		missing = append(missing, "EMBEDDING_MODEL")
	}

	if strings.TrimSpace(c.RerankerModel) == "" {
		missing = append(missing, "RERANKER_MODEL")
	}

	if len(missing) > 0 {
		return fmt.Errorf("缺少必填配置：%s", strings.Join(missing, ", "))
	}

	if c.EmbeddingDim <= 0 {
		return fmt.Errorf("EMBEDDING_DIM 无效：%d", c.EmbeddingDim)
	}

	return nil
}

// loadDefaultFileConfig 允许命令从仓库根目录或嵌套应用目录启动时都能读取 config/default.yaml。
func loadDefaultFileConfig() fileConfig {
	path, ok := findUpward(filepath.Join("config", "default.yaml"))
	if !ok {
		return fileConfig{}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}
	}

	var cfg fileConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return fileConfig{}
	}

	return cfg
}

// loadDotEnvValues 以最小语义解析 dotenv 内容，但不会修改当前进程环境变量。
func loadDotEnvValues() map[string]string {
	path, ok := findUpward(".env")
	if !ok {
		return map[string]string{}
	}

	file, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}

		values[key] = value
	}

	return values
}

// resolveString 按既定优先级解析字符串配置项。
func resolveString(key string, dotenvValues map[string]string, fileValue string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	if value := strings.TrimSpace(dotenvValues[key]); value != "" {
		return value
	}

	if value := strings.TrimSpace(fileValue); value != "" {
		return value
	}

	return fallback
}

// resolveInt 按既定优先级解析整型配置项。
func resolveInt(key string, dotenvValues map[string]string, fileValue int, fallback int) int {
	if value, ok := parseInt(strings.TrimSpace(os.Getenv(key))); ok {
		return value
	}

	if value, ok := parseInt(strings.TrimSpace(dotenvValues[key])); ok {
		return value
	}

	if fileValue > 0 {
		return fileValue
	}

	return fallback
}

// resolveBool 按既定优先级解析布尔配置项。
func resolveBool(key string, dotenvValues map[string]string, fileValue bool, fallback bool) bool {
	if value, ok := parseBool(strings.TrimSpace(os.Getenv(key))); ok {
		return value
	}

	if value, ok := parseBool(strings.TrimSpace(dotenvValues[key])); ok {
		return value
	}

	if fileValue {
		return true
	}

	return fallback
}

// parseInt 在解析失败时返回 false，便于上层继续走回退逻辑。
func parseInt(value string) (int, bool) {
	if value == "" {
		return 0, false
	}

	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}

	return number, true
}

// parseBool 在解析失败时返回 false，便于上层继续走回退逻辑。
func parseBool(value string) (bool, bool) {
	if value == "" {
		return false, false
	}

	boolean, err := strconv.ParseBool(value)
	if err != nil {
		return false, false
	}

	return boolean, true
}

// findUpward 会向上遍历父目录，让本地工具从不同工作目录执行时都能找到目标文件。
func findUpward(target string) (string, bool) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for {
		candidate := filepath.Join(currentDir, target)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true
		}
		if isGitBoundary(currentDir) {
			return "", false
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", false
		}

		currentDir = parentDir
	}
}

func isGitBoundary(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
