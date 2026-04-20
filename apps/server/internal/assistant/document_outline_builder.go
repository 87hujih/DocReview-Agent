package assistant

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/storage/postgres"
)

var outlineOrdinalPrefixPattern = regexp.MustCompile(`^\s*(?:第\s*)?(\d+|[一二三四五六七八九十]+)\s*[\.、:：)\]】）-]?\s*`)

// BuildDocumentOutlineInput 描述构建当前文件 outline 所需的真源输入。
type BuildDocumentOutlineInput struct {
	VersionID     string
	FullText      string
	Sections      []postgres.ResourceSection
	StructureJSON []byte
}

// DocumentOutlineBuilder 负责把全文、section 与 document_json 合成为稳定 outline。
type DocumentOutlineBuilder struct{}

// NewDocumentOutlineBuilder 创建 outline builder。
func NewDocumentOutlineBuilder() *DocumentOutlineBuilder {
	return &DocumentOutlineBuilder{}
}

// Build 从 semantic section 与 heading 结构构建当前文件的稳定 outline。
func (b *DocumentOutlineBuilder) Build(input BuildDocumentOutlineInput) []OutlineNode {
	versionID := strings.TrimSpace(input.VersionID)
	if versionID == "" && strings.TrimSpace(input.FullText) == "" && len(input.Sections) == 0 && len(input.StructureJSON) == 0 {
		return nil
	}

	nodes := []OutlineNode{{
		NodeID:           documentOutlineNodeID(versionID),
		NodeKind:         OutlineNodeDocument,
		Title:            "全文",
		CanonicalContent: strings.TrimSpace(input.FullText),
		Confidence:       1,
		Source:           OutlineSourceHybrid,
	}}

	matchIndex := make(map[string]int)
	semanticNodes := buildSemanticOutlineNodes(versionID, input.Sections)
	nodes = append(nodes, semanticNodes...)
	for index, node := range semanticNodes {
		registerOutlineNodeKeys(matchIndex, node, index+2)
	}

	parsedDocument, ok := parseOutlineDocument(input.StructureJSON)
	if !ok {
		parsedDocument, ok = parseOutlineDocumentFromFullText(input.FullText)
	}
	if !ok {
		return nodes
	}

	nodes = mergeHeadingOutlineNodes(nodes, matchIndex, buildHeadingOutlineNodes(versionID, parsedDocument))
	return nodes
}

func buildSemanticOutlineNodes(versionID string, sections []postgres.ResourceSection) []OutlineNode {
	nodes := make([]OutlineNode, 0, len(sections))
	for _, section := range sections {
		title := cleanOutlineTitle(section.Title)
		if title == "" {
			title = cleanOutlineTitle(section.Summary)
		}
		if title == "" {
			continue
		}

		nodeKind := OutlineNodeHeadingSection
		if strings.EqualFold(strings.TrimSpace(section.SectionType), "project") {
			nodeKind = OutlineNodeProjectItem
		}

		node := OutlineNode{
			NodeID:           fmt.Sprintf("section:%s", strings.TrimSpace(section.ID)),
			NodeKind:         nodeKind,
			Title:            title,
			Ordinal:          section.SectionOrder,
			Aliases:          appendUniqueOutlineStrings(nil, extractSectionAliases(section)...),
			ParentNodeID:     documentOutlineNodeID(versionID),
			SectionID:        strings.TrimSpace(section.ID),
			CanonicalContent: firstNonEmpty(section.Content, section.Summary),
			Confidence:       0.98,
			Source:           OutlineSourceSemantic,
		}

		nodes = append(nodes, node)
	}

	return nodes
}

func parseOutlineDocument(documentJSON []byte) (documentparser.ParsedDocument, bool) {
	if len(documentJSON) == 0 {
		return documentparser.ParsedDocument{}, false
	}

	var document documentparser.ParsedDocument
	if err := json.Unmarshal(documentJSON, &document); err != nil {
		return documentparser.ParsedDocument{}, false
	}

	return document, true
}

