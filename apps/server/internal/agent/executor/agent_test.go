package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/knowledge/sections"
	"agent_project/apps/server/internal/storage/postgres"
)

// TestApplySectionReplacements 验证`applySectionReplacements`在特定边界条件下的行为，防止同类回归。
func TestApplySectionReplacements(t *testing.T) {
	content := strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
		"## 第二章",
		"原始第二章内容",
		"",
	}, "\n")

	updated := applySectionReplacements(content, []editor.DiffSection{
		{
			SectionTitle:      "第一章",
			SectionOccurrence: 1,
			Revised:           "修订后的第一章内容",
		},
	})

	expected := strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"修订后的第一章内容",
		"",
		"## 第二章",
		"原始第二章内容",
		"",
	}, "\n")

	if updated != expected {
		t.Fatalf("expected updated content:\n%s\n\ngot:\n%s", expected, updated)
	}
}

// TestExecuteReindexesNewVersion 验证`executeReindexesNewVersion`在特定边界条件下的行为，防止同类回归。
func TestExecuteReindexesNewVersion(t *testing.T) {
	preview := editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle:      "第一章",
				SectionOccurrence: 1,
				Revised:           "修订后的第一章内容",
			},
		},
	}
	previewBytes, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{
			{
				ArtifactType: "diff_preview",
				Content:      previewBytes,
			},
		},
		task: &postgres.Task{
			ID:         "task-1",
			ResourceID: "resource-1",
		},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{
			ID:    "resource-1",
			Title: "考勤制度",
		},
		currentVersion: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content: strings.Join([]string{
				"# 文档标题",
				"",
				"## 第一章",
				"原始第一章内容",
				"",
			}, "\n"),
		},
		createdVersion: &postgres.ResourceVersion{
			ID:            "version-2",
			ResourceID:    "resource-1",
			VersionNumber: 2,
			Content: strings.Join([]string{
				"# 文档标题",
				"",
				"## 第一章",
				"修订后的第一章内容",
				"",
			}, "\n"),
			Source: "agent_edit",
		},
	}
	versionIndexer := &fakeVersionIndexer{}
	exec := New(taskRepo, resourceRepo, versionIndexer)

	versionID, err := exec.Execute(context.Background(), &postgres.ExecutionJob{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if versionID != "version-2" {
		t.Fatalf("expected version id %q, got %q", "version-2", versionID)
	}
	if versionIndexer.calls != 1 {
		t.Fatalf("expected exactly 1 reindex call, got %d", versionIndexer.calls)
	}
	if versionIndexer.lastInput.Version.ID != "version-2" {
		t.Fatalf("expected reindexed version %q, got %q", "version-2", versionIndexer.lastInput.Version.ID)
	}
	if versionIndexer.lastInput.Resource.ID != "resource-1" {
		t.Fatalf("expected resource id %q, got %q", "resource-1", versionIndexer.lastInput.Resource.ID)
	}
	if len(resourceRepo.createVersionCalls) != 1 {
		t.Fatalf("expected exactly 1 create version call, got %d", len(resourceRepo.createVersionCalls))
	}
	if !strings.Contains(resourceRepo.createVersionCalls[0].content, "修订后的第一章内容") {
		t.Fatalf("expected new content to contain revised text, got %q", resourceRepo.createVersionCalls[0].content)
	}
}

// TestExecuteReturnsIndexerError 验证`execute`在返回值分支下的行为，防止同类回归。
func TestExecuteReturnsIndexerError(t *testing.T) {
	preview := editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle:      "第一章",
				SectionOccurrence: 1,
				Revised:           "修订后的第一章内容",
			},
		},
	}
	previewBytes, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{
			{
				ArtifactType: "diff_preview",
				Content:      previewBytes,
			},
		},
		task: &postgres.Task{
			ID:         "task-1",
			ResourceID: "resource-1",
		},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{
			ID:    "resource-1",
			Title: "考勤制度",
		},
		currentVersion: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content: strings.Join([]string{
				"# 文档标题",
				"",
				"## 第一章",
				"原始第一章内容",
				"",
			}, "\n"),
		},
		createdVersion: &postgres.ResourceVersion{
			ID:            "version-2",
			ResourceID:    "resource-1",
			VersionNumber: 2,
		},
	}
	expectedErr := errors.New("重建索引失败")
	versionIndexer := &fakeVersionIndexer{err: expectedErr}
	exec := New(taskRepo, resourceRepo, versionIndexer)

	_, err = exec.Execute(context.Background(), &postgres.ExecutionJob{TaskID: "task-1"})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected indexer error %v, got %v", expectedErr, err)
	}
	if versionIndexer.calls != 1 {
		t.Fatalf("expected exactly 1 reindex call, got %d", versionIndexer.calls)
	}
}

