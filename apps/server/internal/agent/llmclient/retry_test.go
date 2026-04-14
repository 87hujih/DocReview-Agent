package llmclient

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil 错误", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"包装的 deadline exceeded", fmt.Errorf("操作失败：%w", context.DeadlineExceeded), true},
		{"429 请求过多", errors.New("request failed: 429 Too Many Requests"), true},
		{"502 网关错误", errors.New("server error: 502 Bad Gateway"), true},
		{"503 服务不可用", errors.New("server error: 503"), true},
		{"504 网关超时", errors.New("server error: 504 Gateway Timeout"), true},
		{"400 请求格式错误", errors.New("request failed: 400 Bad Request"), false},
		{"401 未授权", errors.New("unauthorized: 401"), false},
		{"JSON 解析错误", errors.New("invalid character 'x' looking for beginning of value"), false},
		{"通用错误", errors.New("something went wrong"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRetryable(tc.err)
			if got != tc.want {
				t.Errorf("IsRetryable(%v) = %v，期望 %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCallWithRetrySuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	err := CallWithRetry(context.Background(), Config{RetryMax: 2, BackoffMS: 10}, func() error {
		calls++
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("期望无错误，实际得到 %v", err)
	}
	if calls != 1 {
		t.Fatalf("期望调用 1 次，实际调用 %d 次", calls)
	}
}

func TestCallWithRetryExhaustsMaxRetries(t *testing.T) {
	calls := 0
	retryableErr := fmt.Errorf("upstream error: 503 Service Unavailable")
	err := CallWithRetry(context.Background(), Config{RetryMax: 2, BackoffMS: 1}, func() error {
		calls++
		return retryableErr
	}, nil)
	if err == nil {
		t.Fatal("期望返回错误，实际得到 nil")
	}
	if calls != 3 { // 首次 + 2 次重试
		t.Fatalf("期望调用 3 次，实际调用 %d 次", calls)
	}
}

func TestCallWithRetryStopsOnNonRetryableError(t *testing.T) {
	calls := 0
	nonRetryableErr := errors.New("invalid character 'x' looking for beginning of value")
	err := CallWithRetry(context.Background(), Config{RetryMax: 3, BackoffMS: 1}, func() error {
		calls++
		return nonRetryableErr
	}, nil)
	if err == nil {
		t.Fatal("期望返回错误，实际得到 nil")
	}
	if calls != 1 {
		t.Fatalf("不可重试错误不应重试，期望调用 1 次，实际调用 %d 次", calls)
	}
}

func TestCallWithRetrySucceedsAfterRetry(t *testing.T) {
	calls := 0
	err := CallWithRetry(context.Background(), Config{RetryMax: 2, BackoffMS: 1}, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("temporary error: 503")
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("重试后期望无错误，实际得到 %v", err)
	}
	if calls != 3 {
		t.Fatalf("期望调用 3 次，实际调用 %d 次", calls)
	}
}

func TestCallWithRetryRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	callErr := fmt.Errorf("temporary error: 503")

	_ = CallWithRetry(ctx, Config{RetryMax: 5, BackoffMS: 50}, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return callErr
	}, nil)

	if calls > 2 {
		t.Fatalf("context 取消后期望最多调用 2 次，实际调用 %d 次", calls)
	}
}

func TestCallWithRetryInvokesOnRetryCallback(t *testing.T) {
	var retryAttempts []int
	var retryBackoffs []time.Duration
	retryableErr := fmt.Errorf("retry me: 503")

	_ = CallWithRetry(context.Background(), Config{RetryMax: 2, BackoffMS: 1}, func() error {
		return retryableErr
	}, func(attempt int, cause error, backoff time.Duration) {
		retryAttempts = append(retryAttempts, attempt)
		retryBackoffs = append(retryBackoffs, backoff)
	})

	if len(retryAttempts) != 2 {
		t.Fatalf("期望触发 2 次重试回调，实际触发 %d 次", len(retryAttempts))
	}
	if retryAttempts[0] != 1 || retryAttempts[1] != 2 {
		t.Fatalf("期望重试序号为 [1 2]，实际为 %v", retryAttempts)
	}
	for _, b := range retryBackoffs {
		if b <= 0 {
			t.Fatalf("期望退避时长大于 0，实际得到 %v", b)
		}
	}
}

func TestCallWithRetryZeroRetryMax(t *testing.T) {
	calls := 0
	err := CallWithRetry(context.Background(), Config{RetryMax: 0, BackoffMS: 10}, func() error {
		calls++
		return fmt.Errorf("error: 503")
	}, nil)
	if err == nil {
		t.Fatal("期望返回错误，实际得到 nil")
	}
	if calls != 1 {
		t.Fatalf("RetryMax=0 时期望调用 1 次，实际调用 %d 次", calls)
	}
}