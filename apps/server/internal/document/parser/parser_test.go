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

func TestParsePassesThroughTextFiles(t *testing.T) {
	parser, err := New(Options{})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	result, err := parser.Parse(context.Background(), Input{
		FileName: "学生守则.md",
		Content:  []byte("# 学生守则\n这里是正文。"),
	})
	if err != nil {
		t.Fatalf("parse text file: %v", err)
	}

	if result.Text != "# 学生守则\n这里是正文。" {
		t.Fatalf("expected original markdown content, got %q", result.Text)
	}
}

func TestTextParserReturnsStructuredDocument(t *testing.T) {
	parser, err := New(Options{Mode: ModeText})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	result, err := parser.Parse(context.Background(), Input{
		FileName: "resume.md",
		Content:  []byte("# 标题\n\n项目描述"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.Document == nil {
		t.Fatal("expected structured document")
	}
	if result.Document.SourceFormat != "md" {
		t.Fatalf("expected source format %q, got %q", "md", result.Document.SourceFormat)
	}
	if len(result.Document.Blocks) < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", len(result.Document.Blocks))
	}
	if result.Document.Blocks[0].Type != BlockHeading {
		t.Fatalf("expected first block heading, got %s", result.Document.Blocks[0].Type)
	}
	if result.Document.Blocks[1].Type != BlockParagraph {
		t.Fatalf("expected second block paragraph, got %s", result.Document.Blocks[1].Type)
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

func TestParseUsesTikaForDocumentFiles(t *testing.T) {
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
		_, _ = w.Write([]byte("解析后的正文"))
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

	if result.Text != "解析后的正文" {
		t.Fatalf("expected tika text output, got %q", result.Text)
	}
}

func TestTikaParserReturnsStructuredDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("项目经验\nCampusHub 校园活动平台\n项目描述：面向校园活动场景的平台"))
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
		FileName: "resume.pdf",
		Content:  []byte("binary-pdf-content"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.Document == nil {
		t.Fatal("expected structured document")
	}
	if result.Document.SourceFormat != "pdf" {
		t.Fatalf("expected source format %q, got %q", "pdf", result.Document.SourceFormat)
	}
	if len(result.Document.Blocks) == 0 {
		t.Fatal("expected tika blocks")
	}
}

func TestTikaParserMarksSuspiciousShortBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("项目\n项目描述：\n工作内容：\n教育\n技能"))
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
		FileName: "resume.docx",
		Content:  []byte("binary-docx-content"),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.Document == nil {
		t.Fatal("expected structured document")
	}
	if !containsQualityFlag(result.Document.Metadata.QualityFlags, "too_many_short_blocks") &&
		!containsQualityFlag(result.Document.Metadata.QualityFlags, "layout_lost") {
		t.Fatalf("expected suspicious quality flags, got %#v", result.Document.Metadata.QualityFlags)
	}
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

func containsQualityFlag(flags []string, target string) bool {
	for _, flag := range flags {
		if flag == target {
			return true
		}
	}

	return false
}
