package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidSearchRequest     = errors.New("无效的证据搜索请求")
	ErrScopeNotFound            = errors.New("检索作用域未找到")
	ErrEmbeddingProfileMismatch = errors.New("嵌入配置档不匹配数据库向量类型")
	ErrRetrievalUnavailable     = errors.New("全部已配置的检索 channels 均不可用")
)

type Scope struct {
	WorkspaceID      string
	ResourceID       string
	VersionID        string
	SourceType       string
	EmbeddingProfile string
}

type Candidate struct {
	SourceID   string
	ResourceID string
	VersionID  string
	NodeID     string
	SourceType string
	Content    string
	CreatedAt  time.Time
}

type ScoredCandidate struct {
	Candidate Candidate
	Score     float64
}

type EmbeddingProfile struct {
	Version      string `json:"version"`
	Model        string `json:"model"`
	Dimensions   int    `json:"dimensions"`
	VectorType   string `json:"vector_type"`
	IndexVersion string `json:"index_version"`
}

type Repository interface {
	ResolveScope(ctx context.Context, workspaceID, resourceID, versionID string, includeHistory bool) (Scope, error)
	EmbeddingVectorType(ctx context.Context) (string, error)
	SearchLexical(ctx context.Context, scope Scope, query string, limit int) ([]ScoredCandidate, error)
	SearchSemantic(ctx context.Context, scope Scope, vector []float32, profile EmbeddingProfile, limit int) ([]ScoredCandidate, error)
}

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type RerankResult struct {
	Index int
	Score float64
}

type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error)
}

type Config struct {
	ProfileVersion       string
	LexicalEnabled       bool
	SemanticEnabled      bool
	LexicalIndexVersion  string
	SemanticIndexVersion string
	CandidateLimit       int
	FusionAlgorithm      FusionAlgorithm
	LexicalWeight        float64
	VectorWeight         float64
	RRFConstant          float64
	MinimumFusedScore    float64
	Embedding            EmbeddingProfile
	RerankEnabled        bool
	RerankProfileVersion string
	RerankModel          string
	Now                  func() time.Time
}

type SearchRequest struct {
	WorkspaceID    string
	ResourceID     string
	VersionID      string
	IncludeHistory bool
	Query          string
	Limit          int
}

type Service struct {
	cfg      Config
	repo     Repository
	embedder Embedder
	reranker Reranker
}

