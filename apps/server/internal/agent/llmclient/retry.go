package llmclient

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"strings"
	"time"
)

// Config 控制 LLM 调用的超时和重试行为。
type Config struct {
	// TimeoutMS 是单次 HTTP 请求的超时时长（毫秒）。
	TimeoutMS int
	// RetryMax 是最大重试次数，不含首次请求。
	RetryMax int
	// BackoffMS 是首次退避基准时长（毫秒），后续按指数增长并加抖动。
	BackoffMS int
}

// IsRetryable 判断错误是否属于可重试类别：
// HTTP 429/502/503/504、网络超时、context.DeadlineExceeded。
// JSON 解析失败、HTTP 400/401/403 等客户端错误不可重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := err.Error()
	for _, marker := range []string{"429", "502", "503", "504", "Too Many Requests"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// CallWithRetry 使用指数退避重试 fn，最多调用 1+cfg.RetryMax 次。
// 遇到不可重试错误或超出重试次数上限时立即返回最后一次错误。
// onRetry 在每次退避前调用（可为 nil），参数为本次重试序号（从 1 开始）、触发原因和退避时长。
func CallWithRetry(ctx context.Context, cfg Config, fn func() error, onRetry func(attempt int, cause error, backoff time.Duration)) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.RetryMax; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if attempt >= cfg.RetryMax || !IsRetryable(lastErr) {
			break
		}
		wait := backoffDuration(attempt, cfg.BackoffMS)
		if onRetry != nil {
			onRetry(attempt+1, lastErr, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

// backoffDuration 计算指数退避时长，附加最多 20% 随机抖动。
func backoffDuration(attempt int, baseMS int) time.Duration {
	if baseMS <= 0 {
		baseMS = 1000
	}
	ms := baseMS * (1 << attempt)
	jitter := rand.Intn(ms/5 + 1)
	return time.Duration(ms+jitter) * time.Millisecond
}