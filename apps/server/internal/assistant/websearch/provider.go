package websearch

import "context"

// WebSearchProvider 是 assistant 主后端唯一可见的外部搜索抽象。
type WebSearchProvider interface {
	Search(ctx context.Context, query string, opts SearchOptions) (*SearchResponse, error)
	Fetch(ctx context.Context, rawURL string, opts FetchOptions) (*FetchedPage, error)
}