// NewService 校验依赖并创建对应实例。
func NewService(cfg Config, repo Repository, embedder Embedder, reranker Reranker) (*Service, error) {
	cfg.ProfileVersion = strings.TrimSpace(cfg.ProfileVersion)
	cfg.LexicalIndexVersion = strings.TrimSpace(cfg.LexicalIndexVersion)
	cfg.SemanticIndexVersion = strings.TrimSpace(cfg.SemanticIndexVersion)
	cfg.RerankProfileVersion = strings.TrimSpace(cfg.RerankProfileVersion)
	if repo == nil || cfg.ProfileVersion == "" || cfg.CandidateLimit <= 0 || cfg.CandidateLimit > 100 ||
		(!cfg.LexicalEnabled && !cfg.SemanticEnabled) || !cfg.FusionAlgorithm.valid() ||
		!validScore(cfg.MinimumFusedScore) || cfg.RerankProfileVersion == "" {
		return nil, fmt.Errorf("无效的检索服务配置")
	}
	if cfg.LexicalEnabled && (cfg.LexicalIndexVersion == "" || cfg.LexicalWeight <= 0) {
		return nil, fmt.Errorf("词法检索索引和权重不能为空")
	}
	if cfg.SemanticEnabled && (embedder == nil || cfg.SemanticIndexVersion == "" || cfg.VectorWeight <= 0 ||
		blank(cfg.Embedding.Version) || blank(cfg.Embedding.Model) || cfg.Embedding.Dimensions <= 0 || blank(cfg.Embedding.VectorType)) {
		return nil, fmt.Errorf("语义检索嵌入配置档不能为空")
	}
	if cfg.FusionAlgorithm == FusionRRF && cfg.RRFConstant <= 0 {
		return nil, fmt.Errorf("RRF 常量必须为正数")
	}
	if cfg.RerankEnabled && (reranker == nil || blank(cfg.RerankModel)) {
		return nil, fmt.Errorf("已启用的重排需要一个模型和客户端")
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{cfg: cfg, repo: repo, embedder: embedder, reranker: reranker}, nil
}

// 搜索执行该函数负责的核心处理逻辑。
func (service *Service) Search(ctx context.Context, request SearchRequest) (EvidenceSet, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	request.Query = strings.TrimSpace(request.Query)
	if request.WorkspaceID == "" || request.ResourceID == "" || request.Query == "" || request.Limit <= 0 || request.Limit > 50 ||
		(request.IncludeHistory && request.VersionID == "") || (!request.IncludeHistory && request.VersionID != "") {
		return EvidenceSet{}, ErrInvalidSearchRequest
	}

	scope, err := service.repo.ResolveScope(ctx, request.WorkspaceID, request.ResourceID, request.VersionID, request.IncludeHistory)
	if err != nil {
		return EvidenceSet{}, fmt.Errorf("解析检索作用域：%w", err)
	}
	if blank(scope.WorkspaceID) || blank(scope.ResourceID) || blank(scope.VersionID) ||
		scope.WorkspaceID != request.WorkspaceID || scope.ResourceID != request.ResourceID ||
		(request.IncludeHistory && scope.VersionID != request.VersionID) {
		return EvidenceSet{}, ErrScopeNotFound
	}

	now := service.cfg.Now().UTC()
	queryHash := digest(request.Query)
	set := EvidenceSet{
		SchemaVersion: SchemaVersion, SetID: setID(scope, queryHash, service.cfg.ProfileVersion),
		WorkspaceID: scope.WorkspaceID, ResourceID: scope.ResourceID, VersionID: scope.VersionID,
		Query: request.Query, QueryHash: queryHash, ProfileVersion: service.cfg.ProfileVersion,
		CreatedAt: now, Evidence: []Evidence{}, Process: []ProcessRecord{},
	}

	var lexical []ScoredCandidate
	lexicalAvailable := false
	if service.cfg.LexicalEnabled {
		var lexicalErr error
		lexical, lexicalErr = service.repo.SearchLexical(ctx, scope, request.Query, service.cfg.CandidateLimit)
		if lexicalErr != nil {
			lexical = nil
			set.Process = append(set.Process, ProcessRecord{Stage: StageRecall, Status: ProcessDegraded, Channel: ChannelLexical, Reason: "lexical_recall_failed"})
		} else {
			lexicalAvailable = true
			set.Process = append(set.Process, ProcessRecord{Stage: StageRecall, Status: ProcessSucceeded, Channel: ChannelLexical, OutputCount: len(lexical)})
		}
	}

	var semantic []ScoredCandidate
	semanticAvailable := false
	if service.cfg.SemanticEnabled {
		vectorType, vectorTypeErr := service.repo.EmbeddingVectorType(ctx)
		if vectorTypeErr != nil {
			set.Process = append(set.Process, ProcessRecord{Stage: StageRecall, Status: ProcessDegraded, Channel: ChannelSemantic, Reason: "vector_type_lookup_failed"})
		} else if strings.TrimSpace(vectorType) != service.cfg.Embedding.VectorType ||
			strings.TrimSpace(scope.EmbeddingProfile) != service.cfg.Embedding.Version {
			return EvidenceSet{}, fmt.Errorf("%w：已配置的配置档=%s 模型=%s 维度=%d vector_type=%s 数据库_vector_type=%s version_配置档=%s",
				ErrEmbeddingProfileMismatch, service.cfg.Embedding.Version, service.cfg.Embedding.Model,
				service.cfg.Embedding.Dimensions, service.cfg.Embedding.VectorType, strings.TrimSpace(vectorType), strings.TrimSpace(scope.EmbeddingProfile))
		} else {
			embeddings, embedErr := service.embedder.Embed(ctx, []string{request.Query})
			// 根据当前状态或类型选择对应的处理分支。
			switch {
			case embedErr != nil:
				set.Process = append(set.Process, ProcessRecord{Stage: StageRecall, Status: ProcessDegraded, Channel: ChannelSemantic, Reason: "embedding_provider_failed"})
			case len(embeddings) != 1 || len(embeddings[0]) != service.cfg.Embedding.Dimensions:
				actualDimensions := 0
				if len(embeddings) > 0 {
					actualDimensions = len(embeddings[0])
				}
				return EvidenceSet{}, fmt.Errorf("%w：嵌入响应数量=%d 维度=%d 预期的=%d",
					ErrEmbeddingProfileMismatch, len(embeddings), actualDimensions, service.cfg.Embedding.Dimensions)
			default:
				var semanticErr error
				profile := service.cfg.Embedding
				profile.IndexVersion = service.cfg.SemanticIndexVersion
				semantic, semanticErr = service.repo.SearchSemantic(ctx, scope, embeddings[0], profile, service.cfg.CandidateLimit)
				if semanticErr != nil {
					semantic = nil
					set.Process = append(set.Process, ProcessRecord{Stage: StageRecall, Status: ProcessDegraded, Channel: ChannelSemantic, Reason: "semantic_recall_failed"})
				} else {
					semanticAvailable = true
					set.Process = append(set.Process, ProcessRecord{Stage: StageRecall, Status: ProcessSucceeded, Channel: ChannelSemantic, OutputCount: len(semantic)})
				}
			}
		}
	}

	if !lexicalAvailable && !semanticAvailable {
		return EvidenceSet{}, ErrRetrievalUnavailable
	}
	set.Evidence = service.fuse(scope, lexical, semantic, request.IncludeHistory, &set.Process)
	set.Evidence = service.applyRerank(ctx, request.Query, set.Evidence, request.Limit, &set.Process)
	if err := set.Validate(); err != nil {
		return EvidenceSet{}, fmt.Errorf("构建证据Set：%w", err)
	}
	return set, nil
}

type fusedCandidate struct {
	candidate    Candidate
	lexicalScore float64
	lexicalRank  int
	vectorScore  float64
	vectorRank   int
	fusedScore   float64
}

// fuse 执行该函数负责的核心处理逻辑。
func (service *Service) fuse(scope Scope, lexical, semantic []ScoredCandidate, historical bool, process *[]ProcessRecord) []Evidence {
	candidates := make(map[string]*fusedCandidate, len(lexical)+len(semantic))
	add := func(items []ScoredCandidate, channel RetrievalChannel) {
		for index, item := range items {
			if !validScore(item.Score) || blank(item.Candidate.SourceID) || blank(item.Candidate.NodeID) || blank(item.Candidate.Content) ||
				item.Candidate.CreatedAt.IsZero() || item.Candidate.ResourceID != scope.ResourceID || item.Candidate.VersionID != scope.VersionID {
				continue
			}
			key := item.Candidate.SourceID
			candidate := candidates[key]
			if candidate == nil {
				candidate = &fusedCandidate{candidate: item.Candidate}
				candidates[key] = candidate
			}
			if channel == ChannelLexical {
				candidate.lexicalScore, candidate.lexicalRank = item.Score, index+1
			} else {
				candidate.vectorScore, candidate.vectorRank = item.Score, index+1
			}
		}
	}
	add(lexical, ChannelLexical)
	add(semantic, ChannelSemantic)

	activeLexical := len(lexical) > 0
	activeSemantic := len(semantic) > 0
	for _, candidate := range candidates {
		candidate.fusedScore = service.fusedScore(candidate, activeLexical, activeSemantic)
	}
	ordered := make([]*fusedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.fusedScore >= service.cfg.MinimumFusedScore {
			ordered = append(ordered, candidate)
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].fusedScore == ordered[right].fusedScore {
			return ordered[left].candidate.SourceID < ordered[right].candidate.SourceID
		}
		return ordered[left].fusedScore > ordered[right].fusedScore
	})
	*process = append(*process,
		ProcessRecord{Stage: StageFilter, Status: ProcessSucceeded, InputCount: len(candidates), OutputCount: len(ordered), Reason: "minimum_fused_score"},
		ProcessRecord{Stage: StageFusion, Status: ProcessSucceeded, InputCount: len(candidates), OutputCount: len(ordered), Reason: string(service.cfg.FusionAlgorithm)},
	)
	evidence := make([]Evidence, 0, len(ordered))
	for index, candidate := range ordered {
		retrieval := make([]RetrievalRecord, 0, 2)
		if candidate.lexicalRank > 0 {
			retrieval = append(retrieval, RetrievalRecord{Channel: ChannelLexical, Rank: candidate.lexicalRank, Score: candidate.lexicalScore, IndexVersion: service.cfg.LexicalIndexVersion})
		}
		if candidate.vectorRank > 0 {
			retrieval = append(retrieval, RetrievalRecord{Channel: ChannelSemantic, Rank: candidate.vectorRank, Score: candidate.vectorScore, IndexVersion: service.cfg.SemanticIndexVersion})
		}
		scopeReason := "current_version"
		if historical {
			scopeReason = "explicit_historical_version"
		}
		evidence = append(evidence, Evidence{
			EvidenceID: evidenceID(candidate.candidate), ResourceID: candidate.candidate.ResourceID,
			VersionID: candidate.candidate.VersionID, NodeID: candidate.candidate.NodeID,
			SourceType: candidate.candidate.SourceType, Content: candidate.candidate.Content,
			ContentHash: digest(candidate.candidate.Content), LexicalScore: candidate.lexicalScore,
			VectorScore: candidate.vectorScore, FusedScore: candidate.fusedScore,
			TrustLevel: TrustUntrusted, CreatedAt: candidate.candidate.CreatedAt.UTC(),
			Provenance: EvidenceProvenance{
				Retrieval: retrieval,
				Filtering: []FilterRecord{{Stage: "workspace_resource_version_scope", Decision: FilterIncluded, Reason: scopeReason}, {Stage: "minimum_fused_score", Decision: FilterIncluded, Reason: "score_at_or_above_threshold"}},
				Fusion:    FusionRecord{Algorithm: service.cfg.FusionAlgorithm, ProfileVersion: service.cfg.ProfileVersion, PreRerankRank: index + 1, Threshold: service.cfg.MinimumFusedScore},
				Rerank:    RerankRecord{Enabled: service.cfg.RerankEnabled, Applied: false, ProfileVersion: service.cfg.RerankProfileVersion, Model: service.cfg.RerankModel, BeforeRank: index + 1, AfterRank: index + 1},
			},
		})
	}
	return evidence
}

