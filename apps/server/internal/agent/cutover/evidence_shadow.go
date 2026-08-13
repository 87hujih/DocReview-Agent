package cutover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
)

type ShadowEvidenceSearcher interface {
	Search(context.Context, agentevidence.SearchRequest) (agentevidence.EvidenceSet, error)
}

// EvidenceShadowEvaluator exercises the new 类型化的 EvidenceSet contract 不包含
// creating 一个轮次、运行、工具调用、审批、补丁、或 commit. Its only 写入为
// the separate reconciliation fact recorded 由流水线之后评估.
type EvidenceShadowEvaluator struct {
	searcher ShadowEvidenceSearcher
	limit    int
}

// NewEvidenceShadowEvaluator 校验依赖并创建对应实例。
func NewEvidenceShadowEvaluator(searcher ShadowEvidenceSearcher, limit int) (*EvidenceShadowEvaluator, error) {
	if searcher == nil || limit < 1 || limit > 50 {
		return nil, fmt.Errorf("影子证据Set 检索器和有界的上限不能为空")
	}
	return &EvidenceShadowEvaluator{searcher: searcher, limit: limit}, nil
}

// 评估执行该函数负责的核心处理逻辑。
func (evaluator *EvidenceShadowEvaluator) Evaluate(ctx context.Context, request ShadowRequest) (Result, error) {
	if request.AllowWrites {
		return Result{}, fmt.Errorf("影子评估仅允许读取")
	}
	input := request.Request
	if evaluator == nil || evaluator.searcher == nil || !trustedFor(input.Scope, strings.TrimSpace(input.WorkspaceID)) ||
		strings.TrimSpace(input.ResourceID) == "" || strings.TrimSpace(input.Message) == "" {
		return Result{}, fmt.Errorf("可信的影子证据Set 作用域不能为空")
	}
	set, err := evaluator.searcher.Search(ctx, agentevidence.SearchRequest{
		WorkspaceID: input.WorkspaceID, ResourceID: input.ResourceID,
		Query: input.Message, Limit: evaluator.limit,
	})
	if err != nil {
		return Result{}, err
	}
	if err := set.Validate(); err != nil {
		return Result{}, fmt.Errorf("影子证据Set 无效：%w", err)
	}
	if set.WorkspaceID != input.WorkspaceID || set.ResourceID != input.ResourceID || set.Query != input.Message {
		return Result{}, fmt.Errorf("影子证据Set 作用域不匹配")
	}
	payload, _ := json.Marshal(map[string]any{"evidence_set": set})
	dto, _ := json.Marshal(map[string]any{"typed_shadow": map[string]any{
		"schema_version": set.SchemaVersion, "evidence_set": set,
	}})
	return Result{Mode: ModeShadow, DTO: dto, Events: []Event{{
		Sequence: 1, Type: "typed.shadow.evidence_retrieved", Payload: payload,
	}}}, nil
}

var _ ShadowEvaluator = (*EvidenceShadowEvaluator)(nil)
