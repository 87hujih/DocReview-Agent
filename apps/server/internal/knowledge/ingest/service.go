package ingest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	documentnormalize "agent_project/apps/server/internal/document/normalize"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"
)

var (
	// ErrContentRequired 表示导入文档内容为空。
	ErrContentRequired = errors.New("导入内容不能为空")
)

type resourceRepository interface {
	List(ctx context.Context) ([]postgres.Resource, error)
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	CountChunksByVersion(ctx context.Context, versionID string) (int, error)
	UpdateSourceRef(ctx context.Context, resourceID string, sourceRef *string) error
	CreateDocumentGraph(ctx context.Context, params postgres.CreateDocumentGraphParams) (*postgres.Resource, *postgres.ResourceVersion, error)
	ReplaceVersionChunks(ctx context.Context, versionID string, resourceID string, chunks []postgres.ResourceChunkInput) error
}

type embedderClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type versionIndexer interface {
	BuildVersionChunks(ctx context.Context, input indexer.Input) ([]postgres.ResourceChunkInput, error)
	ReindexVersion(ctx context.Context, input indexer.Input) error
}

type structureRepository interface {
	CreateVersionStructure(ctx context.Context, params postgres.CreateVersionStructureParams) (*postgres.ResourceVersionStructure, error)
	ReplaceSectionsForVersion(ctx context.Context, versionID string, resourceID string, sections []postgres.ResourceSectionInput) ([]postgres.ResourceSection, error)
}

type documentNormalizer interface {
	Normalize(document documentparser.ParsedDocument) documentnormalize.NormalizedDocument
}

// ServiceOption 允许按需注入文档解析器等扩展能力。
type ServiceOption func(*Service)

// Service 负责把 Markdown 文档导入为资源、版本和带 embedding 的分块。
type Service struct {
	resourceRepo  resourceRepository
	structureRepo structureRepository
	embedder      embedderClient
	parser        documentparser.Parser
	normalizer    documentNormalizer
	indexer       versionIndexer
}

// NewService 把导入流程依赖的存储层和 embedding 能力接起来。
func NewService(repo resourceRepository, emb embedderClient, options ...ServiceOption) *Service {
	service := &Service{
		resourceRepo: repo,
		embedder:     emb,
		indexer:      indexer.NewService(repo, emb),
	}

	for _, option := range options {
		if option != nil {
			option(service)
		}
	}

	return service
}

// WithParser 为上传导入链路注入正文解析器。
func WithParser(parser documentparser.Parser) ServiceOption {
	return func(service *Service) {
		service.parser = parser
	}
}

// WithIndexer 为导入流程注入可替换的版本索引器，便于测试与自愈。
func WithIndexer(indexer versionIndexer) ServiceOption {
	return func(service *Service) {
		service.indexer = indexer
	}
}

// WithStructureRepo 为结构化解析结果注入持久化仓储。
func WithStructureRepo(repo structureRepository) ServiceOption {
	return func(service *Service) {
		service.structureRepo = repo
	}
}

// WithNormalizer 为结构化文档注入 section 归一化能力。
func WithNormalizer(normalizer documentNormalizer) ServiceOption {
	return func(service *Service) {
		service.normalizer = normalizer
	}
}

// ImportDocument 把单个上传文件直接导入资源库。
type ImportDocumentInput struct {
	FileName string
	Content  []byte
}

// ImportDocumentResult 返回导入后生成的资源与版本。
type ImportDocumentResult struct {
	Resource *postgres.Resource
	Version  *postgres.ResourceVersion
}

// ImportDirectory 以尽力而为的方式导入目录中的 Markdown 文件；只有全部失败时才返回错误。
func (s *Service) ImportDirectory(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var lastErr error
	anySucceeded := false

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		if err := s.importFile(ctx, filePath, entry.Name()); err != nil {
			lastErr = err
			log.Printf("警告：导入 %s 失败：%v", filePath, err)
			continue
		}

		anySucceeded = true
	}

	if anySucceeded || lastErr == nil {
		return nil
	}

	return lastErr
}

