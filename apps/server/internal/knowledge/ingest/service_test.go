package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"
)

func TestImportDocumentCreatesResourceVersionAndChunks(t *testing.T) {
	repo := &fakeResourceRepo{
		resource: &postgres.Resource{
			ID:         "resource-1",
			Title:      "学生守则",
			SourceType: "upload",
			CreatedAt:  time.Unix(1710000000, 0),
			UpdatedAt:  time.Unix(1710000000, 0),
		},
		version: &postgres.ResourceVersion{
			ID:            "version-1",
			ResourceID:    "resource-1",
			VersionNumber: 1,
			Content:       "# 学生守则\n内容",
			Source:        "assistant_upload",
			CreatedAt:     time.Unix(1710000000, 0),
		},
	}
	service := NewService(repo, fakeEmbedder{})

	result, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "学生守则.md",
		Content:  []byte("# 学生守则\n内容"),
	})
	if err != nil {
		t.Fatalf("import document: %v", err)
	}

	if result.Resource == nil || result.Resource.ID != "resource-1" {
		t.Fatalf("expected imported resource, got %#v", result.Resource)
	}

	if repo.createSourceRef == nil || *repo.createSourceRef != "学生守则.md" {
		t.Fatalf("expected source_ref to keep original filename, got %#v", repo.createSourceRef)
	}

	if len(repo.createdChunks) == 0 {
		t.Fatal("expected resource chunks to be created")
	}
}

func TestImportDocumentRejectsEmptyContent(t *testing.T) {
	service := NewService(&fakeResourceRepo{}, fakeEmbedder{})

	if _, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "empty.md",
		Content:  nil,
	}); err == nil {
		t.Fatal("expected empty content to fail")
	}
}

func TestImportDocumentUsesParsedTextWhenParserConfigured(t *testing.T) {
	repo := &fakeResourceRepo{}
	service := NewService(repo, fakeEmbedder{}, WithParser(fakeDocumentParser{
		result: &documentparser.Result{
			Text: "# 解析后的标题\n解析后的正文",
		},
	}))

	result, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "学生守则.pdf",
		Content:  []byte("%PDF-binary-content"),
	})
	if err != nil {
		t.Fatalf("import document with parser: %v", err)
	}

	if result.Version == nil || result.Version.Content != "# 解析后的标题\n解析后的正文" {
		t.Fatalf("expected parsed content to be stored in current version, got %#v", result.Version)
	}

	if len(repo.createdChunks) == 0 {
		t.Fatal("expected parsed content to create chunks")
	}

	if repo.createdChunks[0].SectionTitle != "解析后的标题" {
		t.Fatalf("expected chunk section title %q, got %q", "解析后的标题", repo.createdChunks[0].SectionTitle)
	}

	if repo.createSourceRef == nil || *repo.createSourceRef != "学生守则.pdf" {
		t.Fatalf("expected original filename to remain source_ref, got %#v", repo.createSourceRef)
	}
}

func TestImportDocumentReturnsParserErrorBeforeWritingResource(t *testing.T) {
	repo := &fakeResourceRepo{}
	parserErr := errors.New("解析失败")
	service := NewService(repo, fakeEmbedder{}, WithParser(fakeDocumentParser{
		err: parserErr,
	}))

	_, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "学生守则.docx",
		Content:  []byte("binary-docx-content"),
	})
	if !errors.Is(err, parserErr) {
		t.Fatalf("expected parser error %v, got %v", parserErr, err)
	}

	if repo.createCalls != 0 {
		t.Fatalf("expected parser failure to stop before creating resources, got %d create calls", repo.createCalls)
	}

	if len(repo.createdChunks) != 0 {
		t.Fatalf("expected no chunks when parser fails, got %d", len(repo.createdChunks))
	}
}

func TestImportDirectoryReindexesExistingResourceWithoutChunks(t *testing.T) {
	tempDir := t.TempDir()
	content := "# 考勤制度\n\n## 考勤管理\n员工必须在九点前签到。"
	filePath := filepath.Join(tempDir, "attendance-policy.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	repo := &fakeResourceRepo{
		existingResources: []postgres.Resource{
			{ID: "resource-existing", Title: "考勤制度", SourceType: "upload"},
		},
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-existing",
			ResourceID: "resource-existing",
			Content:    content,
		},
		chunkCount: 0,
	}
	versionIndexer := &fakeVersionIndexer{}
	service := NewService(repo, fakeEmbedder{}, WithIndexer(versionIndexer))

	if err := service.ImportDirectory(context.Background(), tempDir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	if versionIndexer.calls != 1 {
		t.Fatalf("expected exactly 1 reindex call, got %d", versionIndexer.calls)
	}
	if versionIndexer.lastInput.Version.ID != "version-existing" {
		t.Fatalf("expected reindexed version %q, got %q", "version-existing", versionIndexer.lastInput.Version.ID)
	}
	if repo.createCalls != 0 {
		t.Fatalf("expected no new resource create, got %d", repo.createCalls)
	}
}

