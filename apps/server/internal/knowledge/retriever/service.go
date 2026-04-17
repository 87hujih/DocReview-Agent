package retriever

import (
	"context"
	"errors"
	"sort"
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/pgvector/pgvector-go"
)

type resourceRepository interface {
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	SearchChunks(ctx context.Context, embedding pgvector.Vector, limit int) ([]postgres.ResourceChunk, error)
	SearchChunksLexical(ctx context.Context, query string, limit int) ([]postgres.ResourceChunk, error)
	SearchChunksByResource(ctx context.Context, embedding pgvector.Vector, limit int, resourceID string) ([]postgres.ResourceChunk, error)
	SearchChunksLexicalByResource(ctx context.Context, query string, limit int, resourceID string) ([]postgres.ResourceChunk, error)
	SearchChunksByVersion(ctx context.Context, embedding pgvector.Vector, limit int, versionID string) ([]postgres.ResourceChunk, error)
	SearchChunksLexicalByVersion(ctx context.Context, query string, limit int, versionID string) ([]postgres.ResourceChunk, error)
}

type embedderClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type rerankerClient interface {
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]reranker.Result, error)
}

// ServiceOption 允许按需替换词法召回 backend。
type ServiceOption func(*Service)

const (
	semanticCandidateLimit = 8
	lexicalCandidateLimit  = 8
)

// Service 协调 query 向量化、双路候选召回和 reranker 重排序，完成混合检索。
type Service struct {
	resourceRepo  resourceRepository
	embedder      embedderClient
	reranker      rerankerClient
	lexical       LexicalBackend
	searchBackend string
}

// NewService 把检索服务依赖的存储层、embedding 和 reranker 能力接起来。
func NewService(repo resourceRepository, emb embedderClient, rerankerClient rerankerClient, options ...ServiceOption) *Service {
	service := &Service{
		resourceRepo:  repo,
		embedder:      emb,
		reranker:      rerankerClient,
		lexical:       newPostgresLexicalBackend(repo),
		searchBackend: "postgres_legacy",
	}

	for _, option := range options {
		if option != nil {
			option(service)
		}
	}

	if service.lexical == nil {
		service.lexical = newPostgresLexicalBackend(repo)
	}

	return service
}

// WithSearchBackend 记录当前检索后端名称，便于灰度切换和测试断言。
func WithSearchBackend(name string) ServiceOption {
	return func(service *Service) {
		service.searchBackend = strings.ToLower(strings.TrimSpace(name))
	}
}

// WithLexicalBackend 覆盖默认的 PostgreSQL lexical backend。
func WithLexicalBackend(backend LexicalBackend) ServiceOption {
	return func(service *Service) {
		service.lexical = backend
	}
}

// Search 在全部资源范围内执行混合检索。
func (s *Service) Search(ctx context.Context, query string, limit int) ([]citation.Citation, error) {
	return s.search(ctx, query, limit, AnalyzeQuery(query), func(vector pgvector.Vector) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunks(ctx, vector, semanticCandidateLimit)
	}, func(normalizedQuery string) ([]postgres.ResourceChunk, error) {
		return s.lexical.Search(ctx, normalizedQuery, lexicalSearchScope{
			Limit: lexicalCandidateLimit,
		})
	})
}

// SearchByResource 把混合检索范围限制到单个资源。
func (s *Service) SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error) {
	currentVersion, err := s.resourceRepo.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if currentVersion == nil {
		return []citation.Citation{}, nil
	}

	intent := AnalyzeQuery(query)
	return s.search(ctx, query, limit, intent, func(vector pgvector.Vector) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunksByVersion(ctx, vector, semanticCandidateLimit, currentVersion.ID)
	}, func(normalizedQuery string) ([]postgres.ResourceChunk, error) {
		return s.lexical.Search(ctx, normalizedQuery, lexicalSearchScope{
			Limit:     lexicalCandidateLimit,
			VersionID: currentVersion.ID,
		})
	})
}

