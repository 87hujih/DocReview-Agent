// Package documentcommit persists 一个 complete Canonical 文档 版本 bundle.
package documentcommit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	documentcommit "agent_project/apps/server/internal/document/commit"
	"agent_project/apps/server/internal/document/model"
	"agent_project/apps/server/internal/document/validation"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

// New 校验依赖并创建对应实例。
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// GetCommit 按作用域读取并返回所需数据。
func (r *Repository) GetCommit(ctx context.Context, workspaceID, idempotencyKey string) (*documentcommit.StoredCommit, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("文档 commit 数据库不能为空")
	}
	return getCommit(ctx, r.pool, workspaceID, idempotencyKey)
}

// LoadSnapshot 按作用域读取并返回所需数据。
func (r *Repository) LoadSnapshot(ctx context.Context, workspaceID, resourceID string) (validation.Snapshot, error) {
	if r == nil || r.pool == nil {
		return validation.Snapshot{}, fmt.Errorf("文档 commit 数据库不能为空")
	}
	var versionID string
	var astJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT versions.id, canonical.ast_json
		FROM resources AS resource
		JOIN resource_versions AS versions ON versions.resource_id = resource.id
		JOIN canonical_documents AS canonical ON canonical.version_id = versions.id
		WHERE resource.id = $1 AND resource.workspace_id = $2
		ORDER BY versions.version_number DESC
		LIMIT 1
	`, strings.TrimSpace(resourceID), strings.TrimSpace(workspaceID)).Scan(&versionID, &astJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return validation.Snapshot{}, fmt.Errorf("canonical 文档未找到")
		}
		return validation.Snapshot{}, err
	}
	var document model.Document
	if err := json.Unmarshal(astJSON, &document); err != nil {
		return validation.Snapshot{}, fmt.Errorf("解析 canonical AST：%w", err)
	}
	if document.VersionID != versionID || document.DocumentID != resourceID {
		return validation.Snapshot{}, fmt.Errorf("canonical AST 标识不匹配")
	}
	if err := model.Validate(&document); err != nil {
		return validation.Snapshot{}, fmt.Errorf("stored canonical AST 无效的：%w", err)
	}
	return validation.Snapshot{WorkspaceID: workspaceID, ResourceID: resourceID, CurrentVersionID: versionID, Document: &document}, nil
}

// CommitAtomic 执行该函数负责的核心处理逻辑。
func (r *Repository) CommitAtomic(ctx context.Context, request documentcommit.AtomicRequest) (result documentcommit.AtomicResult, resultErr error) {
	defer func() { resultErr = classifyTransactionError(resultErr) }()
	if r == nil || r.pool == nil {
		return documentcommit.AtomicResult{}, fmt.Errorf("文档 commit 数据库不能为空")
	}
	if err := validateAtomicRequest(request); err != nil {
		return documentcommit.AtomicResult{}, err
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return documentcommit.AtomicResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	lockKey := request.WorkspaceID + "\x00" + request.IdempotencyKey
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return documentcommit.AtomicResult{}, err
	}
	existing, err := getCommit(ctx, tx, request.WorkspaceID, request.IdempotencyKey)
	if err != nil {
		return documentcommit.AtomicResult{}, err
	}
	if existing != nil {
		if existing.PatchHash != request.PatchHash {
			return documentcommit.AtomicResult{}, documentcommit.ErrIdempotencyConflict
		}
		result := existing.Result
		result.Created = false
		if err := tx.Commit(ctx); err != nil {
			return documentcommit.AtomicResult{}, err
		}
		return result, nil
	}

	var currentVersionID string
	var nextVersionNumber int
	err = tx.QueryRow(ctx, `
		SELECT versions.id, versions.version_number + 1
		FROM resources AS resource
		JOIN resource_versions AS versions ON versions.resource_id = resource.id
		WHERE resource.id = $1 AND resource.workspace_id = $2
		ORDER BY versions.version_number DESC
		LIMIT 1
		FOR UPDATE OF resource, versions
	`, request.ResourceID, request.WorkspaceID).Scan(&currentVersionID, &nextVersionNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return documentcommit.AtomicResult{}, validationScopeError()
		}
		return documentcommit.AtomicResult{}, err
	}
	if currentVersionID != request.BaseVersionID {
		return documentcommit.AtomicResult{}, documentcommit.ErrVersionConflict
	}
	for nodeID, expectedHash := range request.ExpectedHashes {
		var currentHash string
		err := tx.QueryRow(ctx, `
			SELECT content_hash FROM document_nodes
			WHERE version_id = $1 AND resource_id = $2 AND workspace_id = $3 AND node_id = $4
			FOR UPDATE
		`, request.BaseVersionID, request.ResourceID, request.WorkspaceID, nodeID).Scan(&currentHash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return documentcommit.AtomicResult{}, documentcommit.ErrHashConflict
			}
			return documentcommit.AtomicResult{}, err
		}
		if currentHash != expectedHash {
			return documentcommit.AtomicResult{}, documentcommit.ErrHashConflict
		}
	}

	if err := insertVersionBundle(ctx, tx, request, nextVersionNumber); err != nil {
		return documentcommit.AtomicResult{}, err
	}
	outboxID, err := insertOutbox(ctx, tx, request)
	if err != nil {
		return documentcommit.AtomicResult{}, err
	}
	patchJSON, err := json.Marshal(request.Bundle.Patch)
	if err != nil {
		return documentcommit.AtomicResult{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO document_patch_commits (
			workspace_id, resource_id, idempotency_key, patch_hash, patch_schema_version,
			patch_json, base_version_id, new_version_id, outbox_event_id, actor_id
		) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10)
	`, request.WorkspaceID, request.ResourceID, request.IdempotencyKey, request.PatchHash,
		request.Bundle.Patch.SchemaVersion, patchJSON, request.BaseVersionID, request.Bundle.Document.VersionID, outboxID, request.Bundle.ActorID)
	if err != nil {
		return documentcommit.AtomicResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return documentcommit.AtomicResult{}, err
	}
	return documentcommit.AtomicResult{ResourceID: request.ResourceID, VersionID: request.Bundle.Document.VersionID, OutboxID: outboxID, Created: true}, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// getCommit 执行该函数负责的核心处理逻辑。
