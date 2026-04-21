package embedder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEmbedUsesNonNilHTTPClient 验证显式注入 HTTP client 后不会触发上游 typed nil 问题。
func TestEmbedUsesNonNilHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("expected embeddings path, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer server.Close()

	emb, err := New(context.Background(), server.URL+"/v1", "test-key", "test-model", 3)
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}

	vectors, err := emb.Embed(context.Background(), []string{"考勤"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	if len(vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(vectors))
	}

	if len(vectors[0]) != 3 {
		t.Fatalf("expected vector length 3, got %d", len(vectors[0]))
	}
}

// TestEmbedWrapsDiagnosticsForProviderErrors 验证 embeddings 上游报错时会补齐定位问题所需的诊断信息。
func TestEmbedWrapsDiagnosticsForProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-siliconcloud-trace-id", "trace-embed-123")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":20015,"message":"The parameter is invalid. Please check again.","data":null}`))
	}))
	defer server.Close()

	emb, err := New(context.Background(), server.URL+"/v1", "test-key", "test-model", 3)
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}

	_, err = emb.Embed(context.Background(), []string{"考勤"})
	if err == nil {
		t.Fatal("expected provider error")
	}

	message := err.Error()
	for _, fragment := range []string{
		"embedding 请求失败",
		"model=test-model",
		"dimensions=3",
		"input_count=1",
		"total_runes=2",
		"max_runes=2",
		"trace_id=trace-embed-123",
		"status code: 400",
	} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("expected error to contain %q, got %q", fragment, message)
		}
	}
}
