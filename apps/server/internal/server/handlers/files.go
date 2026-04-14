package handlers

import (
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type uploadedFileReader interface {
	GetByID(ctx context.Context, id string) (*postgres.UploadedFile, error)
}

type fileStoreReader interface {
	Open(ctx context.Context, storageKey string) (io.ReadCloser, error)
	Stat(ctx context.Context, storageKey string) (os.FileInfo, error)
}

// FileHandler 暴露原始上传文件下载接口。
type FileHandler struct {
	files uploadedFileReader
	store fileStoreReader
}

// NewFileHandler 创建文件下载 handler。
func NewFileHandler(files uploadedFileReader, store fileStoreReader) *FileHandler {
	return &FileHandler{
		files: files,
		store: store,
	}
}

// Download 返回指定原始上传文件的附件下载响应。
func (h *FileHandler) Download(requestCtx context.Context, ctx *app.RequestContext) {
	if h.files == nil || h.store == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "文件下载服务未配置"})
		return
	}

	// 在 HTTP 边界校验 UUID 格式，非法输入在到达 repo 层之前返回 400
	fileID := ctx.Param("id")
	if !isValidUUID(fileID) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "文件 ID 非法"})
		return
	}

	file, err := h.files.GetByID(requestCtx, fileID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询文件失败"})
		return
	}
	if file == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}

	// 取文件大小用于设置 Content-Length；失败时以 -1 表示未知大小
	size := -1
	if info, statErr := h.store.Stat(requestCtx, file.StorageKey); statErr == nil {
		size = int(info.Size())
	}

	rc, err := h.store.Open(requestCtx, file.StorageKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "文件内容不存在"})
		} else {
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		}
		return
	}
	// rc 的生命周期由 Hertz 的流式响应管理，不需要 defer Close

	contentType := strings.TrimSpace(file.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	name := file.OriginalFilename
	if name == "" {
		name = "download"
	}
	disposition := "attachment"
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": name}); value != "" {
		disposition = value
	}

	ctx.Header("Content-Disposition", disposition)
	ctx.SetContentType(contentType)
	ctx.Response.SetBodyStream(rc, size)
}
