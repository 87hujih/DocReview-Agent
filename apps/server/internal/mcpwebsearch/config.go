package mcpwebsearch

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTransport      = "streamable_http"
	defaultListenAddr     = "127.0.0.1:18090"
	defaultProvider       = "mock"
	defaultMaxResults     = 5
	defaultMaxFetchBytes  = 262144
	defaultMaxRedirects   = 3
	defaultTimeoutSeconds = 10
)

// LoadConfigFromEnv 从 WEB_SEARCH_* 环境变量读取 MCP 工具服务配置。
func LoadConfigFromEnv() Config {
	return Config{
		Transport:      getenvString("WEB_SEARCH_MCP_TRANSPORT", defaultTransport),
		ListenAddr:     getenvString("WEB_SEARCH_MCP_LISTEN_ADDR", defaultListenAddr),
		Endpoint:       defaultEndpoint,
		AllowedOrigins: getenvCSV("WEB_SEARCH_MCP_ALLOWED_ORIGINS"),
		AuthToken:      strings.TrimSpace(os.Getenv("WEB_SEARCH_MCP_AUTH_TOKEN")),
		ProviderName:   getenvString("WEB_SEARCH_PROVIDER", defaultProvider),
		DefaultLimit:   getenvInt("WEB_SEARCH_DEFAULT_LIMIT", defaultMaxResults),
		MaxResults:     getenvInt("WEB_SEARCH_MAX_RESULTS", defaultMaxResults),
		MaxFetchBytes:  int64(getenvInt("WEB_SEARCH_MAX_FETCH_BYTES", defaultMaxFetchBytes)),
		MaxRedirects:   getenvInt("WEB_SEARCH_MAX_REDIRECTS", defaultMaxRedirects),
		SearchTimeout:  time.Duration(getenvInt("WEB_SEARCH_TIMEOUT_SECONDS", defaultTimeoutSeconds)) * time.Second,
		FetchTimeout:   time.Duration(getenvInt("WEB_SEARCH_FETCH_TIMEOUT_SECONDS", defaultTimeoutSeconds)) * time.Second,
	}
}

func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.Transport) == "" {
		c.Transport = defaultTransport
	}
	if strings.TrimSpace(c.ListenAddr) == "" {
		c.ListenAddr = defaultListenAddr
	}
	if strings.TrimSpace(c.Endpoint) == "" {
		c.Endpoint = defaultEndpoint
	}
	if strings.TrimSpace(c.ProviderName) == "" {
		c.ProviderName = defaultProvider
	}
	if c.MaxResults <= 0 {
		c.MaxResults = defaultMaxResults
	}
	if c.DefaultLimit <= 0 || c.DefaultLimit > c.MaxResults {
		c.DefaultLimit = c.MaxResults
	}
	if c.MaxFetchBytes <= 0 {
		c.MaxFetchBytes = defaultMaxFetchBytes
	}
	if c.MaxRedirects <= 0 {
		c.MaxRedirects = defaultMaxRedirects
	}
	if c.SearchTimeout <= 0 {
		c.SearchTimeout = defaultTimeoutSeconds * time.Second
	}
	if c.FetchTimeout <= 0 {
		c.FetchTimeout = defaultTimeoutSeconds * time.Second
	}
	if c.Resolver == nil {
		c.Resolver = netResolver{}
	}
	return c
}

func getenvString(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return fallback
	}
	return number
}

func getenvCSV(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}