// applyRerank 执行该函数负责的核心处理逻辑。
func (service *Service) applyRerank(ctx context.Context, query string, items []Evidence, limit int, process *[]ProcessRecord) []Evidence {
	if !service.cfg.RerankEnabled || len(items) == 0 {
		bounded := boundEvidence(items, limit)
		*process = append(*process, ProcessRecord{Stage: StageRerank, Status: ProcessSkipped, InputCount: len(items), OutputCount: len(bounded), Reason: "rerank_disabled_or_empty"})
		return bounded
	}
	documents := make([]string, 0, len(items))
	for _, item := range items {
		documents = append(documents, item.Content)
	}
	results, err := service.reranker.Rerank(ctx, query, documents, min(limit, len(items)))
	if err != nil {
		bounded := boundEvidence(items, limit)
		for index := range bounded {
			bounded[index].Provenance.Rerank.DegradedReason = "reranker_failed"
		}
		*process = append(*process,
			ProcessRecord{Stage: StageRerank, Status: ProcessDegraded, InputCount: len(items), OutputCount: len(bounded), Reason: "reranker_failed"},
			ProcessRecord{Stage: StageDegradation, Status: ProcessDegraded, InputCount: len(items), OutputCount: len(bounded), Reason: "fusion_order_retained"},
		)
		return bounded
	}
	ranked := make([]Evidence, 0, min(limit, len(results)))
	seen := make(map[int]struct{}, len(results))
	for _, result := range results {
		if result.Index < 0 || result.Index >= len(items) || !validScore(result.Score) {
			continue
		}
		if _, duplicate := seen[result.Index]; duplicate {
			continue
		}
		seen[result.Index] = struct{}{}
		item := items[result.Index]
		item.Provenance.Rerank.Applied = true
		item.Provenance.Rerank.Score = result.Score
		item.Provenance.Rerank.AfterRank = len(ranked) + 1
		ranked = append(ranked, item)
		if len(ranked) == limit {
			break
		}
	}
	if len(ranked) == 0 {
		bounded := boundEvidence(items, limit)
		for index := range bounded {
			bounded[index].Provenance.Rerank.DegradedReason = "invalid_reranker_response"
		}
		*process = append(*process, ProcessRecord{Stage: StageRerank, Status: ProcessDegraded, InputCount: len(items), OutputCount: len(bounded), Reason: "invalid_reranker_response"})
		return bounded
	}
	*process = append(*process, ProcessRecord{Stage: StageRerank, Status: ProcessSucceeded, InputCount: len(items), OutputCount: len(ranked), Reason: service.cfg.RerankProfileVersion})
	return ranked
}

