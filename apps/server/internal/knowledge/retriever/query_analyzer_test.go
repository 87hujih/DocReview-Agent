package retriever

import "testing"

func TestAnalyzeQuery(t *testing.T) {
	analyzer := QueryAnalyzer{}

	tests := []struct {
		name          string
		query         string
		wantIntent    QueryIntent
		wantEntity    string
		wantSection   string
		wantAttribute string
		wantOrdinal   int
	}{
		{
			name:        "list projects",
			query:       "有哪些项目",
			wantIntent:  QueryIntentListSections,
			wantSection: "project",
		},
		{
			name:        "detail by ordinal",
			query:       "第一个项目做了什么",
			wantIntent:  QueryIntentDetailByOrdinal,
			wantSection: "project",
			wantOrdinal: 1,
		},
		{
			name:        "detail by entity",
			query:       "CampusHub 做了什么",
			wantIntent:  QueryIntentDetailByEntity,
			wantSection: "project",
			wantEntity:  "CampusHub",
		},
		{
			name:          "aggregate tech stack",
			query:         "用了哪些技术栈",
			wantIntent:    QueryIntentAggregateAttribute,
			wantSection:   "project",
			wantAttribute: "tech_stack",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := analyzer.Analyze(testCase.query)
			if got.Intent != testCase.wantIntent {
				t.Fatalf("expected intent %q, got %#v", testCase.wantIntent, got)
			}
			if got.SectionType != testCase.wantSection {
				t.Fatalf("expected section type %q, got %#v", testCase.wantSection, got)
			}
			if got.EntityName != testCase.wantEntity {
				t.Fatalf("expected entity %q, got %#v", testCase.wantEntity, got)
			}
			if got.AggregateField != testCase.wantAttribute {
				t.Fatalf("expected aggregate field %q, got %#v", testCase.wantAttribute, got)
			}
			if got.Ordinal != testCase.wantOrdinal {
				t.Fatalf("expected ordinal %d, got %#v", testCase.wantOrdinal, got)
			}
		})
	}
}
