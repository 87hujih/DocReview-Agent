package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	ListSectionsByVersion(ctx context.Context, versionID string) ([]postgres.ResourceSection, error)
	ListChunksByVersion(ctx context.Context, versionID string) ([]postgres.ResourceChunk, error)
}

type embedderClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type rerankerClient interface {
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]reranker.Result, error)
}

const (
	semanticCandidateLimit = 8
	lexicalCandidateLimit  = 8
)

// Service 协调 query 向量化、双路候选召回和 reranker 重排序，完成混合检索。
type Service struct {
	resourceRepo resourceRepository
	embedder     embedderClient
	reranker     rerankerClient
	analyzer     QueryAnalyzer
}

// NewService 把检索服务依赖的存储层、embedding 和 reranker 能力接起来。
func NewService(repo resourceRepository, emb embedderClient, rerankerClient rerankerClient) *Service {
	return &Service{
		resourceRepo: repo,
		embedder:     emb,
		reranker:     rerankerClient,
		analyzer:     QueryAnalyzer{},
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
	if limit <= 0 {
		return []citation.Citation{}, nil
	}

	currentVersion, err := s.resourceRepo.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if currentVersion == nil {
		return []citation.Citation{}, nil
	}

	if citations, resolved, err := s.searchGroundedByVersion(ctx, currentVersion.ID, strings.TrimSpace(query), limit); err != nil {
		return nil, err
	} else if resolved {
		return citations, nil
	}

	return s.search(ctx, query, limit, func(vector pgvector.Vector) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunksByVersion(ctx, vector, semanticCandidateLimit, currentVersion.ID)
	}, func(normalizedQuery string) ([]postgres.ResourceChunk, error) {
		return s.resourceRepo.SearchChunksLexicalByVersion(ctx, normalizedQuery, lexicalCandidateLimit, currentVersion.ID)
	})
}

func (s *Service) searchGroundedByVersion(ctx context.Context, versionID string, query string, limit int) ([]citation.Citation, bool, error) {
	analysis := s.analyzer.Analyze(query)
	if analysis.Intent == QueryIntentGeneralSearch {
		return nil, false, nil
	}

	sections, err := s.resourceRepo.ListSectionsByVersion(ctx, versionID)
	if err != nil {
		return nil, false, err
	}
	if len(sections) == 0 {
		return nil, false, nil
	}

	targetSections := filterSectionsByType(sections, analysis.SectionType)
	if len(targetSections) == 0 {
		targetSections = sections
	}

	switch analysis.Intent {
	case QueryIntentListSections:
		citations := buildSectionListCitations(targetSections, limit)
		return citations, len(citations) > 0, nil
	case QueryIntentDetailByEntity:
		target := resolveSectionByEntity(targetSections, analysis.EntityName)
		if target == nil {
			return nil, false, nil
		}

		citations, err := s.buildSectionDetailCitations(ctx, versionID, *target, limit)
		return citations, len(citations) > 0, err
	case QueryIntentDetailByOrdinal:
		target := resolveSectionByOrdinal(targetSections, analysis.Ordinal)
		if target == nil {
			return nil, false, nil
		}

		citations, err := s.buildSectionDetailCitations(ctx, versionID, *target, limit)
		return citations, len(citations) > 0, err
	case QueryIntentAggregateAttribute:
		citations, err := s.buildAggregateCitations(ctx, versionID, targetSections, analysis.AggregateField, limit)
		return citations, len(citations) > 0, err
	default:
		return nil, false, nil
	}
}

func (s *Service) buildSectionDetailCitations(ctx context.Context, versionID string, section postgres.ResourceSection, limit int) ([]citation.Citation, error) {
	chunks, err := s.resourceRepo.ListChunksByVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}

	filtered := filterChunksBySection(chunks, section.ID)
	if len(filtered) == 0 {
		return buildSectionListCitations([]postgres.ResourceSection{section}, limit), nil
	}

	grouped := groupChunksByWindow(filtered)
	citations := citation.BuildFromWindowGroups(grouped)
	if len(citations) > limit {
		return citations[:limit], nil
	}

	return citations, nil
}