func (s *Service) search(
	ctx context.Context,
	query string,
	limit int,
	intent QueryIntent,
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

	candidates := FuseReciprocalRank(semanticChunks, lexicalChunks)
	if len(candidates) == 0 {
		return []citation.Citation{}, nil
	}

	filteredCandidates := filterChunksByIntent(candidates, intent)
	if len(filteredCandidates) > 0 {
		candidates = filteredCandidates
	}

	rerankCandidates, rerankDocuments := buildWindowAwareCandidates(candidates, intent)
	rerankResults, err := s.reranker.Rerank(ctx, normalizedQuery, rerankDocuments, limit)
	if err != nil {
		return nil, err
	}

	rankedChunks := rerankResultsToChunks(rerankCandidates, rerankResults, limit)
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

func filterChunksByIntent(chunks []postgres.ResourceChunk, intent QueryIntent) []postgres.ResourceChunk {
	filtered := make([]postgres.ResourceChunk, 0, len(chunks))
	switch intent.Kind {
	case IntentListProjects, IntentProjectDetail:
		for _, chunk := range chunks {
			if chunk.SectionType == "project" {
				filtered = append(filtered, chunk)
			}
		}
	case IntentTechStack:
		for _, chunk := range chunks {
			if chunk.SectionType == "project" && chunk.ChunkRole == "tech_stack" {
				filtered = append(filtered, chunk)
			}
		}
	default:
		return nil
	}

	return filtered
}

func buildWindowAwareCandidates(chunks []postgres.ResourceChunk, intent QueryIntent) ([]postgres.ResourceChunk, []string) {
	if len(chunks) == 0 {
		return nil, nil
	}

	type groupedChunks struct {
		key    string
		chunks []postgres.ResourceChunk
	}

	groups := make([]groupedChunks, 0)
	groupIndexes := make(map[string]int)
	for _, chunk := range chunks {
		key := strings.TrimSpace(chunk.WindowGroupID)
		if key == "" {
			key = chunk.ID
		}

		index, ok := groupIndexes[key]
		if !ok {
			groupIndexes[key] = len(groups)
			groups = append(groups, groupedChunks{key: key})
			index = len(groups) - 1
		}

		groups[index].chunks = append(groups[index].chunks, chunk)
	}

	rerankCandidates := make([]postgres.ResourceChunk, 0, len(groups))
	rerankDocuments := make([]string, 0, len(groups))
	for _, group := range groups {
		sort.SliceStable(group.chunks, func(i, j int) bool {
			return group.chunks[i].ChunkIndex < group.chunks[j].ChunkIndex
		})

		window := make([]string, 0, len(group.chunks))
		for _, chunk := range group.chunks {
			text := strings.TrimSpace(chunk.Content)
			if text == "" {
				continue
			}
			window = append(window, text)
		}

		representative := attachWindowMetadata(selectRepresentativeChunk(group.chunks, intent), window)
		rerankCandidates = append(rerankCandidates, representative)
		rerankDocuments = append(rerankDocuments, strings.Join(window, "\n"))
	}

	return rerankCandidates, rerankDocuments
}

func selectRepresentativeChunk(chunks []postgres.ResourceChunk, intent QueryIntent) postgres.ResourceChunk {
	preferredRoles := []string{"section_summary"}
	switch intent.Kind {
	case IntentListProjects:
		preferredRoles = []string{"project_name", "section_summary", "project_description"}
	case IntentProjectDetail:
		preferredRoles = []string{"project_description", "project_name", "section_summary"}
	case IntentTechStack:
		preferredRoles = []string{"tech_stack", "project_name", "section_summary"}
	}

	for _, role := range preferredRoles {
		for _, chunk := range chunks {
			if chunk.ChunkRole == role {
				return chunk
			}
		}
	}

	return chunks[0]
}

func attachWindowMetadata(chunk postgres.ResourceChunk, window []string) postgres.ResourceChunk {
	metadata := make(map[string]any, len(chunk.Metadata)+1)
	for key, value := range chunk.Metadata {
		metadata[key] = value
	}
	if len(window) > 0 {
		metadata["window"] = append([]string(nil), window...)
	}

	chunk.Metadata = metadata
	return chunk
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
