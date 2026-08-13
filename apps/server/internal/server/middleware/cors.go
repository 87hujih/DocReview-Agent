package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const (
	allowHeaders = "Content-Type, X-Request-ID"
	allowMethods = "GET, POST, PATCH, DELETE, OPTIONS"
)

// CORS only emits cross-origin permissions 用于 一个 exact 已配置的 origin match.
func CORS(allowedOrigins []string) app.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	return func(ctx context.Context, requestCtx *app.RequestContext) {
		origin := strings.TrimSpace(string(requestCtx.Request.Header.Peek("Origin")))
		_, originAllowed := allowed[origin]
		if origin != "" {
			requestCtx.Header("Vary", "Origin")
		}

		if originAllowed {
			requestCtx.Header("Access-Control-Allow-Origin", origin)
			requestCtx.Header("Access-Control-Allow-Methods", allowMethods)
			requestCtx.Header("Access-Control-Allow-Headers", allowHeaders)
			requestCtx.Header("Access-Control-Expose-Headers", requestIDHeader)
		}

		if strings.EqualFold(string(requestCtx.Method()), consts.MethodOptions) {
			if origin != "" && !originAllowed {
				requestCtx.AbortWithStatus(consts.StatusForbidden)
				return
			}
			requestCtx.AbortWithStatus(consts.StatusNoContent)
			return
		}

		requestCtx.Next(ctx)
	}
}
