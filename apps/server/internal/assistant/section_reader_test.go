package assistant

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

func TestSectionReaderReadsFullSectionContent(t *testing.T) {
	reader := NewSectionReader(&fakeActiveFileResourceReader{
		sectionByID: map[string]*postgres.ResourceSection{
			"section-1": {ID: "section-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "完整正文"},
		},
	})

	got, err := reader.ReadSectionFull(context.Background(), "section-1")
	if err != nil {
		t.Fatalf("read section full: %v", err)
	}
	if got == nil || got.Content != "完整正文" || got.IsExcerpt {
		t.Fatalf("expected full section content, got %#v", got)
	}
}

func TestSectionReaderReturnsContinuousExcerptForLongSection(t *testing.T) {
	longContent := strings.Repeat("第一段连续内容。", 40)
	reader := NewSectionReader(&fakeActiveFileResourceReader{
		sectionByID: map[string]*postgres.ResourceSection{
			"section-1": {ID: "section-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: longContent},
		},
	})

	got, err := reader.ReadSectionExcerpt(context.Background(), "section-1", ExcerptPolicy{MaxRunes: 24})
	if err != nil {
		t.Fatalf("read section excerpt: %v", err)
	}
	if got == nil {
		t.Fatal("expected excerpt result, got nil")
	}
	if !got.IsExcerpt || !got.HasMore {
		t.Fatalf("expected long content to return excerpt with remaining flag, got %#v", got)
	}

	want := []rune(longContent)[:24]
	if got.Content != string(want) {
		t.Fatalf("expected continuous leading excerpt %q, got %q", string(want), got.Content)
	}
}
