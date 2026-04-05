package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedDoesNotPanicWhenHTTPClientIsUnsetInThirdPartyConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("expected request path /embeddings, got %s", r.URL.Path)
		}

		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"object":    "embedding",
					"embedding": []float32{0.1, 0.2, 0.3},
					"index":     0,
				},
			},
			"model": "test-model",
			"usage": map[string]any{
				"prompt_tokens":     1,
				"completion_tokens": 0,
				"total_tokens":      1,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	emb, err := New(context.Background(), server.URL, "test-key", "test-model", 3)
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}

	vectors, err := emb.Embed(context.Background(), []string{"hello"})
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
