package assistant

import (
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// CurrentDocument 表示当前对话里默认可见的 canonical 文档对象。
type CurrentDocument struct {
	ResourceID string
	VersionID  string
	Title      string
	SourceType string
	FullText   string
	Sections   []postgres.ResourceSection
	Outline    []OutlineNode
	Ready      bool
}

// cloneCurrentDocument 复制当前文档对象，避免后续阶段误改原始上下文。
func cloneCurrentDocument(document *CurrentDocument) *CurrentDocument {
	if document == nil {
		return nil
	}

	cloned := *document
	if len(document.Sections) > 0 {
		cloned.Sections = append([]postgres.ResourceSection(nil), document.Sections...)
	}
	cloned.Outline = cloneOutlineNodes(document.Outline)
	cloned.FullText = strings.TrimSpace(document.FullText)

	return &cloned
}
