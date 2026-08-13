package agentrun

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ShadowStatus string

const (
	ShadowMatched     ShadowStatus = "matched"
	ShadowDiverged    ShadowStatus = "diverged"
	ShadowUnavailable ShadowStatus = "unavailable"
)

// 有效的 执行该函数负责的核心处理逻辑。
func (status ShadowStatus) valid() bool {
	return status == ShadowMatched || status == ShadowDiverged || status == ShadowUnavailable
}

type ShadowComparison struct {
	ID               string
	RunID            string
	LegacyTaskID     *string
	LegacyOutputHash *string
	TypedOutputHash  *string
	Status           ShadowStatus
	DetailsJSON      json.RawMessage
	CreatedAt        time.Time
}

type ShadowComparisonParams struct {
	RunID            string
	LegacyTaskID     *string
	LegacyOutputHash *string
	TypedOutputHash  *string
	Status           ShadowStatus
	DetailsJSON      json.RawMessage
}

// 处理失败： CompareShadowOutputs compares canonical JSON objects, never rendered text
// ordering. It 为 deterministic 和 safe 用于 use 位于 一个 offline 影子 工作进程.
func CompareShadowOutputs(legacyOutput, typedOutput json.RawMessage) (ShadowComparisonParams, error) {
	legacyHash, err := canonicalObjectHash(legacyOutput)
	if err != nil {
		return ShadowComparisonParams{}, fmt.Errorf("旧版输出：%w", err)
	}
	typedHash, err := canonicalObjectHash(typedOutput)
	if err != nil {
		return ShadowComparisonParams{}, fmt.Errorf("类型化的输出：%w", err)
	}
	status := ShadowDiverged
	if legacyHash == typedHash {
		status = ShadowMatched
	}
	return ShadowComparisonParams{
		LegacyOutputHash: &legacyHash, TypedOutputHash: &typedHash, Status: status, DetailsJSON: json.RawMessage(`{}`),
	}, nil
}

// RecordShadowComparison 按领域约束持久化数据。
func (r *Repository) RecordShadowComparison(ctx context.Context, params ShadowComparisonParams) (*ShadowComparison, error) {
	params.RunID = strings.TrimSpace(params.RunID)
	params.LegacyTaskID = trimOptional(params.LegacyTaskID)
	params.LegacyOutputHash = trimOptional(params.LegacyOutputHash)
	params.TypedOutputHash = trimOptional(params.TypedOutputHash)
	if params.RunID == "" {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	if !params.Status.valid() {
		return nil, fmt.Errorf("影子比较结果状态无效")
	}
	if params.Status == ShadowUnavailable {
		if params.LegacyOutputHash != nil && params.TypedOutputHash != nil {
			return nil, fmt.Errorf("不可用比较结果必须 have 至少一个不可用输出")
		}
	} else if params.LegacyOutputHash == nil || params.TypedOutputHash == nil {
		return nil, fmt.Errorf("matched/diverged 比较结果需要 both 输出 hashes")
	} else if (params.Status == ShadowMatched) != (*params.LegacyOutputHash == *params.TypedOutputHash) {
		return nil, fmt.Errorf("影子状态不匹配输出 hashes")
	}
	details, err := normalizeJSONObject(params.DetailsJSON, "details_json")
	if err != nil {
		return nil, err
	}
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("agent 运行数据库不能为空")
	}

	comparison, err := scanShadowComparison(r.pool.QueryRow(ctx, `
		INSERT INTO agent_shadow_comparisons (
			run_id, legacy_task_id, legacy_output_hash, typed_output_hash, status, details_json
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (run_id) DO UPDATE SET run_id = EXCLUDED.run_id
		RETURNING id, run_id, legacy_task_id, legacy_output_hash, typed_output_hash, status, details_json, created_at
	`, params.RunID, params.LegacyTaskID, params.LegacyOutputHash, params.TypedOutputHash, params.Status, details))
	if err != nil {
		return nil, err
	}
	if !sameShadowComparison(comparison, params, details) {
		return nil, ErrIdempotencyConflict
	}
	return &comparison, nil
}

// scanShadowComparison 执行该函数负责的核心处理逻辑。
func scanShadowComparison(row rowQuerierResult) (ShadowComparison, error) {
	var comparison ShadowComparison
	err := row.Scan(
		&comparison.ID, &comparison.RunID, &comparison.LegacyTaskID, &comparison.LegacyOutputHash,
		&comparison.TypedOutputHash, &comparison.Status, &comparison.DetailsJSON, &comparison.CreatedAt,
	)
	return comparison, err
}

type rowQuerierResult interface {
	Scan(...any) error
}

// sameShadowComparison 执行该函数负责的核心处理逻辑。
func sameShadowComparison(record ShadowComparison, params ShadowComparisonParams, details json.RawMessage) bool {
	return record.RunID == params.RunID && equalStringPointer(record.LegacyTaskID, params.LegacyTaskID) &&
		equalStringPointer(record.LegacyOutputHash, params.LegacyOutputHash) && equalStringPointer(record.TypedOutputHash, params.TypedOutputHash) &&
		record.Status == params.Status && jsonEqual(record.DetailsJSON, details)
}

// canonicalObjectHash 执行该函数负责的核心处理逻辑。
func canonicalObjectHash(raw json.RawMessage) (string, error) {
	canonical, err := normalizeJSONObject(raw, "output")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}