func parseOutlineDocumentFromFullText(fullText string) (documentparser.ParsedDocument, bool) {
	normalized := strings.TrimSpace(strings.ReplaceAll(fullText, "\r\n", "\n"))
	if normalized == "" {
		return documentparser.ParsedDocument{}, false
	}

	lines := strings.Split(normalized, "\n")
	blocks := make([]documentparser.Block, 0, len(lines))
	paragraphLines := make([]string, 0)
	hasHeading := false

	flushParagraph := func() {
		text := strings.TrimSpace(strings.Join(paragraphLines, "\n"))
		if text != "" {
			blocks = append(blocks, documentparser.Block{
				Type: documentparser.BlockParagraph,
				Text: text,
			})
		}
		paragraphLines = paragraphLines[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushParagraph()
			continue
		}

		if level := outlineMarkdownHeadingLevel(trimmed); level > 0 {
			flushParagraph()
			headingText := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if headingText == "" {
				continue
			}

			hasHeading = true
			blocks = append(blocks, documentparser.Block{
				Type:  documentparser.BlockHeading,
				Text:  headingText,
				Level: level,
			})
			continue
		}

		paragraphLines = append(paragraphLines, trimmed)
	}

	flushParagraph()
	if !hasHeading {
		return documentparser.ParsedDocument{}, false
	}

	return documentparser.ParsedDocument{
		SourceFormat: "markdown",
		Blocks:       blocks,
	}, true
}

type headingOutlineNode struct {
	OutlineNode
	matchKey string
}

func buildHeadingOutlineNodes(versionID string, document documentparser.ParsedDocument) []headingOutlineNode {
	if len(document.Blocks) == 0 {
		return nil
	}

	type stackEntry struct {
		Level              int
		Title              string
		NodeID             string
		IsProjectContainer bool
	}

	nodes := make([]headingOutlineNode, 0)
	stack := make([]stackEntry, 0)
	projectOrdinal := 0
	headingOrdinal := 0

	for index, block := range document.Blocks {
		if block.Type != documentparser.BlockHeading {
			continue
		}

		rawTitle := strings.TrimSpace(block.Text)
		if rawTitle == "" {
			continue
		}

		for len(stack) > 0 && stack[len(stack)-1].Level >= block.Level {
			stack = stack[:len(stack)-1]
		}

		parentNodeID := documentOutlineNodeID(versionID)
		parentIsProjectContainer := false
		path := make([]string, 0, len(stack)+1)
		for _, entry := range stack {
			path = append(path, entry.Title)
		}
		if len(stack) > 0 {
			parentNodeID = stack[len(stack)-1].NodeID
			parentIsProjectContainer = stack[len(stack)-1].IsProjectContainer
		}

		nodeKind := OutlineNodeHeadingSection
		ordinal := 0
		title := cleanOutlineTitle(rawTitle)
		if parentIsProjectContainer {
			nodeKind = OutlineNodeProjectItem
			projectOrdinal++
			ordinal = projectOrdinal
		} else {
			headingOrdinal++
			ordinal = headingOrdinal
		}

		path = append(path, title)
		nodeID := headingOutlineNodeID(versionID, nodeKind, path)
		node := headingOutlineNode{
			OutlineNode: OutlineNode{
				NodeID:           nodeID,
				NodeKind:         nodeKind,
				Title:            title,
				Ordinal:          ordinal,
				Aliases:          appendUniqueOutlineStrings(nil, rawTitle, title),
				ParentNodeID:     parentNodeID,
				CanonicalContent: collectHeadingCanonicalContent(document.Blocks, index, block.Level),
				Confidence:       0.85,
				Source:           OutlineSourceHeadingStructure,
			},
			matchKey: normalizeOutlineLookupText(title),
		}
		nodes = append(nodes, node)

		stack = append(stack, stackEntry{
			Level:              block.Level,
			Title:              title,
			NodeID:             nodeID,
			IsProjectContainer: isProjectContainerTitle(rawTitle),
		})
	}

	return nodes
}

func mergeHeadingOutlineNodes(nodes []OutlineNode, matchIndex map[string]int, headingNodes []headingOutlineNode) []OutlineNode {
	for _, candidate := range headingNodes {
		if existingIndex, ok := matchIndex[candidate.matchKey]; ok && existingIndex > 0 && existingIndex <= len(nodes) {
			merged := mergeOutlineNode(nodes[existingIndex-1], candidate.OutlineNode)
			nodes[existingIndex-1] = merged
			registerOutlineNodeKeys(matchIndex, merged, existingIndex)
			continue
		}

		nodes = append(nodes, candidate.OutlineNode)
		registerOutlineNodeKeys(matchIndex, candidate.OutlineNode, len(nodes))
	}

	return nodes
}

