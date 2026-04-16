package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const (
	allowHeaders = "Content-Type, X-Request-ID"
	allowMethods = "GET, POST, DELETE, OPTIONS"
)

// CORS 为当前前后端分离的本地运行方式补齐浏览器跨域头和预检处理。
func CORS() app.HandlerFunc {
	return func(ctx context.Context, requestCtx *app.RequestContext) {
		requestCtx.Header("Access-Control-Allow-Origin", "*")
		requestCtx.Header("Access-Control-Allow-Methods", allowMethods)
		requestCtx.Header("Access-Control-Allow-Headers", allowHeaders)
		requestCtx.Header("Access-Control-Expose-Headers", requestIDHeader)

		if strings.EqualFold(string(requestCtx.Method()), consts.MethodOptions) {
			requestCtx.AbortWithStatus(consts.StatusNoContent)
			return
		}

		requestCtx.Next(ctx)
	}
}
