package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	documentparser "agent_project/apps/server/internal/document/parser"
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

func TestImportDocumentReturnsEmbedderErrorBeforeWritingResource(t *testing.T) {
	repo := &fakeResourceRepo{}
	embedErr := errors.New("embedding failed")
	service := NewService(repo, fakeEmbedderWithError{err: embedErr})

	_, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "学生守则.md",
		Content:  []byte("# 学生守则\n这里是正文"),
	})
	if !errors.Is(err, embedErr) {
		t.Fatalf("expected embedder error %v, got %v", embedErr, err)
	}

	if repo.createCalls != 0 {
		t.Fatalf("expected embedder failure to stop before creating resources, got %d create calls", repo.createCalls)
	}

	if len(repo.createdChunks) != 0 {
		t.Fatalf("expected no chunks when embedder fails, got %d", len(repo.createdChunks))
	}
}

func TestImportDocumentUsesAtomicGraphWriteForPersistence(t *testing.T) {
	repo := &fakeResourceRepo{
		resource: &postgres.Resource{
			ID:         "resource-graph",
			Title:      "学生守则",
			SourceType: "upload",
		},
		version: &postgres.ResourceVersion{
			ID:            "version-graph",
			ResourceID:    "resource-graph",
			VersionNumber: 1,
			Content:       "# 学生守则\n正文",
			Source:        "assistant_upload",
		},
	}
	service := NewService(repo, fakeEmbedder{})

	result, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "学生守则.md",
		Content:  []byte("# 学生守则\n正文"),
	})
	if err != nil {
		t.Fatalf("import document: %v", err)
	}

	if result.Resource == nil || result.Resource.ID != "resource-graph" {
		t.Fatalf("expected graph write resource, got %#v", result.Resource)
	}

	if repo.graphCalls != 1 {
		t.Fatalf("expected atomic graph write to be called once, got %d", repo.graphCalls)
	}

	if repo.createCalls != 0 {
		t.Fatalf("expected old step-by-step create path to stay unused, got %d create calls", repo.createCalls)
	}

	if len(repo.graphChunks) == 0 {
		t.Fatal("expected graph write to receive chunks")
	}
}

func TestImportDirectoryBackfillsSourceRefForLegacyTitleMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "student-handbook.md")
	if err := os.WriteFile(filePath, []byte("# 学生守则\n正文"), 0o600); err != nil {
		t.Fatalf("write markdown file: %v", err)
	}

	repo := &fakeResourceRepo{
		listResult: []postgres.Resource{
			{
				ID:         "resource-legacy",
				Title:      "学生守则",
				SourceType: "upload",
			},
		},
		currentVersions: map[string]*postgres.ResourceVersion{
			"resource-legacy": {
				ID:            "version-legacy",
				ResourceID:    "resource-legacy",
				VersionNumber: 1,
				Content:       "# 学生守则\n正文",
				Source:        "original",
			},
		},
		chunkCountByVersion: map[string]int{
			"version-legacy": 1,
		},
	}
	service := NewService(repo, fakeEmbedder{})

	if err := service.ImportDirectory(context.Background(), dir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	if len(repo.updateSourceRefCalls) != 1 {
		t.Fatalf("expected legacy title match to backfill source_ref once, got %d", len(repo.updateSourceRefCalls))
	}

	if repo.updateSourceRefCalls[0].resourceID != "resource-legacy" {
		t.Fatalf("expected source_ref update for %q, got %q", "resource-legacy", repo.updateSourceRefCalls[0].resourceID)
	}

	if repo.updateSourceRefCalls[0].sourceRef == nil || *repo.updateSourceRefCalls[0].sourceRef != "student-handbook.md" {
		t.Fatalf("expected source_ref %q, got %#v", "student-handbook.md", repo.updateSourceRefCalls[0].sourceRef)
	}

	if repo.graphCalls != 0 {
		t.Fatalf("expected healthy legacy resource to skip reindex, got %d graph writes", repo.graphCalls)
	}
}

