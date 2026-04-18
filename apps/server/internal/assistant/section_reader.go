package assistant

import (
	"context"
)

const defaultSectionExcerptRunes = 600

// ExcerptPolicy 描述 section 摘录时的长度约束。
type ExcerptPolicy struct {
	MaxRunes int
}

// SectionReadResult 表示一次 section 读取结果。
type SectionReadResult struct {
	SectionID    string
	SectionType  string
	SectionOrder int
	Title        string
	Content      string
	IsExcerpt    bool
	HasMore      bool
}

// SectionReader 负责从 resource_sections 真源读取 section 正文。
type SectionReader struct {
	reader activeFileResourceReader
}

// NewSectionReader 创建 section 阅读器。
func NewSectionReader(reader activeFileResourceReader) *SectionReader {
	return &SectionReader{reader: reader}
}

// ReadSectionFull 读取完整 section 正文。
func (r *SectionReader) ReadSectionFull(ctx context.Context, sectionID string) (*SectionReadResult, error) {
	if r == nil || r.reader == nil {
		return nil, nil
	}

	section, err := r.reader.GetSectionByID(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	if section == nil {
		return nil, nil
	}

	return &SectionReadResult{
		SectionID:    section.ID,
		SectionType:  section.SectionType,
		SectionOrder: section.SectionOrder,
		Title:        section.Title,
		Content:      section.Content,
	}, nil
}

// ReadSectionExcerpt 读取连续 section 摘录，不拼接 citation 片段。
func (r *SectionReader) ReadSectionExcerpt(ctx context.Context, sectionID string, policy ExcerptPolicy) (*SectionReadResult, error) {
	full, err := r.ReadSectionFull(ctx, sectionID)
	if err != nil || full == nil {
		return full, err
	}

	maxRunes := policy.MaxRunes
	if maxRunes <= 0 {
		maxRunes = defaultSectionExcerptRunes
	}

	contentRunes := []rune(full.Content)
	if len(contentRunes) <= maxRunes {
		return full, nil
	}

	excerpt := *full
	excerpt.Content = string(contentRunes[:maxRunes])
	excerpt.IsExcerpt = true
	excerpt.HasMore = true
	return &excerpt, nil
}
