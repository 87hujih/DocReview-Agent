package searchindex

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientEnsureChunkIndexCreatesIndexWhenMissing(t *testing.T) {
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))

		switch len(requests) {
		case 1:
			if r.Method != http.MethodHead || r.URL.Path != "/resource_chunks_v1" {
				t.Fatalf("unexpected probe request: %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(http.StatusNotFound)
		case 2:
			if r.Method != http.MethodPut || r.URL.Path != "/resource_chunks_v1" {
				t.Fatalf("unexpected create request: %s %s", r.Method, r.URL.Path)
			}
			if !strings.Contains(string(body), `"version_id"`) {
				t.Fatalf("expected index mapping to include version_id, got %s", string(body))
			}
			if !strings.Contains(string(body), `"section_title"`) {
				t.Fatalf("expected index mapping to include section_title, got %s", string(body))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"acknowledged":true}`))
		default:
			t.Fatalf("unexpected request count %d", len(requests))
		}
	}))
	defer server.Close()

	client := NewClient(ClientOptions{
		BaseURL:     server.URL,
		IndexChunks: "resource_chunks_v1",
	})

	if err := client.EnsureChunkIndex(context.Background()); err != nil {
		t.Fatalf("ensure chunk index: %v", err)
	}
}

func TestClientDeleteVersionDocumentsUsesVersionScopedDeleteQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/resource_chunks_v1/_delete_by_query" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		username, password, ok := r.BasicAuth()
		if !ok {
			t.Fatal("expected basic auth on delete-by-query request")
		}
		if username != "search-user" || password != "search-pass" {
			t.Fatalf("unexpected credentials %q/%q", username, password)
		}

		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"version_id":"version-1"`) {
			t.Fatalf("expected delete-by-query body to filter version-1, got %s", string(body))
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"deleted":3}`))
	}))
	defer server.Close()

	client := NewClient(ClientOptions{
		BaseURL:     server.URL,
		IndexChunks: "resource_chunks_v1",
		Username:    "search-user",
		Password:    "search-pass",
	})

	if err := client.DeleteVersionDocuments(context.Background(), "version-1"); err != nil {
		t.Fatalf("delete version documents: %v", err)
	}
}

func TestClientBulkUpsertChunkDocumentsUsesBulkAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_bulk" {
			t.Fatalf("unexpected bulk request: %s %s", r.Method, r.URL.Path)
		}
		if contentType := r.Header.Get("Content-Type"); !strings.Contains(contentType, "application/x-ndjson") {
			t.Fatalf("expected ndjson content type, got %q", contentType)
		}

		body, _ := io.ReadAll(r.Body)
		payload := string(body)
		if !strings.Contains(payload, `"index":{"_index":"resource_chunks_v1","_id":"chunk-1"}`) {
			t.Fatalf("expected bulk action metadata for chunk-1, got %s", payload)
		}
		if !strings.Contains(payload, `"chunk_id":"chunk-1"`) {
			t.Fatalf("expected bulk payload to include chunk body, got %s", payload)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errors":false,"items":[{"index":{"status":201}}]}`))
	}))
	defer server.Close()

	client := NewClient(ClientOptions{
		BaseURL:     server.URL,
		IndexChunks: "resource_chunks_v1",
	})

	err := client.BulkUpsertChunkDocuments(context.Background(), []ChunkDocument{
		{
			DocumentID:   "chunk-1",
			ResourceID:   "resource-1",
			VersionID:    "version-1",
			ChunkID:      "chunk-1",
			ChunkRole:    "section_body",
			ChunkIndex:   0,
			SectionTitle: "项目经验",
			Content:      "负责跨区域项目交付。",
		},
	})
	if err != nil {
		t.Fatalf("bulk upsert chunk documents: %v", err)
	}
}
