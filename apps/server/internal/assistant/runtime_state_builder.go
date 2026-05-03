package assistant

import (
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
)

// RuntimeStateBuilder 负责把回复上下文收拢成决策阶段可直接消费的统一状态。
type RuntimeStateBuilder struct{}

// Build 把当前消息与回复上下文组装成统一运行时状态。
func (b *RuntimeStateBuilder) Build(currentMessage string, replyContext *ReplyContext) RuntimeState {
	state := RuntimeState{
		Message: strings.TrimSpace(currentMessage),
	}
	if replyContext == nil {
		return state
	}

	state.Snapshot = replyContext.Snapshot
	state.ActiveResource = cloneResourceContext(replyContext.ActiveResource)
	state.CurrentDocument = cloneCurrentDocument(replyContext.CurrentDocument)
	state.Citations = cloneCitations(replyContext.Citations)
	state.GroundedTarget = cloneResolvedReference(replyContext.GroundedTarget)
	state.History = cloneAssistantMessages(replyContext.History)

	if replyContext.Snapshot == nil {
		return state
	}

	state.ActiveNode = cloneActiveNode(replyContext.Snapshot.ActiveNode)
	state.NodeReferenceFrame = cloneNodeReferences(replyContext.Snapshot.NodeReferenceFrame)
	state.RollingSummary = cloneOptionalString(replyContext.Snapshot.RollingSummary)
	state.PendingClarification = clonePendingClarification(replyContext.Snapshot.PendingClarification)
	state.AdvisoryContext = cloneAdvisoryContext(replyContext.Snapshot.AdvisoryContext)
	state.PendingProposal = clonePendingProposal(replyContext.Snapshot.PendingProposal)
	state.AuthorizationState = cloneAuthorizationState(replyContext.Snapshot.AuthorizationState)
	state.ExecutionState = cloneExecutionState(replyContext.Snapshot.ExecutionState)
	state.PendingTaskSuggestion = clonePendingTaskSuggestion(replyContext.Snapshot.PendingTaskSuggestion)
	state.LatestTask = cloneLatestTask(replyContext.Snapshot.LatestTask)
	state.ConfirmedConstraints = cloneConfirmedConstraints(replyContext.Snapshot.ConfirmedConstraints)

	return state
}

// cloneResourceContext 复制活跃资源，避免后续决策阶段误改原始上下文。
func cloneResourceContext(resource *resourceContext) *resourceContext {
	if resource == nil {
		return nil
	}

	cloned := *resource
	return &cloned
}

// cloneResolvedReference 复制 grounding 目标，避免后续决策阶段误改原始上下文。
func cloneResolvedReference(reference *ResolvedReference) *ResolvedReference {
	if reference == nil {
		return nil
	}

	cloned := *reference
	return &cloned
}

// clonePendingClarification 复制待确认澄清状态，避免后续决策阶段误改原始快照。
func clonePendingClarification(value *SnapshotPendingClarification) *SnapshotPendingClarification {
	if value == nil {
		return nil
	}

	cloned := *value
	cloned.Options = append([]string(nil), value.Options...)
	return &cloned
}

// cloneActiveNode 复制当前 active node，避免后续决策阶段误改原始快照。
func cloneActiveNode(value *SnapshotActiveNode) *SnapshotActiveNode {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// cloneNodeReferences 复制 node reference frame，避免后续决策阶段误改原始快照切片。
func cloneNodeReferences(items []NodeReference) []NodeReference {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]NodeReference, len(items))
	copy(cloned, items)
	return cloned
}

// cloneAdvisoryContext 复制顾问结论状态，避免后续决策阶段误改原始快照。
func cloneAdvisoryContext(value *SnapshotAdvisoryContext) *SnapshotAdvisoryContext {
	if value == nil {
		return nil
	}

	cloned := *value
	cloned.Recommendations = append([]string(nil), value.Recommendations...)
	return &cloned
}

// clonePendingProposal 复制待确认 proposal，避免后续决策阶段误改原始快照。
func clonePendingProposal(value *SnapshotPendingProposal) *SnapshotPendingProposal {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// cloneAuthorizationState 复制授权状态，避免后续决策阶段误改原始快照。
func cloneAuthorizationState(value *SnapshotAuthorizationState) *SnapshotAuthorizationState {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// cloneExecutionState 复制执行状态，避免后续决策阶段误改原始快照。
func cloneExecutionState(value *SnapshotExecutionState) *SnapshotExecutionState {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// clonePendingTaskSuggestion 复制待确认任务建议，避免后续决策阶段误改原始快照。
func clonePendingTaskSuggestion(suggestion *SnapshotPendingTaskSuggestion) *SnapshotPendingTaskSuggestion {
	if suggestion == nil {
		return nil
	}

	cloned := *suggestion
	return &cloned
}

// cloneLatestTask 复制最近任务，避免后续决策阶段误改原始快照。
func cloneLatestTask(task *SnapshotLatestTask) *SnapshotLatestTask {
	if task == nil {
		return nil
	}

	cloned := *task
	return &cloned
}

// cloneConfirmedConstraints 复制已确认约束，避免后续决策阶段误改原始快照切片。
func cloneConfirmedConstraints(constraints []ConfirmedConstraint) []ConfirmedConstraint {
	if len(constraints) == 0 {
		return nil
	}

	cloned := make([]ConfirmedConstraint, len(constraints))
	copy(cloned, constraints)
	return cloned
}

// cloneCitations 复制证据片段，避免后续决策阶段误改原始引用切片。
func cloneCitations(items []citation.Citation) []citation.Citation {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]citation.Citation, 0, len(items))
	for _, item := range items {
		copied := item
		if item.Window != nil {
			window := *item.Window
			copied.Window = &window
		}
		cloned = append(cloned, copied)
	}

	return cloned
}

// cloneAssistantMessages 复制历史消息，避免后续决策阶段误改原始消息切片。
func cloneAssistantMessages(messages []postgres.AssistantMessage) []postgres.AssistantMessage {
	if len(messages) == 0 {
		return nil
	}

	cloned := make([]postgres.AssistantMessage, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].Payload = append([]byte(nil), message.Payload...)
	}

	return cloned
}
