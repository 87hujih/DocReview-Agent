// Package commit owns the 已校验的, idempotent Canonical 文档 commit boundary.
package commit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/document/model"
	"agent_project/apps/server/internal/document/patch"
	"agent_project/apps/server/internal/document/renderer"
	"agent_project/apps/server/internal/document/validation"
)

var (
	ErrIdempotencyConflict = errors.New("文档 commit idempotency conflict")
	ErrVersionConflict     = errors.New("文档 base 版本 conflict")
	ErrHashConflict        = errors.New("文档节点哈希 conflict")
	ErrRetryableCommit     = errors.New("文档 commit retryable trans动作失败")
)

type Input struct {
	WorkspaceID       string
	ResourceID        string
	IdempotencyKey    string
	ActorID           string
	Patch             patch.Set
	AuthorizedNodeIDs map[string]struct{}
	EvidenceRefs      map[string]struct{}
}

type Result struct {
	ResourceID string `json:"resource_id"`
	VersionID  string `json:"version_id"`
	OutboxID   string `json:"outbox_id"`
	Created    bool   `json:"created"`
}

type Bundle struct {
	Document        *model.Document
	Projection      model.Projection
	LegacyContent   string
	RendererProfile string
	Patch           patch.Set
	PatchHash       string
	ActorID         string
}

type AtomicRequest struct {
	WorkspaceID    string
	ResourceID     string
	BaseVersionID  string
	IdempotencyKey string
	PatchHash      string
	ExpectedHashes map[string]string
	Bundle         Bundle
	validated      bool
}

// ValidatedByCommitter 校验输入及领域约束。
func (request AtomicRequest) ValidatedByCommitter() bool { return request.validated }

type AtomicResult struct {
	ResourceID string
	VersionID  string
	OutboxID   string
	Created    bool
}

type StoredCommit struct {
	PatchHash string
	Result    AtomicResult
}

type Store interface {
	GetCommit(context.Context, string, string) (*StoredCommit, error)
	LoadSnapshot(context.Context, string, string) (validation.Snapshot, error)
	CommitAtomic(context.Context, AtomicRequest) (AtomicResult, error)
}

type Options struct {
	ProjectionProfile model.ProjectionProfile
	Renderer          renderer.Renderer
	IDGenerator       func() (string, error)
}

type Committer struct {
	store     Store
	validator *validation.Validator
	profile   model.ProjectionProfile
	renderer  renderer.Renderer
	newID     func() (string, error)
}

// New 校验依赖并创建对应实例。
func New(store Store, validator *validation.Validator, options Options) (*Committer, error) {
	if store == nil || validator == nil {
		return nil, fmt.Errorf("文档存储和 Validat或不能为空")
	}
	if strings.TrimSpace(options.ProjectionProfile.SchemaVersion) == "" || strings.TrimSpace(options.ProjectionProfile.ChunkProfile) == "" || strings.TrimSpace(options.ProjectionProfile.EmbeddingProfile) == "" {
		return nil, fmt.Errorf("投影 profiles 不能为空")
	}
	if options.Renderer == nil {
		options.Renderer = renderer.NewMarkdown()
	}
	if options.IDGenerator == nil {
		options.IDGenerator = randomUUID
	}
	return &Committer{store: store, validator: validator, profile: options.ProjectionProfile, renderer: options.Renderer, newID: options.IDGenerator}, nil
}

// Validate performs deterministic validation only 和 never invokes 一个 写入.
func (c *Committer) Validate(ctx context.Context, input Input) (validation.Result, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.ResourceID) == "" {
		return validation.Result{}, fmt.Errorf("工作区_id 和 resource_id 不能为空")
	}
	snapshot, err := c.store.LoadSnapshot(ctx, input.WorkspaceID, input.ResourceID)
	if err != nil {
		return validation.Result{}, err
	}
	snapshot.AuthorizedNodeIDs = copySet(input.AuthorizedNodeIDs)
	snapshot.EvidenceRefs = copySet(input.EvidenceRefs)
	return c.validator.Validate(ctx, validation.Request{
		WorkspaceID: input.WorkspaceID, ResourceID: input.ResourceID, Patch: input.Patch, Snapshot: snapshot,
	}), nil
}

