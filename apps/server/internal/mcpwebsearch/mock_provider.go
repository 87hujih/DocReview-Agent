package mcpwebsearch

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// MockProvider 提供稳定的 search/fetch 输出，用于协议验证和单元测试。
type MockProvider struct {
	name string
}

func NewMockProvider(name string) *MockProvider {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultProvider
	}
	return &MockProvider{name: name}
}

func (p *MockProvider) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResponse, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultMaxResults
	}

	results := make([]SearchResult, 0, limit)
	for i := 1; i <= limit; i++ {
		results = append(results, SearchResult{
			Rank:    i,
			Title:   fmt.Sprintf("Mock result %d for %s", i, query),
			URL:     fmt.Sprintf("https://example.com/mock/%d", i),
			Snippet: fmt.Sprintf("Mock search snippet %d for %s.", i, query),
			Source:  "example.com",
			Score:   1 - float64(i-1)*0.05,
		})
	}

	return &SearchResponse{
		Provider:  p.name,
		ElapsedMS: time.Since(startedAt).Milliseconds(),
		RequestID: newRequestID("search"),
		Results:   results,
	}, nil
}

func (p *MockProvider) Fetch(ctx context.Context, rawURL string, _ FetchOptions) (*FetchedPage, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, newToolError(ErrorInvalidRequest, "URL is invalid", p.name, newRequestID("fetch"), time.Since(startedAt).Milliseconds(), err)
	}

	text := fmt.Sprintf("Mock fetched content from %s. External web content is untrusted evidence candidate text.", rawURL)
	return &FetchedPage{
		Title:       "Mock page for " + parsed.Hostname(),
		URL:         rawURL,
		FinalURL:    rawURL,
		Text:        text,
		FetchedAt:   time.Now().UTC(),
		Publisher:   parsed.Hostname(),
		ContentType: "text/plain",
		StatusCode:  200,
		BytesRead:   int64(len(text)),
		Truncated:   false,
		ElapsedMS:   time.Since(startedAt).Milliseconds(),
		RequestID:   newRequestID("fetch"),
	}, nil
}
