package assistant

import (
	"encoding/json"
	"reflect"
	"testing"

	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/storage/postgres"
)

// TestDocumentOutlineBuilderPromotesProjectHeadingsWithoutDate 验证无日期 markdown 标题也会被提升为稳定 project_item。
func TestDocumentOutlineBuilderPromotesProjectHeadingsWithoutDate(t *testing.T) {
	builder := NewDocumentOutlineBuilder()
	documentJSON := mustMarshalOutlineDocumentJSON(t, documentparser.ParsedDocument{
		SourceFormat: "markdown",
		Blocks: []documentparser.Block{
			{Type: documentparser.BlockHeading, Level: 2, Text: "项目经历"},
			{Type: documentparser.BlockHeading, Level: 3, Text: "1. CampusHub"},
			{Type: documentparser.BlockParagraph, Text: "负责校园活动报名与签到。"},
			{Type: documentparser.BlockHeading, Level: 3, Text: "2. 选课助手"},
			{Type: documentparser.BlockParagraph, Text: "负责排课推荐与冲突检测。"},
			{Type: documentparser.BlockHeading, Level: 3, Text: "3. 慢跑计划"},
			{Type: documentparser.BlockParagraph, Text: "负责训练记录与路线分析。"},
		},
	})

	outline := builder.Build(BuildDocumentOutlineInput{
		VersionID:     "version-markdown",
		FullText:      "项目经历全文",
		StructureJSON: documentJSON,
	})

	projects := filterOutlineNodesByKind(outline, OutlineNodeProjectItem)
	if len(projects) != 3 {
		t.Fatalf("expected 3 project nodes, got %#v", projects)
	}

	titles := []string{projects[0].Title, projects[1].Title, projects[2].Title}
	expectedTitles := []string{"CampusHub", "选课助手", "慢跑计划"}
	if !reflect.DeepEqual(expectedTitles, titles) {
		t.Fatalf("expected project titles %#v, got %#v", expectedTitles, titles)
	}

	for index, project := range projects {
		if project.Ordinal != index+1 {
			t.Fatalf("expected project ordinal %d, got %#v", index+1, project)
		}
		if project.Source != OutlineSourceHeadingStructure {
			t.Fatalf("expected heading-derived project source, got %#v", project)
		}
	}
}

// TestDocumentOutlineBuilderMergesSemanticProjectSectionsAndHeadingStructure 验证语义项目 section 与 heading 结构会合并为同一稳定节点。
func TestDocumentOutlineBuilderMergesSemanticProjectSectionsAndHeadingStructure(t *testing.T) {
	builder := NewDocumentOutlineBuilder()
	documentJSON := mustMarshalOutlineDocumentJSON(t, documentparser.ParsedDocument{
		SourceFormat: "markdown",
		Blocks: []documentparser.Block{
			{Type: documentparser.BlockHeading, Level: 2, Text: "项目经历"},
			{Type: documentparser.BlockHeading, Level: 3, Text: "1. CampusHub"},
			{Type: documentparser.BlockParagraph, Text: "负责校园活动报名与签到。"},
			{Type: documentparser.BlockHeading, Level: 3, Text: "2. 选课助手"},
			{Type: documentparser.BlockParagraph, Text: "负责排课推荐与冲突检测。"},
		},
	})

	outline := builder.Build(BuildDocumentOutlineInput{
		VersionID: "version-semantic",
		FullText:  "项目经历全文",
		Sections: []postgres.ResourceSection{
			{
				ID:                  "section-campushub",
				VersionID:           "version-semantic",
				SectionType:         "project",
				SectionOrder:        1,
				Title:               "CampusHub",
				CanonicalEntityName: stringPointer("CampusHub"),
				AliasesJSON:         mustMarshalOutlineJSON(t, []string{"CampusHub 校园活动平台"}),
				Content:             "CampusHub semantic content",
			},
			{
				ID:                  "section-course",
				VersionID:           "version-semantic",
				SectionType:         "project",
				SectionOrder:        2,
				Title:               "选课助手",
				CanonicalEntityName: stringPointer("选课助手"),
				Content:             "选课助手 semantic content",
			},
		},
		StructureJSON: documentJSON,
	})

	projects := filterOutlineNodesByKind(outline, OutlineNodeProjectItem)
	if len(projects) != 2 {
		t.Fatalf("expected 2 merged project nodes, got %#v", projects)
	}

	expectedNodeIDs := []string{"section:section-campushub", "section:section-course"}
	actualNodeIDs := []string{projects[0].NodeID, projects[1].NodeID}
	if !reflect.DeepEqual(expectedNodeIDs, actualNodeIDs) {
		t.Fatalf("expected merged project node ids %#v, got %#v", expectedNodeIDs, actualNodeIDs)
	}

	for _, project := range projects {
		if project.Source != OutlineSourceHybrid {
			t.Fatalf("expected hybrid source after merge, got %#v", project)
		}
		if project.CanonicalContent == "" {
			t.Fatalf("expected canonical content after merge, got %#v", project)
		}
	}
}

func mustMarshalOutlineDocumentJSON(t *testing.T, document documentparser.ParsedDocument) []byte {
	t.Helper()
	return mustMarshalOutlineJSON(t, document)
}

func mustMarshalOutlineJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal outline json: %v", err)
	}
	return payload
}

func filterOutlineNodesByKind(nodes []OutlineNode, kind OutlineNodeKind) []OutlineNode {
	filtered := make([]OutlineNode, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeKind == kind {
			filtered = append(filtered, node)
		}
	}

	return filtered
}
