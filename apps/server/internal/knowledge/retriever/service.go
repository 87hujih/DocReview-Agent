package retriever

import (
	"context"
	"errors"
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
)

const (
	semanticCandidateLimit = 8
	lexicalCandidateLimit  = 8
)

// Service 协调 query 向量化、双路候选召回和 reranker 重排序，完成混合检索。
type Service struct {
	resourceRepo *postgres.ResourceRepo
	embedder     *embedder.Embedder
	reranker     *reranker.Client
}

// NewService 把检索服务依赖的存储层、embedding 和 reranker 能力接起来。
func NewService(repo *postgres.ResourceRepo, emb *embedder.Embedder, rerankerClient *reranker.Client) *Service {
	return &Service{
		resourceRepo: repo,
		embedder:     emb,
		reranker:     rerankerClient,
	}
}

// Search 在全部资源范围内执行混合检索。
func (s *Service) Search(ctx context.Context, query string, limit int) ([]citation.Citation, error) {
	return s.search(ctx, query, limit, func(vector pgvector.Vector) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunks(ctx, vector, semanticCandidateLimit)
	}, func(normalizedQuery string) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunksLexical(ctx, normalizedQuery, lexicalCandidateLimit)
	})
}

// SearchByResource 把混合检索范围限制到单个资源。
func (s *Service) SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error) {
	return s.search(ctx, query, limit, func(vector pgvector.Vector) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunksByResource(ctx, vector, semanticCandidateLimit, resourceID)
	}, func(normalizedQuery string) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunksLexicalByResource(ctx, normalizedQuery, lexicalCandidateLimit, resourceID)
	})
}

func (s *Service) search(
	ctx context.Context,
	query string,
	limit int,
	semanticSearch func(pgvector.Vector) ([]postgres.ResourceChunk, error),
	lexicalSearch func(string) ([]postgres.ResourceChunk, error),
) ([]citation.Citation, error) {
	if limit <= 0 {
		return []citation.Citation{}, nil
	}
	if s.reranker == nil {
		return nil, errors.New("reranker 未配置")
	}

	normalizedQuery := strings.TrimSpace(query)
	if normalizedQuery == "" {
		return []citation.Citation{}, nil
	}

	vector, err := s.queryVector(ctx, normalizedQuery)
	if err != nil {
		return nil, err
	}
	if vector == nil {
		return []citation.Citation{}, nil
	}

	semanticChunks, err := semanticSearch(*vector)
	if err != nil {
		return nil, err
	}

	lexicalChunks, err := lexicalSearch(normalizedQuery)
	if err != nil {
		return nil, err
	}

	candidates := mergeUniqueChunks(semanticChunks, lexicalChunks)
	if len(candidates) == 0 {
		return []citation.Citation{}, nil
	}

	rerankResults, err := s.reranker.Rerank(ctx, normalizedQuery, buildRerankerDocuments(candidates), limit)
	if err != nil {
		return nil, err
	}

	rankedChunks := rerankResultsToChunks(candidates, rerankResults, limit)
	return citation.BuildFromChunks(rankedChunks), nil
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

func mergeUniqueChunks(groups ...[]postgres.ResourceChunk) []postgres.ResourceChunk {
	seen := make(map[string]struct{})
	merged := make([]postgres.ResourceChunk, 0)

	for _, group := range groups {
		for _, chunk := range group {
			if _, ok := seen[chunk.ID]; ok {
				continue
			}

			seen[chunk.ID] = struct{}{}
			merged = append(merged, chunk)
		}
	}

	return merged
}

func buildRerankerDocuments(chunks []postgres.ResourceChunk) []string {
	documents := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		sectionTitle := strings.TrimSpace(chunk.SectionTitle)
		content := strings.TrimSpace(chunk.Content)
		if sectionTitle == "" {
			documents = append(documents, content)
			continue
		}

		documents = append(documents, sectionTitle+"\n"+content)
	}

	return documents
}

func rerankResultsToChunks(chunks []postgres.ResourceChunk, results []reranker.Result, limit int) []postgres.ResourceChunk {
	if limit <= 0 || len(chunks) == 0 || len(results) == 0 {
		return []postgres.ResourceChunk{}
	}

	ranked := make([]postgres.ResourceChunk, 0, min(limit, len(results)))
	seenIndexes := make(map[int]struct{})
	for _, result := range results {
		if result.Index < 0 || result.Index >= len(chunks) {
			continue
		}
		if _, ok := seenIndexes[result.Index]; ok {
			continue
		}

		seenIndexes[result.Index] = struct{}{}
		ranked = append(ranked, chunks[result.Index])
		if len(ranked) == limit {
			break
		}
	}

	return ranked
}
