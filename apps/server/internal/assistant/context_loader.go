package assistant

import (
	"context"
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
	knowledgeRetriever "agent_project/apps/server/internal/knowledge/retriever"
	"agent_project/apps/server/internal/storage/postgres"
)

type sessionContextSnapshotReader interface {
	GetBySessionID(ctx context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error)
}

type assistantMessageWindowReader interface {
	ListMessagesAfterSequence(ctx context.Context, sessionID string, afterSequenceNo int) ([]postgres.AssistantMessage, error)
}

// ReplyContext 表示一轮回复组装所需的快照、资源与检索结果。
type ReplyContext struct {
	Snapshot       *SessionContextSnapshot
	ActiveResource *resourceContext
	Citations      []citation.Citation
	GroundedTarget *ResolvedReference
	QueryIntent    string
	History        []postgres.AssistantMessage
}

// ContextLoader 负责按 snapshot-first 规则装载本轮回复上下文。
type ContextLoader struct {
	snapshots sessionContextSnapshotReader
	messages  assistantMessageWindowReader
	retriever resourceCitationRetriever
	queries   *RetrievalQueryBuilder
}

// NewContextLoader 构造回复上下文装载器。
func NewContextLoader(
	snapshots sessionContextSnapshotReader,
	retriever resourceCitationRetriever,
	messageReaders ...assistantMessageWindowReader,
) *ContextLoader {
	var messageReader assistantMessageWindowReader
	if len(messageReaders) > 0 {
		messageReader = messageReaders[0]
	}

	return &ContextLoader{
		snapshots: snapshots,
		messages:  messageReader,
		retriever: retriever,
		queries:   &RetrievalQueryBuilder{},
	}
}

// LoadForReply 优先读取快照；快照不可用时回退到历史扫描。
func (l *ContextLoader) LoadForReply(
	ctx context.Context,
	sessionID string,
	history []postgres.AssistantMessage,
	currentMessage string,
) (*ReplyContext, error) {
	replyContext := &ReplyContext{}

	snapshot, err := l.loadSnapshot(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	replyContext.Snapshot = snapshot

	recentHistory, err := l.loadRecentHistory(ctx, sessionID, snapshot, history)
	if err != nil {
		return nil, err
	}
	replyContext.History = recentHistory

	activeResource, err := l.resolveActiveResource(snapshot, history)
	if err != nil {
		return nil, err
	}
	replyContext.ActiveResource = activeResource

	resolvedReference := ReferenceResolver{}.Resolve(strings.TrimSpace(currentMessage), snapshot)
	replyContext.GroundedTarget = resolvedReference

	searchQuery := strings.TrimSpace(currentMessage)
	if l.queries != nil {
		searchQuery = l.queries.Build(RetrievalQueryInput{
			CurrentMessage:        searchQuery,
			ActiveResource:        snapshotActiveResource(snapshot),
			RollingSummary:        snapshotRollingSummary(snapshot),
			PendingTaskSuggestion: snapshotPendingTaskSuggestion(snapshot),
			ResolvedReference:     resolvedReference,
		})
	}
	replyContext.QueryIntent = string(knowledgeRetriever.QueryAnalyzer{}.Analyze(searchQuery).Intent)

	citations, err := l.loadResourceCitations(ctx, activeResource, searchQuery)
	if err != nil {
		return nil, err
	}
	replyContext.Citations = citations

	return replyContext, nil
}

// loadSnapshot 加载 `快照`，为后续助手流程准备输入。
func (l *ContextLoader) loadSnapshot(ctx context.Context, sessionID string) (*SessionContextSnapshot, error) {
	if l == nil || l.snapshots == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}

	record, err := l.snapshots.GetBySessionID(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}

	return SessionContextSnapshotFromRecord(record)
}

