package retriever

import (
	"context"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
)

type Service struct {
	resourceRepo *postgres.ResourceRepo
	embedder     *embedder.Embedder
}

func NewService(repo *postgres.ResourceRepo, emb *embedder.Embedder) *Service {
	return &Service{
		resourceRepo: repo,
		embedder:     emb,
	}
}

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
