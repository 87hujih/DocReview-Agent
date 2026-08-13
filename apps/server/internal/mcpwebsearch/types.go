package mcpwebsearch

import (
	"context"
	"net"
	"time"
)

const (
	// ProtocolVersion 是本工具层当前声明支持的 MCP 协议版本。
	ProtocolVersion = "2025-11-25"
	defaultEndpoint = "/mcp"
)

// Resolver 抽象 DNS 解析，便于测试 URL guard 并避免把解析逻辑散落到抓取器里。
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Config 描述 mcp-web-search 工具服务的运行参数。
type Config struct {
	Transport      string
	ListenAddr     string
	Endpoint       string
	AllowedOrigins []string
	AuthToken      string
	ProviderName   string
	DefaultLimit   int
	MaxResults     int
	MaxFetchBytes  int64
	MaxRedirects   int
	SearchTimeout  time.Duration
	FetchTimeout   time.Duration
	Resolver       Resolver
}

// SearchOptions 是 MCP Server 内部搜索 provider 接收的选项。
type SearchOptions struct {
	Limit   int
	TraceID string
}

// FetchOptions 是 MCP Server 内部网页抓取 provider 接收的选项。
type FetchOptions struct {
	TraceID      string
	MaxBytes     int64
	MaxRedirects int
}

// SearchResult 表示搜索 provider 返回的一条候选网页。
type SearchResult struct {
	Rank        int        `json:"rank"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Snippet     string     `json:"snippet"`
	Source      string     `json:"source"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Score       float64    `json:"score,omitempty"`
}

// SearchResponse 是 web.search 的结构化工具输出。
type SearchResponse struct {
	Provider  string         `json:"provider"`
	ElapsedMS int64          `json:"elapsed_ms"`
	RequestID string         `json:"request_id"`
	Results   []SearchResult `json:"results"`
}

// FetchedPage 是 web.fetch 的结构化工具输出。
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

// SearchProvider 定义 MCP Server 内部搜索服务的最小边界。
type SearchProvider interface {
	Search(ctx context.Context, query string, opts SearchOptions) (*SearchResponse, error)
}

// PageFetcher 定义 MCP Server 内部网页抓取服务的最小边界。
type PageFetcher interface {
	Fetch(ctx context.Context, rawURL string, opts FetchOptions) (*FetchedPage, error)
}