func mergeOutlineNode(base OutlineNode, candidate OutlineNode) OutlineNode {
	merged := base
	if merged.NodeKind == "" {
		merged.NodeKind = candidate.NodeKind
	}
	if merged.NodeKind == OutlineNodeHeadingSection && candidate.NodeKind == OutlineNodeProjectItem {
		merged.NodeKind = OutlineNodeProjectItem
	}
	if strings.TrimSpace(merged.Title) == "" {
		merged.Title = candidate.Title
	}
	if merged.Ordinal == 0 {
		merged.Ordinal = candidate.Ordinal
	}
	if strings.TrimSpace(merged.ParentNodeID) == "" {
		merged.ParentNodeID = candidate.ParentNodeID
	}
	if strings.TrimSpace(merged.CanonicalContent) == "" || len(strings.TrimSpace(candidate.CanonicalContent)) > len(strings.TrimSpace(merged.CanonicalContent)) {
		merged.CanonicalContent = strings.TrimSpace(candidate.CanonicalContent)
	}
	if merged.Confidence < candidate.Confidence {
		merged.Confidence = candidate.Confidence
	}
	merged.Aliases = appendUniqueOutlineStrings(merged.Aliases, candidate.Aliases...)
	if merged.Source != candidate.Source {
		merged.Source = OutlineSourceHybrid
	}

	return merged
}

func collectHeadingCanonicalContent(blocks []documentparser.Block, headingIndex int, level int) string {
	lines := make([]string, 0)
	for index := headingIndex + 1; index < len(blocks); index++ {
		block := blocks[index]
		if block.Type == documentparser.BlockHeading && block.Level <= level {
			break
		}
		text := strings.TrimSpace(block.Text)
		if text != "" {
			lines = append(lines, text)
		}
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractSectionAliases(section postgres.ResourceSection) []string {
	aliases := make([]string, 0, 4)
	aliases = append(aliases, strings.TrimSpace(section.Title))
	if section.CanonicalEntityName != nil {
		aliases = append(aliases, strings.TrimSpace(*section.CanonicalEntityName))
	}

	if len(section.AliasesJSON) > 0 {
		var stored []string
		if err := json.Unmarshal(section.AliasesJSON, &stored); err == nil {
			aliases = append(aliases, stored...)
		}
	}

	for index := range aliases {
		aliases[index] = cleanOutlineTitle(aliases[index])
	}

	return aliases
}

func registerOutlineNodeKeys(index map[string]int, node OutlineNode, position int) {
	if position <= 0 {
		return
	}

	keys := make([]string, 0, len(node.Aliases)+1)
	keys = append(keys, normalizeOutlineLookupText(node.Title))
	for _, alias := range node.Aliases {
		keys = append(keys, normalizeOutlineLookupText(alias))
	}

	for _, key := range keys {
		if key == "" {
			continue
		}
		index[key] = position
	}
}

func cleanOutlineTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}

	return strings.TrimSpace(outlineOrdinalPrefixPattern.ReplaceAllString(trimmed, ""))
}

func normalizeOutlineLookupText(text string) string {
	cleaned := cleanOutlineTitle(text)
	if cleaned == "" {
		return ""
	}

	var builder strings.Builder
	for _, r := range strings.ToLower(cleaned) {
		switch {
		case unicode.IsSpace(r):
			continue
		case unicode.IsPunct(r):
			continue
		case unicode.IsSymbol(r):
			continue
		default:
			builder.WriteRune(r)
		}
	}

	return builder.String()
}

func isProjectContainerTitle(title string) bool {
	normalized := normalizeOutlineLookupText(title)
	if normalized == "" {
		return false
	}

	for _, keyword := range []string{"项目经历", "项目", "projects", "projectexperience", "project"} {
		if strings.Contains(normalized, normalizeOutlineLookupText(keyword)) {
			return true
		}
	}

	return false
}

func headingOutlineNodeID(versionID string, kind OutlineNodeKind, path []string) string {
	raw := strings.Join(path, " > ")
	return fmt.Sprintf("%s:%s:%s", kind, strings.TrimSpace(versionID), stableOutlineHash(raw))
}

func documentOutlineNodeID(versionID string) string {
	return fmt.Sprintf("document:%s", strings.TrimSpace(versionID))
}

func stableOutlineHash(value string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:8])
}

func outlineMarkdownHeadingLevel(line string) int {
	level := 0
	for _, ch := range line {
		if ch != '#' {
			break
		}
		level++
	}
	if level == 0 {
		return 0
	}
	if len(line) > level && line[level] != ' ' {
		return 0
	}

	return level
}

func appendUniqueOutlineStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base))
	result := make([]string, 0, len(base)+len(values))
	for _, candidate := range base {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	for _, candidate := range values {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