func getCommit(ctx context.Context, query rowQuerier, workspaceID, idempotencyKey string) (*documentcommit.StoredCommit, error) {
	var patchHash, resourceID, versionID, outboxID string
	err := query.QueryRow(ctx, `
		SELECT patch_hash, resource_id, new_version_id, outbox_event_id
		FROM document_patch_commits
		WHERE workspace_id = $1 AND idempotency_key = $2
	`, strings.TrimSpace(workspaceID), strings.TrimSpace(idempotencyKey)).Scan(&patchHash, &resourceID, &versionID, &outboxID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &documentcommit.StoredCommit{PatchHash: patchHash, Result: documentcommit.AtomicResult{ResourceID: resourceID, VersionID: versionID, OutboxID: outboxID}}, nil
}

// insertVersionBundle 执行该函数负责的核心处理逻辑。
func insertVersionBundle(ctx context.Context, tx pgx.Tx, request documentcommit.AtomicRequest, versionNumber int) error {
	document := request.Bundle.Document
	astJSON, err := json.Marshal(document)
	if err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(document.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO resource_versions (
			id, resource_id, version_number, content, source, canonical_schema_version, renderer_profile, embedding_profile
		) VALUES ($1,$2,$3,$4,'agent_canonical_patch',$5,$6,$7)
	`, document.VersionID, request.ResourceID, versionNumber, request.Bundle.LegacyContent,
		document.SchemaVersion, request.Bundle.RendererProfile, request.Bundle.Projection.Profile.EmbeddingProfile)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO canonical_documents (
			workspace_id, resource_id, version_id, document_id, root_node_id, schema_version,
			source_format, content_hash, ast_json, metadata_json, renderer_profile,
			chunk_profile, embedding_profile, projection_status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11,$12,$13,'pending')
	`, request.WorkspaceID, request.ResourceID, document.VersionID, document.DocumentID, document.Root.NodeID,
		document.SchemaVersion, document.SourceFormat, document.ContentHash, astJSON, metadataJSON,
		request.Bundle.RendererProfile, request.Bundle.Projection.Profile.ChunkProfile, request.Bundle.Projection.Profile.EmbeddingProfile)
	if err != nil {
		return err
	}
	if err := insertNodes(ctx, tx, request); err != nil {
		return err
	}
	sectionIDs, err := insertSections(ctx, tx, request)
	if err != nil {
		return err
	}
	if err := insertChunks(ctx, tx, request, sectionIDs); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE resources SET metadata_json = metadata_json || $2::jsonb, updated_at = now() WHERE id = $1`, request.ResourceID, metadataJSON)
	return err
}

// insertNodes 执行该函数负责的核心处理逻辑。
func insertNodes(ctx context.Context, tx pgx.Tx, request documentcommit.AtomicRequest) error {
	type entry struct {
		node     *model.Node
		parentID *string
		order    int
	}
	entries := make([]entry, 0)
	var walk func(*model.Node, *string, int)
	walk = func(node *model.Node, parentID *string, order int) {
		entries = append(entries, entry{node: node, parentID: parentID, order: order})
		parent := node.NodeID
		for childOrder, child := range node.Children {
			walk(child, &parent, childOrder)
		}
	}
	walk(request.Bundle.Document.Root, nil, 0)
	for _, item := range entries {
		attributes, _ := json.Marshal(item.node.Attributes)
		source, _ := json.Marshal(item.node.SourceLocation)
		pages, _ := json.Marshal(item.node.PageMapping)
		metadata, _ := json.Marshal(item.node.Metadata)
		_, err := tx.Exec(ctx, `
			INSERT INTO document_nodes (
				workspace_id, resource_id, version_id, node_id, parent_node_id, sibling_order, node_type,
				attributes_json, content, source_location_json, page_mapping_json, metadata_json, content_hash
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10::jsonb,$11::jsonb,$12::jsonb,$13)
		`, request.WorkspaceID, request.ResourceID, request.Bundle.Document.VersionID, item.node.NodeID,
			item.parentID, item.order, item.node.Type, attributes, item.node.Content, source, pages, metadata, item.node.ContentHash)
		if err != nil {
			return err
		}
		mappings := item.node.PageMapping
		if len(mappings) == 0 {
			if err := insertSourceMapping(ctx, tx, request, item.node, 0, nil, item.node.SourceLocation.StartOffset, item.node.SourceLocation.EndOffset); err != nil {
				return err
			}
		} else {
			for mappingOrder, mapping := range mappings {
				page := mapping.Page
				if err := insertSourceMapping(ctx, tx, request, item.node, mappingOrder, &page, mapping.StartOffset, mapping.EndOffset); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// insertSourceMapping 执行该函数负责的核心处理逻辑。
func insertSourceMapping(ctx context.Context, tx pgx.Tx, request documentcommit.AtomicRequest, node *model.Node, order int, page *int, start, end int) error {
	source, _ := json.Marshal(node.SourceLocation)
	_, err := tx.Exec(ctx, `
		INSERT INTO document_node_source_mappings (
			workspace_id, resource_id, version_id, node_id, mapping_order, source_json, page_number, start_offset, end_offset
		) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)
	`, request.WorkspaceID, request.ResourceID, request.Bundle.Document.VersionID, node.NodeID, order, source, page, start, end)
	return err
}

// insertSections 执行该函数负责的核心处理逻辑。
func insertSections(ctx context.Context, tx pgx.Tx, request documentcommit.AtomicRequest) (map[string]string, error) {
	result := make(map[string]string, len(request.Bundle.Projection.Sections))
	for _, section := range request.Bundle.Projection.Sections {
		metadata, _ := json.Marshal(section.Metadata)
		var sectionID string
		err := tx.QueryRow(ctx, `
			INSERT INTO resource_sections (
				resource_id, version_id, section_key, section_type, section_order, title, aliases_json,
				summary, content, page_start, page_end, metadata_json, canonical_node_id
			) VALUES ($1,$2,$3,'canonical',$4,$5,'[]'::jsonb,'',$6,$7,$8,$9::jsonb,$10)
			RETURNING id
		`, request.ResourceID, request.Bundle.Document.VersionID, section.SectionKey, section.Order, section.Title,
			section.Content, section.PageStart, section.PageEnd, metadata, section.NodeID).Scan(&sectionID)
		if err != nil {
			return nil, err
		}
		result[section.SectionKey] = sectionID
	}
	return result, nil
}

// insertChunks 执行该函数负责的核心处理逻辑。
func insertChunks(ctx context.Context, tx pgx.Tx, request documentcommit.AtomicRequest, sectionIDs map[string]string) error {
	for _, chunk := range request.Bundle.Projection.Chunks {
		metadata, _ := json.Marshal(chunk.Metadata)
		pageStart, pageEnd := pageValue(chunk.PageStart), pageValue(chunk.PageEnd)
		_, err := tx.Exec(ctx, `
			INSERT INTO resource_chunks (
				resource_id, version_id, chunk_index, section_title, content, embedding, section_id,
				section_type, chunk_role, window_group_id, order_in_section, page_start, page_end,
				metadata_json, canonical_node_id, content_hash, chunk_profile, embedding_profile, embedding_status
			) VALUES ($1,$2,$3,'',$4,NULL,$5,'canonical','node',$6,$3,$7,$8,$9::jsonb,$10,$11,$12,$13,$14)
		`, request.ResourceID, request.Bundle.Document.VersionID, chunk.ChunkIndex, chunk.Content,
			sectionIDs[chunk.SectionKey], chunk.SectionKey, pageStart, pageEnd, metadata, chunk.NodeID,
			chunk.ContentHash, chunk.ChunkProfile, chunk.EmbeddingProfile, chunk.EmbeddingStatus)
		if err != nil {
			return err
		}
	}
	return nil
}

// insertOutbox 执行该函数负责的核心处理逻辑。
func insertOutbox(ctx context.Context, tx pgx.Tx, request documentcommit.AtomicRequest) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"workspace_id": request.WorkspaceID, "resource_id": request.ResourceID,
		"base_version_id": request.BaseVersionID, "version_id": request.Bundle.Document.VersionID,
		"content_hash": request.Bundle.Document.ContentHash, "embedding_profile": request.Bundle.Projection.Profile.EmbeddingProfile,
		"projection_status": "pending",
	})
	idempotencyKey := "document-patch-commit:" + request.WorkspaceID + ":" + request.IdempotencyKey
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, idempotency_key, payload_json)
		VALUES ('document_version',$1,'document.version.committed',$2,$3::jsonb)
		RETURNING id
	`, request.Bundle.Document.VersionID, idempotencyKey, payload).Scan(&id)
	return id, err
}

