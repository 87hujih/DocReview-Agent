package ingest

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"agent_project/apps/server/internal/knowledge/chunker"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
)

// Service 负责把 Markdown 文档导入为资源、版本和带 embedding 的分块。
type Service struct {
	resourceRepo *postgres.ResourceRepo
	embedder     *embedder.Embedder
}

// NewService 把导入流程依赖的存储层和 embedding 能力接起来。
func NewService(repo *postgres.ResourceRepo, emb *embedder.Embedder) *Service {
	return &Service{
		resourceRepo: repo,
		embedder:     emb,
	}
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
			log.Printf("WARN: import %s failed: %v", filePath, err)
			continue
		}

		anySucceeded = true
	}

	if anySucceeded || lastErr == nil {
		return nil
	}

	return lastErr
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
			log.Printf("skipping already imported: %s", title)
			return nil
		}
	}

	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	resource, err := s.resourceRepo.Create(ctx, title, "upload")
	if err != nil {
		return err
	}

	version, err := s.resourceRepo.CreateVersion(ctx, resource.ID, 1, content, "original")
	if err != nil {
		return err
	}

	chunks := chunker.ChunkMarkdown(content)
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Content)
	}

	vectors, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vectors) != len(chunks) {
		return fmt.Errorf("embedding count mismatch: got %d vectors for %d chunks", len(vectors), len(chunks))
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
			return err
		}
	}

	return nil
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
