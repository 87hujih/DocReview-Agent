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
