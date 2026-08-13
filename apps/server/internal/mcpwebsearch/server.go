package mcpwebsearch

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Server 负责处理 MCP JSON-RPC 请求并分发到 web.search / web.fetch。
type Server struct {
	cfg      Config
	searcher SearchProvider
	fetcher  PageFetcher
}

func NewServer(cfg Config, searcher SearchProvider, fetcher PageFetcher) *Server {
	cfg = cfg.withDefaults()
	if searcher == nil || fetcher == nil {
		provider := NewMockProvider(cfg.ProviderName)
		if searcher == nil {
			searcher = provider
		}
		if fetcher == nil {
			fetcher = provider
		}
	}

	return &Server{cfg: cfg, searcher: searcher, fetcher: fetcher}
}

func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(s.handleHTTP)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.cfg.Endpoint {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if s.cfg.AuthToken != "" && !isValidBearerToken(r.Header.Get("Authorization"), s.cfg.AuthToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if version := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")); version != "" && version != ProtocolVersion {
		http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
		return
	}

	var request rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   newRPCError(-32700, "parse error"),
		})
		return
	}

	response := s.handleRequest(r.Context(), request)
	if response == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	encoder := json.NewEncoder(out)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var request rpcRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			if err := encoder.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   newRPCError(-32700, "parse error"),
			}); err != nil {
				return err
			}
			continue
		}

		response := s.handleRequest(ctx, request)
		if response == nil {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleRequest(ctx context.Context, request rpcRequest) *rpcResponse {
	if request.JSONRPC != "2.0" {
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: newRPCError(-32600, "invalid request")}
	}

	switch request.Method {
	case "initialize":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "mcp-web-search",
				"version": "0.1.0",
			},
		}}
	case "notifications/initialized":
		return nil
	case "tools/list":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": toolDefinitions()}}
	case "tools/call":
		result := s.handleToolCall(ctx, request.Params)
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result}
	default:
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: newRPCError(-32601, "method not found")}
	}
}

func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) toolResult {
	var params toolCallParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return errorToolResult(newToolError(ErrorInvalidRequest, "tool call params are invalid", s.cfg.ProviderName, newRequestID("tool"), 0, err))
	}

	switch params.Name {
	case "web.search":
		return s.handleSearch(ctx, params.Arguments)
	case "web.fetch":
		return s.handleFetch(ctx, params.Arguments)
	default:
		return errorToolResult(newToolError(ErrorInvalidRequest, "unknown tool: "+params.Name, s.cfg.ProviderName, newRequestID("tool"), 0, nil))
	}
}

func (s *Server) handleSearch(ctx context.Context, raw json.RawMessage) toolResult {
	startedAt := time.Now()
	var args searchArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return errorToolResult(newToolError(ErrorInvalidRequest, "web.search arguments are invalid", s.cfg.ProviderName, newRequestID("search"), 0, err))
		}
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		return errorToolResult(newToolError(ErrorInvalidRequest, "query is required", s.cfg.ProviderName, newRequestID("search"), 0, nil))
	}

	limit := args.Limit
	if limit <= 0 {
		limit = s.cfg.DefaultLimit
	}
	if limit > s.cfg.MaxResults {
		limit = s.cfg.MaxResults
	}

	callCtx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
	defer cancel()

	response, err := s.searcher.Search(callCtx, query, SearchOptions{Limit: limit, TraceID: args.TraceID})
	if err != nil {
		code := ErrorProviderUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			code = ErrorProviderTimeout
		}
		return errorToolResult(normalizeToolError(err, code, s.cfg.ProviderName, time.Since(startedAt).Milliseconds()))
	}
	if response.Provider == "" {
		response.Provider = s.cfg.ProviderName
	}
	if response.RequestID == "" {
		response.RequestID = newRequestID("search")
	}
	if response.ElapsedMS == 0 {
		response.ElapsedMS = time.Since(startedAt).Milliseconds()
	}

	return successToolResult(response)
}

func (s *Server) handleFetch(ctx context.Context, raw json.RawMessage) toolResult {
	startedAt := time.Now()
	var args fetchArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return errorToolResult(newToolError(ErrorInvalidRequest, "web.fetch arguments are invalid", s.cfg.ProviderName, newRequestID("fetch"), 0, err))
		}
	}

	parsed, err := ValidateFetchURL(ctx, args.URL, s.cfg.Resolver)
	if err != nil {
		return errorToolResult(normalizeToolError(err, ErrorFetchBlocked, "url_guard", time.Since(startedAt).Milliseconds()))
	}

	callCtx, cancel := context.WithTimeout(ctx, s.cfg.FetchTimeout)
	defer cancel()

	response, err := s.fetcher.Fetch(callCtx, parsed.String(), FetchOptions{
		TraceID:      args.TraceID,
		MaxBytes:     s.cfg.MaxFetchBytes,
		MaxRedirects: s.cfg.MaxRedirects,
	})
	if err != nil {
		code := ErrorProviderUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			code = ErrorFetchTimeout
		}
		return errorToolResult(normalizeToolError(err, code, s.cfg.ProviderName, time.Since(startedAt).Milliseconds()))
	}
	if response.RequestID == "" {
		response.RequestID = newRequestID("fetch")
	}
	if response.ElapsedMS == 0 {
		response.ElapsedMS = time.Since(startedAt).Milliseconds()
	}

	return successToolResult(response)
}

func (s *Server) originAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" || len(s.cfg.AllowedOrigins) == 0 {
		return true
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if origin == strings.TrimSpace(allowed) {
			return true
		}
	}
	return false
}

func isValidBearerToken(header, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header[len(prefix):]), []byte(token)) == 1
}

func toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "web.search",
			Title:       "Web Search",
			Description: "Search public web pages for external evidence candidates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":    map[string]any{"type": "string"},
					"limit":    map[string]any{"type": "integer", "minimum": 1},
					"trace_id": map[string]any{"type": "string"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "web.fetch",
			Title:       "Web Fetch",
			Description: "Fetch text from a public http or https URL as untrusted external evidence.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":      map[string]any{"type": "string"},
					"trace_id": map[string]any{"type": "string"},
				},
				"required": []string{"url"},
			},
		},
	}
}

func successToolResult(value any) toolResult {
	text := mustJSONText(value)
	return toolResult{
		Content:           []toolContent{{Type: "text", Text: text}},
		IsError:           false,
		StructuredContent: value,
	}
}

func errorToolResult(err *ToolError) toolResult {
	if err == nil {
		err = newToolError(ErrorProviderUnavailable, "unknown tool error", defaultProvider, newRequestID("tool"), 0, nil)
	}
	return toolResult{
		Content:           []toolContent{{Type: "text", Text: mustJSONText(err)}},
		IsError:           true,
		StructuredContent: err,
	}
}

func mustJSONText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf(`{"code":"%s","message":"failed to marshal tool result"}`, ErrorInvalidResponse)
	}
	return string(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var requestSeq atomic.Uint64

func newRequestID(prefix string) string {
	seq := requestSeq.Add(1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UTC().UnixNano(), seq)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newRPCError(code int, message string) *rpcError {
	return &rpcError{Code: code, Message: message}
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content           []toolContent `json:"content"`
	IsError           bool          `json:"isError"`
	StructuredContent any           `json:"structuredContent,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type searchArgs struct {
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
	TraceID string `json:"trace_id"`
}

type fetchArgs struct {
	URL     string `json:"url"`
	TraceID string `json:"trace_id"`
}
