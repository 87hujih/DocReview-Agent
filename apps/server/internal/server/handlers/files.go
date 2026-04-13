package handlers

import (
	"context"
	"io"
	"mime"
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

	file, err := h.files.GetByID(requestCtx, ctx.Param("id"))
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询文件失败"})
		return
	}
	if file == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "文件不存在"})
		return
	}

	reader, err := h.store.Open(requestCtx, file.StorageKey)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		return
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		return
	}

	contentType := strings.TrimSpace(file.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	disposition := "attachment"
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": file.OriginalFilename}); value != "" {
		disposition = value
	}

	ctx.Header("Content-Disposition", disposition)
	ctx.SetContentType(contentType)
	ctx.Response.SetBody(content)
}