// TestPrepareUsesJobBaseVersionInsteadOfCurrentVersion 验证`prepare`在依赖选择路径下的行为，防止同类回归。
func TestPrepareUsesJobBaseVersionInsteadOfCurrentVersion(t *testing.T) {
	previewBytes := mustMarshalPreview(t, editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle:      "第一章",
				SectionOccurrence: 1,
				Original:          "基线版本正文",
				Revised:           "修订后的正文",
				Reason:            "补充说明",
				CitationIDs:       []string{"cite_1"},
			},
		},
	})

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{{ArtifactType: "diff_preview", Content: previewBytes}},
		task:      &postgres.Task{ID: "task-1", ResourceID: "resource-1"},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{ID: "resource-1", Title: "考勤制度"},
		currentVersion: &postgres.ResourceVersion{
			ID:            "version-2",
			ResourceID:    "resource-1",
			VersionNumber: 2,
			Content:       "# 文档标题\n\n## 第一章\n当前版本正文\n",
		},
		versionByID: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content:       "# 文档标题\n\n## 第一章\n基线版本正文\n",
		},
	}
	versionIndexer := &fakeVersionIndexer{
		preparedChunks: []postgres.ResourceChunkInput{
			{ChunkIndex: 0, SectionTitle: "第一章", Content: "修订后的正文"},
		},
	}
	exec := New(taskRepo, resourceRepo, versionIndexer)

	prepared, err := exec.Prepare(context.Background(), &postgres.ExecutionJob{
		TaskID:        "task-1",
		BaseVersionID: stringPtr("version-1"),
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	if prepared.BaseVersion.ID != "version-1" {
		t.Fatalf("expected base version %q, got %q", "version-1", prepared.BaseVersion.ID)
	}
	if !strings.Contains(prepared.NewContent, "修订后的正文") {
		t.Fatalf("expected prepared content to contain revised text, got %q", prepared.NewContent)
	}
	if strings.Contains(prepared.NewContent, "当前版本正文") {
		t.Fatalf("expected prepare to ignore current version content, got %q", prepared.NewContent)
	}
	if versionIndexer.buildCalls != 1 {
		t.Fatalf("expected exactly 1 build chunks call, got %d", versionIndexer.buildCalls)
	}
}

// TestPrepareRejectsLegacyJobWithoutBaseVersion 验证`prepare`在非法输入或失败路径下的行为，防止同类回归。
func TestPrepareRejectsLegacyJobWithoutBaseVersion(t *testing.T) {
	exec := New(fakeTaskRepo{}, &fakeResourceRepo{}, &fakeVersionIndexer{})

	if _, err := exec.Prepare(context.Background(), &postgres.ExecutionJob{TaskID: "task-1"}); err == nil {
		t.Fatal("expected prepare to fail when job is missing base_version_id")
	} else if !strings.Contains(err.Error(), "base_version_id") {
		t.Fatalf("expected error to mention base_version_id, got %v", err)
	}
}

// TestPrepareRejectsOriginalMismatch 验证`prepare`在非法输入或失败路径下的行为，防止同类回归。
func TestPrepareRejectsOriginalMismatch(t *testing.T) {
	previewBytes := mustMarshalPreview(t, editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle:      "第一章",
				SectionOccurrence: 1,
				Original:          "不匹配的原文",
				Revised:           "修订后的正文",
				Reason:            "补充说明",
				CitationIDs:       []string{"cite_1"},
			},
		},
	})

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{{ArtifactType: "diff_preview", Content: previewBytes}},
		task:      &postgres.Task{ID: "task-1", ResourceID: "resource-1"},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{ID: "resource-1", Title: "考勤制度"},
		versionByID: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content:       "# 文档标题\n\n## 第一章\n真实原文\n",
		},
	}
	exec := New(taskRepo, resourceRepo, &fakeVersionIndexer{})

	if _, err := exec.Prepare(context.Background(), &postgres.ExecutionJob{
		TaskID:        "task-1",
		BaseVersionID: stringPtr("version-1"),
	}); err == nil {
		t.Fatal("expected prepare to fail when original content mismatches")
	}
}

