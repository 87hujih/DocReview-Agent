package searchindex

import (
	"context"
	"reflect"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

type fakeVersionChunkReader struct {
	listCalls []string
	chunks    []postgres.ResourceChunk
	err       error
}

func (f *fakeVersionChunkReader) ListChunksByVersion(_ context.Context, versionID string) ([]postgres.ResourceChunk, error) {
	f.listCalls = append(f.listCalls, versionID)
	if f.err != nil {
		return nil, f.err
	}

	return append([]postgres.ResourceChunk(nil), f.chunks...), nil
}

type fakeChunkIndexClient struct {
	calls            []string
	deletedVersionID string
	upsertedDocs     []ChunkDocument
	ensureErr        error
	deleteErr        error
	bulkErr          error
}

func (f *fakeChunkIndexClient) EnsureChunkIndex(context.Context) error {
	f.calls = append(f.calls, "ensure")
	return f.ensureErr
}

func (f *fakeChunkIndexClient) DeleteVersionDocuments(_ context.Context, versionID string) error {
	f.calls = append(f.calls, "delete")
	f.deletedVersionID = versionID
	return f.deleteErr
}

func (f *fakeChunkIndexClient) BulkUpsertChunkDocuments(_ context.Context, docs []ChunkDocument) error {
	f.calls = append(f.calls, "bulk")
	f.upsertedDocs = append([]ChunkDocument(nil), docs...)
	return f.bulkErr
}

func TestSyncServiceSyncVersionDeletesThenUpsertsCurrentVersion(t *testing.T) {
	repo := &fakeVersionChunkReader{
		chunks: []postgres.ResourceChunk{
			{
				ID:            "chunk-1",
				ResourceID:    "resource-1",
				VersionID:     "version-1",
				SectionID:     "section-1",
				SectionType:   "project",
				ChunkRole:     "section_body",
				ChunkIndex:    0,
				SectionTitle:  "项目经验",
				Content:       "负责跨区域项目交付。",
				WindowGroupID: "window-1",
				PageStart:     1,
				PageEnd:       2,
			},
		},
	}
	client := &fakeChunkIndexClient{}
	service := NewSyncService(repo, client, "opensearch_bm25")

	if err := service.SyncVersion(context.Background(), "resource-1", "version-1"); err != nil {
		t.Fatalf("sync version: %v", err)
	}

	if !reflect.DeepEqual(repo.listCalls, []string{"version-1"}) {
		t.Fatalf("expected repo list calls %v, got %v", []string{"version-1"}, repo.listCalls)
	}
	if !reflect.DeepEqual(client.calls, []string{"ensure", "delete", "bulk"}) {
		t.Fatalf("expected client calls %v, got %v", []string{"ensure", "delete", "bulk"}, client.calls)
	}
	if client.deletedVersionID != "version-1" {
		t.Fatalf("expected deleted version id %q, got %q", "version-1", client.deletedVersionID)
	}
	if len(client.upsertedDocs) != 1 {
		t.Fatalf("expected 1 upserted doc, got %d", len(client.upsertedDocs))
	}
	if client.upsertedDocs[0].DocumentID != "chunk-1" {
		t.Fatalf("expected upserted doc id %q, got %q", "chunk-1", client.upsertedDocs[0].DocumentID)
	}
}

func TestSyncServiceNoopWhenLegacyBackendOrClientMissing(t *testing.T) {
	t.Run("legacy backend", func(t *testing.T) {
		repo := &fakeVersionChunkReader{
			chunks: []postgres.ResourceChunk{{ID: "chunk-1", VersionID: "version-1"}},
		}
		client := &fakeChunkIndexClient{}
		service := NewSyncService(repo, client, "postgres_legacy")

		if err := service.SyncVersion(context.Background(), "resource-1", "version-1"); err != nil {
			t.Fatalf("legacy sync should be noop, got %v", err)
		}
		if len(repo.listCalls) != 0 {
			t.Fatalf("expected legacy backend to skip repo call, got %v", repo.listCalls)
		}
		if len(client.calls) != 0 {
			t.Fatalf("expected legacy backend to skip client calls, got %v", client.calls)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		repo := &fakeVersionChunkReader{
			chunks: []postgres.ResourceChunk{{ID: "chunk-1", VersionID: "version-1"}},
		}
		service := NewSyncService(repo, nil, "opensearch_bm25")

		if err := service.SyncVersion(context.Background(), "resource-1", "version-1"); err != nil {
			t.Fatalf("nil client sync should be noop, got %v", err)
		}
		if len(repo.listCalls) != 0 {
			t.Fatalf("expected nil client to skip repo call, got %v", repo.listCalls)
		}
	})
}
