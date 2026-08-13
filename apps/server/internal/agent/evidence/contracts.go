// Package 证据 defines the versioned、auditable 检索 contract used 由
// the 类型化的 Agent 运行时. 证据内容为 data、never 控制层输入.
package evidence

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = "1.0"

type TrustLevel string

const TrustUntrusted TrustLevel = "untrusted"

type RetrievalChannel string

const (
	ChannelLexical  RetrievalChannel = "lexical"
	ChannelSemantic RetrievalChannel = "semantic"
)

type FilterDecision string

const (
	FilterIncluded FilterDecision = "included"
	FilterExcluded FilterDecision = "excluded"
)

type FusionAlgorithm string

const (
	FusionWeightedSum FusionAlgorithm = "weighted_sum"
	FusionRRF         FusionAlgorithm = "reciprocal_rank_fusion"
)

type ProcessStage string

const (
	StageRecall      ProcessStage = "recall"
	StageFilter      ProcessStage = "filter"
	StageFusion      ProcessStage = "fusion"
	StageRerank      ProcessStage = "rerank"
	StageDegradation ProcessStage = "degradation"
)

type ProcessStatus string

const (
	ProcessSucceeded ProcessStatus = "succeeded"
	ProcessDegraded  ProcessStatus = "degraded"
	ProcessSkipped   ProcessStatus = "skipped"
)

type RetrievalRecord struct {
	Channel      RetrievalChannel `json:"channel"`
	Rank         int              `json:"rank"`
	Score        float64          `json:"score"`
	IndexVersion string           `json:"index_version"`
}

type FilterRecord struct {
	Stage    string         `json:"stage"`
	Decision FilterDecision `json:"decision"`
	Reason   string         `json:"reason"`
}

type FusionRecord struct {
	Algorithm      FusionAlgorithm `json:"algorithm"`
	ProfileVersion string          `json:"profile_version"`
	PreRerankRank  int             `json:"pre_rerank_rank"`
	Threshold      float64         `json:"threshold"`
}

type RerankRecord struct {
	Enabled        bool    `json:"enabled"`
	Applied        bool    `json:"applied"`
	ProfileVersion string  `json:"profile_version"`
	Model          string  `json:"model,omitempty"`
	BeforeRank     int     `json:"before_rank"`
	AfterRank      int     `json:"after_rank"`
	Score          float64 `json:"score"`
	DegradedReason string  `json:"degraded_reason,omitempty"`
}

type EvidenceProvenance struct {
	Retrieval []RetrievalRecord `json:"retrieval"`
	Filtering []FilterRecord    `json:"filtering"`
	Fusion    FusionRecord      `json:"fusion"`
	Rerank    RerankRecord      `json:"rerank"`
}

type Evidence struct {
	EvidenceID   string             `json:"evidence_id"`
	ResourceID   string             `json:"resource_id"`
	VersionID    string             `json:"version_id"`
	NodeID       string             `json:"node_id"`
	SourceType   string             `json:"source_type"`
	Content      string             `json:"content"`
	ContentHash  string             `json:"content_hash"`
	LexicalScore float64            `json:"lexical_score"`
	VectorScore  float64            `json:"vector_score"`
	FusedScore   float64            `json:"fused_score"`
	TrustLevel   TrustLevel         `json:"trust_level"`
	CreatedAt    time.Time          `json:"created_at"`
	Provenance   EvidenceProvenance `json:"provenance"`
}

type ProcessRecord struct {
	Stage       ProcessStage     `json:"stage"`
	Status      ProcessStatus    `json:"status"`
	Channel     RetrievalChannel `json:"channel,omitempty"`
	InputCount  int              `json:"input_count"`
	OutputCount int              `json:"output_count"`
	Reason      string           `json:"reason,omitempty"`
}

type EvidenceSet struct {
	SchemaVersion  string          `json:"schema_version"`
	SetID          string          `json:"set_id"`
	WorkspaceID    string          `json:"workspace_id"`
	ResourceID     string          `json:"resource_id"`
	VersionID      string          `json:"version_id"`
	Query          string          `json:"query"`
	QueryHash      string          `json:"query_hash"`
	ProfileVersion string          `json:"profile_version"`
	CreatedAt      time.Time       `json:"created_at"`
	Evidence       []Evidence      `json:"evidence"`
	Process        []ProcessRecord `json:"process"`
}

var sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate 校验输入及领域约束。
func (set EvidenceSet) Validate() error {
	if set.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持的证据Set schema_version %q", set.SchemaVersion)
	}
	if blank(set.SetID) || blank(set.WorkspaceID) || blank(set.ResourceID) || blank(set.VersionID) ||
		blank(set.Query) || blank(set.ProfileVersion) || !sha256Pattern.MatchString(set.QueryHash) || set.CreatedAt.IsZero() {
		return fmt.Errorf("证据Set 标识、作用域、查询、配置档、哈希、和 created_at 不能为空")
	}
	if len(set.Process) == 0 {
		return fmt.Errorf("证据Set 处理流程溯源信息不能为空")
	}
	for index, record := range set.Process {
		if !record.Stage.valid() || !record.Status.valid() || record.InputCount < 0 || record.OutputCount < 0 ||
			(record.Channel != "" && !record.Channel.valid()) {
			return fmt.Errorf("证据Set 处理流程[%d] 无效", index)
		}
	}
	seen := make(map[string]struct{}, len(set.Evidence))
	for index, item := range set.Evidence {
		if err := item.validate(set); err != nil {
			return fmt.Errorf("证据[%d]：%w", index, err)
		}
		if _, duplicate := seen[item.EvidenceID]; duplicate {
			return fmt.Errorf("证据[%d]：重复的 evidence_id %q", index, item.EvidenceID)
		}
		seen[item.EvidenceID] = struct{}{}
	}
	return nil
}

// validate 校验输入及领域约束。
func (item Evidence) validate(set EvidenceSet) error {
	if blank(item.EvidenceID) || blank(item.ResourceID) || blank(item.VersionID) || blank(item.NodeID) ||
		blank(item.SourceType) || blank(item.Content) || !sha256Pattern.MatchString(item.ContentHash) || item.CreatedAt.IsZero() {
		return fmt.Errorf("标识、来源、内容、哈希、和 created_at 不能为空")
	}
	if item.ResourceID != set.ResourceID || item.VersionID != set.VersionID {
		return fmt.Errorf("资源/版本作用域不匹配证据Set")
	}
	if item.TrustLevel != TrustUntrusted {
		return fmt.Errorf("retrieved 证据必须标记为不可信")
	}
	if !validScore(item.LexicalScore) || !validScore(item.VectorScore) || !validScore(item.FusedScore) {
		return fmt.Errorf("分数必须是有限数值介于零和一个")
	}
	if len(item.Provenance.Retrieval) == 0 || len(item.Provenance.Filtering) == 0 {
		return fmt.Errorf("检索和过滤溯源信息不能为空")
	}
	for _, record := range item.Provenance.Retrieval {
		if !record.Channel.valid() || record.Rank <= 0 || !validScore(record.Score) || blank(record.IndexVersion) {
			return fmt.Errorf("检索溯源信息无效")
		}
	}
	for _, record := range item.Provenance.Filtering {
		if blank(record.Stage) || !record.Decision.valid() || blank(record.Reason) {
			return fmt.Errorf("过滤溯源信息无效")
		}
	}
	if !item.Provenance.Fusion.Algorithm.valid() || blank(item.Provenance.Fusion.ProfileVersion) ||
		item.Provenance.Fusion.PreRerankRank <= 0 || !validScore(item.Provenance.Fusion.Threshold) {
		return fmt.Errorf("融合溯源信息无效")
	}
	rerank := item.Provenance.Rerank
	if blank(rerank.ProfileVersion) || rerank.BeforeRank <= 0 || rerank.AfterRank <= 0 || !validScore(rerank.Score) {
		return fmt.Errorf("重排溯源信息无效")
	}
	if rerank.Applied && (!rerank.Enabled || blank(rerank.Model)) {
		return fmt.Errorf("已应用的重排溯源信息必须启用模型")
	}
	return nil
}

// validScore 执行该函数负责的核心处理逻辑。
func validScore(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
}

// 空白执行该函数负责的核心处理逻辑。
func blank(value string) bool { return strings.TrimSpace(value) == "" }

// 有效的执行该函数负责的核心处理逻辑。
func (channel RetrievalChannel) valid() bool {
	return channel == ChannelLexical || channel == ChannelSemantic
}

// 有效的执行该函数负责的核心处理逻辑。
func (decision FilterDecision) valid() bool {
	return decision == FilterIncluded || decision == FilterExcluded
}

// 有效的执行该函数负责的核心处理逻辑。
func (algorithm FusionAlgorithm) valid() bool {
	return algorithm == FusionWeightedSum || algorithm == FusionRRF
}

// 有效的执行该函数负责的核心处理逻辑。
func (stage ProcessStage) valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch stage {
	case StageRecall, StageFilter, StageFusion, StageRerank, StageDegradation:
		return true
	default:
		return false
	}
}

// 有效的执行该函数负责的核心处理逻辑。
func (status ProcessStatus) valid() bool {
	return status == ProcessSucceeded || status == ProcessDegraded || status == ProcessSkipped
}
