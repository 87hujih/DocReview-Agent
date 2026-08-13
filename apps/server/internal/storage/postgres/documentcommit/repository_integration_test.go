package documentcommit_test

import (
	"context"
	"encoding/json"
	"testing"

	documentcommit "agent_project/apps/server/internal/document/commit"
	"agent_project/apps/server/internal/document/importer"
	"agent_project/apps/server/internal/document/model"
	"agent_project/apps/server/internal/document/patch"
	"agent_project/apps/server/internal/document/validation"
	"agent_project/apps/server/internal/storage/postgres"
	postgresdocumentcommit "agent_project/apps/server/internal/storage/postgres/documentcommit"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5"
)

// TestCanonicalCommitRoundTripIsAtomicAndIdempotent 验证对应场景下的正常路径与失败路径。
func TestCanonicalCommitRoundTripIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := postgrestest.NewIsolatedPool(t, ctx, "canonical_document_commit", postgres.NewPool, postgres.RunMigrations)
	document, workspaceID := seedCanonicalDocument(t, ctx, pool)
	repository := postgresdocumentcommit.New(pool)
	committer, err := documentcommit.New(repository, validation.New(), documentcommit.Options{
		ProjectionProfile: model.ProjectionProfile{SchemaVersion: "1.0", ChunkProfile: "node-v1", EmbeddingProfile: "embedding-v1"},
		IDGenerator:       func() (string, error) { return "00000000-0000-4000-8000-000000000099", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	node := model.Flatten(document.Root)[1]
	content := "updated"
	set := patch.Set{SchemaVersion: patch.SchemaVersion, ResourceID: document.DocumentID, BaseVersionID: document.VersionID, EvidenceRefs: []string{}, Reason: "approved", Operations: []patch.Operation{{Op: patch.ReplaceNode, NodeID: node.NodeID, ExpectedHash: node.ContentHash, Content: &content}}}
	authorized := map[string]struct{}{}
	for _, candidate := range model.Flatten(document.Root) {
		authorized[candidate.NodeID] = struct{}{}
	}
	input := documentcommit.Input{WorkspaceID: workspaceID, ResourceID: document.DocumentID, IdempotencyKey: "commit-1", ActorID: "user:user-1", Patch: set, AuthorizedNodeIDs: authorized}
	first, err := committer.Commit(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := committer.Commit(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.VersionID != second.VersionID {
		t.Fatalf("idempotency failed: %#v %#v", first, second)
	}

	var versions, canonical, nodes, sections, chunks, commits, outbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_versions WHERE id=$1`, first.VersionID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM canonical_documents WHERE version_id=$1`, first.VersionID).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM document_nodes WHERE version_id=$1`, first.VersionID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_sections WHERE version_id=$1`, first.VersionID).Scan(&sections); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_chunks WHERE version_id=$1`, first.VersionID).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM document_patch_commits WHERE new_version_id=$1`, first.VersionID).Scan(&commits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE id=$1`, first.OutboxID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || canonical != 1 || nodes != 3 || sections != 1 || chunks != 2 || commits != 1 || outbox != 1 {
		t.Fatalf("partial bundle: %d %d %d %d %d %d %d", versions, canonical, nodes, sections, chunks, commits, outbox)
	}
}

// seedCanonicalDocument 执行该函数负责的核心处理逻辑。
func seedCanonicalDocument(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}) (*model.Document, string) {
	t.Helper()
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var organizationID, workspaceID, resourceID, versionID string
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (slug,name) VALUES ('org-canonical','Org') RETURNING id`).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO workspaces (organization_id,slug,name) VALUES ($1,'workspace-canonical','Workspace') RETURNING id`, organizationID).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO resources (title,workspace_id) VALUES ('Document',$1) RETURNING id`, workspaceID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO resource_versions (resource_id,version_number,content,source,canonical_schema_version,renderer_profile,embedding_profile) VALUES ($1,1,'one\ftwo','original','1.0','pdf-canonical-v1','embedding-v1') RETURNING id`, resourceID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	document, err := importer.NewPDF().Import(ctx, importer.Input{DocumentID: resourceID, VersionID: versionID, FileName: "a.pdf", Content: []byte("one\ftwo"), Metadata: map[string]any{"classification": "internal"}})
	if err != nil {
		t.Fatal(err)
	}
	ast, _ := json.Marshal(document)
	metadata, _ := json.Marshal(document.Metadata)
	_, err = tx.Exec(ctx, `INSERT INTO canonical_documents (workspace_id,resource_id,version_id,document_id,root_node_id,schema_version,source_format,content_hash,ast_json,metadata_json,renderer_profile,chunk_profile,embedding_profile) VALUES ($1,$2,$3,$2,$4,'1.0','pdf',$5,$6::jsonb,$7::jsonb,'pdf-canonical-v1','node-v1','embedding-v1')`, workspaceID, resourceID, versionID, document.Root.NodeID, document.ContentHash, ast, metadata)
	if err != nil {
		t.Fatal(err)
	}
	var insert func(*model.Node, *string, int)
	insert = func(node *model.Node, parent *string, order int) {
		attributes, _ := json.Marshal(node.Attributes)
		source, _ := json.Marshal(node.SourceLocation)
		pages, _ := json.Marshal(node.PageMapping)
		meta, _ := json.Marshal(node.Metadata)
		_, insertErr := tx.Exec(ctx, `INSERT INTO document_nodes (workspace_id,resource_id,version_id,node_id,parent_node_id,sibling_order,node_type,attributes_json,content,source_location_json,page_mapping_json,metadata_json,content_hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10::jsonb,$11::jsonb,$12::jsonb,$13)`, workspaceID, resourceID, versionID, node.NodeID, parent, order, node.Type, attributes, node.Content, source, pages, meta, node.ContentHash)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		parentID := node.NodeID
		for childOrder, child := range node.Children {
			insert(child, &parentID, childOrder)
		}
	}
	insert(document.Root, nil, 0)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return document, workspaceID
}
