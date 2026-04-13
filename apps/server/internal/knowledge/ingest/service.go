package ingest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/knowledge/chunker"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
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
}

type embedderClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ServiceOption 允许按需注入文档解析器等扩展能力。
type ServiceOption func(*Service)

// Service 负责把 Markdown 文档导入为资源、版本和带 embedding 的分块。
type Service struct {
	resourceRepo resourceRepository
	embedder     embedderClient
	parser       documentparser.Parser
}

// NewService 把导入流程依赖的存储层和 embedding 能力接起来。
func NewService(repo resourceRepository, emb embedderClient, options ...ServiceOption) *Service {
	service := &Service{
		resourceRepo: repo,
		embedder:     emb,
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
	if s.parser != nil {
		result, err := s.parser.Parse(ctx, documentparser.Input{
			FileName: input.FileName,
			Content:  input.Content,
		})
		if err != nil {
			return nil, err
		}
		content = result.Text
	}

	title := extractTitleFromContent(content, input.FileName)
	sourceRef := strings.TrimSpace(input.FileName)
	resource, version, err := s.saveDocument(ctx, title, "upload", stringPointer(sourceRef), content, "assistant_upload")
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

		return s.ensureResourceIndexed(ctx, *matchedResource, content)
	}

	_, _, err = s.saveDocument(ctx, title, "upload", stringPointer(sourceRef), content, "original")
	return err
}

func (s *Service) saveDocument(
	ctx context.Context,
	title string,
	sourceType string,
	sourceRef *string,
	content string,
	versionSource string,
) (*postgres.Resource, *postgres.ResourceVersion, error) {
	chunkInputs, err := s.buildChunkInputs(ctx, title, content)
	if err != nil {
		return nil, nil, err
	}

	return s.resourceRepo.CreateDocumentGraph(ctx, postgres.CreateDocumentGraphParams{
		Title:         title,
		SourceType:    sourceType,
		SourceRef:     sourceRef,
		VersionSource: versionSource,
		Content:       content,
		Chunks:        chunkInputs,
	})
}

func (s *Service) ensureResourceIndexed(ctx context.Context, resource postgres.Resource, content string) error {
	currentVersion, err := s.resourceRepo.GetCurrentVersion(ctx, resource.ID)
	if err != nil {
		return err
	}

	if currentVersion != nil {
		chunkCount, err := s.resourceRepo.CountChunksByVersion(ctx, currentVersion.ID)
		if err != nil {
			return err
		}
		if chunkCount > 0 {
			log.Printf("跳过已导入文件：%s", resource.Title)
			return nil
		}
	}

	chunkInputs, err := s.buildChunkInputs(ctx, resource.Title, content)
	if err != nil {
		return err
	}

	versionNumber := 1
	versionSource := "original"
	if currentVersion != nil {
		versionNumber = currentVersion.VersionNumber + 1
		versionSource = "repair_reindex"
	}

	_, _, err = s.resourceRepo.CreateDocumentGraph(ctx, postgres.CreateDocumentGraphParams{
		ResourceID:     resource.ID,
		Title:          resource.Title,
		SourceType:     resource.SourceType,
		SourceRef:      resource.SourceRef,
		VersionNumber:  versionNumber,
		VersionSource:  versionSource,
		Content:        content,
		Chunks:         chunkInputs,
	})
	return err
}

func (s *Service) buildChunkInputs(ctx context.Context, title string, content string) ([]postgres.ResourceChunkInput, error) {
	chunks := chunker.ChunkMarkdown(content)
	if len(chunks) == 0 && strings.TrimSpace(content) != "" {
		chunks = []chunker.Chunk{
			{
				ChunkIndex:   0,
				SectionTitle: title,
				Content:      strings.TrimSpace(content),
			},
		}
	}
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Content)
	}

	vectors, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(chunks) {
		return nil, fmt.Errorf("embedding 数量不匹配：得到 %d 个向量，对应 %d 个分块", len(vectors), len(chunks))
	}

	chunkInputs := make([]postgres.ResourceChunkInput, 0, len(chunks))
	for index, chunk := range chunks {
		chunkInputs = append(chunkInputs, postgres.ResourceChunkInput{
			ChunkIndex:   chunk.ChunkIndex,
			SectionTitle: chunk.SectionTitle,
			Content:      chunk.Content,
			Embedding:    pgvector.NewVector(vectors[index]),
		})
	}

	return chunkInputs, nil
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
