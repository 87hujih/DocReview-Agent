package handlers

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Preflight 为浏览器 OPTIONS 预检返回空成功响应。
func Preflight(_ context.Context, ctx *app.RequestContext) {
	ctx.Status(consts.StatusNoContent)
}