// TestPrepareSupportsLegacyPreviewWithoutOccurrenceWhenTitleUnique 验证`prepare`在合法输入或兼容路径下的行为，防止同类回归。
func TestPrepareSupportsLegacyPreviewWithoutOccurrenceWhenTitleUnique(t *testing.T) {
	previewBytes := mustMarshalPreview(t, editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle: "第一章",
				Original:     "原始正文",
				Revised:      "修订后的正文",
				Reason:       "补充说明",
				CitationIDs:  []string{"cite_1"},
			},
		},
	})

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{{ArtifactType: "diff_preview", Content: previewBytes}},
		task:      &postgres.Task{ID: "task-1", ResourceID: "resource-1"},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{ID: "resource-1", Title: "考勤制度"},
		versionByID: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content:       "# 文档标题\n\n## 第一章\n原始正文\n",
		},
	}
	exec := New(taskRepo, resourceRepo, &fakeVersionIndexer{})

	prepared, err := exec.Prepare(context.Background(), &postgres.ExecutionJob{
		TaskID:        "task-1",
		BaseVersionID: stringPtr("version-1"),
	})
	if err != nil {
		t.Fatalf("prepare legacy preview: %v", err)
	}
	if !strings.Contains(prepared.NewContent, "修订后的正文") {
		t.Fatalf("expected prepared content to contain revised text, got %q", prepared.NewContent)
	}
}

// TestPrepareRejectsLegacyPreviewWithoutOccurrenceWhenTitleDuplicated 验证`prepare`在非法输入或失败路径下的行为，防止同类回归。
func TestPrepareRejectsLegacyPreviewWithoutOccurrenceWhenTitleDuplicated(t *testing.T) {
	previewBytes := mustMarshalPreview(t, editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle: "重复章节",
				Original:     "第一处正文",
				Revised:      "修订后的正文",
				Reason:       "补充说明",
				CitationIDs:  []string{"cite_1"},
			},
		},
	})

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{{ArtifactType: "diff_preview", Content: previewBytes}},
		task:      &postgres.Task{ID: "task-1", ResourceID: "resource-1"},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{ID: "resource-1", Title: "考勤制度"},
		versionByID: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content:       "# 文档标题\n\n## 重复章节\n第一处正文\n\n## 重复章节\n第二处正文\n",
		},
	}
	exec := New(taskRepo, resourceRepo, &fakeVersionIndexer{})

	if _, err := exec.Prepare(context.Background(), &postgres.ExecutionJob{
		TaskID:        "task-1",
		BaseVersionID: stringPtr("version-1"),
	}); err == nil {
		t.Fatal("expected prepare to fail for duplicated legacy title without occurrence")
	} else if !strings.Contains(err.Error(), "section_occurrence") {
		t.Fatalf("expected error to mention section_occurrence, got %v", err)
	}
}

// TestPrepareUsesWholeDocumentFallback 验证`prepare`在依赖选择路径下的行为，防止同类回归。
func TestPrepareUsesWholeDocumentFallback(t *testing.T) {
	original := "这是没有二级标题的正文。"
	previewBytes := mustMarshalPreview(t, editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle:      sections.WholeDocumentTitle,
				SectionOccurrence: 1,
				Original:          original,
				Revised:           "这是修订后的整篇正文。",
				Reason:            "补充说明",
				CitationIDs:       []string{"cite_1"},
			},
		},
	})

	taskRepo := fakeTaskRepo{
		artifacts: []postgres.TaskArtifact{{ArtifactType: "diff_preview", Content: previewBytes}},
		task:      &postgres.Task{ID: "task-1", ResourceID: "resource-1"},
	}
	resourceRepo := &fakeResourceRepo{
		resource: &postgres.Resource{ID: "resource-1", Title: "员工手册"},
		versionByID: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content:       original,
		},
	}
	exec := New(taskRepo, resourceRepo, &fakeVersionIndexer{})

	prepared, err := exec.Prepare(context.Background(), &postgres.ExecutionJob{
		TaskID:        "task-1",
		BaseVersionID: stringPtr("version-1"),
	})
	if err != nil {
		t.Fatalf("prepare whole document fallback: %v", err)
	}
	if prepared.NewContent != "这是修订后的整篇正文。" {
		t.Fatalf("expected whole document content to be replaced, got %q", prepared.NewContent)
	}
}

