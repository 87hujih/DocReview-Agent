package config

import (
	"os"
	"strings"
)

const (
	defaultServerPort  = "8080"
	defaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/agent_project?sslmode=disable"
)

type Config struct {
	ServerPort  string
	DatabaseURL string
}

func Load() Config {
	return Config{
		ServerPort:  envOrDefault("SERVER_PORT", defaultServerPort),
		DatabaseURL: envOrDefault("DATABASE_URL", defaultDatabaseURL),
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}
