package websearch

import (
	"fmt"
	"time"
)

// SearchOptions 是 assistant 主后端调用 WebSearchProvider 时传入的搜索选项。
type SearchOptions struct {
	Limit   int
	TraceID string
}

// FetchOptions 是 assistant 主后端调用 WebSearchProvider 时传入的抓取选项。
type FetchOptions struct {
	TraceID string
}

// SearchResult 表示 assistant 主后端收到的一条网页候选结果。
type SearchResult struct {
	Rank        int        `json:"rank"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Snippet     string     `json:"snippet"`
	Source      string     `json:"source"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Score       float64    `json:"score,omitempty"`
}

// SearchResponse 是 assistant 主后端看到的搜索响应。
type SearchResponse struct {
	Provider  string         `json:"provider"`
	ElapsedMS int64          `json:"elapsed_ms"`
	RequestID string         `json:"request_id"`
	Results   []SearchResult `json:"results"`
}

// FetchedPage 是 assistant 主后端看到的网页抓取响应。
type FetchedPage struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	FinalURL    string    `json:"final_url"`
	Text        string    `json:"text"`
	FetchedAt   time.Time `json:"fetched_at"`
	Publisher   string    `json:"publisher"`
	ContentType string    `json:"content_type"`
	StatusCode  int       `json:"status_code"`
	BytesRead   int64     `json:"bytes_read"`
	Truncated   bool      `json:"truncated"`
	ElapsedMS   int64     `json:"elapsed_ms"`
	RequestID   string    `json:"request_id"`
}

// ErrorCode 是 provider 层向 assistant 主链暴露的稳定错误码。
type ErrorCode string

const (
	ErrorInvalidRequest      ErrorCode = "invalid_request"
	ErrorProviderTimeout     ErrorCode = "provider_timeout"
	ErrorProviderUnavailable ErrorCode = "provider_unavailable"
	ErrorInvalidResponse     ErrorCode = "invalid_response"
)

// ProviderError 表示 MCP provider 调用失败或工具层返回错误。
type ProviderError struct {
	Code      ErrorCode
	Message   string
	Provider  string
	RequestID string
	Err       error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("mcp web search %s: %s", e.Code, e.Message)
	}
	return "mcp web search " + string(e.Code)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
