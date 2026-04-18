package assistant

import (
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
)

// RuntimeState 表示 assistant 在决策阶段消费的统一运行时状态。
type RuntimeState struct {
	Message               string
	Snapshot              *SessionContextSnapshot
	ActiveResource        *resourceContext
	Citations             []citation.Citation
	GroundedTarget        *ResolvedReference
	History               []postgres.AssistantMessage
	RollingSummary        *string
	PendingTaskSuggestion *SnapshotPendingTaskSuggestion
	LatestTask            *SnapshotLatestTask
	ConfirmedConstraints  []ConfirmedConstraint
}
