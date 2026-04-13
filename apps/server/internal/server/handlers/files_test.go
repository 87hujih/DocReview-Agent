package handlers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestDownloadUploadedFileHandler(t *testing.T) {
	handler := NewFileHandler(
		fakeUploadedFileReader{
			file: &postgres.UploadedFile{
				ID:               "file-1",
				OriginalFilename: "学生守则.md",
				ContentType:      "text/markdown",
				SizeBytes:        12,
				StorageKey:       "sh/file-1",
				CreatedAt:        time.Unix(1710000000, 0),
			},
		},
		fakeFileStoreReader{content: "文件原文"},
	)
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/file-1/download", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
	if string(response.Body()) != "文件原文" {
		t.Fatalf("expected file content, got %q", string(response.Body()))
	}
	if contentType := string(response.Header.Peek("Content-Type")); contentType != "text/markdown" {
		t.Fatalf("expected content type %q, got %q", "text/markdown", contentType)
	}
	if disposition := string(response.Header.Peek("Content-Disposition")); !strings.Contains(disposition, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", disposition)
	}
}

func TestDownloadUploadedFileHandlerNotFound(t *testing.T) {
	handler := NewFileHandler(fakeUploadedFileReader{}, fakeFileStoreReader{})
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/missing/download", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

type fakeUploadedFileReader struct {
	file *postgres.UploadedFile
	err  error
}

func (r fakeUploadedFileReader) GetByID(context.Context, string) (*postgres.UploadedFile, error) {
	if r.err != nil {
		return nil, r.err
	}

	return r.file, nil
}

type fakeFileStoreReader struct {
	content string
	err     error
}

func (r fakeFileStoreReader) Open(context.Context, string) (io.ReadCloser, error) {
	if r.err != nil {
		return nil, r.err
	}

	return io.NopCloser(strings.NewReader(r.content)), nil
}