func (s *Service) buildAggregateCitations(ctx context.Context, versionID string, sections []postgres.ResourceSection, field string, limit int) ([]citation.Citation, error) {
	if limit <= 0 {
		return []citation.Citation{}, nil
	}
	if strings.TrimSpace(field) != "tech_stack" {
		return nil, nil
	}

	chunks, err := s.resourceRepo.ListChunksByVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}

	sectionOrder := make(map[string]int, len(sections))
	for _, section := range sections {
		sectionOrder[section.ID] = section.SectionOrder
	}

	techChunks := make([]postgres.ResourceChunk, 0)
	for _, chunk := range chunks {
		if optionalChunkValue(chunk.ChunkRole) != "tech_stack" {
			continue
		}
		sectionID := optionalChunkValue(chunk.SectionID)
		if _, ok := sectionOrder[sectionID]; !ok {
			continue
		}

		techChunks = append(techChunks, chunk)
	}
	if len(techChunks) > 0 {
		slices.SortStableFunc(techChunks, func(left postgres.ResourceChunk, right postgres.ResourceChunk) int {
			return cmpInts(sectionOrder[optionalChunkValue(left.SectionID)], sectionOrder[optionalChunkValue(right.SectionID)])
		})
		if len(techChunks) > limit {
			techChunks = techChunks[:limit]
		}

		return citation.BuildFromChunks(techChunks), nil
	}

	return buildSectionAggregateCitations(sections, limit), nil
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

func filterSectionsByType(sections []postgres.ResourceSection, sectionType string) []postgres.ResourceSection {
	trimmedType := strings.TrimSpace(sectionType)
	if trimmedType == "" {
		return append([]postgres.ResourceSection(nil), sections...)
	}

	filtered := make([]postgres.ResourceSection, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section.SectionType) == trimmedType {
			filtered = append(filtered, section)
		}
	}

	return filtered
}

func resolveSectionByOrdinal(sections []postgres.ResourceSection, ordinal int) *postgres.ResourceSection {
	if ordinal <= 0 {
		return nil
	}

	for index := range sections {
		if sections[index].SectionOrder == ordinal {
			return &sections[index]
		}
	}

	return nil
}

func resolveSectionByEntity(sections []postgres.ResourceSection, entityName string) *postgres.ResourceSection {
	normalizedEntity := normalizeEntityToken(entityName)
	if normalizedEntity == "" {
		return nil
	}

	bestIndex := -1
	bestScore := 0
	for index := range sections {
		score := sectionEntityMatchScore(sections[index], normalizedEntity)
		if score > bestScore {
			bestIndex = index
			bestScore = score
		}
	}

	if bestIndex < 0 {
		return nil
	}

	return &sections[bestIndex]
}

func sectionEntityMatchScore(section postgres.ResourceSection, normalizedEntity string) int {
	for _, candidate := range collectSectionEntityCandidates(section) {
		normalizedCandidate := normalizeEntityToken(candidate)
		switch {
		case normalizedCandidate == normalizedEntity:
			return 3
		case normalizedCandidate != "" && (strings.Contains(normalizedCandidate, normalizedEntity) || strings.Contains(normalizedEntity, normalizedCandidate)):
			return max(2, 0)
		}
	}

	return 0
}

func collectSectionEntityCandidates(section postgres.ResourceSection) []string {
	candidates := []string{section.Title}
	if section.CanonicalEntityName != nil {
		candidates = append(candidates, *section.CanonicalEntityName)
	}
	if len(section.AliasesJSON) > 0 {
		var aliases []string
		if err := json.Unmarshal(section.AliasesJSON, &aliases); err == nil {
			candidates = append(candidates, aliases...)
		}
	}

	return candidates
}

func normalizeEntityToken(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\n", "",
		"项目", "",
		"经历", "",
		"：", "",
		":", "",
		"（", "",
		"）", "",
		"(", "",
		")", "",
	)

	return replacer.Replace(normalized)
}

func filterChunksBySection(chunks []postgres.ResourceChunk, sectionID string) []postgres.ResourceChunk {
	filtered := make([]postgres.ResourceChunk, 0)
	for _, chunk := range chunks {
		if optionalChunkValue(chunk.SectionID) != strings.TrimSpace(sectionID) {
			continue
		}

		filtered = append(filtered, chunk)
	}

	return filtered
}

