package assistant

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

func TestDeterministicReadResponderFormatsProjectList(t *testing.T) {
	responder := DeterministicReadResponder{}

	reply := responder.Reply(ReadIntent{Kind: ReadIntentListSections}, DeterministicReadInput{
		SectionType: "project",
		Sections: []postgres.ResourceSection{
			{SectionOrder: 1, Title: "CampusHub"},
			{SectionOrder: 2, Title: "选课助手"},
		},
	})
	if reply == nil {
		t.Fatal("expected deterministic list reply, got nil")
	}
	if !strings.Contains(reply.Reply, "1. CampusHub") || !strings.Contains(reply.Reply, "2. 选课助手") {
		t.Fatalf("expected ordered project list reply, got %#v", reply)
	}
}

func TestDeterministicReadResponderFormatsExcerptWithContinuationNote(t *testing.T) {
	responder := DeterministicReadResponder{}

	reply := responder.Reply(ReadIntent{Kind: ReadIntentExcerptSection}, DeterministicReadInput{
		LocatedSection: &LocatedSection{SectionID: "section-3", Title: "第三个项目"},
		SectionRead:    &SectionReadResult{SectionID: "section-3", Title: "第三个项目", Content: "第三个项目正文", IsExcerpt: true, HasMore: true},
	})
	if reply == nil {
		t.Fatal("expected deterministic excerpt reply, got nil")
	}
	if !strings.Contains(reply.Reply, "第三个项目正文") {
		t.Fatalf("expected excerpt reply to contain section text, got %#v", reply)
	}
	if !strings.Contains(reply.Reply, "可继续输出剩余部分") {
		t.Fatalf("expected excerpt reply to mention continuation, got %#v", reply)
	}
}
