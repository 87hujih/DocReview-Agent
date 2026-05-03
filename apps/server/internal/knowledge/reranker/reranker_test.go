package reranker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestRerankSendsExpectedRequestAndParsesResponse 验证`rerankSendsExpectedRequestAnd`在约束校验路径下的行为，防止同类回归。
func TestRerankSendsExpectedRequestAndParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		if r.URL.Path != "/v1/rerank" {
			t.Fatalf("expected path /v1/rerank, got %s", r.URL.Path)
		}

		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer auth header, got %q", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		if got := body["model"]; got != "Qwen/Qwen3-Reranker-8B" {
			t.Fatalf("expected model %q, got %#v", "Qwen/Qwen3-Reranker-8B", got)
		}

		if got := body["query"]; got != "考勤" {
			t.Fatalf("expected query %q, got %#v", "考勤", got)
		}

		documents, ok := body["documents"].([]any)
		if !ok || len(documents) != 2 {
			t.Fatalf("expected 2 documents, got %#v", body["documents"])
		}

		if got := body["top_n"]; got != float64(2) {
			t.Fatalf("expected top_n 2, got %#v", got)
		}

		if got := body["return_documents"]; got != false {
			t.Fatalf("expected return_documents false, got %#v", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.91},{"index":0,"relevance_score":0.42}]}`))
	}))
	defer server.Close()

	client := New(server.URL+"/v1", "test-key", "Qwen/Qwen3-Reranker-8B")
	results, err := client.Rerank(context.Background(), "考勤", []string{"文档一", "文档二"}, 2)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Index != 1 || results[0].RelevanceScore != 0.91 {
		t.Fatalf("unexpected first result: %#v", results[0])
	}

	if results[1].Index != 0 || results[1].RelevanceScore != 0.42 {
		t.Fatalf("unexpected second result: %#v", results[1])
	}
}

// TestRerankSkipsEmptyDocuments 验证`rerank`在跳过或空操作路径下的行为，防止同类回归。
func TestRerankSkipsEmptyDocuments(t *testing.T) {
	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL+"/v1", "test-key", "Qwen/Qwen3-Reranker-8B")
	results, err := client.Rerank(context.Background(), "考勤", nil, 3)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected no rerank results, got %d", len(results))
	}

	if called.Load() {
		t.Fatal("expected rerank to skip network call for empty documents")
	}
}
