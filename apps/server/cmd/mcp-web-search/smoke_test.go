//go:build smoke

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMCPWebSearchCommandHTTPSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exePath := filepath.Join(t.TempDir(), "mcp-web-search-smoke")
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}

	build := exec.CommandContext(ctx, "go", "build", "-o", exePath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build smoke command: %v\n%s", err, string(output))
	}

	addr := reserveTCPAddr(t)
	cmd := exec.CommandContext(ctx, exePath, "--transport", "streamable_http", "--listen", addr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start smoke command: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	baseURL := "http://" + addr + "/mcp"
	initResponse := waitForMCPReady(t, ctx, baseURL)
	if got := initResponse.Result["serverInfo"].(map[string]any)["name"]; got != "mcp-web-search" {
		t.Fatalf("expected server name mcp-web-search, got %#v", got)
	}

	toolsResponse := callMCP(t, ctx, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "tools-1",
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	toolNames := map[string]bool{}
	for _, rawTool := range toolsResponse.Result["tools"].([]any) {
		tool := rawTool.(map[string]any)
		toolNames[tool["name"].(string)] = true
	}
	if !toolNames["web.search"] || !toolNames["web.fetch"] {
		t.Fatalf("tools/list missing web.search or web.fetch: %#v", toolNames)
	}

	searchResponse := callMCP(t, ctx, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "search-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web.search",
			"arguments": map[string]any{
				"query":    "assistant web search mcp provider",
				"limit":    2,
				"trace_id": "smoke-search",
			},
		},
	})
	searchResult := searchResponse.Result
	if searchResult["isError"] == true {
		t.Fatalf("web.search returned tool error: %#v", searchResult["structuredContent"])
	}
	searchContent := searchResult["structuredContent"].(map[string]any)
	if searchContent["provider"] != "mock" {
		t.Fatalf("expected mock provider, got %#v", searchContent["provider"])
	}
	if got := len(searchContent["results"].([]any)); got != 2 {
		t.Fatalf("expected 2 search results, got %d", got)
	}

	fetchResponse := callMCP(t, ctx, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "fetch-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web.fetch",
			"arguments": map[string]any{
				"url":      "https://example.com/article",
				"trace_id": "smoke-fetch",
			},
		},
	})
	fetchResult := fetchResponse.Result
	if fetchResult["isError"] == true {
		t.Fatalf("web.fetch returned tool error: %#v", fetchResult["structuredContent"])
	}
	fetchContent := fetchResult["structuredContent"].(map[string]any)
	if fetchContent["final_url"] != "https://example.com/article" {
		t.Fatalf("unexpected fetch final_url: %#v", fetchContent["final_url"])
	}
	if fetchContent["text"] == "" {
		t.Fatalf("expected fetched text")
	}

	blockedResponse := callMCP(t, ctx, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "fetch-blocked-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "web.fetch",
			"arguments": map[string]any{"url": "http://127.0.0.1/admin"},
		},
	})
	blockedResult := blockedResponse.Result
	if blockedResult["isError"] != true {
		t.Fatalf("localhost fetch was not blocked: %#v", blockedResult)
	}
	blockedContent := blockedResult["structuredContent"].(map[string]any)
	if blockedContent["code"] != "fetch_blocked" {
		t.Fatalf("expected fetch_blocked, got %#v", blockedContent["code"])
	}
}

func reserveTCPAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp addr: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func waitForMCPReady(t *testing.T, ctx context.Context, baseURL string) rpcSmokeResponse {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := tryCallMCP(ctx, baseURL, map[string]any{
			"jsonrpc": "2.0",
			"id":      "init-1",
			"method":  "initialize",
			"params":  map[string]any{},
		})
		if err == nil {
			return response
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mcp server did not become ready: %v", lastErr)
	return rpcSmokeResponse{}
}

func callMCP(t *testing.T, ctx context.Context, baseURL string, payload map[string]any) rpcSmokeResponse {
	t.Helper()

	response, err := tryCallMCP(ctx, baseURL, payload)
	if err != nil {
		t.Fatalf("call mcp %s: %v", payload["method"], err)
	}
	return response
}

func tryCallMCP(ctx context.Context, baseURL string, payload map[string]any) (rpcSmokeResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return rpcSmokeResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return rpcSmokeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rpcSmokeResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return rpcSmokeResponse{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var decoded rpcSmokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return rpcSmokeResponse{}, err
	}
	if decoded.Error != nil {
		return rpcSmokeResponse{}, fmt.Errorf("json-rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return decoded, nil
}

type rpcSmokeResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  map[string]any `json:"result"`
	Error   *rpcSmokeError `json:"error"`
}

type rpcSmokeError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