func TestImportDirectoryRepairsResourceWhenCurrentVersionHasNoChunks(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "student-handbook.md")
	if err := os.WriteFile(filePath, []byte("# 学生守则\n正文"), 0o600); err != nil {
		t.Fatalf("write markdown file: %v", err)
	}

	sourceRef := "student-handbook.md"
	repo := &fakeResourceRepo{
		listResult: []postgres.Resource{
			{
				ID:         "resource-1",
				Title:      "学生守则",
				SourceType: "upload",
				SourceRef:  &sourceRef,
			},
		},
		currentVersions: map[string]*postgres.ResourceVersion{
			"resource-1": {
				ID:            "version-1",
				ResourceID:    "resource-1",
				VersionNumber: 1,
				Content:       "# 学生守则\n旧正文",
				Source:        "original",
			},
		},
		chunkCountByVersion: map[string]int{
			"version-1": 0,
		},
	}
	service := NewService(repo, fakeEmbedder{})

	if err := service.ImportDirectory(context.Background(), dir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	if repo.graphCalls != 1 {
		t.Fatalf("expected missing chunks to trigger one repair write, got %d", repo.graphCalls)
	}

	if repo.lastGraphParams.ResourceID != "resource-1" {
		t.Fatalf("expected repair to target existing resource %q, got %q", "resource-1", repo.lastGraphParams.ResourceID)
	}

	if repo.lastGraphParams.VersionNumber != 2 {
		t.Fatalf("expected repair to append version 2, got %d", repo.lastGraphParams.VersionNumber)
	}
}

type fakeResourceRepo struct {
	createSourceRef *string
	createdChunks   []*postgres.ResourceChunk
	graphChunks     []postgres.ResourceChunkInput
	resource        *postgres.Resource
	version         *postgres.ResourceVersion
	createCalls     int
	graphCalls      int
	listResult      []postgres.Resource
	currentVersions map[string]*postgres.ResourceVersion
	chunkCountByVersion map[string]int
	updateSourceRefCalls []sourceRefUpdateCall
	lastGraphParams postgres.CreateDocumentGraphParams
}

func (r *fakeResourceRepo) List(context.Context) ([]postgres.Resource, error) {
	return append([]postgres.Resource(nil), r.listResult...), nil
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

func (r *fakeResourceRepo) CreateDocumentGraph(_ context.Context, params postgres.CreateDocumentGraphParams) (*postgres.Resource, *postgres.ResourceVersion, error) {
	r.graphCalls++
	r.createSourceRef = params.SourceRef
	r.graphChunks = append([]postgres.ResourceChunkInput(nil), params.Chunks...)
	r.lastGraphParams = params
	r.createdChunks = make([]*postgres.ResourceChunk, 0, len(params.Chunks))
	for _, chunk := range params.Chunks {
		r.createdChunks = append(r.createdChunks, &postgres.ResourceChunk{
			ChunkIndex:   chunk.ChunkIndex,
			SectionTitle: chunk.SectionTitle,
			Content:      chunk.Content,
			Embedding:    chunk.Embedding,
		})
	}
	if r.resource != nil && r.version != nil {
		return r.resource, r.version, nil
	}

	return &postgres.Resource{
			ID:         "resource-graph-default",
			Title:      params.Title,
			SourceType: params.SourceType,
		}, &postgres.ResourceVersion{
			ID:            "version-graph-default",
			ResourceID:    "resource-graph-default",
			VersionNumber: 1,
			Content:       params.Content,
			Source:        params.VersionSource,
		}, nil
}

func (r *fakeResourceRepo) GetCurrentVersion(_ context.Context, resourceID string) (*postgres.ResourceVersion, error) {
	if r.currentVersions == nil {
		return nil, nil
	}

	return r.currentVersions[resourceID], nil
}

func (r *fakeResourceRepo) CountChunksByVersion(_ context.Context, versionID string) (int, error) {
	if r.chunkCountByVersion == nil {
		return 0, nil
	}

	return r.chunkCountByVersion[versionID], nil
}

func (r *fakeResourceRepo) UpdateSourceRef(_ context.Context, resourceID string, sourceRef *string) error {
	r.updateSourceRefCalls = append(r.updateSourceRefCalls, sourceRefUpdateCall{
		resourceID: resourceID,
		sourceRef:  sourceRef,
	})
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

type fakeEmbedderWithError struct {
	err error
}

func (fake fakeEmbedderWithError) Embed(context.Context, []string) ([][]float32, error) {
	return nil, fake.err
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

type sourceRefUpdateCall struct {
	resourceID string
	sourceRef  *string
}
