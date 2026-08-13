package commit_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"agent_project/apps/server/internal/document/commit"
	"agent_project/apps/server/internal/document/importer"
	"agent_project/apps/server/internal/document/model"
	"agent_project/apps/server/internal/document/patch"
	"agent_project/apps/server/internal/document/validation"
)

// TestCommitRetryCreatesOneCompleteVersionBundle 验证对应场景下的正常路径与失败路径。
func TestCommitRetryCreatesOneCompleteVersionBundle(t *testing.T) {
	document := canonicalPDF(t)
	store := newMemoryStore(document)
	committer := newCommitter(t, store)
	input := commitInput(document, "commit-1", replaceFirstContent(document, "updated"))

	first, err := committer.Commit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := committer.Commit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.VersionID != second.VersionID {
		t.Fatalf("retry was not idempotent: %#v %#v", first, second)
	}
	versions, nodes, sections, chunks, outbox := store.counts()
	if versions != 1 || nodes != 3 || sections != 1 || chunks != 2 || outbox != 1 {
		t.Fatalf("incomplete/duplicate bundle: versions=%d nodes=%d sections=%d chunks=%d outbox=%d", versions, nodes, sections, chunks, outbox)
	}
}

// TestSameIdempotencyKeyWithDifferentPatchConflicts 验证对应场景下的正常路径与失败路径。
func TestSameIdempotencyKeyWithDifferentPatchConflicts(t *testing.T) {
	document := canonicalPDF(t)
	store := newMemoryStore(document)
	committer := newCommitter(t, store)
	if _, err := committer.Commit(context.Background(), commitInput(document, "commit-1", replaceFirstContent(document, "one"))); err != nil {
		t.Fatal(err)
	}
	_, err := committer.Commit(context.Background(), commitInput(store.current, "commit-1", replaceFirstContent(store.current, "two")))
	if !errors.Is(err, commit.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

// TestCommitPreservesSectionsChunksPagesAndMetadata 验证对应场景下的正常路径与失败路径。
func TestCommitPreservesSectionsChunksPagesAndMetadata(t *testing.T) {
	document := canonicalPDF(t)
	store := newMemoryStore(document)
	committer := newCommitter(t, store)
	result, err := committer.Commit(context.Background(), commitInput(document, "commit-1", replaceFirstContent(document, "updated")))
	if err != nil {
		t.Fatal(err)
	}
	bundle := store.bundles[result.VersionID]
	if bundle.Document.Metadata["classification"] != "internal" || len(bundle.Projection.Sections) != 1 || len(bundle.Projection.Chunks) != 2 {
		t.Fatalf("derived data or metadata lost: %#v", bundle)
	}
	if bundle.Projection.Chunks[0].PageStart == nil || *bundle.Projection.Chunks[0].PageStart != 1 || bundle.Projection.Chunks[1].PageEnd == nil || *bundle.Projection.Chunks[1].PageEnd != 2 {
		t.Fatalf("page mapping lost: %#v", bundle.Projection.Chunks)
	}
	for _, chunk := range bundle.Projection.Chunks {
		if chunk.EmbeddingStatus != "pending" || chunk.EmbeddingProfile == "" {
			t.Fatalf("embedding projection is half-complete: %#v", chunk)
		}
	}
}

// TestAtomicFailureLeavesNoVersionStructureProjectionOrOutbox 验证对应场景下的正常路径与失败路径。
func TestAtomicFailureLeavesNoVersionStructureProjectionOrOutbox(t *testing.T) {
	document := canonicalPDF(t)
	store := newMemoryStore(document)
	store.fail = errors.New("注入的 trans动作失败")
	committer := newCommitter(t, store)
	_, err := committer.Commit(context.Background(), commitInput(document, "commit-1", replaceFirstContent(document, "updated")))
	if err == nil {
		t.Fatal("expected commit failure")
	}
	versions, nodes, sections, chunks, outbox := store.counts()
	if versions+nodes+sections+chunks+outbox != 0 {
		t.Fatalf("partial transaction escaped: %d %d %d %d %d", versions, nodes, sections, chunks, outbox)
	}
}

// TestCommitRechecksBaseAndHashesAtAtomicBoundary 验证对应场景下的正常路径与失败路径。
func TestCommitRechecksBaseAndHashesAtAtomicBoundary(t *testing.T) {
	document := canonicalPDF(t)
	store := newMemoryStore(document)
	store.beforeAtomic = func() {
		store.current.VersionID = "version-raced"
		_ = model.Rehash(store.current)
	}
	committer := newCommitter(t, store)
	_, err := committer.Commit(context.Background(), commitInput(document, "commit-1", replaceFirstContent(document, "updated")))
	if !errors.Is(err, commit.ErrVersionConflict) {
		t.Fatalf("expected atomic base recheck conflict, got %v", err)
	}
}

// canonicalPDF 执行该函数负责的核心处理逻辑。
func canonicalPDF(t *testing.T) *model.Document {
	t.Helper()
	document, err := importer.NewPDF().Import(context.Background(), importer.Input{
		DocumentID: "resource-1", VersionID: "version-1", FileName: "a.pdf", Content: []byte("one\ftwo"), Metadata: map[string]any{"classification": "internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

// replaceFirstContent 执行该函数负责的核心处理逻辑。
func replaceFirstContent(document *model.Document, content string) patch.Set {
	node := model.Flatten(document.Root)[1]
	return patch.Set{SchemaVersion: patch.SchemaVersion, ResourceID: document.DocumentID, BaseVersionID: document.VersionID, EvidenceRefs: []string{}, Reason: "approved", Operations: []patch.Operation{{Op: patch.ReplaceNode, NodeID: node.NodeID, ExpectedHash: node.ContentHash, Content: &content}}}
}

// commitInput 执行该函数负责的核心处理逻辑。
func commitInput(document *model.Document, key string, set patch.Set) commit.Input {
	authorized := map[string]struct{}{}
	for _, node := range model.Flatten(document.Root) {
		authorized[node.NodeID] = struct{}{}
	}
	return commit.Input{WorkspaceID: "workspace-1", ResourceID: document.DocumentID, IdempotencyKey: key, Patch: set, AuthorizedNodeIDs: authorized, ActorID: "user-1"}
}

// newCommitter 执行该函数负责的核心处理逻辑。
func newCommitter(t *testing.T, store commit.Store) *commit.Committer {
	t.Helper()
	committer, err := commit.New(store, validation.New(), commit.Options{
		ProjectionProfile: model.ProjectionProfile{SchemaVersion: "1.0", ChunkProfile: "node-v1", EmbeddingProfile: "embedding-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return committer
}

type memoryStore struct {
	mu           sync.Mutex
	current      *model.Document
	commits      map[string]commit.AtomicResult
	patchHashes  map[string]string
	bundles      map[string]commit.Bundle
	outbox       map[string]struct{}
	fail         error
	beforeAtomic func()
}

// newMemoryStore 执行该函数负责的核心处理逻辑。
func newMemoryStore(document *model.Document) *memoryStore {
	clone, _ := model.Clone(document)
	return &memoryStore{current: clone, commits: map[string]commit.AtomicResult{}, patchHashes: map[string]string{}, bundles: map[string]commit.Bundle{}, outbox: map[string]struct{}{}}
}

// LoadSnapshot 按作用域读取并返回所需数据。
func (s *memoryStore) LoadSnapshot(_ context.Context, workspaceID, resourceID string) (validation.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone, _ := model.Clone(s.current)
	return validation.Snapshot{WorkspaceID: workspaceID, ResourceID: resourceID, CurrentVersionID: clone.VersionID, Document: clone}, nil
}

// GetCommit 按作用域读取并返回所需数据。
func (s *memoryStore) GetCommit(_ context.Context, workspaceID, idempotencyKey string) (*commit.StoredCommit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workspaceID + "\x00" + idempotencyKey
	result, exists := s.commits[key]
	if !exists {
		return nil, nil
	}
	return &commit.StoredCommit{PatchHash: s.patchHashes[key], Result: result}, nil
}

// CommitAtomic 执行该函数负责的核心处理逻辑。
func (s *memoryStore) CommitAtomic(_ context.Context, request commit.AtomicRequest) (commit.AtomicResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.commits[request.WorkspaceID+"\x00"+request.IdempotencyKey]; ok {
		if s.patchHashes[request.WorkspaceID+"\x00"+request.IdempotencyKey] != request.PatchHash {
			return commit.AtomicResult{}, commit.ErrIdempotencyConflict
		}
		existing.Created = false
		return existing, nil
	}
	if s.beforeAtomic != nil {
		callback := s.beforeAtomic
		s.beforeAtomic = nil
		callback()
	}
	if s.current.VersionID != request.BaseVersionID {
		return commit.AtomicResult{}, commit.ErrVersionConflict
	}
	current := map[string]*model.Node{}
	for _, node := range model.Flatten(s.current.Root) {
		current[node.NodeID] = node
	}
	for nodeID, hash := range request.ExpectedHashes {
		if current[nodeID] == nil || current[nodeID].ContentHash != hash {
			return commit.AtomicResult{}, commit.ErrHashConflict
		}
	}
	if s.fail != nil {
		return commit.AtomicResult{}, s.fail
	}
	result := commit.AtomicResult{ResourceID: request.ResourceID, VersionID: request.Bundle.Document.VersionID, OutboxID: "outbox-" + request.Bundle.Document.VersionID, Created: true}
	key := request.WorkspaceID + "\x00" + request.IdempotencyKey
	s.commits[key], s.patchHashes[key] = result, request.PatchHash
	s.bundles[result.VersionID] = request.Bundle
	s.outbox[result.OutboxID] = struct{}{}
	s.current, _ = model.Clone(request.Bundle.Document)
	return result, nil
}

// 数量 执行该函数负责的核心处理逻辑。
func (s *memoryStore) counts() (int, int, int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions, nodes, sections, chunks := len(s.bundles), 0, 0, 0
	for _, bundle := range s.bundles {
		nodes += len(model.Flatten(bundle.Document.Root))
		sections += len(bundle.Projection.Sections)
		chunks += len(bundle.Projection.Chunks)
	}
	return versions, nodes, sections, chunks, len(s.outbox)
}
