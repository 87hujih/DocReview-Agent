package assistant

import (
	"context"
	"encoding/json"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// LocatedSectionMode 表示当前文件定位结果对应的读取模式。
type LocatedSectionMode string

const (
	LocatedSectionModeSection     LocatedSectionMode = "section"
	LocatedSectionModeSectionList LocatedSectionMode = "section_list"
	LocatedSectionModeDocument    LocatedSectionMode = "document"
)

// LocateSectionInput 归拢当前文件 section 定位所需的输入字段。
type LocateSectionInput struct {
	ResourceID      string
	VersionID       string
	Message         string
	Intent          ReadIntent
	ActiveSectionID string
}

// LocatedSection 承载当前文件目标定位结果，供 canonical 读取直接消费。
type LocatedSection struct {
	VersionID    string
	Mode         LocatedSectionMode
	SectionID    string
	SectionType  string
	SectionTitle string
	Reason       string
}

type currentFileSectionLookup interface {
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	GetSectionByID(ctx context.Context, sectionID string) (*postgres.ResourceSection, error)
	GetSectionByOrder(ctx context.Context, versionID string, sectionType string, ordinal int) (*postgres.ResourceSection, error)
	ListSectionsForReading(ctx context.Context, versionID string, sectionType string) ([]postgres.ResourceSection, error)
}

// SectionLocator 负责把当前消息定位到当前文件内的 section / list / document 目标。
type SectionLocator struct {
	reader currentFileSectionLookup
}

// NewSectionLocator 构造当前文件 section 定位器。
func NewSectionLocator(reader currentFileSectionLookup) *SectionLocator {
	return &SectionLocator{reader: reader}
}

// Locate 按“显式实体 -> ordinal -> active section -> section list -> document”的顺序定位当前文件目标。
func (l *SectionLocator) Locate(ctx context.Context, input LocateSectionInput) (*LocatedSection, error) {
	if l == nil || l.reader == nil {
		return nil, nil
	}

	input.Message = strings.TrimSpace(input.Message)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.VersionID = strings.TrimSpace(input.VersionID)
	input.ActiveSectionID = strings.TrimSpace(input.ActiveSectionID)
	if input.Message == "" {
		return nil, nil
	}

	if input.VersionID == "" {
		versionID, err := l.resolveCurrentVersionID(ctx, input.ResourceID)
		if err != nil {
			return nil, err
		}
		input.VersionID = versionID
	}

	allSections, err := l.listSections(ctx, input.VersionID, "")
	if err != nil {
		return nil, err
	}

	if located := locateExplicitSection(input.Message, allSections); located != nil {
		return located, nil
	}

	located, err := l.locateOrdinalSection(ctx, input, allSections)
	if err != nil {
		return nil, err
	}
	if located != nil {
		return located, nil
	}

	located, err = l.locateActiveSection(ctx, input.ActiveSectionID)
	if err != nil {
		return nil, err
	}
	if located != nil {
		return located, nil
	}

	if located := locateSectionListSignal(input, allSections); located != nil {
		return located, nil
	}

	if located, err := l.locateDocumentFallback(ctx, input, allSections); err != nil || located != nil {
		return located, err
	}

	return nil, nil
}

// resolveCurrentVersionID 读取当前资源版本号，避免调用方在 service 层重复补齐版本信息。
func (l *SectionLocator) resolveCurrentVersionID(ctx context.Context, resourceID string) (string, error) {
	if strings.TrimSpace(resourceID) == "" {
		return "", nil
	}

	version, err := l.reader.GetCurrentVersion(ctx, strings.TrimSpace(resourceID))
	if err != nil {
		return "", err
	}
	if version == nil {
		return "", nil
	}

	return strings.TrimSpace(version.ID), nil
}

// listSections 返回当前版本的 section 列表，把仓储调用细节收口在单点。
func (l *SectionLocator) listSections(ctx context.Context, versionID string, sectionType string) ([]postgres.ResourceSection, error) {
	if strings.TrimSpace(versionID) == "" {
		return nil, nil
	}

	return l.reader.ListSectionsForReading(ctx, versionID, strings.TrimSpace(sectionType))
}

// locateOrdinalSection 解析第几个项目 / 章节这类顺序引用。
func (l *SectionLocator) locateOrdinalSection(
	ctx context.Context,
	input LocateSectionInput,
	allSections []postgres.ResourceSection,
) (*LocatedSection, error) {
	ordinal := extractOrdinal(input.Message)
	if ordinal == 0 {
		return nil, nil
	}

	sectionType := inferSectionTypeForMessage(input.Message)
	if sectionType != "" {
		section, err := l.reader.GetSectionByOrder(ctx, input.VersionID, sectionType, ordinal)
		if err != nil {
			return nil, err
		}
		if section != nil {
			return locatedSectionFromRecord(*section, "ordinal_reference"), nil
		}
	}

	sections := filterSectionsByType(allSections, sectionType)
	if ordinal > len(sections) || ordinal <= 0 {
		return nil, nil
	}

	return locatedSectionFromRecord(sections[ordinal-1], "ordinal_reference"), nil
}

// locateActiveSection 在当前消息带承接指代时回退到 active section。
func (l *SectionLocator) locateActiveSection(ctx context.Context, activeSectionID string) (*LocatedSection, error) {
	if strings.TrimSpace(activeSectionID) == "" {
		return nil, nil
	}

	section, err := l.reader.GetSectionByID(ctx, activeSectionID)
	if err != nil {
		return nil, err
	}
	if section == nil {
		return nil, nil
	}

	return locatedSectionFromRecord(*section, "active_section"), nil
}

// locateDocumentFallback 在 section 不可用时给出全文读取信号。
func (l *SectionLocator) locateDocumentFallback(
	ctx context.Context,
	input LocateSectionInput,
	allSections []postgres.ResourceSection,
) (*LocatedSection, error) {
	if !referencesWholeDocument(input.Message) && len(allSections) > 0 {
		return nil, nil
	}
	if input.ResourceID == "" {
		return nil, nil
	}

	version, err := l.reader.GetCurrentVersion(ctx, input.ResourceID)
	if err != nil {
		return nil, err
	}
	if version == nil || strings.TrimSpace(version.Content) == "" {
		return nil, nil
	}

	return &LocatedSection{
		VersionID: strings.TrimSpace(version.ID),
		Mode:      LocatedSectionModeDocument,
		Reason:    "document_fallback",
	}, nil
}

// locateExplicitSection 尝试在当前文件已知 sections 中按标题 / entity / alias 直接命中目标。
func locateExplicitSection(message string, sections []postgres.ResourceSection) *LocatedSection {
	for _, section := range sections {
		if !sectionMatchesMessage(section, message) {
			continue
		}

		return locatedSectionFromRecord(section, "explicit_entity")
	}

	return nil
}

// locateSectionListSignal 判断当前消息是否在要求列举当前文件 sections。
func locateSectionListSignal(input LocateSectionInput, sections []postgres.ResourceSection) *LocatedSection {
	if input.Intent.Kind != ReadIntentListSections || len(sections) == 0 {
		return nil
	}

	return &LocatedSection{
		VersionID:   input.VersionID,
		Mode:        LocatedSectionModeSectionList,
		SectionType: inferSectionTypeForMessage(input.Message),
		Reason:      "section_list",
	}
}

// sectionMatchesMessage 判断当前消息是否显式提到了某个 section。
func sectionMatchesMessage(section postgres.ResourceSection, message string) bool {
	for _, token := range sectionMatchTokens(section) {
		if token == "" {
			continue
		}
		if strings.Contains(message, token) {
			return true
		}
	}

	return false
}

// sectionMatchTokens 生成 section 可用于显式命中的候选词。
func sectionMatchTokens(section postgres.ResourceSection) []string {
	tokens := make([]string, 0, 4)
	if title := strings.TrimSpace(section.Title); title != "" {
		tokens = append(tokens, title)
	}
	if section.CanonicalEntityName != nil {
		if entityName := strings.TrimSpace(*section.CanonicalEntityName); entityName != "" {
			tokens = append(tokens, entityName)
		}
	}

	var aliases []string
	if err := json.Unmarshal(section.AliasesJSON, &aliases); err == nil {
		for _, alias := range aliases {
			if trimmed := strings.TrimSpace(alias); trimmed != "" {
				tokens = append(tokens, trimmed)
			}
		}
	}

	return tokens
}

// inferSectionTypeForMessage 从用户问题里推断当前读取目标的 section 类型。
func inferSectionTypeForMessage(message string) string {
	switch {
	case containsAny(message, []string{"项目", "经历"}):
		return "project"
	case containsAny(message, []string{"章节", "章", "小节", "节", "部分"}):
		return "section"
	default:
		return ""
	}
}

// filterSectionsByType 在内存里按 sectionType 过滤有序 section 列表。
func filterSectionsByType(sections []postgres.ResourceSection, sectionType string) []postgres.ResourceSection {
	if strings.TrimSpace(sectionType) == "" {
		return sections
	}

	filtered := make([]postgres.ResourceSection, 0, len(sections))
	for _, section := range sections {
		if section.SectionType != sectionType {
			continue
		}

		filtered = append(filtered, section)
	}

	return filtered
}

// locatedSectionFromRecord 把仓储返回的 section 记录投影成定位结果。
func locatedSectionFromRecord(section postgres.ResourceSection, reason string) *LocatedSection {
	return &LocatedSection{
		VersionID:    strings.TrimSpace(section.VersionID),
		Mode:         LocatedSectionModeSection,
		SectionID:    section.ID,
		SectionType:  strings.TrimSpace(section.SectionType),
		SectionTitle: strings.TrimSpace(section.Title),
		Reason:       reason,
	}
}

// referencesWholeDocument 判断消息是否在明确索要整份文件级内容。
func referencesWholeDocument(message string) bool {
	return containsAny(message, []string{
		"全文",
		"整份文件",
		"整个文件",
		"全部内容",
		"文件内容",
		"这份文件",
		"这份简历",
	})
}
