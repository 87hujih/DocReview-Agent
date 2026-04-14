package handlers

import (
	"context"
	"io"
	"io/fs"
	"os"
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

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/00000000-0000-0000-0000-000000000001/download", nil).Result()

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

func TestDownloadUploadedFileHandlerInvalidID(t *testing.T) {
	handler := NewFileHandler(fakeUploadedFileReader{}, fakeFileStoreReader{})
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	for _, id := range []string{"not-a-uuid", "missing", "123"} {
		path := "/api/files/" + id + "/download"
		response := ut.PerformRequest(engine.Engine, "GET", path, nil).Result()
		if response.StatusCode() != consts.StatusBadRequest {
			t.Errorf("id=%q: expected 400, got %d", id, response.StatusCode())
		}
		if !strings.Contains(string(response.Body()), "文件 ID 非法") {
			t.Errorf("id=%q: expected '文件 ID 非法' in body, got %s", id, string(response.Body()))
		}
	}
}

func TestDownloadUploadedFileHandlerNotFound(t *testing.T) {
	handler := NewFileHandler(fakeUploadedFileReader{}, fakeFileStoreReader{})
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/00000000-0000-0000-0000-000000000000/download", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "文件不存在") {
		t.Fatalf("expected '文件不存在' in body, got %s", string(response.Body()))
	}
}

func TestDownloadUploadedFileHandlerStoreMissing(t *testing.T) {
	file := &postgres.UploadedFile{
		ID:               "00000000-0000-0000-0000-000000000002",
		OriginalFilename: "test.md",
		ContentType:      "text/markdown",
		StorageKey:       "ab/cdef",
	}
	handler := NewFileHandler(
		fakeUploadedFileReader{file: file},
		fakeFileStoreReader{openErr: os.ErrNotExist},
	)
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/00000000-0000-0000-0000-000000000002/download", nil).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected 404 for missing store file, got %d", response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "文件内容不存在") {
		t.Fatalf("expected '文件内容不存在' in body, got %s", string(response.Body()))
	}
}

func TestDownloadUploadedFileHandlerContentLengthSet(t *testing.T) {
	content := "hello world content"
	file := &postgres.UploadedFile{
		ID:               "00000000-0000-0000-0000-000000000003",
		OriginalFilename: "readme.md",
		ContentType:      "text/markdown",
		StorageKey:       "ab/cdef",
	}
	handler := NewFileHandler(
		fakeUploadedFileReader{file: file},
		fakeFileStoreReader{content: content},
	)
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/00000000-0000-0000-0000-000000000003/download", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.StatusCode(), string(response.Body()))
	}
	if string(response.Body()) != content {
		t.Fatalf("body mismatch: got %q, want %q", string(response.Body()), content)
	}
}

func TestDownloadUploadedFileHandlerFallbackContentType(t *testing.T) {
	file := &postgres.UploadedFile{
		ID:               "00000000-0000-0000-0000-000000000004",
		OriginalFilename: "data",
		ContentType:      "",
		StorageKey:       "ab/cdef",
	}
	handler := NewFileHandler(
		fakeUploadedFileReader{file: file},
		fakeFileStoreReader{content: "bytes"},
	)
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/00000000-0000-0000-0000-000000000004/download", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode())
	}
	ct := string(response.Header.Peek("Content-Type"))
	if ct != "application/octet-stream" {
		t.Fatalf("expected fallback content-type, got %q", ct)
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
	openErr error
	statErr error
}

func (r fakeFileStoreReader) Open(context.Context, string) (io.ReadCloser, error) {
	if r.openErr != nil {
		return nil, r.openErr
	}
	return io.NopCloser(strings.NewReader(r.content)), nil
}

func (r fakeFileStoreReader) Stat(context.Context, string) (os.FileInfo, error) {
	if r.statErr != nil {
		return nil, r.statErr
	}
	if r.openErr != nil {
		return nil, r.openErr
	}
	return &fakeFileInfo{size: int64(len(r.content))}, nil
}

type fakeFileInfo struct {
	size int64
}

func (f *fakeFileInfo) Name() string      { return "fake" }
func (f *fakeFileInfo) Size() int64       { return f.size }
func (f *fakeFileInfo) Mode() fs.FileMode { return 0o600 }
func (f *fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f *fakeFileInfo) IsDir() bool       { return false }
func (f *fakeFileInfo) Sys() any          { return nil }
