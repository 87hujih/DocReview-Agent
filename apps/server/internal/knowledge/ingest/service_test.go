package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	documentnormalize "agent_project/apps/server/internal/document/normalize"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/knowledge/sections"
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

	if repo.createdChunks[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("expected chunk section title %q, got %q", sections.WholeDocumentTitle, repo.createdChunks[0].SectionTitle)
	}

	if repo.createSourceRef == nil || *repo.createSourceRef != "学生守则.pdf" {
		t.Fatalf("expected original filename to remain source_ref, got %#v", repo.createSourceRef)
	}
}

func TestImportDocumentPersistsStructuredDocumentAndSections(t *testing.T) {
	repo := &fakeResourceRepo{
		resource: &postgres.Resource{
			ID:         "resource-structured",
			Title:      "结构化简历",
			SourceType: "upload",
		},
		version: &postgres.ResourceVersion{
			ID:         "version-structured",
			ResourceID: "resource-structured",
			Content:    "CampusHub 项目正文",
			Source:     "assistant_upload",
		},
	}
	service := NewService(
		repo,
		fakeEmbedder{},
		WithParser(fakeDocumentParser{
			result: &documentparser.Result{
				Text: "CampusHub 项目正文",
				Document: &documentparser.ParsedDocument{
					SourceFormat: "pdf",
					Blocks: []documentparser.Block{
						{Type: documentparser.BlockParagraph, Text: "CampusHub 项目正文"},
					},
				},
			},
		}),
		WithNormalizer(fakeDocumentNormalizer{
			result: documentnormalize.NormalizedDocument{
				Sections: []documentnormalize.NormalizedSection{
					{
						SectionKey: "project-1",
						Type:       documentnormalize.SectionTypeProject,
						Title:      "CampusHub",
						Content:    "项目正文",
						TechStack:  []string{"Go", "Redis"},
						PageStart:  1,
						PageEnd:    1,
					},
				},
			},
		}),
	)

	result, err := service.ImportDocument(context.Background(), ImportDocumentInput{
		FileName: "resume.pdf",
		Content:  []byte("%PDF-binary-content"),
	})
	if err != nil {
		t.Fatalf("import document with structure: %v", err)
	}

	if result.Version == nil || result.Version.ID != "version-structured" {
		t.Fatalf("expected structured version result, got %#v", result.Version)
	}
	if repo.createdVersionStructure == nil {
		t.Fatal("expected structured version payload to be persisted")
	}
	if repo.createdVersionStructure.SourceFormat != "pdf" {
		t.Fatalf("expected source format pdf, got %q", repo.createdVersionStructure.SourceFormat)
	}
	if len(repo.replacedSections) != 1 {
		t.Fatalf("expected 1 persisted normalized section, got %d", len(repo.replacedSections))
	}
	if repo.replacedSections[0].SectionType != string(documentnormalize.SectionTypeProject) {
		t.Fatalf("expected persisted section type %q, got %q", documentnormalize.SectionTypeProject, repo.replacedSections[0].SectionType)
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

	if repo.graphCalls != 0 {
		t.Fatalf("expected parser failure to stop before graph write, got %d graph calls", repo.graphCalls)
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

	if repo.graphCalls != 0 {
		t.Fatalf("expected embedder failure to stop before graph write, got %d graph calls", repo.graphCalls)
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

	if repo.replaceCalls != 0 {
		t.Fatalf("expected new document import to avoid post-create reindex writes, got %d", repo.replaceCalls)
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
	versionIndexer := &fakeVersionIndexer{}
	service := NewService(repo, fakeEmbedder{}, WithIndexer(versionIndexer))

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

	if versionIndexer.reindexCalls != 0 {
		t.Fatalf("expected healthy legacy resource to skip reindex, got %d calls", versionIndexer.reindexCalls)
	}
}

func TestImportDirectoryReindexesExistingResourceWithoutChunks(t *testing.T) {
	tempDir := t.TempDir()
	content := "# 考勤制度\n\n## 考勤管理\n员工必须在九点前签到。"
	filePath := filepath.Join(tempDir, "attendance-policy.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	sourceRef := "attendance-policy.md"
	repo := &fakeResourceRepo{
		listResult: []postgres.Resource{
			{ID: "resource-existing", Title: "考勤制度", SourceType: "upload", SourceRef: &sourceRef},
		},
		currentVersions: map[string]*postgres.ResourceVersion{
			"resource-existing": {
				ID:         "version-existing",
				ResourceID: "resource-existing",
				Content:    content,
			},
		},
		chunkCountByVersion: map[string]int{
			"version-existing": 0,
		},
	}
	versionIndexer := &fakeVersionIndexer{}
	service := NewService(repo, fakeEmbedder{}, WithIndexer(versionIndexer))

	if err := service.ImportDirectory(context.Background(), tempDir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	if versionIndexer.reindexCalls != 1 {
		t.Fatalf("expected exactly 1 reindex call, got %d", versionIndexer.reindexCalls)
	}
	if versionIndexer.lastInput.Version.ID != "version-existing" {
		t.Fatalf("expected reindexed version %q, got %q", "version-existing", versionIndexer.lastInput.Version.ID)
	}
	if repo.graphCalls != 0 {
		t.Fatalf("expected no repair version graph write, got %d", repo.graphCalls)
	}
}

func TestImportDirectorySkipsIndexedResource(t *testing.T) {
	tempDir := t.TempDir()
	content := "# 考勤制度\n\n## 考勤管理\n员工必须在九点前签到。"
	filePath := filepath.Join(tempDir, "attendance-policy.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	sourceRef := "attendance-policy.md"
	repo := &fakeResourceRepo{
		listResult: []postgres.Resource{
			{ID: "resource-existing", Title: "考勤制度", SourceType: "upload", SourceRef: &sourceRef},
		},
		currentVersions: map[string]*postgres.ResourceVersion{
			"resource-existing": {
				ID:         "version-existing",
				ResourceID: "resource-existing",
				Content:    content,
			},
		},
		chunkCountByVersion: map[string]int{
			"version-existing": 2,
		},
	}
	versionIndexer := &fakeVersionIndexer{}
	service := NewService(repo, fakeEmbedder{}, WithIndexer(versionIndexer))

	if err := service.ImportDirectory(context.Background(), tempDir); err != nil {
		t.Fatalf("import directory: %v", err)
	}

	if versionIndexer.reindexCalls != 0 {
		t.Fatalf("expected indexed resource to skip reindex, got %d calls", versionIndexer.reindexCalls)
	}
	if repo.graphCalls != 0 {
		t.Fatalf("expected no new graph write, got %d", repo.graphCalls)
	}
}

type fakeResourceRepo struct {
	listResult              []postgres.Resource
	currentVersion          *postgres.ResourceVersion
	currentVersions         map[string]*postgres.ResourceVersion
	chunkCount              int
	chunkCountByVersion     map[string]int
	createSourceRef         *string
	createdChunks           []*postgres.ResourceChunk
	graphChunks             []postgres.ResourceChunkInput
	resource                *postgres.Resource
	version                 *postgres.ResourceVersion
	graphCalls              int
	replaceCalls            int
	structureCalls          int
	updateSourceRefCalls    []sourceRefUpdateCall
	lastGraphParams         postgres.CreateDocumentGraphParams
	lastReplaceVersionID    string
	lastReplaceResourceID   string
	createdVersionStructure *postgres.ResourceVersionStructureInput
	replacedSections        []postgres.ResourceSectionInput
}

func (r *fakeResourceRepo) List(context.Context) ([]postgres.Resource, error) {
	return append([]postgres.Resource(nil), r.listResult...), nil
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

	resource := r.resource
	if resource == nil {
		resource = &postgres.Resource{
			ID:         "resource-graph-default",
			Title:      params.Title,
			SourceType: params.SourceType,
			SourceRef:  params.SourceRef,
		}
	}

	version := r.version
	if version == nil {
		version = &postgres.ResourceVersion{
			ID:            "version-graph-default",
			ResourceID:    resource.ID,
			VersionNumber: 1,
			Content:       params.Content,
			Source:        params.VersionSource,
		}
	}

	return resource, version, nil
}

func (r *fakeResourceRepo) GetCurrentVersion(_ context.Context, resourceID string) (*postgres.ResourceVersion, error) {
	if r.currentVersions != nil {
		return r.currentVersions[resourceID], nil
	}

	return r.currentVersion, nil
}

func (r *fakeResourceRepo) CountChunksByVersion(_ context.Context, versionID string) (int, error) {
	if r.chunkCountByVersion != nil {
		return r.chunkCountByVersion[versionID], nil
	}

	return r.chunkCount, nil
}

func (r *fakeResourceRepo) UpdateSourceRef(_ context.Context, resourceID string, sourceRef *string) error {
	r.updateSourceRefCalls = append(r.updateSourceRefCalls, sourceRefUpdateCall{
		resourceID: resourceID,
		sourceRef:  sourceRef,
	})
	return nil
}

func (r *fakeResourceRepo) ReplaceVersionChunks(_ context.Context, versionID string, resourceID string, chunks []postgres.ResourceChunkInput) error {
	r.replaceCalls++
	r.lastReplaceVersionID = versionID
	r.lastReplaceResourceID = resourceID
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

func (r *fakeResourceRepo) CreateVersionStructure(_ context.Context, input postgres.ResourceVersionStructureInput) (*postgres.ResourceVersionStructure, error) {
	r.structureCalls++
	r.createdVersionStructure = &input
	return &postgres.ResourceVersionStructure{
		ID:               "structure-1",
		ResourceID:       input.ResourceID,
		VersionID:        input.VersionID,
		SourceFormat:     input.SourceFormat,
		ParserName:       input.ParserName,
		ParserVersion:    input.ParserVersion,
		DocumentJSON:     input.DocumentJSON,
		QualityFlagsJSON: input.QualityFlagsJSON,
	}, nil
}

func (r *fakeResourceRepo) ReplaceSectionsForVersion(_ context.Context, versionID string, resourceID string, sections []postgres.ResourceSectionInput) error {
	r.lastReplaceVersionID = versionID
	r.lastReplaceResourceID = resourceID
	r.replacedSections = append([]postgres.ResourceSectionInput(nil), sections...)
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

func (p fakeDocumentParser) SupportsFileName(string) bool {
	return true
}

func (p fakeDocumentParser) SupportedExtensions() []string {
	return []string{".md"}
}

func (p fakeDocumentParser) UnsupportedFileMessage(string) string {
	return "不支持的文件格式"
}

type fakeDocumentNormalizer struct {
	result documentnormalize.NormalizedDocument
}

func (f fakeDocumentNormalizer) Normalize(documentparser.ParsedDocument) documentnormalize.NormalizedDocument {
	return f.result
}

type fakeVersionIndexer struct {
	buildCalls   int
	reindexCalls int
	lastInput    indexer.Input
	buildChunks  []postgres.ResourceChunkInput
	buildErr     error
	reindexErr   error
}

func (f *fakeVersionIndexer) BuildVersionChunks(_ context.Context, input indexer.Input) ([]postgres.ResourceChunkInput, error) {
	f.buildCalls++
	f.lastInput = input
	if f.buildErr != nil {
		return nil, f.buildErr
	}

	return append([]postgres.ResourceChunkInput(nil), f.buildChunks...), nil
}

func (f *fakeVersionIndexer) ReindexVersion(_ context.Context, input indexer.Input) error {
	f.reindexCalls++
	f.lastInput = input
	return f.reindexErr
}

type sourceRefUpdateCall struct {
	resourceID string
	sourceRef  *string
}
