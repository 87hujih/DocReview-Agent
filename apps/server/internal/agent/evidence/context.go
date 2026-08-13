package evidence

import (
	"fmt"

	agentcontext "agent_project/apps/server/internal/agent/context"
)

// 处理失败： ContextItems maps 一个 already-authorized EvidenceSet into candidates 用于 the
// 数据库-free ContextAssembler. Selection remains the 组装器's concern.
func ContextItems(set EvidenceSet) ([]agentcontext.Item, error) {
	if err := set.Validate(); err != nil {
		return nil, fmt.Errorf("无效的证据Set 上下文来源：%w", err)
	}
	items := make([]agentcontext.Item, 0, len(set.Evidence))
	for _, evidence := range set.Evidence {
		items = append(items, agentcontext.Item{
			Layer: agentcontext.LayerEvidence, ItemType: evidence.SourceType,
			SourceID: evidence.EvidenceID, ResourceID: evidence.ResourceID,
			VersionID: evidence.VersionID, NodeID: evidence.NodeID,
			TrustLevel: agentcontext.TrustUntrusted, RelevanceScore: evidence.FusedScore,
			Content: evidence.Content, ContentHash: evidence.ContentHash,
			SelectedReason: "highest fused relevance within evidence budget",
			CreatedAt:      evidence.CreatedAt,
		})
	}
	return items, nil
}
