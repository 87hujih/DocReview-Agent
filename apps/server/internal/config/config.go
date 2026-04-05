package config

import (
	"os"
	"strconv"
	"strings"
)

const (
	defaultServerPort  = "8080"
	defaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/agent_project?sslmode=disable"
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

func Load() Config {
	return Config{
		ServerPort:          envOrDefault("SERVER_PORT", defaultServerPort),
		DatabaseURL:         envOrDefault("DATABASE_URL", defaultDatabaseURL),
		SiliconFlowAPIKey:   envOrDefault("SILICONFLOW_API_KEY", ""),
		SiliconFlowBaseURL:  envOrDefault("SILICONFLOW_BASE_URL", "https://api.siliconflow.cn/v1"),
		LLMModel:            envOrDefault("LLM_MODEL", ""),
		EmbeddingModel:      envOrDefault("EMBEDDING_MODEL", ""),
		EmbeddingDim:        envOrDefaultInt("EMBEDDING_DIM", 1024),
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}

func envOrDefaultInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	number, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return number
}
