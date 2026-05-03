package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

var (
	// ErrCanonicalReadUnavailable 表示当前文件缺少稳定可读的 canonical 内容。
	ErrCanonicalReadUnavailable = errors.New("当前文件缺少可直接读取的内容")
)

// CanonicalReadMode 表示 canonical read 返回的内容模式。
type CanonicalReadMode string

const (
	CanonicalReadModeSection     CanonicalReadMode = "section"
	CanonicalReadModeSectionList CanonicalReadMode = "section_list"
	CanonicalReadModeDocument    CanonicalReadMode = "document"
)

// CanonicalReadInput 归拢当前文件 canonical read 所需的输入字段。
type CanonicalReadInput struct {
	ResourceID string
	VersionID  string
	Located    *LocatedSection
}

// CanonicalReadSectionItem 表示列表型读取结果里的单个 section 项。
type CanonicalReadSectionItem struct {
	SectionID    string
	SectionType  string
	SectionTitle string
	Ordinal      int
	EntityName   string
}

// CanonicalReadResult 承载当前文件 canonical read 的读取结果。
type CanonicalReadResult struct {
	Mode         CanonicalReadMode
	ResourceID   string
	VersionID    string
	SectionID    string
	SectionType  string
	SectionTitle string
	Content      string
	Sections     []CanonicalReadSectionItem
}

// SectionReader 负责从 resource_sections / 当前版本全文中读取 canonical 内容。
type SectionReader struct {
	reader currentFileSectionLookup
}

// NewSectionReader 构造当前文件 canonical 读取器。
func NewSectionReader(reader currentFileSectionLookup) *SectionReader {
	return &SectionReader{reader: reader}
}

// Read 按定位结果返回当前文件的 canonical 内容。
func (r *SectionReader) Read(ctx context.Context, input CanonicalReadInput) (*CanonicalReadResult, error) {
	if r == nil || r.reader == nil || input.Located == nil {
		return nil, nil
	}

	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.VersionID = strings.TrimSpace(input.VersionID)
	if input.VersionID == "" {
		input.VersionID = strings.TrimSpace(input.Located.VersionID)
	}
	switch input.Located.Mode {
	case LocatedSectionModeSection:
		return r.readSection(ctx, input)
	case LocatedSectionModeSectionList:
		return r.readSectionList(ctx, input)
	case LocatedSectionModeDocument:
		return r.readDocument(ctx, input)
	default:
		return nil, nil
	}
}

// readSection 读取单个 section 的 canonical 正文。
func (r *SectionReader) readSection(ctx context.Context, input CanonicalReadInput) (*CanonicalReadResult, error) {
	section, err := r.reader.GetSectionByID(ctx, strings.TrimSpace(input.Located.SectionID))
	if err != nil {
		return nil, err
	}
	if section == nil {
		return nil, fmt.Errorf("read section: %w", ErrCanonicalReadUnavailable)
	}
	if strings.TrimSpace(section.Content) == "" {
		return nil, fmt.Errorf("read section: %w", ErrCanonicalReadUnavailable)
	}

	return &CanonicalReadResult{
		Mode:         CanonicalReadModeSection,
		ResourceID:   section.ResourceID,
		VersionID:    section.VersionID,
		SectionID:    section.ID,
		SectionType:  strings.TrimSpace(section.SectionType),
		SectionTitle: displaySectionTitle(*section),
		Content:      strings.TrimSpace(section.Content),
	}, nil
}

// readSectionList 读取某类 section 的稳定列表视图。
func (r *SectionReader) readSectionList(ctx context.Context, input CanonicalReadInput) (*CanonicalReadResult, error) {
	sections, err := r.reader.ListSectionsForReading(ctx, input.VersionID, strings.TrimSpace(input.Located.SectionType))
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("read section list: %w", ErrCanonicalReadUnavailable)
	}

	items := make([]CanonicalReadSectionItem, 0, len(sections))
	for _, section := range sections {
		entityName := ""
		if section.CanonicalEntityName != nil {
			entityName = strings.TrimSpace(*section.CanonicalEntityName)
		}
		items = append(items, CanonicalReadSectionItem{
			SectionID:    section.ID,
			SectionType:  strings.TrimSpace(section.SectionType),
			SectionTitle: displaySectionTitle(section),
			Ordinal:      section.SectionOrder,
			EntityName:   entityName,
		})
	}

	return &CanonicalReadResult{
		Mode:       CanonicalReadModeSectionList,
		ResourceID: input.ResourceID,
		VersionID:  input.VersionID,
		Sections:   items,
	}, nil
}

// readDocument 读取当前版本全文，作为 section 缺失时的显式兜底。
func (r *SectionReader) readDocument(ctx context.Context, input CanonicalReadInput) (*CanonicalReadResult, error) {
	version, err := r.reader.GetCurrentVersion(ctx, input.ResourceID)
	if err != nil {
		return nil, err
	}
	if version == nil || strings.TrimSpace(version.Content) == "" {
		return nil, fmt.Errorf("read document: %w", ErrCanonicalReadUnavailable)
	}

	return &CanonicalReadResult{
		Mode:       CanonicalReadModeDocument,
		ResourceID: version.ResourceID,
		VersionID:  version.ID,
		Content:    strings.TrimSpace(version.Content),
	}, nil
}

// displaySectionTitle 生成当前 section 的稳定展示标题，避免上游读到空标题。
func displaySectionTitle(section postgres.ResourceSection) string {
	if title := strings.TrimSpace(section.Title); title != "" {
		return title
	}
	if section.CanonicalEntityName != nil {
		if entityName := strings.TrimSpace(*section.CanonicalEntityName); entityName != "" {
			return entityName
		}
	}

	if section.SectionOrder > 0 {
		return fmt.Sprintf("第%d节", section.SectionOrder)
	}

	return strings.TrimSpace(section.SectionType)
}
