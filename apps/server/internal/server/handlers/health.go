package handlers

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Health 是本地冒烟检查和部署探活共用的存活接口。
func Health(_ context.Context, ctx *app.RequestContext) {
	ctx.JSON(consts.StatusOK, map[string]string{
		"status":  "ok",
		"service": "server",
	})
}
