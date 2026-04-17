package indexer

import (
	"context"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/knowledge/chunker"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
)

type resourceChunkReplacer interface {
	ReplaceVersionChunks(ctx context.Context, versionID string, resourceID string, chunks []postgres.ResourceChunkInput) error
}

type embedderClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Input 描述一次重建版本索引所需的资源与版本上下文。
type Input struct {
	Resource postgres.Resource
	Version  postgres.ResourceVersion
	Sections []postgres.ResourceSection
}

// Service 负责把某个资源版本重建为一组可检索分块。
type Service struct {
	repo     resourceChunkReplacer
	embedder embedderClient
}

// NewService 创建版本索引器。
func NewService(repo resourceChunkReplacer, embedder embedderClient) *Service {
	return &Service{
		repo:     repo,
		embedder: embedder,
	}
}

// BuildVersionChunks 根据版本正文生成待写入的分块输入。
func (s *Service) BuildVersionChunks(ctx context.Context, input Input) ([]postgres.ResourceChunkInput, error) {
	content := strings.TrimSpace(input.Version.Content)
	if content == "" {
		return nil, nil
	}

	chunks := buildSectionAwareChunkInputs(input.Sections)
	if len(chunks) == 0 {
		legacyChunks := chunker.ChunkMarkdown(content)
		chunks = make([]postgres.ResourceChunkInput, 0, len(legacyChunks))
		for _, chunk := range legacyChunks {
			chunks = append(chunks, postgres.ResourceChunkInput{
				ChunkIndex:   chunk.ChunkIndex,
				SectionTitle: chunk.SectionTitle,
				Content:      chunk.Content,
			})
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

	inputChunks := make([]postgres.ResourceChunkInput, 0, len(chunks))
	for index, chunk := range chunks {
		chunk.Embedding = pgvector.NewVector(vectors[index])
		inputChunks = append(inputChunks, chunk)
	}

	return inputChunks, nil
}

// ReindexVersion 根据当前版本正文重新构建该版本的全部分块。
func (s *Service) ReindexVersion(ctx context.Context, input Input) error {
	inputChunks, err := s.BuildVersionChunks(ctx, input)
	if err != nil {
		return err
	}

	return s.repo.ReplaceVersionChunks(ctx, input.Version.ID, input.Resource.ID, inputChunks)
}
