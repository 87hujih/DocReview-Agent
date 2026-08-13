package agentartifact

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
)

type RuntimeStore struct{ repo *Repository }

// NewRuntimeStore 校验依赖并创建对应实例。
func NewRuntimeStore(repo *Repository) *RuntimeStore { return &RuntimeStore{repo: repo} }

var _ agenttools.ArtifactStore = (*RuntimeStore)(nil)

// 写入 执行该函数负责的核心处理逻辑。
func (s *RuntimeStore) Write(ctx context.Context, input agenttools.ArtifactWrite) (agenttools.ArtifactReference, error) {
	provenance, err := json.Marshal(input.Provenance)
	if err != nil {
		return agenttools.ArtifactReference{}, err
	}
	record, _, err := s.repo.CreateOrGet(ctx, CreateParams{
		WorkspaceID: input.WorkspaceID, RunID: input.RunID, StepID: input.StepID,
		IdempotencyKey: input.IdempotencyKey, DataClassification: string(input.DataClassification),
		ContentJSON: input.Content, TokenCount: input.TokenCount, ProvenanceJSON: provenance,
	})
	if err != nil {
		return agenttools.ArtifactReference{}, err
	}
	return agenttools.ArtifactReference{
		ID: record.ID, URI: "artifact://" + record.ID,
		ContentHash: record.ContentHash, TokenCount: record.TokenCount,
	}, nil
}

type BuiltinBackend struct{ repo *Repository }

// NewBuiltinBackend 校验依赖并创建对应实例。
func NewBuiltinBackend(repo *Repository) *BuiltinBackend { return &BuiltinBackend{repo: repo} }

var _ builtin.ArtifactBackend = (*BuiltinBackend)(nil)

// 读取 执行该函数负责的核心处理逻辑。
func (b *BuiltinBackend) Read(ctx context.Context, workspaceID string, input builtin.ArtifactReadInput) (*builtin.Artifact, error) {
	record, err := b.repo.Get(ctx, workspaceID, input.ArtifactID)
	if err != nil || record == nil {
		return nil, err
	}
	return toBuiltin(record), nil
}

// 写入 执行该函数负责的核心处理逻辑。
func (b *BuiltinBackend) Write(ctx context.Context, workspaceID string, input builtin.ArtifactWriteInput, idempotencyKey string) (*builtin.Artifact, error) {
	classification := strings.TrimSpace(input.DataClassification)
	if !validClassification(classification) {
		return nil, &agenttools.ToolError{Category: agenttools.ErrorInvalidInput, Message: "制品数据分类无效"}
	}
	record, _, err := b.repo.CreateOrGet(ctx, CreateParams{
		WorkspaceID: workspaceID, IdempotencyKey: idempotencyKey,
		DataClassification: classification, ContentJSON: input.Content,
		TokenCount: 0, ProvenanceJSON: json.RawMessage(`[]`),
	})
	if errorsIsConflict(err) {
		return nil, &agenttools.ToolError{Category: agenttools.ErrorConflict, Message: "制品幂等性冲突", Cause: err}
	}
	if err != nil {
		return nil, err
	}
	return toBuiltin(record), nil
}

// toBuiltin 执行该函数负责的核心处理逻辑。
func toBuiltin(record *Record) *builtin.Artifact {
	return &builtin.Artifact{
		ID: record.ID, URI: "artifact://" + record.ID, WorkspaceID: record.WorkspaceID,
		DataClassification: record.DataClassification, Content: append(json.RawMessage(nil), record.ContentJSON...),
		ContentHash: record.ContentHash, CreatedAt: record.CreatedAt,
	}
}

// errorsIsConflict 执行该函数负责的核心处理逻辑。
func errorsIsConflict(err error) bool {
	return errors.Is(err, ErrIdempotencyConflict)
}
