package agentrun

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	agentcontext "agent_project/apps/server/internal/agent/context"
)

var _ agentcontext.Store = (*Repository)(nil)
var _ agentcontext.Reader = (*Repository)(nil)

// 保存 implements 上下文.存储 和 persists the exact ordered 项目 set
// selected 由 ContextAssembler. The storage layer does not re-rank 或 mutate
// 模型-facing 上下文.
func (r *Repository) Save(ctx context.Context, manifest agentcontext.Manifest) (string, error) {
	itemsJSON, err := json.Marshal(manifest.Items)
	if err != nil {
		return "", fmt.Errorf("编码上下文清单 items：%w", err)
	}
	record, err := r.CreateContextManifest(ctx, CreateContextManifestParams{
		RunID:                manifest.RunID,
		StepID:               manifest.StepID,
		TokenBudget:          int64(manifest.TokenBudget),
		ReservedOutputTokens: int64(manifest.ReservedOutputTokens),
		Tokenizer:            manifest.Tokenizer,
		ItemsJSON:            itemsJSON,
		TotalTokens:          int64(manifest.TotalTokens),
		ContentHash:          manifest.ContentHash,
	})
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

// 加载 按作用域读取并返回所需数据。
func (r *Repository) Load(ctx context.Context, manifestID string) (agentcontext.Manifest, error) {
	record, err := r.GetContextManifest(ctx, manifestID)
	if err != nil {
		return agentcontext.Manifest{}, err
	}
	if record == nil {
		return agentcontext.Manifest{}, fmt.Errorf("上下文清单未找到")
	}
	var items []agentcontext.Item
	if err := json.Unmarshal(record.ItemsJSON, &items); err != nil {
		return agentcontext.Manifest{}, fmt.Errorf("解析上下文清单 items：%w", err)
	}
	canonicalItems, err := json.Marshal(items)
	if err != nil {
		return agentcontext.Manifest{}, fmt.Errorf("规范化上下文清单 items：%w", err)
	}
	digest := sha256.Sum256(canonicalItems)
	wantHash := fmt.Sprintf("sha256:%x", digest[:])
	if !strings.EqualFold(record.ContentHash, wantHash) {
		return agentcontext.Manifest{}, fmt.Errorf("上下文清单内容哈希不匹配")
	}
	return agentcontext.Manifest{
		ID: record.ID, RunID: record.RunID, StepID: record.StepID,
		TokenBudget: int(record.TokenBudget), ReservedOutputTokens: int(record.ReservedOutputTokens),
		Tokenizer: record.Tokenizer, Items: items, TotalTokens: int(record.TotalTokens),
		ContentHash: record.ContentHash, CreatedAt: record.CreatedAt,
	}, nil
}
