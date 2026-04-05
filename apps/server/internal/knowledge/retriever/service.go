package retriever

import (
	"context"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
)

// Service 协调查询向量化和分块查找，完成语义检索。
type Service struct {
	resourceRepo *postgres.ResourceRepo
	embedder     *embedder.Embedder
}

// NewService 把检索服务依赖的存储层和 embedding 能力接起来。
func NewService(repo *postgres.ResourceRepo, emb *embedder.Embedder) *Service {
	return &Service{
		resourceRepo: repo,
		embedder:     emb,
	}
}

// Search 在全部资源范围内执行语义检索。
func (s *Service) Search(ctx context.Context, query string, limit int) ([]citation.Citation, error) {
	vector, err := s.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	if vector == nil {
		return []citation.Citation{}, nil
	}

	chunks, err := s.resourceRepo.SearchChunks(ctx, *vector, limit)
	if err != nil {
		return nil, err
	}

	return citation.BuildFromChunks(chunks), nil
}

// SearchByResource 把语义检索范围限制到单个资源。
func (s *Service) SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error) {
	vector, err := s.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	if vector == nil {
		return []citation.Citation{}, nil
	}

	chunks, err := s.resourceRepo.SearchChunksByResource(ctx, *vector, limit, resourceID)
	if err != nil {
		return nil, err
	}

	return citation.BuildFromChunks(chunks), nil
}

// queryVector 统一负责把用户查询转成向量，保证两个检索入口使用同一套向量生成逻辑。
func (s *Service) queryVector(ctx context.Context, query string) (*pgvector.Vector, error) {
	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, nil
	}

	vector := pgvector.NewVector(embeddings[0])
	return &vector, nil
}