// validateAtomicRequest 校验输入及领域约束。
func validateAtomicRequest(request documentcommit.AtomicRequest) error {
	if !request.ValidatedByCommitter() {
		return fmt.Errorf("atomic 请求 was not prepared 由 the canonical 提交器")
	}
	if strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.ResourceID) == "" || strings.TrimSpace(request.BaseVersionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.PatchHash) == "" {
		return fmt.Errorf("文档 atomic commit 作用域、base、idempotency、和哈希不能为空")
	}
	if request.Bundle.Document == nil || request.Bundle.Document.DocumentID != request.ResourceID || request.Bundle.Document.VersionID == request.BaseVersionID {
		return fmt.Errorf("new canonical 文档标识无效")
	}
	if err := model.Validate(request.Bundle.Document); err != nil {
		return err
	}
	return nil
}

// pageValue 执行该函数负责的核心处理逻辑。
func pageValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// validationScopeError 执行该函数负责的核心处理逻辑。
func validationScopeError() error { return fmt.Errorf("资源作用域 denied") }

// classifyTransactionError 执行该函数负责的核心处理逻辑。
func classifyTransactionError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && (databaseError.Code == "40001" || databaseError.Code == "40P01") {
		return fmt.Errorf("%w：%v", documentcommit.ErrRetryableCommit, err)
	}
	return err
}

var _ documentcommit.Store = (*Repository)(nil)
