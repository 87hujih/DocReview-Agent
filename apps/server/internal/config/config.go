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
	defaultLLMModel           = "MiniMax/MiniMax-M2.5"
	defaultEmbeddingModel     = "Qwen/Qwen3-Embedding-8B"
	defaultEmbeddingDim       = 1024
)

type Config struct {
	ServerPort  string
	DatabaseURL string

	SiliconFlowAPIKey  string
	SiliconFlowBaseURL string
	LLMModel           string
	EmbeddingModel     string
	EmbeddingDim       int
}

type fileConfig struct {
	Server serverConfig `yaml:"server"`
	AI     aiConfig     `yaml:"ai"`
}

type serverConfig struct {
	Port string `yaml:"port"`
}

type aiConfig struct {
	SiliconFlowBaseURL string `yaml:"siliconflow_base_url"`
	LLMModel           string `yaml:"llm_model"`
	EmbeddingModel     string `yaml:"embedding_model"`
	EmbeddingDim       int    `yaml:"embedding_dim"`
}

func Load() Config {
	defaults := loadDefaultFileConfig()
	dotenvValues := loadDotEnvValues()

	return Config{
		ServerPort:          resolveString("SERVER_PORT", dotenvValues, defaults.Server.Port, defaultServerPort),
		DatabaseURL:         resolveString("DATABASE_URL", dotenvValues, "", ""),
		SiliconFlowAPIKey:   resolveString("SILICONFLOW_API_KEY", dotenvValues, "", ""),
		SiliconFlowBaseURL:  resolveString("SILICONFLOW_BASE_URL", dotenvValues, defaults.AI.SiliconFlowBaseURL, defaultSiliconFlowBaseURL),
		LLMModel:            resolveString("LLM_MODEL", dotenvValues, defaults.AI.LLMModel, defaultLLMModel),
		EmbeddingModel:      resolveString("EMBEDDING_MODEL", dotenvValues, defaults.AI.EmbeddingModel, defaultEmbeddingModel),
		EmbeddingDim:        resolveInt("EMBEDDING_DIM", dotenvValues, defaults.AI.EmbeddingDim, defaultEmbeddingDim),
	}
}

func (c Config) ValidateForServer() error {
	var missing []string

	if strings.TrimSpace(c.ServerPort) == "" {
		missing = append(missing, "SERVER_PORT")
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}

	if strings.TrimSpace(c.EmbeddingModel) == "" {
		missing = append(missing, "EMBEDDING_MODEL")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}

	if c.EmbeddingDim <= 0 {
		return fmt.Errorf("invalid EMBEDDING_DIM: %d", c.EmbeddingDim)
	}

	return nil
}

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

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", false
		}

		currentDir = parentDir
	}
}
