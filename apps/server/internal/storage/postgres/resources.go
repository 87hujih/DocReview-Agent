package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	documentnormalize "agent_project/apps/server/internal/document/normalize"
	"agent_project/apps/server/internal/knowledge/sections"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// ResourceRepo 封装资源、版本和可检索分块的 PostgreSQL 访问。
type ResourceRepo struct {
	pool *pgxpool.Pool
}

// Resource 是对 API 消费方暴露的顶层文档记录。
type Resource struct {
	ID         string
	Title      string
	SourceType string
	SourceRef  *string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ResourceVersion 保存资源内容的不可变快照。
type ResourceVersion struct {
	ID            string
	ResourceID    string
	VersionNumber int
	Content       string
	Source        string
	CreatedAt     time.Time
}

// ResourceChunk 是从某个资源版本切分出的、带 embedding 的检索单元。
type ResourceChunk struct {
	ID             string
	ResourceID     string
	VersionID      string
	ChunkIndex     int
	SectionTitle   string
	Content        string
	Embedding      pgvector.Vector
	SectionID      *string
	SectionType    *string
	ChunkRole      *string
	WindowGroupID  *string
	OrderInSection *int
	PageStart      *int
	PageEnd        *int
	MetadataJSON   []byte
	CreatedAt      time.Time
}

// ResourceChunkInput 表示写入资源图或重建版本索引时预先算好的单个 chunk。
type ResourceChunkInput struct {
	ChunkIndex     int
	SectionTitle   string
	Content        string
	Embedding      pgvector.Vector
	SectionID      *string
	SectionType    *string
	ChunkRole      *string
	WindowGroupID  *string
	OrderInSection *int
	PageStart      *int
	PageEnd        *int
	MetadataJSON   []byte
}

// CreateDocumentGraphParams 描述一次原子写入资源、版本和 chunks 所需的数据。
type CreateDocumentGraphParams struct {
	ResourceID    string
	Title         string
	SourceType    string
	SourceRef     *string
	VersionNumber int
	VersionSource string
	Content       string
	Chunks        []ResourceChunkInput
}

// NewResourceRepo 使用给定连接池创建仓储实例。
func NewResourceRepo(pool *pgxpool.Pool) *ResourceRepo {
	return &ResourceRepo{pool: pool}
}

// List 按创建时间倒序返回资源列表，供资源浏览页使用。
func (r *ResourceRepo) List(ctx context.Context) ([]Resource, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, title, source_type, source_ref, created_at, updated_at
		FROM resources
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []Resource
	for rows.Next() {
		resource, err := scanResource(rows)
		if err != nil {
			return nil, err
		}

		resources = append(resources, resource)
	}

	return resources, rows.Err()
}

// GetByID 在资源不存在时返回 nil，而不是把 pgx.ErrNoRows 直接暴露给上层。
func (r *ResourceRepo) GetByID(ctx context.Context, id string) (*Resource, error) {
	resource, err := scanResource(r.pool.QueryRow(ctx, `
		SELECT id, title, source_type, source_ref, created_at, updated_at
		FROM resources
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &resource, nil
}

// Create 新增一条顶层资源记录。
func (r *ResourceRepo) Create(ctx context.Context, title string, sourceType string) (*Resource, error) {
	return r.CreateWithSourceRef(ctx, title, sourceType, nil)
}

// CreateWithSourceRef 新增一条带 source_ref 的顶层资源记录。
func (r *ResourceRepo) CreateWithSourceRef(ctx context.Context, title string, sourceType string, sourceRef *string) (*Resource, error) {
	resource, err := scanResource(r.pool.QueryRow(ctx, `
		INSERT INTO resources (title, source_type, source_ref)
		VALUES ($1, $2, $3)
		RETURNING id, title, source_type, source_ref, created_at, updated_at
	`, title, sourceType, sourceRef))
	if err != nil {
		return nil, err
	}

	return &resource, nil
}

// CreateVersion 为已有资源追加一个版本记录。
func (r *ResourceRepo) CreateVersion(ctx context.Context, resourceID string, versionNumber int, content string, source string) (*ResourceVersion, error) {
	version, err := scanResourceVersion(r.pool.QueryRow(ctx, `
		INSERT INTO resource_versions (resource_id, version_number, content, source)
		VALUES ($1, $2, $3, $4)
		RETURNING id, resource_id, version_number, content, source, created_at
	`, resourceID, versionNumber, content, source))
	if err != nil {
		return nil, err
	}

	return &version, nil
}

// GetCurrentVersion 返回 version_number 最大的版本；如果不存在则返回 nil。
func (r *ResourceRepo) GetCurrentVersion(ctx context.Context, resourceID string) (*ResourceVersion, error) {
	version, err := scanResourceVersion(r.pool.QueryRow(ctx, `
		SELECT id, resource_id, version_number, content, source, created_at
		FROM resource_versions
		WHERE resource_id = $1
		ORDER BY version_number DESC
		LIMIT 1
	`, resourceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &version, nil
}

// GetVersionByID 按主键读取单个资源版本，不存在时返回 nil。
func (r *ResourceRepo) GetVersionByID(ctx context.Context, versionID string) (*ResourceVersion, error) {
	version, err := scanResourceVersion(r.pool.QueryRow(ctx, `
		SELECT id, resource_id, version_number, content, source, created_at
		FROM resource_versions
		WHERE id = $1
	`, versionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &version, nil
}

// ListSectionsByVersion 返回某个资源版本下的全部逻辑 sections。
func (r *ResourceRepo) ListSectionsByVersion(ctx context.Context, versionID string) ([]ResourceSection, error) {
	return NewResourceStructureRepo(r.pool).ListSectionsByVersion(ctx, versionID)
}

// GetSectionByID 按主键读取单个逻辑 section，不存在时返回 nil。
func (r *ResourceRepo) GetSectionByID(ctx context.Context, sectionID string) (*ResourceSection, error) {
	return NewResourceStructureRepo(r.pool).GetSectionByID(ctx, sectionID)
}

// GetSectionByOrder 按 section 类型和顺序读取单个逻辑 section，不存在时返回 nil。
func (r *ResourceRepo) GetSectionByOrder(ctx context.Context, versionID string, sectionType string, ordinal int) (*ResourceSection, error) {
	return NewResourceStructureRepo(r.pool).GetSectionByOrder(ctx, versionID, sectionType, ordinal)
}

// FindSectionByEntity 按 canonical entity 或 alias 读取单个逻辑 section，不存在时返回 nil。
func (r *ResourceRepo) FindSectionByEntity(ctx context.Context, versionID string, entityName string) (*ResourceSection, error) {
	return NewResourceStructureRepo(r.pool).FindSectionByEntity(ctx, versionID, entityName)
}

// ListSectionsForReading 返回 assistant 当前文件直接阅读所需的有序 section 视图。
func (r *ResourceRepo) ListSectionsForReading(ctx context.Context, versionID string, sectionType string) ([]ResourceSection, error) {
	return NewResourceStructureRepo(r.pool).ListSectionsForReading(ctx, versionID, sectionType)
}

// GetVersionStructureByVersionID 返回指定版本的结构化文档 JSON。
func (r *ResourceRepo) GetVersionStructureByVersionID(ctx context.Context, versionID string) (*ResourceVersionStructure, error) {
	return NewResourceStructureRepo(r.pool).GetVersionStructureByVersionID(ctx, versionID)
}

// CountChunksByVersion 返回指定版本当前已有的 chunk 数。
func (r *ResourceRepo) CountChunksByVersion(ctx context.Context, versionID string) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM resource_chunks
		WHERE version_id = $1
	`, versionID).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

// ListChunksByVersion 按 chunk_index 顺序返回某个版本下的全部 grounded chunks。
func (r *ResourceRepo) ListChunksByVersion(ctx context.Context, versionID string) ([]ResourceChunk, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		WHERE version_id = $1
		ORDER BY chunk_index ASC, created_at ASC
	`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// UpdateSourceRef 为资源回填或更新来源引用。
func (r *ResourceRepo) UpdateSourceRef(ctx context.Context, resourceID string, sourceRef *string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE resources
		SET source_ref = $2,
		    updated_at = now()
		WHERE id = $1
	`, resourceID, sourceRef)
	return err
}

// CreateChunk 持久化一个可检索分块，并把生成出的 ID 和时间戳回填到入参结构体。
func (r *ResourceRepo) CreateChunk(ctx context.Context, chunk *ResourceChunk) error {
	normalizeChunkForInsert(chunk)

	return r.pool.QueryRow(ctx, `
		INSERT INTO resource_chunks (
		    resource_id,
		    version_id,
		    chunk_index,
		    section_title,
		    content,
		    embedding,
		    section_id,
		    section_type,
		    chunk_role,
		    window_group_id,
		    order_in_section,
		    page_start,
		    page_end,
		    metadata_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)
		RETURNING id, created_at
	`,
		chunk.ResourceID,
		chunk.VersionID,
		chunk.ChunkIndex,
		chunk.SectionTitle,
		chunk.Content,
		chunk.Embedding,
		chunk.SectionID,
		chunk.SectionType,
		chunk.ChunkRole,
		chunk.WindowGroupID,
		chunk.OrderInSection,
		chunk.PageStart,
		chunk.PageEnd,
		normalizeJSONArgument(chunk.MetadataJSON, `{}`),
	).Scan(&chunk.ID, &chunk.CreatedAt)
}

// CreateDocumentGraph 在单事务里写入资源、版本和全部 chunks，避免残留半成品资源。
func (r *ResourceRepo) CreateDocumentGraph(ctx context.Context, params CreateDocumentGraphParams) (*Resource, *ResourceVersion, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var (
		resource      Resource
		version       ResourceVersion
		versionNumber = params.VersionNumber
	)
	if versionNumber <= 0 {
		versionNumber = 1
	}

	if strings.TrimSpace(params.ResourceID) == "" {
		resource, err = scanResource(tx.QueryRow(ctx, `
			INSERT INTO resources (title, source_type, source_ref)
			VALUES ($1, $2, $3)
			RETURNING id, title, source_type, source_ref, created_at, updated_at
		`, params.Title, params.SourceType, params.SourceRef))
		if err != nil {
			return nil, nil, err
		}
	} else {
		resource, err = scanResource(tx.QueryRow(ctx, `
			SELECT id, title, source_type, source_ref, created_at, updated_at
			FROM resources
			WHERE id = $1
			FOR UPDATE
		`, params.ResourceID))
		if err != nil {
			return nil, nil, err
		}
	}

	version, err = scanResourceVersion(tx.QueryRow(ctx, `
		INSERT INTO resource_versions (resource_id, version_number, content, source)
		VALUES ($1, $2, $3, $4)
		RETURNING id, resource_id, version_number, content, source, created_at
	`, resource.ID, versionNumber, params.Content, params.VersionSource))
	if err != nil {
		return nil, nil, err
	}

	for _, chunk := range params.Chunks {
		if err := createChunkTx(ctx, tx, &ResourceChunk{
			ResourceID:     resource.ID,
			VersionID:      version.ID,
			ChunkIndex:     chunk.ChunkIndex,
			SectionTitle:   chunk.SectionTitle,
			Content:        chunk.Content,
			Embedding:      chunk.Embedding,
			SectionID:      chunk.SectionID,
			SectionType:    chunk.SectionType,
			ChunkRole:      chunk.ChunkRole,
			WindowGroupID:  chunk.WindowGroupID,
			OrderInSection: chunk.OrderInSection,
			PageStart:      chunk.PageStart,
			PageEnd:        chunk.PageEnd,
			MetadataJSON:   chunk.MetadataJSON,
		}); err != nil {
			return nil, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	return &resource, &version, nil
}

// ReplaceVersionChunks 以“先删后建”的方式重建一个版本的全部分块。
func (r *ResourceRepo) ReplaceVersionChunks(ctx context.Context, versionID string, resourceID string, chunks []ResourceChunkInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		DELETE FROM resource_chunks
		WHERE version_id = $1
	`, versionID); err != nil {
		return err
	}

	for _, chunk := range chunks {
		if err := createChunkTx(ctx, tx, &ResourceChunk{
			ResourceID:     resourceID,
			VersionID:      versionID,
			ChunkIndex:     chunk.ChunkIndex,
			SectionTitle:   chunk.SectionTitle,
			Content:        chunk.Content,
			Embedding:      chunk.Embedding,
			SectionID:      chunk.SectionID,
			SectionType:    chunk.SectionType,
			ChunkRole:      chunk.ChunkRole,
			WindowGroupID:  chunk.WindowGroupID,
			OrderInSection: chunk.OrderInSection,
			PageStart:      chunk.PageStart,
			PageEnd:        chunk.PageEnd,
			MetadataJSON:   chunk.MetadataJSON,
		}); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// SearchChunks 使用 pgvector 距离排序，在全部资源范围内执行语义检索。
func (r *ResourceRepo) SearchChunks(ctx context.Context, embedding pgvector.Vector, limit int) ([]ResourceChunk, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		ORDER BY embedding <=> $1
		LIMIT $2
	`, embedding, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// SearchChunksByResource 把语义检索范围收敛到单个资源。
func (r *ResourceRepo) SearchChunksByResource(ctx context.Context, embedding pgvector.Vector, limit int, resourceID string) ([]ResourceChunk, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		WHERE resource_id = $2
		ORDER BY embedding <=> $1
		LIMIT $3
	`, embedding, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// SearchChunksByVersion 把语义检索范围收敛到单个资源版本。
func (r *ResourceRepo) SearchChunksByVersion(ctx context.Context, embedding pgvector.Vector, limit int, versionID string) ([]ResourceChunk, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		WHERE version_id = $2
		ORDER BY embedding <=> $1
		LIMIT $3
	`, embedding, versionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// SearchChunksLexical 使用 section_title + content 的 trigram / 子串匹配执行词法候选召回。
func (r *ResourceRepo) SearchChunksLexical(ctx context.Context, query string, limit int) ([]ResourceChunk, error) {
	normalizedQuery := normalizeLexicalQuery(query)
	if normalizedQuery == "" || limit <= 0 {
		return []ResourceChunk{}, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		WHERE lower(coalesce(section_title, '') || ' ' || content) LIKE '%' || $1 || '%'
		   OR lower(coalesce(section_title, '') || ' ' || content) % $1
		ORDER BY
			CASE
				WHEN lower(coalesce(section_title, '') || ' ' || content) LIKE '%' || $1 || '%' THEN 1
				ELSE 0
			END DESC,
			similarity(lower(coalesce(section_title, '') || ' ' || content), $1) DESC,
			chunk_index ASC
		LIMIT $2
	`, normalizedQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// SearchChunksLexicalByResource 把词法候选召回范围限制在单个资源内。
func (r *ResourceRepo) SearchChunksLexicalByResource(ctx context.Context, query string, limit int, resourceID string) ([]ResourceChunk, error) {
	normalizedQuery := normalizeLexicalQuery(query)
	if normalizedQuery == "" || limit <= 0 {
		return []ResourceChunk{}, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		WHERE resource_id = $2
		  AND (
			lower(coalesce(section_title, '') || ' ' || content) LIKE '%' || $1 || '%'
			OR lower(coalesce(section_title, '') || ' ' || content) % $1
		  )
		ORDER BY
			CASE
				WHEN lower(coalesce(section_title, '') || ' ' || content) LIKE '%' || $1 || '%' THEN 1
				ELSE 0
			END DESC,
			similarity(lower(coalesce(section_title, '') || ' ' || content), $1) DESC,
			chunk_index ASC
		LIMIT $3
	`, normalizedQuery, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// SearchChunksLexicalByVersion 把词法候选召回范围限制在单个资源版本内。
func (r *ResourceRepo) SearchChunksLexicalByVersion(ctx context.Context, query string, limit int, versionID string) ([]ResourceChunk, error) {
	normalizedQuery := normalizeLexicalQuery(query)
	if normalizedQuery == "" || limit <= 0 {
		return []ResourceChunk{}, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       chunk_index,
		       section_title,
		       content,
		       embedding,
		       section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_chunks
		WHERE version_id = $2
		  AND (
			lower(coalesce(section_title, '') || ' ' || content) LIKE '%' || $1 || '%'
			OR lower(coalesce(section_title, '') || ' ' || content) % $1
		  )
		ORDER BY
			CASE
				WHEN lower(coalesce(section_title, '') || ' ' || content) LIKE '%' || $1 || '%' THEN 1
				ELSE 0
			END DESC,
			similarity(lower(coalesce(section_title, '') || ' ' || content), $1) DESC,
			chunk_index ASC
		LIMIT $3
	`, normalizedQuery, versionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceChunks(rows)
}

// collectResourceChunks 把查询结果逐行扫描为 ResourceChunk 切片。
func collectResourceChunks(rows pgx.Rows) ([]ResourceChunk, error) {
	var chunks []ResourceChunk
	for rows.Next() {
		chunk, err := scanResourceChunk(rows)
		if err != nil {
			return nil, err
		}

		chunks = append(chunks, chunk)
	}

	return chunks, rows.Err()
}

// createChunkTx 在事务内写入单个 chunk 及其 grounded 字段，收口资源仓储的插入细节。
func createChunkTx(ctx context.Context, tx pgx.Tx, chunk *ResourceChunk) error {
	normalizeChunkForInsert(chunk)

	return tx.QueryRow(ctx, `
		INSERT INTO resource_chunks (
		    resource_id,
		    version_id,
		    chunk_index,
		    section_title,
		    content,
		    embedding,
		    section_id,
		    section_type,
		    chunk_role,
		    window_group_id,
		    order_in_section,
		    page_start,
		    page_end,
		    metadata_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)
		RETURNING id, created_at
	`,
		chunk.ResourceID,
		chunk.VersionID,
		chunk.ChunkIndex,
		chunk.SectionTitle,
		chunk.Content,
		chunk.Embedding,
		chunk.SectionID,
		chunk.SectionType,
		chunk.ChunkRole,
		chunk.WindowGroupID,
		chunk.OrderInSection,
		chunk.PageStart,
		chunk.PageEnd,
		normalizeJSONArgument(chunk.MetadataJSON, `{}`),
	).Scan(&chunk.ID, &chunk.CreatedAt)
}

// scanResource 从单行结果中解析 Resource。
func scanResource(row pgx.Row) (Resource, error) {
	var resource Resource

	err := row.Scan(
		&resource.ID,
		&resource.Title,
		&resource.SourceType,
		&resource.SourceRef,
		&resource.CreatedAt,
		&resource.UpdatedAt,
	)
	if err != nil {
		return Resource{}, err
	}

	return resource, nil
}

// scanResourceVersion 从单行结果中解析 ResourceVersion。
func scanResourceVersion(row pgx.Row) (ResourceVersion, error) {
	var version ResourceVersion

	err := row.Scan(
		&version.ID,
		&version.ResourceID,
		&version.VersionNumber,
		&version.Content,
		&version.Source,
		&version.CreatedAt,
	)
	if err != nil {
		return ResourceVersion{}, err
	}

	return version, nil
}

// scanResourceChunk 从单行结果中解析 ResourceChunk。
func scanResourceChunk(row pgx.Row) (ResourceChunk, error) {
	var chunk ResourceChunk

	err := row.Scan(
		&chunk.ID,
		&chunk.ResourceID,
		&chunk.VersionID,
		&chunk.ChunkIndex,
		&chunk.SectionTitle,
		&chunk.Content,
		&chunk.Embedding,
		&chunk.SectionID,
		&chunk.SectionType,
		&chunk.ChunkRole,
		&chunk.WindowGroupID,
		&chunk.OrderInSection,
		&chunk.PageStart,
		&chunk.PageEnd,
		&chunk.MetadataJSON,
		&chunk.CreatedAt,
	)
	if err != nil {
		return ResourceChunk{}, err
	}

	return chunk, nil
}

// normalizeLexicalQuery 归一化 `Lexical查询`，避免后续流程重复处理边界输入。
func normalizeLexicalQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

// normalizeChunkForInsert 在写入 chunk 前补齐 grounded 元数据默认值，保证显式 INSERT 也能满足线上非空列约束。
func normalizeChunkForInsert(chunk *ResourceChunk) {
	if chunk == nil {
		return
	}

	chunk.SectionType = defaultChunkSectionType(chunk.SectionType, chunk.SectionTitle)
	chunk.ChunkRole = defaultChunkRole(chunk.ChunkRole)
	chunk.WindowGroupID = defaultChunkWindowGroupID(chunk.WindowGroupID, chunk.SectionID, chunk.SectionTitle)
	chunk.PageStart, chunk.PageEnd = defaultChunkPages(chunk.PageStart, chunk.PageEnd)
}

// defaultChunkSectionType 为缺失的 chunk section type 提供兜底值，避免 legacy/fallback chunk 在首次写库时违反约束。
func defaultChunkSectionType(current *string, sectionTitle string) *string {
	if trimmed := optionalTrimmedText(derefOptionalString(current)); trimmed != nil {
		return trimmed
	}

	defaultType := string(documentnormalize.SectionTypeSection)
	title := strings.TrimSpace(sectionTitle)
	if title == "" || title == sections.WholeDocumentTitle {
		defaultType = string(documentnormalize.SectionTypeDocument)
	}

	return &defaultType
}

// defaultChunkRole 为缺失的 chunk role 统一补默认语义，保持检索和写库链路看到一致的 grounded 角色。
func defaultChunkRole(current *string) *string {
	if trimmed := optionalTrimmedText(derefOptionalString(current)); trimmed != nil {
		return trimmed
	}

	defaultRole := "section_body"
	return &defaultRole
}

// defaultChunkWindowGroupID 为缺失的窗口分组 ID 生成稳定兜底值，避免显式写 NULL 触发数据库非空错误。
func defaultChunkWindowGroupID(current *string, sectionID *string, sectionTitle string) *string {
	if trimmed := optionalTrimmedText(derefOptionalString(current)); trimmed != nil {
		return trimmed
	}
	if trimmed := optionalTrimmedText(derefOptionalString(sectionID)); trimmed != nil {
		return trimmed
	}
	if trimmed := optionalTrimmedText(sectionTitle); trimmed != nil {
		return trimmed
	}

	empty := ""
	return &empty
}

// defaultChunkPages 为缺失页码的 chunk 统一补齐开始页和结束页，保持 grounded 元数据契约完整。
func defaultChunkPages(pageStart *int, pageEnd *int) (*int, *int) {
	start := 0
	end := 0

	if pageStart != nil {
		start = *pageStart
	}
	if pageEnd != nil {
		end = *pageEnd
	}
	if pageStart == nil && pageEnd != nil {
		start = end
	}
	if pageEnd == nil && pageStart != nil {
		end = start
	}

	return &start, &end
}

// derefOptionalString 把可选字符串指针解引用成稳定文本值，减少调用方散落的 nil 判断。
func derefOptionalString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
