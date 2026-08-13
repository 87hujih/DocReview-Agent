package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"agent_project/apps/server/internal/mcpwebsearch"
)

// main 启动独立的 Web Search MCP Server sidecar。
func main() {
	cfg := mcpwebsearch.LoadConfigFromEnv()
	transport := flag.String("transport", cfg.Transport, "MCP transport: streamable_http or stdio")
	listenAddr := flag.String("listen", cfg.ListenAddr, "Streamable HTTP listen address")
	flag.Parse()

	cfg.Transport = strings.TrimSpace(*transport)
	cfg.ListenAddr = strings.TrimSpace(*listenAddr)

	if cfg.ProviderName != "mock" {
		log.Fatalf("WEB_SEARCH_PROVIDER=%s 尚未实现；第一阶段仅支持 mock provider", cfg.ProviderName)
	}

	provider := mcpwebsearch.NewMockProvider(cfg.ProviderName)
	server := mcpwebsearch.NewServer(cfg, provider, provider)

	switch cfg.Transport {
	case "stdio":
		if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
			log.Fatalf("stdio MCP server failed: %v", err)
		}
	case "streamable_http", "":
		log.Printf("mcp-web-search listening on %s%s", cfg.ListenAddr, "/mcp")
		if err := http.ListenAndServe(cfg.ListenAddr, server.HTTPHandler()); err != nil {
			log.Fatalf("streamable HTTP MCP server failed: %v", err)
		}
	default:
		log.Fatalf("unsupported MCP transport: %s", cfg.Transport)
	}
}