// ImportDocument 支持助手会话上传后直接入库。
func (s *Service) ImportDocument(ctx context.Context, input ImportDocumentInput) (*ImportDocumentResult, error) {
	if len(input.Content) == 0 {
		return nil, ErrContentRequired
	}

	content := string(input.Content)
	var parsedDocument *documentparser.ParsedDocument
	if s.parser != nil {
		result, err := s.parser.Parse(ctx, documentparser.Input{
			FileName: input.FileName,
			Content:  input.Content,
		})
		if err != nil {
			return nil, err
		}
		content = result.Text
		parsedDocument = result.Document
	}

	title := extractTitleFromContent(content, input.FileName)
	sourceRef := strings.TrimSpace(input.FileName)
	resource, version, err := s.saveDocument(ctx, title, "upload", stringPointer(sourceRef), content, "assistant_upload", parsedDocument)
	if err != nil {
		return nil, err
	}

	return &ImportDocumentResult{
		Resource: resource,
		Version:  version,
	}, nil
}

// importFile 会落库原始文档、首个版本，以及后续检索使用的分块 embedding。
func (s *Service) importFile(ctx context.Context, filePath string, fileName string) error {
	title, err := extractTitle(filePath, fileName)
	if err != nil {
		return err
	}

	sourceRef := strings.TrimSpace(fileName)

	existingResources, err := s.resourceRepo.List(ctx)
	if err != nil {
		return err
	}

	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	matchedResource, backfillSourceRef := findImportTarget(existingResources, title, sourceRef)
	if matchedResource != nil {
		if backfillSourceRef {
			ref := stringPointer(sourceRef)
			if err := s.resourceRepo.UpdateSourceRef(ctx, matchedResource.ID, ref); err != nil {
				return err
			}
			matchedResource.SourceRef = ref
		}

		return s.ensureResourceIndexed(ctx, *matchedResource)
	}

	_, _, err = s.saveDocument(ctx, title, "upload", stringPointer(sourceRef), content, "original", nil)
	return err
}

func (s *Service) ensureResourceIndexed(ctx context.Context, resource postgres.Resource) error {
	currentVersion, err := s.resourceRepo.GetCurrentVersion(ctx, resource.ID)
	if err != nil {
		return err
	}
	if currentVersion == nil {
		return fmt.Errorf("资源 %s 当前版本不存在", resource.Title)
	}

	chunkCount, err := s.resourceRepo.CountChunksByVersion(ctx, currentVersion.ID)
	if err != nil {
		return err
	}
	if chunkCount > 0 {
		log.Printf("跳过已导入文件：%s", resource.Title)
		return nil
	}

	log.Printf("检测到资源缺少索引，开始自愈：%s", resource.Title)
	return s.indexer.ReindexVersion(ctx, indexer.Input{
		Resource: resource,
		Version:  *currentVersion,
	})
}

func (s *Service) saveDocument(
	ctx context.Context,
	title string,
	sourceType string,
	sourceRef *string,
	content string,
	versionSource string,
	parsedDocument *documentparser.ParsedDocument,
) (*postgres.Resource, *postgres.ResourceVersion, error) {
	chunks, err := s.indexer.BuildVersionChunks(ctx, indexer.Input{
		Resource: postgres.Resource{
			Title:      title,
			SourceType: sourceType,
			SourceRef:  sourceRef,
		},
		Version: postgres.ResourceVersion{
			Content: content,
			Source:  versionSource,
		},
	})
	if err != nil {
		return nil, nil, err
	}

	resource, version, err := s.resourceRepo.CreateDocumentGraph(ctx, postgres.CreateDocumentGraphParams{
		Title:         title,
		SourceType:    sourceType,
		SourceRef:     sourceRef,
		VersionSource: versionSource,
		Content:       content,
		Chunks:        chunks,
	})
	if err != nil {
		return nil, nil, err
	}

	persistedSections, err := s.persistStructuredDocument(ctx, resource.ID, version.ID, parsedDocument)
	if err != nil {
		return nil, nil, err
	}
	if len(persistedSections) > 0 {
		if err := s.indexer.ReindexVersion(ctx, indexer.Input{
			Resource: *resource,
			Version:  *version,
			Sections: persistedSections,
		}); err != nil {
			return nil, nil, err
		}
	}

	return resource, version, nil
}

