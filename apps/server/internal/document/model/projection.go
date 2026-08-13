package model

import (
	"fmt"
	"strings"
)

type ProjectionProfile struct {
	SchemaVersion    string
	ChunkProfile     string
	EmbeddingProfile string
}

type SectionProjection struct {
	SectionKey string
	NodeID     string
	Order      int
	Title      string
	Content    string
	PageStart  *int
	PageEnd    *int
	Metadata   map[string]any
}

type ChunkProjection struct {
	ChunkIndex       int
	SectionKey       string
	NodeID           string
	NodeType         NodeType
	Content          string
	ContentHash      string
	PageStart        *int
	PageEnd          *int
	Metadata         map[string]any
	ChunkProfile     string
	EmbeddingProfile string
	EmbeddingStatus  string
}

type Projection struct {
	Sections []SectionProjection
	Chunks   []ChunkProjection
	Metadata map[string]any
	Profile  ProjectionProfile
}

// Derive 为 the only Section/Chunk/embedding-fact 投影 来自 canonical nodes.
func Derive(document *Document, profile ProjectionProfile) (Projection, error) {
	if err := Validate(document); err != nil {
		return Projection{}, err
	}
	if strings.TrimSpace(profile.SchemaVersion) == "" || strings.TrimSpace(profile.ChunkProfile) == "" || strings.TrimSpace(profile.EmbeddingProfile) == "" {
		return Projection{}, fmt.Errorf("投影 schema、chunk、和嵌入 profiles 不能为空")
	}
	result := Projection{
		Sections: []SectionProjection{}, Chunks: []ChunkProjection{}, Metadata: cloneJSONMap(document.Metadata), Profile: profile,
	}
	var current *SectionProjection
	for _, node := range Flatten(document.Root)[1:] {
		if node.Type == NodeHeading {
			section := SectionProjection{
				SectionKey: node.NodeID, NodeID: node.NodeID, Order: len(result.Sections), Title: node.Content,
				Metadata: cloneJSONMap(node.Metadata),
			}
			section.PageStart, section.PageEnd = nodePages(node)
			result.Sections = append(result.Sections, section)
			current = &result.Sections[len(result.Sections)-1]
		}
		if current == nil {
			section := SectionProjection{SectionKey: document.Root.NodeID, NodeID: document.Root.NodeID, Order: 0, Metadata: cloneJSONMap(document.Metadata)}
			result.Sections = append(result.Sections, section)
			current = &result.Sections[0]
		}
		if node.Content != "" {
			if current.Content == "" {
				current.Content = node.Content
			} else {
				current.Content += "\n\n" + node.Content
			}
			pageStart, pageEnd := nodePages(node)
			mergePages(current, pageStart, pageEnd)
			result.Chunks = append(result.Chunks, ChunkProjection{
				ChunkIndex: len(result.Chunks), SectionKey: current.SectionKey, NodeID: node.NodeID, NodeType: node.Type,
				Content: node.Content, ContentHash: node.ContentHash, PageStart: pageStart, PageEnd: pageEnd,
				Metadata: cloneJSONMap(node.Metadata), ChunkProfile: profile.ChunkProfile,
				EmbeddingProfile: profile.EmbeddingProfile, EmbeddingStatus: "pending",
			})
		}
	}
	return result, nil
}

// nodePages 执行该函数负责的核心处理逻辑。
func nodePages(node *Node) (*int, *int) {
	if len(node.PageMapping) == 0 {
		return nil, nil
	}
	start, end := node.PageMapping[0].Page, node.PageMapping[0].Page
	for _, mapping := range node.PageMapping[1:] {
		if mapping.Page < start {
			start = mapping.Page
		}
		if mapping.Page > end {
			end = mapping.Page
		}
	}
	return &start, &end
}

// mergePages 执行该函数负责的核心处理逻辑。
func mergePages(section *SectionProjection, start, end *int) {
	if start != nil && (section.PageStart == nil || *start < *section.PageStart) {
		value := *start
		section.PageStart = &value
	}
	if end != nil && (section.PageEnd == nil || *end > *section.PageEnd) {
		value := *end
		section.PageEnd = &value
	}
}

// cloneJSONMap 执行该函数负责的核心处理逻辑。
func cloneJSONMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
