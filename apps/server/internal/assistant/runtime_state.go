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
	CurrentDocument       *CurrentDocument
	ActiveNode            *SnapshotActiveNode
	NodeReferenceFrame    []NodeReference
	Citations             []citation.Citation
	GroundedTarget        *ResolvedReference
	History               []postgres.AssistantMessage
	RollingSummary        *string
	PendingClarification  *SnapshotPendingClarification
	AdvisoryContext       *SnapshotAdvisoryContext
	PendingProposal       *SnapshotPendingProposal
	AuthorizationState    *SnapshotAuthorizationState
	ExecutionState        *SnapshotExecutionState
	PendingTaskSuggestion *SnapshotPendingTaskSuggestion
	LatestTask            *SnapshotLatestTask
	ConfirmedConstraints  []ConfirmedConstraint
}
