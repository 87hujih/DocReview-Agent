package agentartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrIdempotencyConflict = errors.New("制品 idempotency 键 conflicts 包含 different 内容")

type Repository struct{ pool *pgxpool.Pool }

// NewRepository 校验依赖并创建对应实例。
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

type Record struct {
	ID                 string
	WorkspaceID        string
	RunID              *string
	StepID             *string
	IdempotencyKey     string
	DataClassification string
	ContentJSON        json.RawMessage
	ContentHash        string
	TokenCount         int
	ProvenanceJSON     json.RawMessage
	CreatedAt          time.Time
}

type CreateParams struct {
	WorkspaceID        string
	RunID              string
	StepID             string
	IdempotencyKey     string
	DataClassification string
	ContentJSON        json.RawMessage
	TokenCount         int
	ProvenanceJSON     json.RawMessage
}

const artifactColumns = `
	id, workspace_id, run_id, step_id, idempotency_key, data_classification,
	content_json, content_hash, token_count, provenance_json, created_at`

// CreateOrGet 按领域约束持久化数据。
func (r *Repository) CreateOrGet(ctx context.Context, params CreateParams) (*Record, bool, error) {
	params.WorkspaceID = strings.TrimSpace(params.WorkspaceID)
	params.RunID = strings.TrimSpace(params.RunID)
	params.StepID = strings.TrimSpace(params.StepID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.DataClassification = strings.TrimSpace(params.DataClassification)
	if params.WorkspaceID == "" || params.IdempotencyKey == "" {
		return nil, false, fmt.Errorf("workspace_id and idempotency_key are required")
	}
	if !validClassification(params.DataClassification) || params.TokenCount < 0 {
		return nil, false, fmt.Errorf("制品 classification 或 token_count 无效")
	}
	content, err := normalizeObject(params.ContentJSON, "content_json")
	if err != nil {
		return nil, false, err
	}
	provenance, err := normalizeArray(params.ProvenanceJSON, "provenance_json")
	if err != nil {
		return nil, false, err
	}
	digest := sha256.Sum256(content)
	contentHash := "sha256:" + hex.EncodeToString(digest[:])
	if r == nil || r.pool == nil {
		return nil, false, fmt.Errorf("制品数据库不能为空")
	}
	record, err := scanRecord(r.pool.QueryRow(ctx, `
		INSERT INTO agent_artifacts (
			workspace_id, run_id, step_id, idempotency_key, data_classification,
			content_json, content_hash, token_count, provenance_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
		RETURNING `+artifactColumns,
		params.WorkspaceID, nullable(params.RunID), nullable(params.StepID), params.IdempotencyKey,
		params.DataClassification, content, contentHash, params.TokenCount, provenance,
	))
	if err == nil {
		return &record, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	record, err = scanRecord(r.pool.QueryRow(ctx, `
		SELECT `+artifactColumns+`
		FROM agent_artifacts
		WHERE workspace_id = $1 AND idempotency_key = $2
	`, params.WorkspaceID, params.IdempotencyKey))
	if err != nil {
		return nil, false, err
	}
	if record.ContentHash != contentHash || record.DataClassification != params.DataClassification ||
		record.TokenCount != params.TokenCount || !jsonEqual(record.ContentJSON, content) || !jsonEqual(record.ProvenanceJSON, provenance) {
		return nil, false, ErrIdempotencyConflict
	}
	return &record, false, nil
}

// Get 按作用域读取并返回所需数据。
func (r *Repository) Get(ctx context.Context, workspaceID, artifactID string) (*Record, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	artifactID = strings.TrimSpace(artifactID)
	if workspaceID == "" || artifactID == "" {
		return nil, fmt.Errorf("workspace_id and artifact_id are required")
	}
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("制品数据库不能为空")
	}
	record, err := scanRecord(r.pool.QueryRow(ctx, `
		SELECT `+artifactColumns+`
		FROM agent_artifacts
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, artifactID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// scanRecord 执行该函数负责的核心处理逻辑。
func scanRecord(row pgx.Row) (Record, error) {
	var value Record
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.RunID, &value.StepID, &value.IdempotencyKey,
		&value.DataClassification, &value.ContentJSON, &value.ContentHash, &value.TokenCount,
		&value.ProvenanceJSON, &value.CreatedAt,
	)
	return value, err
}

// normalizeObject 执行该函数负责的核心处理逻辑。
func normalizeObject(raw json.RawMessage, name string) (json.RawMessage, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, fmt.Errorf("%s 必须是 JSON 对象", name)
	}
	return json.Marshal(value)
}

// normalizeArray 执行该函数负责的核心处理逻辑。
func normalizeArray(raw json.RawMessage, name string) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`[]`)
	}
	var value []any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, fmt.Errorf("%s 必须为一个 JSON 数组", name)
	}
	return json.Marshal(value)
}

// jsonEqual 执行该函数负责的核心处理逻辑。
func jsonEqual(left, right json.RawMessage) bool {
	var leftBuffer, rightBuffer bytes.Buffer
	if json.Compact(&leftBuffer, left) != nil || json.Compact(&rightBuffer, right) != nil {
		return false
	}
	return bytes.Equal(leftBuffer.Bytes(), rightBuffer.Bytes())
}

// nullable 执行该函数负责的核心处理逻辑。
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// validClassification 执行该函数负责的核心处理逻辑。
func validClassification(value string) bool {
	return value == "public" || value == "internal" || value == "confidential" || value == "restricted"
}
