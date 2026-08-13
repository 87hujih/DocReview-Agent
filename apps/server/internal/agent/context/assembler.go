// Package 上下文 assembles the only 模型-facing 上下文 representation used
// 由类型化的 Agent nodes. It 没有数据库查询 capability：callers provide
// 溯源信息-rich candidates 和 the 组装器 selects 和 records what the
// 模型 actually sees.
package context

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrRequiredContextBudget = errors.New("必需上下文超过令牌预算")

type Layer string

const (
	LayerControl           Layer = "control"
	LayerTask              Layer = "task"
	LayerWorkingMemory     Layer = "working_memory"
	LayerEvidence          Layer = "evidence"
	LayerConversation      Layer = "conversation_memory"
	LayerArtifactReference Layer = "artifact_reference"
)

type TrustLevel string

const (
	TrustSystem    TrustLevel = "system"
	TrustTrusted   TrustLevel = "trusted"
	TrustUntrusted TrustLevel = "untrusted"
)

type Tokenizer interface {
	Name() string
	Count(text string) int
}

type Item struct {
	Layer          Layer      `json:"layer"`
	ItemType       string     `json:"item_type"`
	SourceID       string     `json:"source_id,omitempty"`
	ResourceID     string     `json:"resource_id,omitempty"`
	VersionID      string     `json:"version_id,omitempty"`
	NodeID         string     `json:"node_id,omitempty"`
	TrustLevel     TrustLevel `json:"trust_level"`
	RelevanceScore float64    `json:"relevance_score"`
	TokenCount     int        `json:"token_count"`
	ContentHash    string     `json:"content_hash"`
	SelectedReason string     `json:"selected_reason"`
	Truncated      bool       `json:"truncated"`
	Content        string     `json:"content,omitempty"`
	Reference      string     `json:"reference,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type Request struct {
	RunID  string
	StepID string
	Items  []Item
}

type Manifest struct {
	ID                   string    `json:"id,omitempty"`
	RunID                string    `json:"run_id"`
	StepID               string    `json:"step_id"`
	TokenBudget          int       `json:"token_budget"`
	ReservedOutputTokens int       `json:"reserved_output_tokens"`
	Tokenizer            string    `json:"tokenizer"`
	Items                []Item    `json:"items"`
	TotalTokens          int       `json:"total_tokens"`
	ContentHash          string    `json:"content_hash"`
	CreatedAt            time.Time `json:"created_at"`
}

type Result struct {
	Manifest Manifest
}

type Store interface {
	Save(ctx context.Context, manifest Manifest) (string, error)
}

type Reader interface {
	Load(ctx context.Context, manifestID string) (Manifest, error)
}

type Config struct {
	Tokenizer            Tokenizer
	TokenBudget          int
	ReservedOutputTokens int
	LayerBudgets         map[Layer]int
}

type Assembler struct {
	cfg   Config
	store Store
}

// NewAssembler 校验依赖并创建对应实例。
func NewAssembler(cfg Config, store Store) (*Assembler, error) {
	if cfg.Tokenizer == nil || strings.TrimSpace(cfg.Tokenizer.Name()) == "" {
		return nil, fmt.Errorf("上下文分词器不能为空")
	}
	if cfg.TokenBudget <= 0 || cfg.ReservedOutputTokens < 0 || cfg.ReservedOutputTokens >= cfg.TokenBudget {
		return nil, fmt.Errorf("无效的上下文令牌预算")
	}
	for layer, budget := range cfg.LayerBudgets {
		if !layer.Valid() || budget < 0 {
			return nil, fmt.Errorf("无效的预算用于上下文层 %q", layer)
		}
	}
	return &Assembler{cfg: cfg, store: store}, nil
}

// Assemble 执行该函数负责的核心处理逻辑。
func (a *Assembler) Assemble(ctx context.Context, request Request) (Result, error) {
	if a.store != nil && (strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.StepID) == "") {
		return Result{}, fmt.Errorf("run_id 和 step_id 不能为空用于持久化上下文清单")
	}

	now := time.Now().UTC()
	grouped := make(map[Layer][]Item, len(layerOrder))
	for _, candidate := range request.Items {
		item, err := a.prepareItem(candidate, now)
		if err != nil {
			return Result{}, err
		}
		grouped[item.Layer] = append(grouped[item.Layer], item)
	}

	available := a.cfg.TokenBudget - a.cfg.ReservedOutputTokens
	selected := make([]Item, 0, len(request.Items))
	total := 0
	for _, layer := range layerOrder {
		items := grouped[layer]
		if layer == LayerEvidence || layer == LayerConversation {
			sort.SliceStable(items, func(i, j int) bool {
				if items[i].RelevanceScore == items[j].RelevanceScore {
					return items[i].SourceID < items[j].SourceID
				}
				return items[i].RelevanceScore > items[j].RelevanceScore
			})
		}
		layerRemaining := available - total
		if configured, ok := a.cfg.LayerBudgets[layer]; ok && configured < layerRemaining {
			layerRemaining = configured
		}
		for _, item := range items {
			if item.TokenCount > layerRemaining || item.TokenCount > available-total {
				if layer == LayerControl || layer == LayerTask {
					return Result{}, fmt.Errorf("%w：%s 项目 %q", ErrRequiredContextBudget, layer, item.ItemType)
				}
				continue
			}
			selected = append(selected, item)
			total += item.TokenCount
			layerRemaining -= item.TokenCount
		}
	}

	itemsJSON, err := json.Marshal(selected)
	if err != nil {
		return Result{}, fmt.Errorf("编码上下文清单 items：%w", err)
	}
	digest := sha256.Sum256(itemsJSON)
	manifest := Manifest{
		RunID: request.RunID, StepID: request.StepID,
		TokenBudget: a.cfg.TokenBudget, ReservedOutputTokens: a.cfg.ReservedOutputTokens,
		Tokenizer: a.cfg.Tokenizer.Name(), Items: selected, TotalTokens: total,
		ContentHash: "sha256:" + hex.EncodeToString(digest[:]), CreatedAt: now,
	}
	if a.store != nil {
		manifest.ID, err = a.store.Save(ctx, manifest)
		if err != nil {
			return Result{}, fmt.Errorf("持久化上下文清单：%w", err)
		}
	}
	return Result{Manifest: manifest}, nil
}

// prepareItem 执行该函数负责的核心处理逻辑。
func (a *Assembler) prepareItem(item Item, now time.Time) (Item, error) {
	item.ItemType = strings.TrimSpace(item.ItemType)
	item.SourceID = strings.TrimSpace(item.SourceID)
	item.Reference = strings.TrimSpace(item.Reference)
	if !item.Layer.Valid() || item.ItemType == "" || !item.TrustLevel.Valid() {
		return Item{}, fmt.Errorf("上下文项目层、项目_type、和 trust_level 不能为空")
	}
	if item.Layer == LayerControl && item.TrustLevel != TrustSystem {
		return Item{}, fmt.Errorf("控制层上下文需要系统信任级别")
	}
	if (item.Layer == LayerEvidence || item.Layer == LayerConversation || item.Layer == LayerArtifactReference) && item.TrustLevel == TrustSystem {
		return Item{}, fmt.Errorf("%s 上下文不能声明系统信任级别", item.Layer)
	}
	if item.RelevanceScore < 0 || item.RelevanceScore > 1 {
		return Item{}, fmt.Errorf("上下文 relevance_score 必须介于零和一之间")
	}
	countedText := item.Content
	if item.Layer == LayerArtifactReference {
		if item.Reference == "" {
			return Item{}, fmt.Errorf("制品上下文必须提供引用")
		}
		item.Content = ""
		countedText = item.Reference
		if item.SelectedReason == "" {
			item.SelectedReason = "large object retained as artifact reference"
		}
	} else if strings.TrimSpace(item.Content) == "" {
		return Item{}, fmt.Errorf("内联上下文项目内容不能为空")
	}
	item.TokenCount = a.cfg.Tokenizer.Count(countedText)
	if item.TokenCount < 0 {
		return Item{}, fmt.Errorf("分词器返回了负数令牌计数")
	}
	contentDigest := sha256.Sum256([]byte(countedText))
	item.ContentHash = "sha256:" + hex.EncodeToString(contentDigest[:])
	if item.SelectedReason == "" {
		item.SelectedReason = "selected within layer budget"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	return item, nil
}

var layerOrder = []Layer{
	LayerControl,
	LayerTask,
	LayerWorkingMemory,
	LayerEvidence,
	LayerConversation,
	LayerArtifactReference,
}

// 有效的执行该函数负责的核心处理逻辑。
func (layer Layer) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch layer {
	case LayerControl, LayerTask, LayerWorkingMemory, LayerEvidence, LayerConversation, LayerArtifactReference:
		return true
	default:
		return false
	}
}

// 有效的执行该函数负责的核心处理逻辑。
func (trust TrustLevel) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch trust {
	case TrustSystem, TrustTrusted, TrustUntrusted:
		return true
	default:
		return false
	}
}
