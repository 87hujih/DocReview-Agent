package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

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
	ID           string
	ResourceID   string
	VersionID    string
	ChunkIndex   int
	SectionTitle string
	Content      string
	Embedding    pgvector.Vector
	CreatedAt    time.Time
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
	resource, err := scanResource(r.pool.QueryRow(ctx, `
		INSERT INTO resources (title, source_type)
		VALUES ($1, $2)
		RETURNING id, title, source_type, source_ref, created_at, updated_at
	`, title, sourceType))
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

// CreateChunk 持久化一个可检索分块，并把生成出的 ID 和时间戳回填到入参结构体。
func (r *ResourceRepo) CreateChunk(ctx context.Context, chunk *ResourceChunk) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO resource_chunks (resource_id, version_id, chunk_index, section_title, content, embedding)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, chunk.ResourceID, chunk.VersionID, chunk.ChunkIndex, chunk.SectionTitle, chunk.Content, chunk.Embedding).Scan(&chunk.ID, &chunk.CreatedAt)
}

// SearchChunks 使用 pgvector 距离排序，在全部资源范围内执行语义检索。
func (r *ResourceRepo) SearchChunks(ctx context.Context, embedding pgvector.Vector, limit int) ([]ResourceChunk, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, resource_id, version_id, chunk_index, section_title, content, embedding, created_at
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
		SELECT id, resource_id, version_id, chunk_index, section_title, content, embedding, created_at
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

// SearchChunksLexical 使用 section_title + content 的 trigram / 子串匹配执行词法候选召回。
func (r *ResourceRepo) SearchChunksLexical(ctx context.Context, query string, limit int) ([]ResourceChunk, error) {
	normalizedQuery := normalizeLexicalQuery(query)
	if normalizedQuery == "" || limit <= 0 {
		return []ResourceChunk{}, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, resource_id, version_id, chunk_index, section_title, content, embedding, created_at
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
		SELECT id, resource_id, version_id, chunk_index, section_title, content, embedding, created_at
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
		&chunk.CreatedAt,
	)
	if err != nil {
		return ResourceChunk{}, err
	}

	return chunk, nil
}

func normalizeLexicalQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}
