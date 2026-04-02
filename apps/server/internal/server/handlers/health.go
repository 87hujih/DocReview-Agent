package handlers

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func Health(_ context.Context, ctx *app.RequestContext) {
	ctx.JSON(consts.StatusOK, map[string]string{
		"status":  "ok",
		"service": "server",
	})
}
