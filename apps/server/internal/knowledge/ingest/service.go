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
	CreateWithSourceRef(ctx context.Context, title string, sourceType string, sourceRef *string) (*postgres.Resource, error)
	CreateVersion(ctx context.Context, resourceID string, versionNumber int, content string, source string) (*postgres.ResourceVersion, error)
	CreateChunk(ctx context.Context, chunk *postgres.ResourceChunk) error
}

type embedderClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Service 负责把 Markdown 文档导入为资源、版本和带 embedding 的分块。
type Service struct {
	resourceRepo resourceRepository
	embedder     embedderClient
}

// NewService 把导入流程依赖的存储层和 embedding 能力接起来。
func NewService(repo resourceRepository, emb embedderClient) *Service {
	return &Service{
		resourceRepo: repo,
		embedder:     emb,
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

	title := extractTitleFromContent(string(input.Content), input.FileName)
	sourceRef := strings.TrimSpace(input.FileName)
	resource, version, err := s.saveDocument(ctx, title, "upload", stringPointer(sourceRef), string(input.Content), "assistant_upload")
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

	existingResources, err := s.resourceRepo.List(ctx)
	if err != nil {
		return err
	}
	for _, resource := range existingResources {
		if resource.Title == title {
			log.Printf("跳过已导入文件：%s", title)
			return nil
		}
	}

	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	_, _, err = s.saveDocument(ctx, title, "upload", nil, content, "original")
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
	resource, err := s.resourceRepo.CreateWithSourceRef(ctx, title, sourceType, sourceRef)
	if err != nil {
		return nil, nil, err
	}

	version, err := s.resourceRepo.CreateVersion(ctx, resource.ID, 1, content, versionSource)
	if err != nil {
		return nil, nil, err
	}

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
		return nil, nil, err
	}
	if len(vectors) != len(chunks) {
		return nil, nil, fmt.Errorf("embedding 数量不匹配：得到 %d 个向量，对应 %d 个分块", len(vectors), len(chunks))
	}

	for index, chunk := range chunks {
		err := s.resourceRepo.CreateChunk(ctx, &postgres.ResourceChunk{
			ResourceID:   resource.ID,
			VersionID:    version.ID,
			ChunkIndex:   chunk.ChunkIndex,
			SectionTitle: chunk.SectionTitle,
			Content:      chunk.Content,
			Embedding:    pgvector.NewVector(vectors[index]),
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return resource, version, nil
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
