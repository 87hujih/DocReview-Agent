package evidence

import "strings"

// RecallAtK measures unique relevant canonical 节点标识列表 present 位于 the first k
// 证据 records. Relevant labels 为 independent 评估 truth.
func RecallAtK(set EvidenceSet, relevantNodeIDs []string, k int) float64 {
	if k <= 0 || len(relevantNodeIDs) == 0 {
		return 0
	}
	relevant := make(map[string]struct{}, len(relevantNodeIDs))
	for _, nodeID := range relevantNodeIDs {
		if nodeID = strings.TrimSpace(nodeID); nodeID != "" {
			relevant[nodeID] = struct{}{}
		}
	}
	if len(relevant) == 0 {
		return 0
	}
	hits := make(map[string]struct{}, len(relevant))
	for index, item := range set.Evidence {
		if index == k {
			break
		}
		if _, ok := relevant[item.NodeID]; ok {
			hits[item.NodeID] = struct{}{}
		}
	}
	return float64(len(hits)) / float64(len(relevant))
}
