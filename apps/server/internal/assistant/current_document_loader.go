package assistant

import (
	"context"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// currentDocumentLoader 定义当前文件 canonical 文档对象的装载能力。
type currentDocumentLoader interface {
	Load(ctx context.Context, resource *resourceContext) (*CurrentDocument, error)
}

// CurrentDocumentLoader 负责从当前活跃资源构建一等 current document 视图。
type CurrentDocumentLoader struct {
	reader         currentFileSectionLookup
	outlineBuilder *DocumentOutlineBuilder
}

// NewCurrentDocumentLoader 构造当前文档装载器。
func NewCurrentDocumentLoader(reader currentFileSectionLookup) *CurrentDocumentLoader {
	return &CurrentDocumentLoader{
		reader:         reader,
		outlineBuilder: NewDocumentOutlineBuilder(),
	}
}

// Load 按 active resource -> current version -> ordered sections 组装当前文档。
func (l *CurrentDocumentLoader) Load(ctx context.Context, resource *resourceContext) (*CurrentDocument, error) {
	if l == nil || l.reader == nil || resource == nil {
		return nil, nil
	}

	resourceID := strings.TrimSpace(resource.ID)
	if resourceID == "" {
		return nil, nil
	}

	version, err := l.reader.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, nil
	}

	fullText := strings.TrimSpace(version.Content)
	if fullText == "" {
		return nil, nil
	}

	sections, err := l.reader.ListSectionsForReading(ctx, strings.TrimSpace(version.ID), "")
	if err != nil {
		return nil, err
	}

	structure, err := l.reader.GetVersionStructureByVersionID(ctx, strings.TrimSpace(version.ID))
	if err != nil {
		return nil, err
	}

	var structureJSON []byte
	if structure != nil {
		structureJSON = append([]byte(nil), structure.DocumentJSON...)
	}

	outline := l.outlineBuilder.Build(BuildDocumentOutlineInput{
		VersionID:     strings.TrimSpace(version.ID),
		FullText:      fullText,
		Sections:      sections,
		StructureJSON: structureJSON,
	})

	return &CurrentDocument{
		ResourceID: resourceID,
		VersionID:  strings.TrimSpace(version.ID),
		Title:      strings.TrimSpace(resource.Title),
		SourceType: strings.TrimSpace(resource.Source),
		FullText:   fullText,
		Sections:   append([]postgres.ResourceSection(nil), sections...),
		Outline:    outline,
		Ready:      true,
	}, nil
}