// boundEvidence 执行该函数负责的核心处理逻辑。
func boundEvidence(items []Evidence, limit int) []Evidence {
	if len(items) <= limit {
		return items
	}
	return append([]Evidence(nil), items[:limit]...)
}

// fusedScore 执行该函数负责的核心处理逻辑。
func (service *Service) fusedScore(candidate *fusedCandidate, lexical, semantic bool) float64 {
	lexicalWeight, vectorWeight := service.cfg.LexicalWeight, service.cfg.VectorWeight
	if !lexical {
		lexicalWeight = 0
	}
	if !semantic {
		vectorWeight = 0
	}
	weight := lexicalWeight + vectorWeight
	if weight == 0 {
		return 0
	}
	if service.cfg.FusionAlgorithm == FusionRRF {
		score := 0.0
		if candidate.lexicalRank > 0 {
			score += lexicalWeight / (service.cfg.RRFConstant + float64(candidate.lexicalRank))
		}
		if candidate.vectorRank > 0 {
			score += vectorWeight / (service.cfg.RRFConstant + float64(candidate.vectorRank))
		}
		maximum := weight / (service.cfg.RRFConstant + 1)
		return clampScore(score / maximum)
	}
	return clampScore((candidate.lexicalScore*lexicalWeight + candidate.vectorScore*vectorWeight) / weight)
}

// clampScore 执行该函数负责的核心处理逻辑。
func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

// digest 执行该函数负责的核心处理逻辑。
func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(hash[:])
}

// evidenceID 执行该函数负责的核心处理逻辑。
func evidenceID(candidate Candidate) string {
	hash := sha256.Sum256([]byte(candidate.ResourceID + "\x00" + candidate.VersionID + "\x00" + candidate.NodeID + "\x00" + candidate.SourceID))
	return "ev_" + hex.EncodeToString(hash[:16])
}

// setID 执行该函数负责的核心处理逻辑。
func setID(scope Scope, queryHash, profile string) string {
	hash := sha256.Sum256([]byte(scope.WorkspaceID + "\x00" + scope.ResourceID + "\x00" + scope.VersionID + "\x00" + queryHash + "\x00" + profile))
	return "evset_" + hex.EncodeToString(hash[:16])
}