// loadRecentHistory 加载 `Recent历史`，为后续助手流程准备输入。
func (l *ContextLoader) loadRecentHistory(
	ctx context.Context,
	sessionID string,
	snapshot *SessionContextSnapshot,
	history []postgres.AssistantMessage,
) ([]postgres.AssistantMessage, error) {
	afterSequenceNo := 0
	if snapshot != nil && snapshot.SummaryBaseSequenceNo > 0 {
		afterSequenceNo = snapshot.SummaryBaseSequenceNo
	}

	if l != nil && l.messages != nil && strings.TrimSpace(sessionID) != "" {
		messages, err := l.messages.ListMessagesAfterSequence(ctx, strings.TrimSpace(sessionID), afterSequenceNo)
		if err != nil {
			return nil, err
		}

		return filterRecentTextMessages(messages, afterSequenceNo), nil
	}

	return filterRecentTextMessages(history, afterSequenceNo), nil
}

// resolveActiveResource 解析 `Active资源`，确定当前处理应落到的目标对象。
func (l *ContextLoader) resolveActiveResource(
	snapshot *SessionContextSnapshot,
	history []postgres.AssistantMessage,
) (*resourceContext, error) {
	if resource := resourceContextFromSnapshot(snapshot); resource != nil {
		return resource, nil
	}

	return latestResourceFromMessages(history)
}

// loadResourceCitations 加载 `资源引用`，为后续助手流程准备输入。
func (l *ContextLoader) loadResourceCitations(
	ctx context.Context,
	resource *resourceContext,
	query string,
) ([]citation.Citation, error) {
	if l == nil || l.retriever == nil || resource == nil {
		return nil, nil
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, nil
	}

	return l.retriever.SearchByResource(ctx, resource.ID, trimmedQuery, 4)
}

// resourceContextFromSnapshot 从会话快照提取当前激活资源的上下文信息，供回复阶段直接注入。
func resourceContextFromSnapshot(snapshot *SessionContextSnapshot) *resourceContext {
	if snapshot == nil || snapshot.ActiveResource == nil {
		return nil
	}

	if strings.TrimSpace(snapshot.ActiveResource.ID) == "" ||
		strings.TrimSpace(snapshot.ActiveResource.Title) == "" ||
		strings.TrimSpace(snapshot.ActiveResource.SourceType) == "" {
		return nil
	}

	return &resourceContext{
		ID:     strings.TrimSpace(snapshot.ActiveResource.ID),
		Title:  strings.TrimSpace(snapshot.ActiveResource.Title),
		Source: strings.TrimSpace(snapshot.ActiveResource.SourceType),
	}
}

// filterRecentTextMessages 过滤 `Recent文本消息`，把筛选规则收口在单点。
func filterRecentTextMessages(messages []postgres.AssistantMessage, afterSequenceNo int) []postgres.AssistantMessage {
	if len(messages) == 0 {
		return nil
	}

	filtered := make([]postgres.AssistantMessage, 0, len(messages))
	for _, message := range messages {
		if message.SequenceNo <= afterSequenceNo || message.Kind != KindText {
			continue
		}

		filtered = append(filtered, message)
	}

	return filtered
}

// snapshotActiveResource 把当前激活资源投影成快照字段，统一会话上下文持久化格式。
func snapshotActiveResource(snapshot *SessionContextSnapshot) *SnapshotActiveResource {
	if snapshot == nil || snapshot.ActiveResource == nil {
		return nil
	}

	resource := *snapshot.ActiveResource
	return &resource
}

// snapshotRollingSummary 把滚动摘要投影成快照字段，供后续轮次直接复用。
func snapshotRollingSummary(snapshot *SessionContextSnapshot) *string {
	if snapshot == nil || snapshot.RollingSummary == nil {
		return nil
	}

	summary := *snapshot.RollingSummary
	return &summary
}

// snapshotPendingTaskSuggestion 把待确认任务建议投影成快照字段，保持会话状态可恢复。
func snapshotPendingTaskSuggestion(snapshot *SessionContextSnapshot) *SnapshotPendingTaskSuggestion {
	if snapshot == nil || snapshot.PendingTaskSuggestion == nil {
		return nil
	}

	suggestion := *snapshot.PendingTaskSuggestion
	return &suggestion
}
