package assistant

import "strings"

// GroundedAnalysisInput 描述分析型回复需要消费的真实 section 正文。
type GroundedAnalysisInput struct {
	SectionID       string
	SectionType     string
	SectionTitle    string
	SectionOrder    int
	SectionText     string
	UserInstruction string
}

// GroundedAnalysisInputBuilder 负责把定位结果和正文读取结果组装成分析输入。
type GroundedAnalysisInputBuilder struct{}

// Build 从已定位 section 和正文读取结果生成 grounded analysis 输入。
func (b GroundedAnalysisInputBuilder) Build(
	located *LocatedSection,
	readResult *SectionReadResult,
	userInstruction string,
) *GroundedAnalysisInput {
	if readResult == nil {
		return nil
	}

	sectionID := strings.TrimSpace(readResult.SectionID)
	if sectionID == "" && located != nil {
		sectionID = strings.TrimSpace(located.SectionID)
	}
	if sectionID == "" {
		return nil
	}

	sectionType := strings.TrimSpace(readResult.SectionType)
	if sectionType == "" && located != nil {
		sectionType = strings.TrimSpace(located.SectionType)
	}

	sectionTitle := strings.TrimSpace(readResult.Title)
	if sectionTitle == "" && located != nil {
		sectionTitle = strings.TrimSpace(located.Title)
	}

	sectionOrder := readResult.SectionOrder
	if sectionOrder == 0 && located != nil {
		sectionOrder = located.SectionOrder
	}

	sectionText := strings.TrimSpace(readResult.Content)
	if sectionText == "" {
		return nil
	}

	return &GroundedAnalysisInput{
		SectionID:       sectionID,
		SectionType:     sectionType,
		SectionTitle:    sectionTitle,
		SectionOrder:    sectionOrder,
		SectionText:     sectionText,
		UserInstruction: strings.TrimSpace(userInstruction),
	}
}
