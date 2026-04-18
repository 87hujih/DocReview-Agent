package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResourceVersionStructure 保存资源版本对应的结构化解析结果。
type ResourceVersionStructure struct {
	ID               string
	ResourceID       string
	VersionID        string
	SourceFormat     string
	ParserName       string
	ParserVersion    *string
	DocumentJSON     []byte
	QualityFlagsJSON []byte
	CreatedAt        time.Time
}

// CreateVersionStructureParams 描述写入结构化解析结果所需的字段。
type CreateVersionStructureParams struct {
	ResourceID       string
	VersionID        string
	SourceFormat     string
	ParserName       string
	ParserVersion    string
	DocumentJSON     []byte
	QualityFlagsJSON []byte
}

// ResourceSection 保存一个逻辑 section 的持久化形态。
type ResourceSection struct {
	ID                  string
	ResourceID          string
	VersionID           string
	SectionKey          string
	SectionType         string
	SectionOrder        int
	Title               string
	CanonicalEntityName *string
	AliasesJSON         []byte
	Summary             string
	Content             string
	PageStart           *int
	PageEnd             *int
	MetadataJSON        []byte
	CreatedAt           time.Time
}

// ResourceSectionInput 描述重建 version sections 时单个 section 的输入。
type ResourceSectionInput struct {
	SectionKey          string
	SectionType         string
	SectionOrder        int
	Title               string
	CanonicalEntityName *string
	AliasesJSON         []byte
	Summary             string
	Content             string
	PageStart           *int
	PageEnd             *int
	MetadataJSON        []byte
}

// ResourceStructureRepo 负责结构化解析结果和 section 图的持久化。
type ResourceStructureRepo struct {
	pool *pgxpool.Pool
}

// NewResourceStructureRepo 创建结构化文档仓储。
func NewResourceStructureRepo(pool *pgxpool.Pool) *ResourceStructureRepo {
	return &ResourceStructureRepo{pool: pool}
}

// CreateVersionStructure 为某个版本写入结构化解析结果。
func (r *ResourceStructureRepo) CreateVersionStructure(ctx context.Context, params CreateVersionStructureParams) (*ResourceVersionStructure, error) {
	params = normalizeVersionStructureParamsForInsert(params)

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
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)
		RETURNING id,
		          resource_id,
		          version_id,
		          source_format,
		          parser_name,
		          parser_version,
		          document_json,
		          quality_flags_json,
		          created_at
	`,
		params.ResourceID,
		params.VersionID,
		params.SourceFormat,
		params.ParserName,
		params.ParserVersion,
		normalizeJSONArgument(params.DocumentJSON, `{}`),
		normalizeJSONArgument(params.QualityFlagsJSON, `[]`),
	))
	if err != nil {
		return nil, err
	}

	return &structure, nil
}

// GetVersionStructureByVersionID 按版本读取结构化解析结果。
func (r *ResourceStructureRepo) GetVersionStructureByVersionID(ctx context.Context, versionID string) (*ResourceVersionStructure, error) {
	structure, err := scanResourceVersionStructure(r.pool.QueryRow(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       source_format,
		       parser_name,
		       parser_version,
		       document_json,
		       quality_flags_json,
		       created_at
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

// ReplaceSectionsForVersion 重建某个资源版本的全部逻辑 sections。
func (r *ResourceStructureRepo) ReplaceSectionsForVersion(ctx context.Context, versionID string, resourceID string, sections []ResourceSectionInput) ([]ResourceSection, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		DELETE FROM resource_sections
		WHERE version_id = $1
	`, versionID); err != nil {
		return nil, err
	}

	inserted := make([]ResourceSection, 0, len(sections))
	for _, section := range sections {
		section = normalizeSectionInputForInsert(section)

		record, err := scanResourceSection(tx.QueryRow(ctx, `
			INSERT INTO resource_sections (
			    resource_id,
			    version_id,
			    section_key,
			    section_type,
			    section_order,
			    title,
			    canonical_entity_name,
			    aliases_json,
			    summary,
			    content,
			    page_start,
			    page_end,
			    metadata_json
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13::jsonb)
			RETURNING id,
			          resource_id,
			          version_id,
			          section_key,
			          section_type,
			          section_order,
			          title,
			          canonical_entity_name,
			          aliases_json,
			          summary,
			          content,
			          page_start,
			          page_end,
			          metadata_json,
			          created_at
		`,
			resourceID,
			versionID,
			section.SectionKey,
			section.SectionType,
			section.SectionOrder,
			section.Title,
			section.CanonicalEntityName,
			normalizeJSONArgument(section.AliasesJSON, `[]`),
			section.Summary,
			section.Content,
			section.PageStart,
			section.PageEnd,
			normalizeJSONArgument(section.MetadataJSON, `{}`),
		))
		if err != nil {
			return nil, err
		}

		inserted = append(inserted, record)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return inserted, nil
}

