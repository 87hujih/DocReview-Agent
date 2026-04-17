package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ResourceVersionStructure 保存资源版本对应的结构化解析结果。
type ResourceVersionStructure struct {
	ID               string
	ResourceID       string
	VersionID        string
	SourceFormat     string
	ParserName       string
	ParserVersion    string
	DocumentJSON     json.RawMessage
	QualityFlagsJSON json.RawMessage
	CreatedAt        time.Time
}

// ResourceVersionStructureInput 表示写入结构化解析结果时的输入载荷。
type ResourceVersionStructureInput struct {
	ResourceID       string
	VersionID        string
	SourceFormat     string
	ParserName       string
	ParserVersion    string
	DocumentJSON     json.RawMessage
	QualityFlagsJSON json.RawMessage
}

// ResourceSection 表示归一化后的逻辑 section。
type ResourceSection struct {
	ID          string
	ResourceID  string
	VersionID   string
	SectionKey  string
	SectionType string
	Title       string
	Summary     string
	Content     string
	PageStart   int
	PageEnd     int
	Metadata    map[string]any
	CreatedAt   time.Time
}

// ResourceSectionInput 表示写入逻辑 section 时的输入载荷。
type ResourceSectionInput struct {
	SectionID   string
	SectionKey  string
	SectionType string
	Title       string
	Summary     string
	Content     string
	PageStart   int
	PageEnd     int
	Metadata    map[string]any
}

// CreateVersionStructure 为某个资源版本创建或覆盖结构化解析结果。
func (r *ResourceRepo) CreateVersionStructure(ctx context.Context, input ResourceVersionStructureInput) (*ResourceVersionStructure, error) {
	structure, err := scanResourceVersionStructure(r.pool.QueryRow(ctx, `
		INSERT INTO resource_version_structures (
			resource_id,
			version_id,
			source_format,
			parser_name,
			parser_version,
			document_json,
			quality_flags_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (version_id) DO UPDATE
		SET resource_id = EXCLUDED.resource_id,
		    source_format = EXCLUDED.source_format,
		    parser_name = EXCLUDED.parser_name,
		    parser_version = EXCLUDED.parser_version,
		    document_json = EXCLUDED.document_json,
		    quality_flags_json = EXCLUDED.quality_flags_json
		RETURNING id, resource_id, version_id, source_format, parser_name, parser_version, document_json, quality_flags_json, created_at
	`,
		input.ResourceID,
		input.VersionID,
		strings.TrimSpace(input.SourceFormat),
		strings.TrimSpace(input.ParserName),
		strings.TrimSpace(input.ParserVersion),
		normalizeJSONRaw(input.DocumentJSON, `{}`),
		normalizeJSONRaw(input.QualityFlagsJSON, `[]`),
	))
	if err != nil {
		return nil, err
	}

	return &structure, nil
}

