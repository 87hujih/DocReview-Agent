package assistant

import "testing"

// TestNodeReaderReturnsCanonicalContentForResolvedProjectNode 验证 node reader 会返回 resolved project node 的 canonical 内容。
func TestNodeReaderReturnsCanonicalContentForResolvedProjectNode(t *testing.T) {
	reader := NewNodeReader()

	result, err := reader.Read(NodeReadInput{
		Document: testCurrentDocumentWithProjects(),
		Resolved: &ResolvedNode{
			Mode:     ResolvedNodeModeNode,
			NodeID:   "project-3",
			NodeKind: OutlineNodeProjectItem,
			Title:    "慢跑计划",
		},
	})
	if err != nil {
		t.Fatalf("read node: %v", err)
	}
	if result == nil {
		t.Fatal("expected node read result")
	}
	if result.Content != "慢跑计划 正文" {
		t.Fatalf("expected canonical node content, got %#v", result)
	}
	if result.NodeID != "project-3" || result.NodeKind != OutlineNodeProjectItem {
		t.Fatalf("expected resolved node metadata to be preserved, got %#v", result)
	}
}