func groupChunksByWindow(chunks []postgres.ResourceChunk) [][]postgres.ResourceChunk {
	groupIndexes := make(map[string]int)
	grouped := make([][]postgres.ResourceChunk, 0)

	for _, chunk := range chunks {
		key := strings.TrimSpace(optionalChunkValue(chunk.WindowGroupID))
		if key == "" {
			key = strings.TrimSpace(chunk.ID)
		}

		groupIndex, ok := groupIndexes[key]
		if !ok {
			groupIndexes[key] = len(grouped)
			grouped = append(grouped, []postgres.ResourceChunk{chunk})
			continue
		}

		grouped[groupIndex] = append(grouped[groupIndex], chunk)
	}

	for index := range grouped {
		slices.SortStableFunc(grouped[index], func(left postgres.ResourceChunk, right postgres.ResourceChunk) int {
			return compareChunkOrder(left, right)
		})
	}

	return grouped
}

func compareChunkOrder(left postgres.ResourceChunk, right postgres.ResourceChunk) int {
	leftOrder := optionalIntValue(left.OrderInSection)
	rightOrder := optionalIntValue(right.OrderInSection)
	if leftOrder != rightOrder {
		return cmpInts(leftOrder, rightOrder)
	}

	return cmpInts(left.ChunkIndex, right.ChunkIndex)
}

func buildSectionListCitations(sections []postgres.ResourceSection, limit int) []citation.Citation {
	if limit <= 0 {
		return []citation.Citation{}
	}

	output := make([]citation.Citation, 0, min(limit, len(sections)))
	for _, section := range sections {
		snippet := strings.TrimSpace(section.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(section.Content)
		}
		if snippet == "" {
			snippet = strings.TrimSpace(section.Title)
		}

		output = append(output, citation.Citation{
			CitationID:   fmt.Sprintf("cite_%d", len(output)+1),
			ResourceID:   section.ResourceID,
			SectionID:    section.ID,
			SectionType:  section.SectionType,
			SectionTitle: section.Title,
			Snippet:      truncateRetrieverSnippet(snippet, 200),
			Window: &citation.Window{
				GroupID: strings.TrimSpace(section.SectionKey),
			},
		})
		if len(output) == limit {
			break
		}
	}

	return output
}

func buildSectionAggregateCitations(sections []postgres.ResourceSection, limit int) []citation.Citation {
	if limit <= 0 {
		return []citation.Citation{}
	}

	output := make([]citation.Citation, 0, min(limit, len(sections)))
	for _, section := range sections {
		techStack := extractSectionTechStack(section.MetadataJSON)
		if len(techStack) == 0 {
			continue
		}

		output = append(output, citation.Citation{
			CitationID:   fmt.Sprintf("cite_%d", len(output)+1),
			ResourceID:   section.ResourceID,
			SectionID:    section.ID,
			SectionType:  section.SectionType,
			SectionTitle: section.Title,
			Snippet:      truncateRetrieverSnippet(strings.Join(techStack, " "), 200),
			Window: &citation.Window{
				GroupID: strings.TrimSpace(section.SectionKey),
			},
		})
		if len(output) == limit {
			break
		}
	}

	return output
}

func extractSectionTechStack(metadataJSON []byte) []string {
	if len(metadataJSON) == 0 {
		return nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return nil
	}

	value, ok := metadata["tech_stack"]
	if !ok {
		return nil
	}

	items, ok := value.([]any)
	if !ok {
		return nil
	}

	techStack := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			techStack = append(techStack, trimmed)
		}
	}

	return techStack
}

func optionalChunkValue(value *string) string {
	if value == nil {
		return ""
	}

	return strings.TrimSpace(*value)
}

func optionalIntValue(value *int) int {
	if value == nil {
		return 0
	}

	return *value
}

func truncateRetrieverSnippet(content string, maxLength int) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= maxLength {
		return string(runes)
	}

	return string(runes[:maxLength]) + "..."
}

func cmpInts(left int, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
