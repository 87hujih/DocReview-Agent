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

type versionSync interface {
	SyncVersion(ctx context.Context, resourceID string, versionID string) error
}

// ServiceOption 允许为索引器注入额外的同步副作用。
type ServiceOption func(*Service)

// Input 描述一次重建版本索引所需的资源与版本上下文。
type Input struct {
	Resource postgres.Resource
	Version  postgres.ResourceVersion
	Sections []postgres.ResourceSectionInput
}

// Service 负责把某个资源版本重建为一组可检索分块。
type Service struct {
	repo        resourceChunkReplacer
	embedder    embedderClient
	versionSync versionSync
}

// NewService 创建版本索引器。
func NewService(repo resourceChunkReplacer, embedder embedderClient, options ...ServiceOption) *Service {
	service := &Service{
		repo:     repo,
		embedder: embedder,
	}

	for _, option := range options {
		if option != nil {
			option(service)
		}
	}

	return service
}

// WithVersionSync 为索引器注入可选的版本级搜索投影同步。
func WithVersionSync(sync versionSync) ServiceOption {
	return func(service *Service) {
		service.versionSync = sync
	}
}

// BuildVersionChunks 根据版本正文生成待写入的分块输入。
func (s *Service) BuildVersionChunks(ctx context.Context, input Input) ([]postgres.ResourceChunkInput, error) {
	if len(input.Sections) > 0 {
		specs := buildSectionChunkSpecs(input.Sections)
		if len(specs) == 0 {
			return nil, nil
		}

		texts := make([]string, 0, len(specs))
		for _, spec := range specs {
			texts = append(texts, spec.Content)
		}

		vectors, err := s.embedder.Embed(ctx, texts)
		if err != nil {
			return nil, err
		}
		if len(vectors) != len(specs) {
			return nil, fmt.Errorf("embedding 数量不匹配：得到 %d 个向量，对应 %d 个结构化分块", len(vectors), len(specs))
		}

		inputChunks := make([]postgres.ResourceChunkInput, 0, len(specs))
		for index, spec := range specs {
			inputChunks = append(inputChunks, postgres.ResourceChunkInput{
				ChunkIndex:    index,
				SectionTitle:  spec.SectionTitle,
				Content:       spec.Content,
				Embedding:     pgvector.NewVector(vectors[index]),
				SectionID:     spec.SectionID,
				SectionType:   spec.SectionType,
				ChunkRole:     spec.ChunkRole,
				WindowGroupID: spec.WindowGroupID,
				PageStart:     spec.PageStart,
				PageEnd:       spec.PageEnd,
				Metadata:      spec.Metadata,
			})
		}

		return inputChunks, nil
	}

	content := strings.TrimSpace(input.Version.Content)
	if content == "" {
		return nil, nil
	}

	chunks := chunker.ChunkMarkdown(content)

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
		inputChunks = append(inputChunks, postgres.ResourceChunkInput{
			ChunkIndex:   chunk.ChunkIndex,
			SectionTitle: chunk.SectionTitle,
			Content:      chunk.Content,
			Embedding:    pgvector.NewVector(vectors[index]),
			SectionType:  "whole_document",
			ChunkRole:    "section_body",
		})
	}

	return inputChunks, nil
}

// ReindexVersion 根据当前版本正文重新构建该版本的全部分块。
func (s *Service) ReindexVersion(ctx context.Context, input Input) error {
	inputChunks, err := s.BuildVersionChunks(ctx, input)
	if err != nil {
		return err
	}

	if err := s.repo.ReplaceVersionChunks(ctx, input.Version.ID, input.Resource.ID, inputChunks); err != nil {
		return err
	}
	if s.versionSync == nil {
		return nil
	}

	return s.versionSync.SyncVersion(ctx, input.Resource.ID, input.Version.ID)
}
