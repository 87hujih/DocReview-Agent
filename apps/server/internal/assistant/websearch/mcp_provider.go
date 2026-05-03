package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"agent_project/apps/server/internal/mcpwebsearch"
)

// MCPConfig 描述 MCPWebSearchProvider 连接 mcp-web-search sidecar 的参数。
type MCPConfig struct {
	URL             string
	ProtocolVersion string
	HTTPClient      *http.Client
	Timeout         time.Duration
	AuthToken       string
	Origin          string
}

// MCPWebSearchProvider 通过 MCP tools/call 调用 web.search / web.fetch。
type MCPWebSearchProvider struct {
	cfg    MCPConfig
	client *http.Client
	seq    atomic.Uint64
}

func NewMCPWebSearchProvider(cfg MCPConfig) *MCPWebSearchProvider {
	if cfg.ProtocolVersion == "" {
		cfg.ProtocolVersion = mcpwebsearch.ProtocolVersion
	}
	cfg.URL = normalizeMCPURL(cfg.URL)
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &MCPWebSearchProvider{cfg: cfg, client: client}
}

func normalizeMCPURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/mcp"
	}
	return parsed.String()
}

func (p *MCPWebSearchProvider) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResponse, error) {
	args := map[string]any{
		"query":    query,
		"limit":    opts.Limit,
		"trace_id": opts.TraceID,
	}

	var response SearchResponse
	if err := p.callTool(ctx, "web.search", args, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (p *MCPWebSearchProvider) Fetch(ctx context.Context, rawURL string, opts FetchOptions) (*FetchedPage, error) {
	args := map[string]any{
		"url":      rawURL,
		"trace_id": opts.TraceID,
	}

	var page FetchedPage
	if err := p.callTool(ctx, "web.fetch", args, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (p *MCPWebSearchProvider) callTool(ctx context.Context, name string, args map[string]any, target any) error {
	if strings.TrimSpace(p.cfg.URL) == "" {
		return &ProviderError{Code: ErrorInvalidRequest, Message: "MCP URL is required"}
	}

	callCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	requestPayload := rpcRequest{
		JSONRPC: "2.0",
		ID:      p.nextID(name),
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      name,
			Arguments: args,
		},
	}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		return &ProviderError{Code: ErrorInvalidRequest, Message: "marshal MCP request failed", Err: err}
	}

	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return &ProviderError{Code: ErrorInvalidRequest, Message: "create MCP request failed", Err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("MCP-Protocol-Version", p.cfg.ProtocolVersion)
	request.Header.Set("Mcp-Method", "tools/call")
	request.Header.Set("Mcp-Name", name)
	if p.cfg.AuthToken != "" {
		request.Header.Set("Authorization", "Bearer "+p.cfg.AuthToken)
	}
	if p.cfg.Origin != "" {
		request.Header.Set("Origin", p.cfg.Origin)
	}

	response, err := p.client.Do(request)
	if err != nil {
		code := ErrorProviderUnavailable
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			code = ErrorProviderTimeout
		}
		return &ProviderError{Code: code, Message: "call MCP server failed", Err: err}
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return &ProviderError{Code: ErrorInvalidResponse, Message: "read MCP response failed", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &ProviderError{Code: ErrorProviderUnavailable, Message: fmt.Sprintf("MCP server returned HTTP %d", response.StatusCode)}
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(responseBody, &rpcResp); err != nil {
		return &ProviderError{Code: ErrorInvalidResponse, Message: "decode MCP JSON-RPC response failed", Err: err}
	}
	if rpcResp.Error != nil {
		return &ProviderError{Code: ErrorInvalidResponse, Message: rpcResp.Error.Message}
	}

	var result toolResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return &ProviderError{Code: ErrorInvalidResponse, Message: "decode MCP tool result failed", Err: err}
	}
	if result.IsError {
		return providerErrorFromToolResult(result)
	}

	if len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		if err := json.Unmarshal(result.StructuredContent, target); err != nil {
			return &ProviderError{Code: ErrorInvalidResponse, Message: "decode MCP structured content failed", Err: err}
		}
		return nil
	}
	if len(result.Content) == 0 {
		return &ProviderError{Code: ErrorInvalidResponse, Message: "MCP tool result has no content"}
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), target); err != nil {
		return &ProviderError{Code: ErrorInvalidResponse, Message: "decode MCP text content failed", Err: err}
	}
	return nil
}

func (p *MCPWebSearchProvider) nextID(name string) string {
	return fmt.Sprintf("%s-%d", strings.ReplaceAll(name, ".", "-"), p.seq.Add(1))
}

func providerErrorFromToolResult(result toolResult) error {
	var payload toolErrorPayload
	if len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		_ = json.Unmarshal(result.StructuredContent, &payload)
	}
	if payload.Code == "" && len(result.Content) > 0 {
		_ = json.Unmarshal([]byte(result.Content[0].Text), &payload)
	}
	if payload.Code == "" {
		payload.Code = string(ErrorProviderUnavailable)
	}
	if payload.Message == "" {
		payload.Message = "MCP tool returned an error"
	}
	return &ProviderError{
		Code:      ErrorCode(payload.Code),
		Message:   payload.Message,
		Provider:  payload.Provider,
		RequestID: payload.RequestID,
	}
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  toolCallParams `json:"params"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolResult struct {
	Content           []toolContent   `json:"content"`
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Provider  string `json:"provider"`
	RequestID string `json:"request_id"`
}