// ListSectionsByVersion 返回某个版本下按顺序排列的 sections。
func (r *ResourceStructureRepo) ListSectionsByVersion(ctx context.Context, versionID string) ([]ResourceSection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       section_key,
		       section_type,
		       section_order,
		       title,
		       canonical_entity_name,
		       aliases_json,
		       summary,
		       content,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_sections
		WHERE version_id = $1
		ORDER BY section_order ASC, created_at ASC
	`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceSections(rows)
}

// ListSectionsByVersionAndType 返回某个版本下指定类型的 sections。
func (r *ResourceStructureRepo) ListSectionsByVersionAndType(ctx context.Context, versionID string, sectionType string) ([]ResourceSection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,
		       resource_id,
		       version_id,
		       section_key,
		       section_type,
		       section_order,
		       title,
		       canonical_entity_name,
		       aliases_json,
		       summary,
		       content,
		       page_start,
		       page_end,
		       metadata_json,
		       created_at
		FROM resource_sections
		WHERE version_id = $1
		  AND section_type = $2
		ORDER BY section_order ASC, created_at ASC
	`, versionID, strings.TrimSpace(sectionType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectResourceSections(rows)
}

// collectResourceSections 遍历结果集收集 `资源section`，把游标处理细节隔离在仓储层。
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

// scanResourceVersionStructure 把当前数据库行扫描成 `资源版本结构化结果`，统一查询结果到领域结构的映射。
func scanResourceVersionStructure(row pgx.Row) (ResourceVersionStructure, error) {
	var structure ResourceVersionStructure

	err := row.Scan(
		&structure.ID,
		&structure.ResourceID,
		&structure.VersionID,
		&structure.SourceFormat,
		&structure.ParserName,
		&structure.ParserVersion,
		&structure.DocumentJSON,
		&structure.QualityFlagsJSON,
		&structure.CreatedAt,
	)
	if err != nil {
		return ResourceVersionStructure{}, err
	}

	return structure, nil
}

// scanResourceSection 把当前数据库行扫描成 `资源section`，统一查询结果到领域结构的映射。
func scanResourceSection(row pgx.Row) (ResourceSection, error) {
	var section ResourceSection

	err := row.Scan(
		&section.ID,
		&section.ResourceID,
		&section.VersionID,
		&section.SectionKey,
		&section.SectionType,
		&section.SectionOrder,
		&section.Title,
		&section.CanonicalEntityName,
		&section.AliasesJSON,
		&section.Summary,
		&section.Content,
		&section.PageStart,
		&section.PageEnd,
		&section.MetadataJSON,
		&section.CreatedAt,
	)
	if err != nil {
		return ResourceSection{}, err
	}

	return section, nil
}

// normalizeJSONArgument 在写库前为 JSON 参数补齐合法兜底内容，避免空切片或空字节直接变成非法 JSON。
func normalizeJSONArgument(value []byte, fallback string) string {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return fallback
	}

	return trimmed
}

// normalizeVersionStructureParamsForInsert 在写入结构化解析结果前统一归一化文本和 JSON 字段，避免显式 INSERT 绕过数据库默认值。
func normalizeVersionStructureParamsForInsert(params CreateVersionStructureParams) CreateVersionStructureParams {
	params.SourceFormat = strings.TrimSpace(params.SourceFormat)
	params.ParserName = strings.TrimSpace(params.ParserName)
	params.ParserVersion = strings.TrimSpace(params.ParserVersion)

	return params
}

// normalizeSectionInputForInsert 在批量写入 section 前补齐非空列需要的默认值，避免缺页码或空 section_type 直接写成 SQL NULL。
func normalizeSectionInputForInsert(section ResourceSectionInput) ResourceSectionInput {
	section.SectionKey = strings.TrimSpace(section.SectionKey)
	section.SectionType = defaultSectionType(section.SectionType)
	section.Title = strings.TrimSpace(section.Title)
	section.CanonicalEntityName = trimOptionalString(section.CanonicalEntityName)
	section.Summary = strings.TrimSpace(section.Summary)
	section.Content = strings.TrimSpace(section.Content)
	section.PageStart, section.PageEnd = defaultSectionPages(section.PageStart, section.PageEnd)

	return section
}

// defaultSectionType 为缺失的 section 类型提供稳定兜底值，保持 `resource_sections.section_type` 满足非空约束。
func defaultSectionType(sectionType string) string {
	trimmed := strings.TrimSpace(sectionType)
	if trimmed == "" {
		return "unknown"
	}

	return trimmed
}

// defaultSectionPages 为缺失页码的 section 补齐对称的开始页和结束页，统一数据库侧的 0/0 缺省契约。
func defaultSectionPages(pageStart *int, pageEnd *int) (*int, *int) {
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

// optionalTrimmedText 把可选文本统一裁剪后再决定是否落为 NULL，避免写库边界重复处理空白字符串。
func optionalTrimmedText(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}
