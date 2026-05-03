package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UploadedFile 表示保存到本地磁盘的原始上传文件元数据。
type UploadedFile struct {
	ID               string
	ResourceID       *string
	SessionID        *string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	SHA256           string
	StorageKey       string
	CreatedAt        time.Time
}

// UploadedFileCreateParams 描述写入原始上传文件元数据所需字段。
type UploadedFileCreateParams struct {
	ResourceID       *string
	SessionID        *string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	SHA256           string
	StorageKey       string
}

// UploadedFileRepo 封装 uploaded_files 表访问。
type UploadedFileRepo struct {
	pool *pgxpool.Pool
}

// NewUploadedFileRepo 使用连接池创建原始上传文件仓储。
func NewUploadedFileRepo(pool *pgxpool.Pool) *UploadedFileRepo {
	return &UploadedFileRepo{pool: pool}
}

// Create 写入一条原始上传文件元数据。
func (r *UploadedFileRepo) Create(ctx context.Context, input UploadedFileCreateParams) (*UploadedFile, error) {
	file, err := scanUploadedFile(r.pool.QueryRow(ctx, `
		INSERT INTO uploaded_files (resource_id, session_id, original_filename, content_type, size_bytes, sha256, storage_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, resource_id, session_id, original_filename, content_type, size_bytes, sha256, storage_key, created_at
	`, input.ResourceID, input.SessionID, input.OriginalFilename, input.ContentType, input.SizeBytes, input.SHA256, input.StorageKey))
	if err != nil {
		return nil, err
	}

	return &file, nil
}

// GetByID 按主键读取原始上传文件，不存在时返回 nil。
func (r *UploadedFileRepo) GetByID(ctx context.Context, id string) (*UploadedFile, error) {
	file, err := scanUploadedFile(r.pool.QueryRow(ctx, `
		SELECT id, resource_id, session_id, original_filename, content_type, size_bytes, sha256, storage_key, created_at
		FROM uploaded_files
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &file, nil
}

// UpdateResourceID 为已保存的原始文件补上解析后生成的 resource_id。
func (r *UploadedFileRepo) UpdateResourceID(ctx context.Context, id string, resourceID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE uploaded_files
		SET resource_id = $2
		WHERE id = $1
	`, id, resourceID)
	return err
}

// UpdateSessionID 为已保存的原始文件补上所属会话 ID。
func (r *UploadedFileRepo) UpdateSessionID(ctx context.Context, id string, sessionID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE uploaded_files
		SET session_id = $2
		WHERE id = $1
	`, id, sessionID)
	return err
}

// scanUploadedFile 把当前数据库行扫描成 `Uploaded文件`，统一查询结果到领域结构的映射。
func scanUploadedFile(row pgx.Row) (UploadedFile, error) {
	var file UploadedFile

	err := row.Scan(
		&file.ID,
		&file.ResourceID,
		&file.SessionID,
		&file.OriginalFilename,
		&file.ContentType,
		&file.SizeBytes,
		&file.SHA256,
		&file.StorageKey,
		&file.CreatedAt,
	)
	if err != nil {
		return UploadedFile{}, err
	}

	return file, nil
}
