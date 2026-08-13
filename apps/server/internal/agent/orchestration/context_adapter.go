package orchestration

import (
	"context"
	"fmt"

	agentcontext "agent_project/apps/server/internal/agent/context"
)

type ContextCandidateSource interface {
	Candidates(ctx context.Context, request ContextRequest) ([]agentcontext.Item, error)
}

// ManagedContextAssembler 处理失败： ManagedContextAssembler 为 the orchestration-facing 适配器 around the 一个
// ContextAssembler. 候选结果查询为明确的和 separate; the 组装器
// itself remains 数据库-free 和 owns selection/budgeting/persistence.
type ManagedContextAssembler struct {
	assembler *agentcontext.Assembler
	reader    agentcontext.Reader
	source    ContextCandidateSource
}

// NewManagedContextAssembler 校验依赖并创建对应实例。
func NewManagedContextAssembler(assembler *agentcontext.Assembler, reader agentcontext.Reader, source ContextCandidateSource) (*ManagedContextAssembler, error) {
	if assembler == nil || reader == nil || source == nil {
		return nil, fmt.Errorf("上下文组装器、清单读取器、和候选结果来源不能为空")
	}
	return &ManagedContextAssembler{assembler: assembler, reader: reader, source: source}, nil
}

// Assemble 执行该函数负责的核心处理逻辑。
func (adapter *ManagedContextAssembler) Assemble(ctx context.Context, request ContextRequest) (ContextSnapshot, error) {
	items, err := adapter.source.Candidates(ctx, request)
	if err != nil {
		return ContextSnapshot{}, err
	}
	result, err := adapter.assembler.Assemble(ctx, agentcontext.Request{RunID: request.RunID, StepID: request.StepID, Items: items})
	if err != nil {
		return ContextSnapshot{}, err
	}
	return snapshotFromManifest(result.Manifest), nil
}

// Load 加载按作用域读取并返回所需数据。
func (adapter *ManagedContextAssembler) Load(ctx context.Context, manifestID string) (ContextSnapshot, error) {
	manifest, err := adapter.reader.Load(ctx, manifestID)
	if err != nil {
		return ContextSnapshot{}, err
	}
	return snapshotFromManifest(manifest), nil
}

// snapshotFromManifest 执行该函数负责的核心处理逻辑。
func snapshotFromManifest(manifest agentcontext.Manifest) ContextSnapshot {
	return ContextSnapshot{ManifestID: manifest.ID, Items: append([]agentcontext.Item(nil), manifest.Items...)}
}

var _ ContextAssembler = (*ManagedContextAssembler)(nil)
