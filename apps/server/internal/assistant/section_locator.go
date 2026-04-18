package assistant

import (
	"context"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// activeFileResourceReader 描述活跃文件阅读模式需要的最小资源读取能力。
type activeFileResourceReader interface {
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	GetSectionByID(ctx context.Context, sectionID string) (*postgres.ResourceSection, error)
	GetSectionByOrder(ctx context.Context, versionID string, sectionType string, ordinal int) (*postgres.ResourceSection, error)
	FindSectionByEntity(ctx context.Context, versionID string, entityName string) (*postgres.ResourceSection, error)
	ListSectionsForReading(ctx context.Context, versionID string, sectionType string) ([]postgres.ResourceSection, error)
}

// LocateSectionInput 描述一次 section 定位所需的消息与上下文。
type LocateSectionInput struct {
	ResourceID string
	Message    string
	Snapshot   *SessionContextSnapshot
}

// LocatedSection 表示已经定位到的活跃文件 section。
type LocatedSection struct {
	SectionID    string
	VersionID    string
	SectionType  string
	SectionOrder int
	Title        string
	EntityName   string
	Reason       string
}

// SectionLocator 负责把当前消息稳定映射到具体 section。
type SectionLocator struct {
	reader activeFileResourceReader
}

// NewSectionLocator 创建 section 定位器。
func NewSectionLocator(reader activeFileResourceReader) *SectionLocator {
	return &SectionLocator{reader: reader}
}

// Locate 按显式实体、ordinal、anaphora、版本回退的顺序定位当前消息对应的 section。
func (l *SectionLocator) Locate(ctx context.Context, input LocateSectionInput) (*LocatedSection, error) {
	if l == nil || l.reader == nil {
		return nil, nil
	}

	resourceID := strings.TrimSpace(input.ResourceID)
	message := strings.TrimSpace(input.Message)
	if resourceID == "" || message == "" {
		return nil, nil
	}

	version, err := l.reader.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, nil
	}

	if resolved := (ReferenceResolver{}).Resolve(message, input.Snapshot); resolved != nil {
		located, err := l.locateResolvedReference(ctx, version.ID, resolved)
		if err != nil {
			return nil, err
		}
		if located != nil {
			return located, nil
		}
	}

	ordinal := extractOrdinal(message)
	sectionType := inferOrdinalSectionType(message)
	if ordinal > 0 && sectionType != "" {
		section, err := l.reader.GetSectionByOrder(ctx, version.ID, sectionType, ordinal)
		if err != nil {
			return nil, err
		}
		if section != nil {
			return buildLocatedSection(section, "", "ordinal_lookup"), nil
		}
	}

	entityName := findExplicitEntityName(message, input.Snapshot)
	if strings.TrimSpace(entityName) != "" {
		section, err := l.reader.FindSectionByEntity(ctx, version.ID, entityName)
		if err != nil {
			return nil, err
		}
		if section != nil {
			return buildLocatedSection(section, entityName, "entity_lookup"), nil
		}
	}

	return nil, nil
}

func (l *SectionLocator) locateResolvedReference(ctx context.Context, versionID string, resolved *ResolvedReference) (*LocatedSection, error) {
	if resolved == nil {
		return nil, nil
	}

	if strings.TrimSpace(resolved.SectionID) != "" {
		section, err := l.reader.GetSectionByID(ctx, strings.TrimSpace(resolved.SectionID))
		if err != nil {
			return nil, err
		}
		if section != nil {
			return buildLocatedSection(section, resolved.EntityName, resolved.Reason), nil
		}

		return &LocatedSection{
			SectionID:   strings.TrimSpace(resolved.SectionID),
			VersionID:   versionID,
			SectionType: strings.TrimSpace(resolved.SectionType),
			EntityName:  strings.TrimSpace(resolved.EntityName),
			Reason:      resolved.Reason,
		}, nil
	}

	if strings.TrimSpace(resolved.EntityName) != "" {
		section, err := l.reader.FindSectionByEntity(ctx, versionID, resolved.EntityName)
		if err != nil {
			return nil, err
		}
		if section != nil {
			return buildLocatedSection(section, resolved.EntityName, resolved.Reason), nil
		}
	}

	return nil, nil
}

func buildLocatedSection(section *postgres.ResourceSection, entityName string, reason string) *LocatedSection {
	if section == nil {
		return nil
	}

	located := &LocatedSection{
		SectionID:    section.ID,
		VersionID:    section.VersionID,
		SectionType:  section.SectionType,
		SectionOrder: section.SectionOrder,
		Title:        section.Title,
		EntityName:   strings.TrimSpace(entityName),
		Reason:       reason,
	}
	if located.EntityName == "" && section.CanonicalEntityName != nil {
		located.EntityName = strings.TrimSpace(*section.CanonicalEntityName)
	}

	return located
}

func inferOrdinalSectionType(message string) string {
	switch {
	case strings.Contains(message, "项目"):
		return "project"
	case strings.Contains(message, "经历"):
		return "experience"
	case strings.Contains(message, "章节"), strings.Contains(message, "小节"), strings.Contains(message, "部分"):
		return "section"
	default:
		return ""
	}
}