func (s *Service) persistStructuredDocument(ctx context.Context, resourceID string, versionID string, parsedDocument *documentparser.ParsedDocument) ([]postgres.ResourceSection, error) {
	if parsedDocument == nil || s.structureRepo == nil {
		return nil, nil
	}

	documentJSON, err := json.Marshal(parsedDocument)
	if err != nil {
		return nil, err
	}
	qualityFlagsJSON, err := json.Marshal(parsedDocument.QualityFlags)
	if err != nil {
		return nil, err
	}

	if _, err := s.structureRepo.CreateVersionStructure(ctx, postgres.CreateVersionStructureParams{
		ResourceID:       resourceID,
		VersionID:        versionID,
		SourceFormat:     parsedDocument.SourceFormat,
		ParserName:       parsedDocument.Metadata.ParserName,
		DocumentJSON:     documentJSON,
		QualityFlagsJSON: qualityFlagsJSON,
	}); err != nil {
		return nil, err
	}

	if s.normalizer == nil {
		return nil, nil
	}

	normalizedDocument := s.normalizer.Normalize(*parsedDocument)
	sections, err := buildSectionInputs(normalizedDocument.Sections)
	if err != nil {
		return nil, err
	}
	return s.structureRepo.ReplaceSectionsForVersion(ctx, versionID, resourceID, sections)
}

func buildSectionInputs(sections []documentnormalize.NormalizedSection) ([]postgres.ResourceSectionInput, error) {
	inputs := make([]postgres.ResourceSectionInput, 0, len(sections))
	for _, section := range sections {
		aliasesJSON, err := json.Marshal(section.Aliases)
		if err != nil {
			return nil, err
		}

		metadata := make(map[string]any, len(section.Metadata)+1)
		for key, value := range section.Metadata {
			metadata[key] = value
		}
		if len(section.TechStack) > 0 {
			metadata["tech_stack"] = append([]string(nil), section.TechStack...)
		}
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return nil, err
		}

		input := postgres.ResourceSectionInput{
			SectionKey:   section.SectionKey,
			SectionType:  string(section.Type),
			SectionOrder: section.Order,
			Title:        section.Title,
			AliasesJSON:  aliasesJSON,
			Summary:      section.Summary,
			Content:      section.Content,
			MetadataJSON: metadataJSON,
		}
		if strings.TrimSpace(section.CanonicalEntityName) != "" {
			input.CanonicalEntityName = stringPointer(section.CanonicalEntityName)
		}
		if section.PageStart > 0 {
			input.PageStart = intPointer(section.PageStart)
		}
		if section.PageEnd > 0 {
			input.PageEnd = intPointer(section.PageEnd)
		}

		inputs = append(inputs, input)
	}

	return inputs, nil
}

func findImportTarget(resources []postgres.Resource, title string, sourceRef string) (*postgres.Resource, bool) {
	var legacyMatch *postgres.Resource
	for index := range resources {
		resource := &resources[index]
		if normalizeSourceRef(resource.SourceRef) == sourceRef && sourceRef != "" {
			return resource, false
		}

		if legacyMatch == nil && resource.Title == title && normalizeSourceRef(resource.SourceRef) == "" {
			legacyMatch = resource
		}
	}

	if legacyMatch != nil {
		return legacyMatch, true
	}

	return nil, false
}

func normalizeSourceRef(sourceRef *string) string {
	if sourceRef == nil {
		return ""
	}

	return strings.TrimSpace(*sourceRef)
}

// extractTitle 优先读取文件顶部附近的 H1 标题，读不到时再退回到基于文件名生成标题。
func extractTitle(filePath string, fileName string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 0; lineNumber < 5 && scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# ")), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	return strings.ReplaceAll(name, "-", " "), nil
}

func extractTitleFromContent(content string, fileName string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNumber := 0; lineNumber < 5 && scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}

	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	return strings.ReplaceAll(name, "-", " ")
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return &value
}

func intPointer(value int) *int {
	if value <= 0 {
		return nil
	}

	return &value
}
