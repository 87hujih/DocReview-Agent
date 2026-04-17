package assistant

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/knowledge/citation"
)

func TestEvidenceGateRejectsHeadingOnlyCitations(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality([]citation.Citation{
		{SectionTitle: "全文", Snippet: "项目"},
		{SectionTitle: "全文", Snippet: "项目描述："},
	})
	if ok {
		t.Fatalf("expected low quality evidence, got ok")
	}
	if strings.TrimSpace(reason) == "" {
		t.Fatal("expected rejection reason")
	}
}

func TestEvidenceGateAcceptsProjectWindowCitations(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality([]citation.Citation{
		{
			SectionID:    "project-1",
			SectionType:  "project",
			SectionTitle: "智能排班系统",
			Snippet:      "智能排班系统",
			Window: []string{
				"项目名称：智能排班系统",
				"项目描述：负责多门店排班、班次冲突校验与成本优化。",
				"技术栈：Go、React、PostgreSQL",
			},
		},
	})
	if !ok {
		t.Fatalf("expected project evidence to be accepted, got reason=%q", reason)
	}
	if reason != "" {
		t.Fatalf("expected empty reason for accepted evidence, got %q", reason)
	}
}

func TestEvidenceGateRequiresFallbackWhenAdjacentWindowMissing(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality([]citation.Citation{
		{
			SectionID:    "project-1",
			SectionType:  "project",
			SectionTitle: "智能排班系统",
			Snippet:      "智能排班系统",
		},
	})
	if ok {
		t.Fatalf("expected missing window evidence to require fallback")
	}
	if strings.TrimSpace(reason) == "" {
		t.Fatal("expected fallback reason")
	}
}
