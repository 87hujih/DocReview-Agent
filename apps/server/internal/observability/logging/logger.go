package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger 创建统一的结构化日志实例。
func NewLogger(service string, level string, format string, addSource bool, writer io.Writer) *slog.Logger {
	if writer == nil {
		writer = os.Stdout
	}

	options := &slog.HandlerOptions{
		AddSource: addSource,
		Level:     parseLevel(level),
	}

	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(format), "text") {
		handler = slog.NewTextHandler(writer, options)
	} else {
		handler = slog.NewJSONHandler(writer, options)
	}

	environment := strings.TrimSpace(os.Getenv("APP_ENV"))
	if environment == "" {
		environment = "local"
	}

	return slog.New(handler).With(
		slog.String("service", service),
		slog.String("env", environment),
	)
}

// parseLevel 解析 `Level`，把格式和参数错误收口到当前边界。
func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
