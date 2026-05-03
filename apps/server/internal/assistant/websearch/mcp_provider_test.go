package websearch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent_project/apps/server/internal/mcpwebsearch"
)

func TestMCPWebSearchProviderSearchCallsStreamableHTTPServer(t *testing.T) {
	server := httptest.NewServer(newMockMCPServerHandler())
	defer server.Close()

	provider := NewMCPWebSearchProvider(MCPConfig{
		URL:        server.URL,
		HTTPClient: server.Client(),
		Timeout:    time.Second,
	})

	response, err := provider.Search(context.Background(), "mcp provider", SearchOptions{
		Limit:   1,
		TraceID: "trace-search",
	})
	if err != nil {
		t.Fatalf("search via mcp provider: %v", err)
	}

	if response.Provider != "mock" {
		t.Fatalf("expected mock provider, got %q", response.Provider)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(response.Results))
	}
	if response.Results[0].URL == "" || response.RequestID == "" {
		t.Fatalf("expected result URL and request ID, got %#v", response)
	}
}

func TestMCPWebSearchProviderFetchCallsStreamableHTTPServer(t *testing.T) {
	server := httptest.NewServer(newMockMCPServerHandler())
	defer server.Close()

	provider := NewMCPWebSearchProvider(MCPConfig{
		URL:        server.URL,
		HTTPClient: server.Client(),
		Timeout:    time.Second,
	})

	page, err := provider.Fetch(context.Background(), "https://example.com/article", FetchOptions{
		TraceID: "trace-fetch",
	})
	if err != nil {
		t.Fatalf("fetch via mcp provider: %v", err)
	}

	if page.FinalURL != "https://example.com/article" {
		t.Fatalf("expected final url to match, got %q", page.FinalURL)
	}
	if page.Text == "" || page.RequestID == "" {
		t.Fatalf("expected text and request ID, got %#v", page)
	}
}

func TestMCPWebSearchProviderUnavailableReturnsProviderError(t *testing.T) {
	server := httptest.NewServer(newMockMCPServerHandler())
	server.Close()

	provider := NewMCPWebSearchProvider(MCPConfig{
		URL:        server.URL,
		HTTPClient: server.Client(),
		Timeout:    50 * time.Millisecond,
	})

	_, err := provider.Search(context.Background(), "mcp provider", SearchOptions{Limit: 1})
	if err == nil {
		t.Fatalf("expected unavailable error")
	}

	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ErrorProviderUnavailable {
		t.Fatalf("expected provider_unavailable, got %s", providerErr.Code)
	}
}

func newMockMCPServerHandler() http.Handler {
	cfg := mcpwebsearch.Config{
		ProviderName:  "mock",
		DefaultLimit:  2,
		MaxResults:    3,
		MaxFetchBytes: 1024,
		SearchTimeout: time.Second,
		FetchTimeout:  time.Second,
		Resolver: mockResolver{
			ips: map[string][]net.IP{
				"example.com": {net.ParseIP("93.184.216.34")},
			},
		},
	}
	provider := mcpwebsearch.NewMockProvider("mock")
	return mcpwebsearch.NewServer(cfg, provider, provider).HTTPHandler()
}

type mockResolver struct {
	ips map[string][]net.IP
}

func (r mockResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips := r.ips[host]
	if len(ips) == 0 {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	addrs := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, net.IPAddr{IP: ip})
	}
	return addrs, nil
}
