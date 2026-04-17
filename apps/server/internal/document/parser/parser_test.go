package parser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTextParserReturnsStructuredDocument(t *testing.T) {
	parser, err := New(Options{})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	result, err := parser.Parse(context.Background(), Input{
		FileName: "学生守则.md",
		Content:  []byte("# 学生守则\n这里是正文。\n\n第二段说明。"),
	})
	if err != nil {
		t.Fatalf("parse text file: %v", err)
	}

	if result.Text != "# 学生守则\n这里是正文。\n\n第二段说明。" {
		t.Fatalf("expected original markdown content, got %q", result.Text)
	}
	if result.Document == nil {
		t.Fatal("expected structured document for markdown parse")
	}
	if result.Document.SourceFormat != "markdown" {
		t.Fatalf("expected source format markdown, got %q", result.Document.SourceFormat)
	}
	if len(result.Document.Blocks) != 3 {
		t.Fatalf("expected 3 markdown blocks, got %d", len(result.Document.Blocks))
	}
	if result.Document.Blocks[0].Type != BlockHeading || result.Document.Blocks[0].Text != "学生守则" {
		t.Fatalf("expected first block to be heading '学生守则', got %#v", result.Document.Blocks[0])
	}
	if result.Document.Blocks[1].Type != BlockParagraph || result.Document.Blocks[1].Text != "这里是正文。" {
		t.Fatalf("expected second block to be paragraph, got %#v", result.Document.Blocks[1])
	}
	if result.Document.Blocks[2].Type != BlockParagraph || result.Document.Blocks[2].Text != "第二段说明。" {
		t.Fatalf("expected third block to be paragraph, got %#v", result.Document.Blocks[2])
	}
}

func TestTextParserCapabilityOnlyAcceptsTextFiles(t *testing.T) {
	parser, err := New(Options{Mode: ModeText})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	if !parser.SupportsFileName("学生守则.md") || !parser.SupportsFileName("说明.txt") {
		t.Fatal("expected text mode to support md and txt")
	}
	if parser.SupportsFileName("合同.pdf") || parser.SupportsFileName("会议纪要.docx") {
		t.Fatal("expected text mode to reject document formats that need Tika")
	}

	message := parser.UnsupportedFileMessage("合同.pdf")
	if !strings.Contains(message, "仅支持 md、txt") || !strings.Contains(message, "Tika") {
		t.Fatalf("expected Tika guidance message, got %q", message)
	}
}

func TestTikaParserCapabilityAcceptsDocumentFiles(t *testing.T) {
	parser, err := New(Options{
		Mode:    ModeTika,
		TikaURL: "http://127.0.0.1:9998",
	})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	for _, fileName := range []string{"a.md", "a.txt", "a.doc", "a.docx", "a.pdf", "a.rtf", "a.odt"} {
		if !parser.SupportsFileName(fileName) {
			t.Fatalf("expected tika mode to support %s", fileName)
		}
	}
	if parser.SupportsFileName("archive.zip") {
		t.Fatal("expected tika mode to reject unknown extension")
	}
}

