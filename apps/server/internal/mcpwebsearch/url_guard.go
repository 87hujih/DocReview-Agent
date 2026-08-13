package mcpwebsearch

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

type netResolver struct{}

func (netResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// ValidateFetchURL 在发起抓取前校验 scheme、hostname 和解析后的 IP，避免 SSRF。
func ValidateFetchURL(ctx context.Context, rawURL string, resolver Resolver) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, newToolError(ErrorInvalidRequest, "url is required", "url_guard", "", 0, nil)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, newToolError(ErrorInvalidRequest, "url is invalid", "url_guard", "", 0, err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, newToolError(ErrorInvalidRequest, "url scheme must be http or https", "url_guard", "", 0, nil)
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, newToolError(ErrorFetchBlocked, "URL resolves to localhost", "url_guard", "", 0, nil)
	}

	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return nil, newToolError(ErrorFetchBlocked, "URL resolves to a private or local network address", "url_guard", "", 0, nil)
		}
		return parsed, nil
	}

	if resolver == nil {
		resolver = netResolver{}
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil, newToolError(ErrorFetchBlocked, fmt.Sprintf("URL host %q cannot be resolved safely", host), "url_guard", "", 0, err)
	}
	for _, addr := range addrs {
		if isBlockedIP(addr.IP) {
			return nil, newToolError(ErrorFetchBlocked, "URL resolves to a private or local network address", "url_guard", "", 0, nil)
		}
	}

	return parsed, nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	addr, err := netip.ParseAddr(ip.String())
	if err != nil {
		return true
	}

	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}
