package mcpwebsearch

import (
	"errors"
	"fmt"
)

// ErrorCode 是 MCP 工具层对外暴露的稳定错误码。
type ErrorCode string

const (
	ErrorInvalidRequest      ErrorCode = "invalid_request"
	ErrorProviderAuthFailed  ErrorCode = "provider_auth_failed"
	ErrorProviderRateLimited ErrorCode = "provider_rate_limited"
	ErrorProviderTimeout     ErrorCode = "provider_timeout"
	ErrorProviderUnavailable ErrorCode = "provider_unavailable"
	ErrorFetchBlocked        ErrorCode = "fetch_blocked"
	ErrorFetchTimeout        ErrorCode = "fetch_timeout"
	ErrorFetchTooLarge       ErrorCode = "fetch_too_large"
	ErrorUnsupportedContent  ErrorCode = "unsupported_content_type"
	ErrorInvalidResponse     ErrorCode = "invalid_response"
)

// ToolError 保存工具层错误的机器可读信息。
type ToolError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Provider  string    `json:"provider,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	ElapsedMS int64     `json:"elapsed_ms"`
	Err       error     `json:"-"`
}

func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return string(e.Code)
}

func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newToolError(code ErrorCode, message string, provider string, requestID string, elapsedMS int64, err error) *ToolError {
	return &ToolError{
		Code:      code,
		Message:   message,
		Provider:  provider,
		RequestID: requestID,
		ElapsedMS: elapsedMS,
		Err:       err,
	}
}

func normalizeToolError(err error, fallback ErrorCode, provider string, elapsedMS int64) *ToolError {
	if err == nil {
		return nil
	}

	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		if toolErr.Provider == "" {
			toolErr.Provider = provider
		}
		if toolErr.ElapsedMS == 0 {
			toolErr.ElapsedMS = elapsedMS
		}
		return toolErr
	}

	return newToolError(fallback, err.Error(), provider, "", elapsedMS, err)
}
