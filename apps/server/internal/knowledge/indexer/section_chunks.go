package indexer

import (
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

type sectionChunkSpec struct {
	SectionTitle  string
	Content       string
	SectionID     string
	SectionType   string
	ChunkRole     string
	WindowGroupID string
	PageStart     int
	PageEnd       int
	Metadata      map[string]any
}

func buildSectionChunkSpecs(sections []postgres.ResourceSectionInput) []sectionChunkSpec {
	specs := make([]sectionChunkSpec, 0)
	for _, section := range sections {
		switch strings.TrimSpace(section.SectionType) {
		case "project":
			specs = append(specs, buildProjectSectionSpecs(section)...)
		default:
			specs = append(specs, buildGenericSectionSpecs(section)...)
		}
	}

	return specs
}

func buildProjectSectionSpecs(section postgres.ResourceSectionInput) []sectionChunkSpec {
	windowGroupID := strings.TrimSpace(section.SectionKey)
	if windowGroupID == "" {
		windowGroupID = strings.TrimSpace(section.Title)
	}

	base := sectionChunkSpec{
		SectionTitle:  section.Title,
		SectionID:     section.SectionID,
		SectionType:   "project",
		WindowGroupID: windowGroupID,
		PageStart:     section.PageStart,
		PageEnd:       section.PageEnd,
		Metadata:      cloneMetadata(section.Metadata),
	}

	specs := make([]sectionChunkSpec, 0, 5)
	if summary := strings.TrimSpace(section.Summary); summary != "" {
		specs = append(specs, withChunkRole(base, "section_summary", summary))
	}
	if title := strings.TrimSpace(section.Title); title != "" {
		specs = append(specs, withChunkRole(base, "project_name", title))
	}
	if techStack := strings.Join(extractTechStack(section.Metadata), " "); strings.TrimSpace(techStack) != "" {
		specs = append(specs, withChunkRole(base, "tech_stack", techStack))
	}

	paragraphs := splitParagraphs(section.Content)
	if len(paragraphs) > 0 {
		specs = append(specs, withChunkRole(base, "project_description", paragraphs[0]))
	}
	if len(paragraphs) > 1 {
		specs = append(specs, withChunkRole(base, "project_work", strings.Join(paragraphs[1:], "\n")))
	}

	return specs
}

func buildGenericSectionSpecs(section postgres.ResourceSectionInput) []sectionChunkSpec {
	windowGroupID := strings.TrimSpace(section.SectionKey)
	if windowGroupID == "" {
		windowGroupID = strings.TrimSpace(section.Title)
	}

	base := sectionChunkSpec{
		SectionTitle:  section.Title,
		SectionID:     section.SectionID,
		SectionType:   strings.TrimSpace(section.SectionType),
		WindowGroupID: windowGroupID,
		PageStart:     section.PageStart,
		PageEnd:       section.PageEnd,
		Metadata:      cloneMetadata(section.Metadata),
	}

	specs := make([]sectionChunkSpec, 0, 2)
	if summary := strings.TrimSpace(section.Summary); summary != "" {
		specs = append(specs, withChunkRole(base, "section_summary", summary))
	}
	if body := strings.TrimSpace(section.Content); body != "" {
		specs = append(specs, withChunkRole(base, "section_body", body))
	}

	return specs
}

func withChunkRole(base sectionChunkSpec, role string, content string) sectionChunkSpec {
	base.ChunkRole = role
	base.Content = strings.TrimSpace(content)
	return base
}

func splitParagraphs(content string) []string {
	parts := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	paragraphs := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		paragraphs = append(paragraphs, trimmed)
	}

	return paragraphs
}

func extractTechStack(metadata map[string]any) []string {
	value, ok := metadata["tech_stack"]
	if !ok {
		return nil
	}

	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}

			items = append(items, text)
		}
		return items
	default:
		return nil
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}

	return cloned
}
