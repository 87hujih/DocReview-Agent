package indexer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent_project/apps/server/internal/knowledge/sections"
	"agent_project/apps/server/internal/storage/postgres"
)

func TestReindexVersionRebuildsMarkdownChunks(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-1",
			Title: "考勤制度",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content: strings.Join([]string{
				"# 考勤制度",
				"",
				"## 考勤管理",
				"员工必须在九点前签到。",
				"",
				"## 请假管理",
				"请假需要提前审批。",
			}, "\n"),
		},
	})
	if err != nil {
		t.Fatalf("reindex version: %v", err)
	}

	if repo.replaceCalls != 1 {
		t.Fatalf("expected exactly 1 replace call, got %d", repo.replaceCalls)
	}
	if repo.lastVersionID != "version-1" {
		t.Fatalf("expected version id %q, got %q", "version-1", repo.lastVersionID)
	}
	if repo.lastResourceID != "resource-1" {
		t.Fatalf("expected resource id %q, got %q", "resource-1", repo.lastResourceID)
	}
	if len(repo.lastChunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(repo.lastChunks))
	}
	if repo.lastChunks[0].SectionTitle != "考勤管理" {
		t.Fatalf("expected first section title %q, got %q", "考勤管理", repo.lastChunks[0].SectionTitle)
	}
	if repo.lastChunks[1].SectionTitle != "请假管理" {
		t.Fatalf("expected second section title %q, got %q", "请假管理", repo.lastChunks[1].SectionTitle)
	}
}

func TestReindexVersionUsesSingleChunkFallback(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-2",
			Title: "员工手册",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-2",
			ResourceID: "resource-2",
			Content:    "这是没有二级标题的正文。",
		},
	})
	if err != nil {
		t.Fatalf("reindex version with fallback: %v", err)
	}

	if len(repo.lastChunks) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d", len(repo.lastChunks))
	}
	if repo.lastChunks[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback section title %q, got %q", sections.WholeDocumentTitle, repo.lastChunks[0].SectionTitle)
	}
	if repo.lastChunks[0].Content != "这是没有二级标题的正文。" {
		t.Fatalf("expected fallback content %q, got %q", "这是没有二级标题的正文。", repo.lastChunks[0].Content)
	}
}

func TestBuildVersionChunksKeepsWholeDocumentFallback(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	chunks, err := service.BuildVersionChunks(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-5",
			Title: "员工手册",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-5",
			ResourceID: "resource-5",
			Content:    "这是没有二级标题的正文。",
		},
	})
	if err != nil {
		t.Fatalf("build version chunks: %v", err)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d", len(chunks))
	}
	if chunks[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback section title %q, got %q", sections.WholeDocumentTitle, chunks[0].SectionTitle)
	}
}

func TestReindexVersionClearsChunksForBlankContent(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-3",
			Title: "空文档",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-3",
			ResourceID: "resource-3",
			Content:    "   \n\t",
		},
	})
	if err != nil {
		t.Fatalf("reindex blank version: %v", err)
	}

	if repo.replaceCalls != 1 {
		t.Fatalf("expected replace call for blank content, got %d", repo.replaceCalls)
	}
	if len(repo.lastChunks) != 0 {
		t.Fatalf("expected blank content to clear chunks, got %d", len(repo.lastChunks))
	}
}

func TestReindexVersionReturnsEmbedderError(t *testing.T) {
	repo := &fakeIndexRepo{}
	expectedErr := errors.New("embedding 失败")
	service := NewService(repo, fakeEmbedder{
		err: expectedErr,
	})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-4",
			Title: "错误文档",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-4",
			ResourceID: "resource-4",
			Content:    "## 章节\n正文",
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected embedder error %v, got %v", expectedErr, err)
	}

	if repo.replaceCalls != 0 {
		t.Fatalf("expected no replace call when embedder fails, got %d", repo.replaceCalls)
	}
}

type fakeIndexRepo struct {
	replaceCalls   int
	lastVersionID  string
	lastResourceID string
	lastChunks     []postgres.ResourceChunkInput
}

func (r *fakeIndexRepo) ReplaceVersionChunks(_ context.Context, versionID string, resourceID string, chunks []postgres.ResourceChunkInput) error {
	r.replaceCalls++
	r.lastVersionID = versionID
	r.lastResourceID = resourceID
	r.lastChunks = append([]postgres.ResourceChunkInput(nil), chunks...)
	return nil
}

type fakeEmbedder struct {
	err error
}

func (e fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}

	vectors := make([][]float32, 0, len(texts))
	for index := range texts {
		values := make([]float32, 1024)
		for offset := range values {
			values[offset] = float32(index+1) + float32(offset%5)/10
		}
		vectors = append(vectors, values)
	}

	return vectors, nil
}
