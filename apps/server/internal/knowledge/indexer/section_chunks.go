package indexer

import (
	"encoding/json"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

func buildSectionAwareChunkInputs(sections []postgres.ResourceSection) []postgres.ResourceChunkInput {
	inputs := make([]postgres.ResourceChunkInput, 0)
	chunkIndex := 0

	for _, section := range sections {
		groundedChunks := buildChunksForSection(section, chunkIndex)
		chunkIndex += len(groundedChunks)
		inputs = append(inputs, groundedChunks...)
	}

	return inputs
}

func buildChunksForSection(section postgres.ResourceSection, startIndex int) []postgres.ResourceChunkInput {
	if strings.TrimSpace(section.SectionType) == "project" {
		return buildProjectChunks(section, startIndex)
	}

	return buildGenericSectionChunks(section, startIndex)
}

func buildProjectChunks(section postgres.ResourceSection, startIndex int) []postgres.ResourceChunkInput {
	windowGroupID := sectionWindowGroupID(section)
	techStack := extractTechStack(section.MetadataJSON)
	chunks := make([]postgres.ResourceChunkInput, 0, 5)

	addChunk := func(role string, content string, order int) {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return
		}

		chunks = append(chunks, postgres.ResourceChunkInput{
			ChunkIndex:     startIndex + len(chunks),
			SectionTitle:   section.Title,
			Content:        trimmed,
			SectionID:      optionalString(section.ID),
			SectionType:    optionalString(section.SectionType),
			ChunkRole:      optionalString(role),
			WindowGroupID:  optionalString(windowGroupID),
			OrderInSection: optionalInt(order),
			PageStart:      section.PageStart,
			PageEnd:        section.PageEnd,
			MetadataJSON:   buildChunkMetadata(section),
		})
	}

	addChunk("section_summary", strings.TrimSpace(joinNonEmptyLines(section.Title, section.Summary)), 1)
	addChunk("project_name", section.Title, 2)
	if len(techStack) > 0 {
		addChunk("tech_stack", strings.Join(techStack, " "), 3)
	}
	addChunk("project_description", section.Summary, 4)
	addChunk("project_work", section.Content, 5)

	return chunks
}

func buildGenericSectionChunks(section postgres.ResourceSection, startIndex int) []postgres.ResourceChunkInput {
	windowGroupID := sectionWindowGroupID(section)
	chunks := make([]postgres.ResourceChunkInput, 0, 2)

	addChunk := func(role string, content string, order int) {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return
		}

		chunks = append(chunks, postgres.ResourceChunkInput{
			ChunkIndex:     startIndex + len(chunks),
			SectionTitle:   section.Title,
			Content:        trimmed,
			SectionID:      optionalString(section.ID),
			SectionType:    optionalString(section.SectionType),
			ChunkRole:      optionalString(role),
			WindowGroupID:  optionalString(windowGroupID),
			OrderInSection: optionalInt(order),
			PageStart:      section.PageStart,
			PageEnd:        section.PageEnd,
			MetadataJSON:   buildChunkMetadata(section),
		})
	}

	addChunk("section_summary", strings.TrimSpace(joinNonEmptyLines(section.Title, section.Summary)), 1)
	addChunk("section_body", section.Content, 2)
	return chunks
}

func sectionWindowGroupID(section postgres.ResourceSection) string {
	if trimmed := strings.TrimSpace(section.SectionKey); trimmed != "" {
		return trimmed
	}

	return strings.TrimSpace(section.ID)
}

func extractTechStack(metadataJSON []byte) []string {
	if len(metadataJSON) == 0 {
		return nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return nil
	}

	value, ok := metadata["tech_stack"]
	if !ok {
		return nil
	}

	items, ok := value.([]any)
	if !ok {
		return nil
	}

	techStack := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			techStack = append(techStack, trimmed)
		}
	}

	return techStack
}

func buildChunkMetadata(section postgres.ResourceSection) []byte {
	metadata := map[string]any{
		"section_key": section.SectionKey,
	}
	if section.CanonicalEntityName != nil && strings.TrimSpace(*section.CanonicalEntityName) != "" {
		metadata["canonical_entity_name"] = strings.TrimSpace(*section.CanonicalEntityName)
	}
	if len(section.AliasesJSON) > 0 {
		var aliases []string
		if err := json.Unmarshal(section.AliasesJSON, &aliases); err == nil && len(aliases) > 0 {
			metadata["aliases"] = aliases
		}
	}

	body, err := json.Marshal(metadata)
	if err != nil {
		return []byte(`{}`)
	}

	return body
}

func joinNonEmptyLines(values ...string) string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	return strings.Join(lines, "\n")
}

func optionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func optionalInt(value int) *int {
	if value <= 0 {
		return nil
	}

	return &value
}
