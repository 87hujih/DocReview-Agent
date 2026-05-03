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

// TestDownloadUploadedFileHandler 验证`downloadUploadedFileHandler`在特定边界条件下的行为，防止同类回归。
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

// TestDownloadUploadedFileHandlerInvalidID 验证`downloadUploadedFileHandlerInvalidID`在特定边界条件下的行为，防止同类回归。
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

// TestDownloadUploadedFileHandlerNotFound 验证`downloadUploadedFileHandlerNotFound`在特定边界条件下的行为，防止同类回归。
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

// TestDownloadUploadedFileHandlerStoreMissing 验证`downloadUploadedFileHandlerStoreMissing`在特定边界条件下的行为，防止同类回归。
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

// TestDownloadUploadedFileHandlerContentLengthSet 验证`downloadUploadedFileHandlerContentLengthSet`在特定边界条件下的行为，防止同类回归。
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

// TestDownloadUploadedFileHandlerFallbackContentType 验证`downloadUploadedFileHandler`在回退路径下的行为，防止同类回归。
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

// fakeUploadedFileReader 作为Uploaded文件读取器的测试替身，用于在用例里提供可控的依赖行为。
type fakeUploadedFileReader struct {
	file *postgres.UploadedFile
	err  error
}

// GetByID 实现测试替身需要的 `GetByID` 接口方法，为用例分支提供可控返回。
func (r fakeUploadedFileReader) GetByID(context.Context, string) (*postgres.UploadedFile, error) {
	if r.err != nil {
		return nil, r.err
	}

	return r.file, nil
}

// fakeFileStoreReader 作为文件Store读取器的测试替身，用于在用例里提供可控的依赖行为。
type fakeFileStoreReader struct {
	content string
	openErr error
	statErr error
}

// Open 实现测试替身需要的 `Open` 接口方法，为用例分支提供可控返回。
func (r fakeFileStoreReader) Open(context.Context, string) (io.ReadCloser, error) {
	if r.openErr != nil {
		return nil, r.openErr
	}
	return io.NopCloser(strings.NewReader(r.content)), nil
}

// Stat 实现测试替身需要的 `Stat` 接口方法，为用例分支提供可控返回。
func (r fakeFileStoreReader) Stat(context.Context, string) (os.FileInfo, error) {
	if r.statErr != nil {
		return nil, r.statErr
	}
	if r.openErr != nil {
		return nil, r.openErr
	}
	return &fakeFileInfo{size: int64(len(r.content))}, nil
}

// fakeFileInfo 作为文件Info的测试替身，用于在用例里提供可控的依赖行为。
type fakeFileInfo struct {
	size int64
}

// Name 实现测试替身需要的 `Name` 接口方法，为用例分支提供可控返回。
func (f *fakeFileInfo) Name() string { return "fake" }

// Size 实现测试替身需要的 `Size` 接口方法，为用例分支提供可控返回。
func (f *fakeFileInfo) Size() int64 { return f.size }

// Mode 实现测试替身需要的 `Mode` 接口方法，为用例分支提供可控返回。
func (f *fakeFileInfo) Mode() fs.FileMode { return 0o600 }

// ModTime 实现测试替身需要的 `ModTime` 接口方法，为用例分支提供可控返回。
func (f *fakeFileInfo) ModTime() time.Time { return time.Time{} }

// IsDir 实现测试替身需要的 `IsDir` 接口方法，为用例分支提供可控返回。
func (f *fakeFileInfo) IsDir() bool { return false }

// Sys 实现测试替身需要的 `Sys` 接口方法，为用例分支提供可控返回。
func (f *fakeFileInfo) Sys() any { return nil }
