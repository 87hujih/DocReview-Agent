package indexer

import (
	"encoding/json"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// buildSectionAwareChunkInputs 按 section 类型生成 grounded chunk 输入，保证项目目录和普通文档走各自的切块策略。
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

// buildChunksForSection 根据 section 类型选择项目专用或通用切块逻辑，返回当前 section 需要写库的 chunk。
func buildChunksForSection(section postgres.ResourceSection, startIndex int) []postgres.ResourceChunkInput {
	if strings.TrimSpace(section.SectionType) == "project" {
		return buildProjectChunks(section, startIndex)
	}

	return buildGenericSectionChunks(section, startIndex)
}

// buildProjectChunks 把项目结构 section 展开成按节点命名的 grounded chunk，保留路径和层级信息。
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

// buildGenericSectionChunks 把普通 section 切成连续文本 chunk，并沿用 grounded 元数据描述其来源范围。
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

// sectionWindowGroupID 为同一 section 生成稳定的窗口分组 ID，方便引用窗口按 section 归并。
func sectionWindowGroupID(section postgres.ResourceSection) string {
	if trimmed := strings.TrimSpace(section.SectionKey); trimmed != "" {
		return trimmed
	}

	return strings.TrimSpace(section.ID)
}

// extractTechStack 从现有内容里提取 `技术栈`，避免调用方重复解析同一份数据。
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

// buildChunkMetadata 生成写入 chunk 的 grounded 元数据 JSON，保留标题、页码、路径和原始 section 信息。
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

// joinNonEmptyLines 按换行拼接非空文本片段，避免提示词和摘要内容混入多余空行。
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

// optionalString 把空白字符串折叠为 nil，统一可选文本字段的缺省语义。
func optionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

// optionalInt 把有效整数包装成可选值，统一构造 grounded 元数据时的空值表达。
func optionalInt(value int) *int {
	if value <= 0 {
		return nil
	}

	return &value
}
