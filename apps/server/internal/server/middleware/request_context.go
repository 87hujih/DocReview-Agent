package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent_project/apps/server/internal/observability/logging"

	"github.com/cloudwego/hertz/pkg/app"
)

const requestIDHeader = "X-Request-ID"

// RequestContext 注入 request_id、返回响应头，并输出结构化 access log。
func RequestContext(service string, logger *slog.Logger) app.HandlerFunc {
	if logger == nil {
		logger = logging.NewLogger(service, "info", "json", false, nil)
	}

	return func(ctx context.Context, requestCtx *app.RequestContext) {
		requestID := strings.TrimSpace(string(requestCtx.Request.Header.Peek(requestIDHeader)))
		if requestID == "" {
			requestID = newRequestID()
		}

		requestCtx.Set("request_id", requestID)
		requestCtx.Response.Header.Set(requestIDHeader, requestID)

		ctx = logging.WithRequestID(ctx, requestID)
		startedAt := time.Now()
		method := string(requestCtx.Method())
		path := requestCtx.FullPath()
		if path == "" {
			path = string(requestCtx.Path())
		}

		defer func() {
			latency := time.Since(startedAt).Milliseconds()
			statusCode := requestCtx.Response.StatusCode()

			if recovered := recover(); recovered != nil {
				logger.ErrorContext(ctx, "request panic",
					slog.String("component", "http"),
					slog.String("event", "http.request.panic"),
					slog.String("request_id", requestID),
					slog.String("method", method),
					slog.String("path", path),
					slog.Int("status", statusCode),
					slog.Int64("latency_ms", latency),
					slog.Any("panic", recovered),
				)
				panic(recovered)
			}

			logger.InfoContext(ctx, "request completed",
				slog.String("component", "http"),
				slog.String("event", "http.request.completed"),
				slog.String("request_id", requestID),
				slog.String("method", method),
				slog.String("path", path),
				slog.Int("status", statusCode),
				slog.Int64("latency_ms", latency),
			)
		}()

		requestCtx.Next(ctx)
	}
}

// newRequestID 创建请求ID，并补齐当前链路需要的默认依赖和缺省行为。
func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	return hex.EncodeToString(buffer)
}