func TestTikaParserReturnsStructuredDocument(t *testing.T) {
	var receivedPath string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		receivedBody = body

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("项目经历\nCampusHub 校园活动平台\n负责活动发布与报名。"))
	}))
	defer server.Close()

	parser, err := New(Options{
		Mode:        ModeTika,
		TikaURL:     server.URL,
		TikaTimeout: 2 * time.Second,
		HTTPClient:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	result, err := parser.Parse(context.Background(), Input{
		FileName: "会议纪要.docx",
		Content:  []byte("binary-docx-content"),
	})
	if err != nil {
		t.Fatalf("parse document file: %v", err)
	}

	if receivedPath != "/tika" {
		t.Fatalf("expected tika endpoint /tika, got %q", receivedPath)
	}

	if string(receivedBody) != "binary-docx-content" {
		t.Fatalf("expected original file bytes to be sent to tika, got %q", string(receivedBody))
	}

	if result.Text != "项目经历\nCampusHub 校园活动平台\n负责活动发布与报名。" {
		t.Fatalf("expected tika text output, got %q", result.Text)
	}
	if result.Document == nil {
		t.Fatal("expected structured document for tika parse")
	}
	if result.Document.SourceFormat != "docx" {
		t.Fatalf("expected source format docx, got %q", result.Document.SourceFormat)
	}
	if len(result.Document.Blocks) < 2 {
		t.Fatalf("expected at least 2 blocks from tika parse, got %d", len(result.Document.Blocks))
	}
	if result.Document.Blocks[0].Type != BlockParagraph {
		t.Fatalf("expected first tika block to be paragraph, got %#v", result.Document.Blocks[0])
	}
}

func TestTikaParserMarksQualityFlags(t *testing.T) {
	t.Run("too many short blocks", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("项目\n项目描述\n工作内容\n技术栈\n成果"))
		}))
		defer server.Close()

		parser, err := New(Options{
			Mode:        ModeTika,
			TikaURL:     server.URL,
			TikaTimeout: 2 * time.Second,
			HTTPClient:  server.Client(),
		})
		if err != nil {
			t.Fatalf("new parser: %v", err)
		}

		result, err := parser.Parse(context.Background(), Input{
			FileName: "简历.pdf",
			Content:  []byte("binary-pdf-content"),
		})
		if err != nil {
			t.Fatalf("parse short-fragment pdf: %v", err)
		}
		if result.Document == nil {
			t.Fatal("expected structured document for short-fragment pdf")
		}
		if !result.Document.QualityFlags.Has("too_many_short_blocks") {
			t.Fatalf("expected too_many_short_blocks flag, got %#v", result.Document.QualityFlags)
		}
	})

	t.Run("requires ocr when text is empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("   \n\t"))
		}))
		defer server.Close()

		parser, err := New(Options{
			Mode:        ModeTika,
			TikaURL:     server.URL,
			TikaTimeout: 2 * time.Second,
			HTTPClient:  server.Client(),
		})
		if err != nil {
			t.Fatalf("new parser: %v", err)
		}

		result, err := parser.Parse(context.Background(), Input{
			FileName: "扫描件.pdf",
			Content:  []byte("binary-pdf-content"),
		})
		if err != nil {
			t.Fatalf("parse empty pdf: %v", err)
		}
		if result.Document == nil {
			t.Fatal("expected structured document for empty pdf")
		}
		if !result.Document.QualityFlags.Has("requires_ocr") {
			t.Fatalf("expected requires_ocr flag, got %#v", result.Document.QualityFlags)
		}
		if !result.Document.QualityFlags.Has("text_empty") {
			t.Fatalf("expected text_empty flag, got %#v", result.Document.QualityFlags)
		}
	})
}

func TestParseRejectsUnknownExtensions(t *testing.T) {
	parser, err := New(Options{})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	_, err = parser.Parse(context.Background(), Input{
		FileName: "archive.zip",
		Content:  []byte("zip-bytes"),
	})
	if err == nil {
		t.Fatal("expected unsupported extension error, got nil")
	}

	if !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("expected unsupported extension error, got %v", err)
	}
}

func TestParseReturnsErrorWhenTikaFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "tika failed", http.StatusBadGateway)
	}))
	defer server.Close()

	parser, err := New(Options{
		Mode:        ModeTika,
		TikaURL:     server.URL,
		TikaTimeout: 2 * time.Second,
		HTTPClient:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	_, err = parser.Parse(context.Background(), Input{
		FileName: "公告.pdf",
		Content:  []byte("binary-pdf-content"),
	})
	if err == nil {
		t.Fatal("expected tika error, got nil")
	}

	if !strings.Contains(err.Error(), "Tika") {
		t.Fatalf("expected tika error, got %v", err)
	}
}