// GetVersionStructureByVersionID 按资源版本 ID 读取结构化解析结果，不存在时返回 nil。
func (r *ResourceRepo) GetVersionStructureByVersionID(ctx context.Context, versionID string) (*ResourceVersionStructure, error) {
	structure, err := scanResourceVersionStructure(r.pool.QueryRow(ctx, `
		SELECT id, resource_id, version_id, source_format, parser_name, parser_version, document_json, quality_flags_json, created_at
		FROM resource_version_structures
		WHERE version_id = $1
	`, versionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &structure, nil
}

// ReplaceSectionsForVersion 以“先删后建”的方式重建一个版本的全部逻辑 section。
func (r *ResourceRepo) ReplaceSectionsForVersion(ctx context.Context, versionID string, resourceID string, sections []ResourceSectionInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		DELETE FROM resource_sections
		WHERE version_id = $1
	`, versionID); err != nil {
		return err
	}

	for index, section := range sections {
		metadataJSON, err := marshalJSONObject(section.Metadata)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO resource_sections (
				resource_id,
				version_id,
				section_key,
				section_index,
				section_type,
				title,
				summary,
				content,
				page_start,
				page_end,
				metadata_json
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`,
			resourceID,
			versionID,
			strings.TrimSpace(section.SectionKey),
			index,
			normalizeSectionType(section.SectionType),
			strings.TrimSpace(section.Title),
			strings.TrimSpace(section.Summary),
			section.Content,
			section.PageStart,
			section.PageEnd,
			metadataJSON,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// ListSectionsByVersion 返回某个资源版本的全部 section，并保持输入顺序稳定。
func (r *ResourceRepo) ListSectionsByVersion(ctx context.Context, versionID string) ([]ResourceSection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, resource_id, version_id, section_key, section_type, title, summary, content, page_start, page_end, metadata_json, created_at
		FROM resource_sections
		WHERE version_id = $1
		ORDER BY section_index ASC, id ASC
	`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceSections(rows)
}

// ListSectionsByVersionAndType 返回某个资源版本中指定类型的全部 section，并保持输入顺序稳定。
func (r *ResourceRepo) ListSectionsByVersionAndType(ctx context.Context, versionID string, sectionType string) ([]ResourceSection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, resource_id, version_id, section_key, section_type, title, summary, content, page_start, page_end, metadata_json, created_at
		FROM resource_sections
		WHERE version_id = $1
		  AND section_type = $2
		ORDER BY section_index ASC, id ASC
	`, versionID, normalizeSectionType(sectionType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceSections(rows)
}

func collectResourceSections(rows pgx.Rows) ([]ResourceSection, error) {
	var sections []ResourceSection
	for rows.Next() {
		section, err := scanResourceSection(rows)
		if err != nil {
			return nil, err
		}

		sections = append(sections, section)
	}

	return sections, rows.Err()
}

func scanResourceVersionStructure(row pgx.Row) (ResourceVersionStructure, error) {
	var (
		structure        ResourceVersionStructure
		documentJSON     []byte
		qualityFlagsJSON []byte
	)

	err := row.Scan(
		&structure.ID,
		&structure.ResourceID,
		&structure.VersionID,
		&structure.SourceFormat,
		&structure.ParserName,
		&structure.ParserVersion,
		&documentJSON,
		&qualityFlagsJSON,
		&structure.CreatedAt,
	)
	if err != nil {
		return ResourceVersionStructure{}, err
	}

	structure.DocumentJSON = append(json.RawMessage(nil), documentJSON...)
	structure.QualityFlagsJSON = append(json.RawMessage(nil), qualityFlagsJSON...)
	return structure, nil
}

func scanResourceSection(row pgx.Row) (ResourceSection, error) {
	var (
		section      ResourceSection
		metadataJSON []byte
	)

	err := row.Scan(
		&section.ID,
		&section.ResourceID,
		&section.VersionID,
		&section.SectionKey,
		&section.SectionType,
		&section.Title,
		&section.Summary,
		&section.Content,
		&section.PageStart,
		&section.PageEnd,
		&metadataJSON,
		&section.CreatedAt,
	)
	if err != nil {
		return ResourceSection{}, err
	}

	metadata, err := unmarshalJSONObject(metadataJSON)
	if err != nil {
		return ResourceSection{}, err
	}
	section.Metadata = metadata

	return section, nil
}

func normalizeJSONRaw(raw json.RawMessage, fallback string) string {
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return fallback
	}

	return value
}

func marshalJSONObject(value map[string]any) (string, error) {
	if len(value) == 0 {
		return `{}`, nil
	}

	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func unmarshalJSONObject(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return map[string]any{}, nil
	}

	return value, nil
}

func normalizeSectionType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}

	return trimmed
}

func normalizeChunkSectionType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "whole_document"
	}

	return trimmed
}

func normalizeChunkRole(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "section_body"
	}

	return trimmed
}

func nullableUUIDString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	return trimmed
}