func TestImportDirectorySkipsIndexedResource(t *testing.T) {
	tempDir := t.TempDir()
	content := "# 考勤制度\n\n## 考勤管理\n员工必须在九点前签到。"
	filePath := filepath.Join(tempDir, "attendance-policy.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	repo := &fakeResourceRepo{
		existingResources: []postgres.Resource{
			{ID: "resource-existing", Title: "考勤制度", SourceType: "upload"},
		},
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-existing",
			ResourceID: "resource-existing",
			Content:    content,
		},
		chunkCount: 2,
	}
	versionIndexer := &fakeVersionIndexer{}
	service := NewService(repo, fakeEmbedder{}, WithIndexer(versionIndexer))

	if err := service.ImportDirectory(context.Background(), tempDir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	if versionIndexer.calls != 0 {
		t.Fatalf("expected indexed resource to skip reindex, got %d calls", versionIndexer.calls)
	}
	if repo.createCalls != 0 {
		t.Fatalf("expected no new resource create, got %d", repo.createCalls)
	}
}

type fakeResourceRepo struct {
	existingResources []postgres.Resource
	currentVersion    *postgres.ResourceVersion
	chunkCount        int
	createSourceRef   *string
	createdChunks     []*postgres.ResourceChunk
	resource          *postgres.Resource
	version           *postgres.ResourceVersion
	createCalls       int
}

func (r *fakeResourceRepo) List(context.Context) ([]postgres.Resource, error) {
	return r.existingResources, nil
}

func (r *fakeResourceRepo) CreateWithSourceRef(_ context.Context, title string, sourceType string, sourceRef *string) (*postgres.Resource, error) {
	r.createCalls++
	r.createSourceRef = sourceRef
	if r.resource != nil {
		return r.resource, nil
	}

	return &postgres.Resource{
		ID:         "resource-default",
		Title:      title,
		SourceType: sourceType,
	}, nil
}

func (r *fakeResourceRepo) CreateVersion(_ context.Context, resourceID string, versionNumber int, content string, source string) (*postgres.ResourceVersion, error) {
	if r.version != nil {
		return r.version, nil
	}

	return &postgres.ResourceVersion{
		ID:            "version-default",
		ResourceID:    resourceID,
		VersionNumber: versionNumber,
		Content:       content,
		Source:        source,
	}, nil
}

func (r *fakeResourceRepo) CreateChunk(_ context.Context, chunk *postgres.ResourceChunk) error {
	r.createdChunks = append(r.createdChunks, chunk)
	return nil
}

func (r *fakeResourceRepo) GetCurrentVersion(context.Context, string) (*postgres.ResourceVersion, error) {
	return r.currentVersion, nil
}

func (r *fakeResourceRepo) CountChunksByVersion(context.Context, string) (int, error) {
	return r.chunkCount, nil
}

func (r *fakeResourceRepo) ReplaceVersionChunks(_ context.Context, versionID string, resourceID string, chunks []postgres.ResourceChunkInput) error {
	r.createdChunks = r.createdChunks[:0]
	for _, chunk := range chunks {
		r.createdChunks = append(r.createdChunks, &postgres.ResourceChunk{
			ResourceID:   resourceID,
			VersionID:    versionID,
			ChunkIndex:   chunk.ChunkIndex,
			SectionTitle: chunk.SectionTitle,
			Content:      chunk.Content,
			Embedding:    chunk.Embedding,
		})
	}

	return nil
}

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for range texts {
		vectors = append(vectors, testVector(0.3))
	}

	return vectors, nil
}

func testVector(seed float32) []float32 {
	values := make([]float32, 1024)
	for index := range values {
		values[index] = seed + float32(index%5)/10
	}

	return values
}

type fakeDocumentParser struct {
	result *documentparser.Result
	err    error
}

func (p fakeDocumentParser) Parse(context.Context, documentparser.Input) (*documentparser.Result, error) {
	if p.err != nil {
		return nil, p.err
	}

	return p.result, nil
}

type fakeVersionIndexer struct {
	calls     int
	lastInput indexer.Input
}

func (f *fakeVersionIndexer) ReindexVersion(_ context.Context, input indexer.Input) error {
	f.calls++
	f.lastInput = input
	return nil
}
