package mcpwebsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPToolsListExposesWebSearchAndFetch(t *testing.T) {
	handler := newTestMCPHandler()

	response := postJSONRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "list-1",
		"method":  "tools/list",
		"params":  map[string]any{},
	})

	tools := response["result"].(map[string]any)["tools"].([]any)
	names := make(map[string]bool, len(tools))
	for _, rawTool := range tools {
		tool := rawTool.(map[string]any)
		names[tool["name"].(string)] = true
	}

	if !names["web.search"] || !names["web.fetch"] {
		t.Fatalf("expected web.search and web.fetch tools, got %#v", names)
	}
}

func TestSearchToolValidatesQueryAndCapsLimit(t *testing.T) {
	handler := newTestMCPHandler()

	invalid := postJSONRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "search-invalid",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "web.search",
			"arguments": map[string]any{"limit": 2},
		},
	})

	invalidResult := invalid["result"].(map[string]any)
	if invalidResult["isError"] != true {
		t.Fatalf("expected invalid search to return tool error, got %#v", invalidResult)
	}
	if code := invalidResult["structuredContent"].(map[string]any)["code"]; code != string(ErrorInvalidRequest) {
		t.Fatalf("expected invalid_request, got %#v", code)
	}

	valid := postJSONRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "search-valid",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web.search",
			"arguments": map[string]any{
				"query":    "mcp provider",
				"limit":    99,
				"trace_id": "trace-search",
			},
		},
	})

	result := valid["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("expected search success, got %#v", result)
	}
	content := result["structuredContent"].(map[string]any)
	if provider := content["provider"]; provider != "mock" {
		t.Fatalf("expected mock provider, got %#v", provider)
	}
	results := content["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("expected limit capped to 3 results, got %d", len(results))
	}
	if requestID := content["request_id"]; requestID == "" {
		t.Fatalf("expected request_id in search response")
	}
}

func TestFetchToolBlocksUnsafeURLs(t *testing.T) {
	handler := newTestMCPHandler()

	for _, tc := range []struct {
		name string
		url  string
		code ErrorCode
	}{
		{name: "unsupported scheme", url: "file:///etc/passwd", code: ErrorInvalidRequest},
		{name: "localhost", url: "http://127.0.0.1/admin", code: ErrorFetchBlocked},
		{name: "metadata ip", url: "http://169.254.169.254/latest/meta-data", code: ErrorFetchBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := postJSONRPC(t, handler, map[string]any{
				"jsonrpc": "2.0",
				"id":      "fetch-unsafe",
				"method":  "tools/call",
				"params": map[string]any{
					"name":      "web.fetch",
					"arguments": map[string]any{"url": tc.url},
				},
			})

			result := response["result"].(map[string]any)
			if result["isError"] != true {
				t.Fatalf("expected fetch to be blocked, got %#v", result)
			}
			if code := result["structuredContent"].(map[string]any)["code"]; code != string(tc.code) {
				t.Fatalf("expected %s, got %#v", tc.code, code)
			}
		})
	}
}

func TestFetchToolReturnsMockPageForAllowedPublicURL(t *testing.T) {
	handler := newTestMCPHandler()

	response := postJSONRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "fetch-ok",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web.fetch",
			"arguments": map[string]any{
				"url":      "https://example.com/article",
				"trace_id": "trace-fetch",
			},
		},
	})

	result := response["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("expected fetch success, got %#v", result)
	}
	content := result["structuredContent"].(map[string]any)
	if content["final_url"] != "https://example.com/article" {
		t.Fatalf("expected final URL to match input, got %#v", content["final_url"])
	}
	if content["text"] == "" || content["request_id"] == "" {
		t.Fatalf("expected text and request_id in fetch response, got %#v", content)
	}
}

func TestHTTPOriginGuardRejectsUnexpectedOrigin(t *testing.T) {
	handler := newTestMCPHandler()
	body := mustMarshalJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "list-1",
		"method":  "tools/list",
	})

	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden for unexpected origin, got %d", recorder.Code)
	}
}

func newTestMCPHandler() http.Handler {
	cfg := Config{
		ProviderName:   "mock",
		DefaultLimit:   2,
		MaxResults:     3,
		MaxFetchBytes:  1024,
		MaxRedirects:   3,
		SearchTimeout:  time.Second,
		FetchTimeout:   time.Second,
		AllowedOrigins: []string{"http://127.0.0.1:18080"},
		Resolver: staticResolver{
			ips: map[string][]net.IP{
				"example.com": {net.ParseIP("93.184.216.34")},
			},
		},
	}
	provider := NewMockProvider("mock")
	return NewServer(cfg, provider, provider).HTTPHandler()
}

func postJSONRPC(t *testing.T, handler http.Handler, payload map[string]any) map[string]any {
	t.Helper()

	body := mustMarshalJSON(t, payload)
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	request.Header.Set("Origin", "http://127.0.0.1:18080")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, recorder.Body.String())
	}
	if _, hasProtocolError := response["error"]; hasProtocolError {
		t.Fatalf("unexpected JSON-RPC error: %#v", response["error"])
	}
	return response
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return body
}

type staticResolver struct {
	ips map[string][]net.IP
}

func (r staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
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