// fakeTaskRepo 作为任务仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeTaskRepo struct {
	artifacts []postgres.TaskArtifact
	task      *postgres.Task
}

// GetArtifacts 实现测试替身需要的 `GetArtifacts` 接口方法，为用例分支提供可控返回。
func (r fakeTaskRepo) GetArtifacts(context.Context, string) ([]postgres.TaskArtifact, error) {
	return r.artifacts, nil
}

// GetByID 实现测试替身需要的 `GetByID` 接口方法，为用例分支提供可控返回。
func (r fakeTaskRepo) GetByID(context.Context, string) (*postgres.Task, error) {
	return r.task, nil
}

// createVersionCall 记录创建版本的调用参数，便于测试断言副作用。
type createVersionCall struct {
	resourceID    string
	versionNumber int
	content       string
	source        string
}

// fakeResourceRepo 作为资源仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeResourceRepo struct {
	resource           *postgres.Resource
	currentVersion     *postgres.ResourceVersion
	versionByID        *postgres.ResourceVersion
	createdVersion     *postgres.ResourceVersion
	createVersionCalls []createVersionCall
	createVersionErr   error
	currentVersionErr  error
	versionByIDErr     error
}

// GetByID 实现测试替身需要的 `GetByID` 接口方法，为用例分支提供可控返回。
func (r *fakeResourceRepo) GetByID(context.Context, string) (*postgres.Resource, error) {
	return r.resource, nil
}

// GetCurrentVersion 实现测试替身需要的 `GetCurrentVersion` 接口方法，为用例分支提供可控返回。
func (r *fakeResourceRepo) GetCurrentVersion(context.Context, string) (*postgres.ResourceVersion, error) {
	return r.currentVersion, r.currentVersionErr
}

// GetVersionByID 实现测试替身需要的 `GetVersionByID` 接口方法，为用例分支提供可控返回。
func (r *fakeResourceRepo) GetVersionByID(context.Context, string) (*postgres.ResourceVersion, error) {
	return r.versionByID, r.versionByIDErr
}

// CreateVersion 实现测试替身需要的 `CreateVersion` 接口方法，为用例分支提供可控返回。
func (r *fakeResourceRepo) CreateVersion(_ context.Context, resourceID string, versionNumber int, content string, source string) (*postgres.ResourceVersion, error) {
	r.createVersionCalls = append(r.createVersionCalls, createVersionCall{
		resourceID:    resourceID,
		versionNumber: versionNumber,
		content:       content,
		source:        source,
	})
	if r.createVersionErr != nil {
		return nil, r.createVersionErr
	}
	if r.createdVersion != nil {
		r.createdVersion.Content = content
		r.createdVersion.Source = source
		r.createdVersion.VersionNumber = versionNumber
		return r.createdVersion, nil
	}

	return &postgres.ResourceVersion{
		ID:            "version-created",
		ResourceID:    resourceID,
		VersionNumber: versionNumber,
		Content:       content,
		Source:        source,
	}, nil
}

// fakeVersionIndexer 作为版本索引器的测试替身，用于在用例里提供可控的依赖行为。
type fakeVersionIndexer struct {
	calls          int
	buildCalls     int
	lastInput      indexer.Input
	err            error
	buildErr       error
	preparedChunks []postgres.ResourceChunkInput
}

// ReindexVersion 实现测试替身需要的 `ReindexVersion` 接口方法，为用例分支提供可控返回。
func (f *fakeVersionIndexer) ReindexVersion(_ context.Context, input indexer.Input) error {
	f.calls++
	f.lastInput = input
	return f.err
}

// BuildVersionChunks 实现测试替身需要的 `BuildVersionChunks` 接口方法，为用例分支提供可控返回。
func (f *fakeVersionIndexer) BuildVersionChunks(_ context.Context, input indexer.Input) ([]postgres.ResourceChunkInput, error) {
	f.buildCalls++
	f.lastInput = input
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return append([]postgres.ResourceChunkInput(nil), f.preparedChunks...), nil
}

// mustMarshalPreview 在测试里强制构造 `Marshal预览`，失败时立即终止当前用例。
func mustMarshalPreview(t *testing.T, preview editor.DiffPreview) []byte {
	t.Helper()

	previewBytes, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	return previewBytes
}

// stringPtr 返回字符串指针，简化测试和构造函数里的可选字段赋值。
func stringPtr(value string) *string {
	return &value
}
