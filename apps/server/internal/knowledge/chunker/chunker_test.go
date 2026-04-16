package chunker

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/knowledge/sections"
)

// TestChunkMarkdownBasic 验证普通二级标题会被切成有序分块。
func TestChunkMarkdownBasic(t *testing.T) {
	input := strings.Join([]string{
		"# Title",
		"",
		"文档导语会被丢弃。",
		"",
		"## Section1",
		"第一部分内容。",
		"",
		"## Section2",
		"第二部分内容。",
	}, "\n")

	chunks := ChunkMarkdown(input)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	if chunks[0].SectionTitle != "Section1" {
		t.Fatalf("expected first section title %q, got %q", "Section1", chunks[0].SectionTitle)
	}

	if chunks[0].ChunkIndex != 0 {
		t.Fatalf("expected first chunk index 0, got %d", chunks[0].ChunkIndex)
	}

	if chunks[0].Content != "第一部分内容。" {
		t.Fatalf("expected first chunk content %q, got %q", "第一部分内容。", chunks[0].Content)
	}

	if chunks[1].SectionTitle != "Section2" {
		t.Fatalf("expected second section title %q, got %q", "Section2", chunks[1].SectionTitle)
	}

	if chunks[1].ChunkIndex != 1 {
		t.Fatalf("expected second chunk index 1, got %d", chunks[1].ChunkIndex)
	}

	if chunks[1].Content != "第二部分内容。" {
		t.Fatalf("expected second chunk content %q, got %q", "第二部分内容。", chunks[1].Content)
	}
}

// TestChunkMarkdownLongSection 验证超长章节会按段落拆成多个分块。
func TestChunkMarkdownLongSection(t *testing.T) {
	paragraph1 := strings.Repeat("第一段很长。", 40)
	paragraph2 := strings.Repeat("第二段很长。", 40)
	paragraph3 := strings.Repeat("第三段很长。", 40)

	input := strings.Join([]string{
		"# Title",
		"",
		"## LongSection",
		paragraph1,
		"",
		paragraph2,
		"",
		paragraph3,
	}, "\n")

	chunks := ChunkMarkdown(input)

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	for index, chunk := range chunks {
		if chunk.SectionTitle != "LongSection" {
			t.Fatalf("expected section title %q, got %q", "LongSection", chunk.SectionTitle)
		}

		if chunk.ChunkIndex != index {
			t.Fatalf("expected chunk index %d, got %d", index, chunk.ChunkIndex)
		}
	}
}

// TestChunkMarkdownEmpty 验证空内容不会产出分块。
func TestChunkMarkdownEmpty(t *testing.T) {
	chunks := ChunkMarkdown("")

	if len(chunks) != 0 {
		t.Fatalf("expected no chunks, got %d", len(chunks))
	}
}

func TestChunkMarkdownUsesWholeDocumentFallback(t *testing.T) {
	input := "这是没有二级标题的正文。"

	chunks := ChunkMarkdown(input)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d", len(chunks))
	}
	if chunks[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback section title %q, got %q", sections.WholeDocumentTitle, chunks[0].SectionTitle)
	}
	if chunks[0].Content != input {
		t.Fatalf("expected fallback content %q, got %q", input, chunks[0].Content)
	}
}
