package assistant

import (
	"errors"
	"testing"
)

// TestTargetResolverResolvesThirdProjectWithinProjectItemsOnly 验证“第三个项目”只会在 project_item 集合里编号。
func TestTargetResolverResolvesThirdProjectWithinProjectItemsOnly(t *testing.T) {
	resolver := NewTargetResolver()

	resolved, err := resolver.Resolve(ResolveTargetInput{
		Message:  "把第三个项目先输出一遍",
		Document: testCurrentDocumentWithProjects(),
	})
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected resolved node")
	}
	if resolved.Mode != ResolvedNodeModeNode {
		t.Fatalf("expected resolved mode %q, got %#v", ResolvedNodeModeNode, resolved)
	}
	if resolved.NodeID != "project-3" {
		t.Fatalf("expected third project node id %q, got %#v", "project-3", resolved)
	}
	if resolved.NodeKind != OutlineNodeProjectItem {
		t.Fatalf("expected project item node kind, got %#v", resolved)
	}
}

// TestTargetResolverResolvesThisProjectFromActiveNode 验证“这个项目”会承接 node-aware active node，而不是继续依赖 active section。
func TestTargetResolverResolvesThisProjectFromActiveNode(t *testing.T) {
	resolver := NewTargetResolver()

	resolved, err := resolver.Resolve(ResolveTargetInput{
		Message:  "这个项目的问题是什么",
		Document: testCurrentDocumentWithProjects(),
		Snapshot: &SessionContextSnapshot{
			ActiveNode: &SnapshotActiveNode{
				ID:   "project-2",
				Kind: string(OutlineNodeProjectItem),
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected resolved node")
	}
	if resolved.NodeID != "project-2" || resolved.Reason != "anaphora" {
		t.Fatalf("expected active node to resolve anaphora, got %#v", resolved)
	}
}

// TestTargetResolverReturnsListNodeForWhichProjectsQuestion 验证列举项目问题会返回列表聚合目标，而不是某个单节点。
func TestTargetResolverReturnsListNodeForWhichProjectsQuestion(t *testing.T) {
	resolver := NewTargetResolver()

	resolved, err := resolver.Resolve(ResolveTargetInput{
		Message:  "这份简历里有哪些项目",
		Document: testCurrentDocumentWithProjects(),
	})
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected resolved list target")
	}
	if resolved.Mode != ResolvedNodeModeList || resolved.NodeKind != OutlineNodeProjectItem {
		t.Fatalf("expected project list target, got %#v", resolved)
	}
}

// TestTargetResolverFailsWhenRequestedProjectOrdinalMissing 验证缺少第三个项目时 resolver 会显式失败，而不是偷退回其他节点。
func TestTargetResolverFailsWhenRequestedProjectOrdinalMissing(t *testing.T) {
	resolver := NewTargetResolver()
	document := testCurrentDocumentWithProjects()
	document.Outline = document.Outline[:4]

	resolved, err := resolver.Resolve(ResolveTargetInput{
		Message:  "把第三个项目先输出一遍",
		Document: document,
	})
	if !errors.Is(err, ErrTargetNodeNotFound) {
		t.Fatalf("expected ErrTargetNodeNotFound, got resolved=%#v err=%v", resolved, err)
	}
	if resolved != nil {
		t.Fatalf("expected nil resolved node when ordinal missing, got %#v", resolved)
	}
}

func testCurrentDocumentWithProjects() *CurrentDocument {
	return &CurrentDocument{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Title:      "产品经理简历",
		SourceType: "upload",
		FullText:   "项目经历全文",
		Ready:      true,
		Outline: []OutlineNode{
			{NodeID: "document:version-1", NodeKind: OutlineNodeDocument, Title: "全文", CanonicalContent: "项目经历全文"},
			{NodeID: "heading-projects", NodeKind: OutlineNodeHeadingSection, Title: "项目经历", CanonicalContent: "项目经历"},
			{NodeID: "project-1", NodeKind: OutlineNodeProjectItem, Title: "CampusHub", Ordinal: 1, ParentNodeID: "heading-projects", CanonicalContent: "CampusHub 正文", Aliases: []string{"CampusHub"}},
			{NodeID: "project-2", NodeKind: OutlineNodeProjectItem, Title: "选课助手", Ordinal: 2, ParentNodeID: "heading-projects", CanonicalContent: "选课助手 正文", Aliases: []string{"选课助手"}},
			{NodeID: "project-3", NodeKind: OutlineNodeProjectItem, Title: "慢跑计划", Ordinal: 3, ParentNodeID: "heading-projects", CanonicalContent: "慢跑计划 正文", Aliases: []string{"慢跑计划"}},
		},
	}
}