// Commit 执行该函数负责的核心处理逻辑。
func (c *Committer) Commit(ctx context.Context, input Input) (Result, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.ResourceID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.ActorID) == "" {
		return Result{}, fmt.Errorf("工作区_id、resource_id、idempotency_键、和 act或_id 不能为空")
	}
	if err := patch.ValidateSet(input.Patch); err != nil {
		return Result{}, &ValidationError{Result: validation.Result{Errors: []validation.Violation{{Category: validation.InvalidPatch, Message: err.Error()}}}}
	}
	if input.Patch.ResourceID != input.ResourceID {
		return Result{}, &ValidationError{Result: validation.Result{Errors: []validation.Violation{{Category: validation.ResourceScopeDenied, Message: "PatchSet 资源与可信作用域不匹配"}}}}
	}
	patchHash, err := patch.Hash(input.Patch)
	if err != nil {
		return Result{}, fmt.Errorf("处理失败：哈希 PatchSet：%w", err)
	}
	existing, err := c.store.GetCommit(ctx, input.WorkspaceID, input.IdempotencyKey)
	if err != nil {
		return Result{}, err
	}
	if existing != nil {
		if existing.PatchHash != patchHash {
			return Result{}, ErrIdempotencyConflict
		}
		return publicResult(existing.Result, false), nil
	}
	validated, err := c.Validate(ctx, input)
	if err != nil {
		return Result{}, err
	}
	if !validated.Valid {
		return Result{}, &ValidationError{Result: validated}
	}
	newVersionID, err := c.newID()
	if err != nil {
		return Result{}, fmt.Errorf("处理失败：allocate 版本 id：%w", err)
	}
	document, err := model.Clone(validated.Document)
	if err != nil {
		return Result{}, err
	}
	document.VersionID = newVersionID
	if err := model.Rehash(document); err != nil {
		return Result{}, err
	}
	projection, err := model.Derive(document, c.profile)
	if err != nil {
		return Result{}, err
	}
	rendered, err := c.renderer.Render(ctx, document)
	if err != nil {
		return Result{}, err
	}
	bundle := Bundle{
		Document: document, Projection: projection, LegacyContent: string(rendered.Content), RendererProfile: rendered.Profile,
		Patch: input.Patch, PatchHash: patchHash, ActorID: input.ActorID,
	}
	atomic, err := c.store.CommitAtomic(ctx, AtomicRequest{
		WorkspaceID: input.WorkspaceID, ResourceID: input.ResourceID, BaseVersionID: input.Patch.BaseVersionID,
		IdempotencyKey: input.IdempotencyKey, PatchHash: patchHash, ExpectedHashes: expectedHashes(input.Patch), Bundle: bundle,
		validated: true,
	})
	if err != nil {
		return Result{}, err
	}
	return publicResult(atomic, atomic.Created), nil
}

type ValidationError struct{ Result validation.Result }

// 错误 执行该函数负责的核心处理逻辑。
func (e *ValidationError) Error() string {
	if len(e.Result.Errors) == 0 {
		return "document PatchSet validation failed"
	}
	return fmt.Sprintf("document PatchSet validation failed: %s: %s", e.Result.Errors[0].Category, e.Result.Errors[0].Message)
}

// expectedHashes 执行该函数负责的核心处理逻辑。
func expectedHashes(set patch.Set) map[string]string {
	result := make(map[string]string, len(set.Operations)*2)
	for _, operation := range set.Operations {
		result[operation.NodeID] = operation.ExpectedHash
		if operation.ExpectedParentID != "" {
			result[operation.ExpectedParentID] = operation.ExpectedParentHash
		}
	}
	return result
}

// publicResult 执行该函数负责的核心处理逻辑。
func publicResult(result AtomicResult, created bool) Result {
	return Result{ResourceID: result.ResourceID, VersionID: result.VersionID, OutboxID: result.OutboxID, Created: created}
}

// copySet 执行该函数负责的核心处理逻辑。
func copySet(input map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(input))
	for key := range input {
		result[key] = struct{}{}
	}
	return result
}

// randomUUID 执行该函数负责的核心处理逻辑。
func randomUUID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}
