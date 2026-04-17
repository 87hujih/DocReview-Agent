package assistant

import (
	"context"
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
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

	citations, err := l.loadResourceCitations(ctx, activeResource, snapshot, currentMessage)
	if err != nil {
		return nil, err
	}
	replyContext.Citations = citations

	return replyContext, nil
}

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

func (l *ContextLoader) resolveActiveResource(
	snapshot *SessionContextSnapshot,
	history []postgres.AssistantMessage,
) (*resourceContext, error) {
	if resource := resourceContextFromSnapshot(snapshot); resource != nil {
		return resource, nil
	}

	return latestResourceFromMessages(history)
}

func (l *ContextLoader) loadResourceCitations(
	ctx context.Context,
	resource *resourceContext,
	snapshot *SessionContextSnapshot,
	query string,
) ([]citation.Citation, error) {
	if l == nil || l.retriever == nil || resource == nil {
		return nil, nil
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, nil
	}

	if l.queries != nil {
		trimmedQuery = l.queries.Build(RetrievalQueryInput{
			CurrentMessage: trimmedQuery,
			ActiveResource: snapshotActiveResource(snapshot),
			RollingSummary: snapshotRollingSummary(snapshot),
			PendingTaskSuggestion: snapshotPendingTaskSuggestion(snapshot),
		})
	}
	if strings.TrimSpace(trimmedQuery) == "" {
		return nil, nil
	}

	return l.retriever.SearchByResource(ctx, resource.ID, trimmedQuery, 4)
}

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

func snapshotActiveResource(snapshot *SessionContextSnapshot) *SnapshotActiveResource {
	if snapshot == nil || snapshot.ActiveResource == nil {
		return nil
	}

	resource := *snapshot.ActiveResource
	return &resource
}

func snapshotRollingSummary(snapshot *SessionContextSnapshot) *string {
	if snapshot == nil || snapshot.RollingSummary == nil {
		return nil
	}

	summary := *snapshot.RollingSummary
	return &summary
}

func snapshotPendingTaskSuggestion(snapshot *SessionContextSnapshot) *SnapshotPendingTaskSuggestion {
	if snapshot == nil || snapshot.PendingTaskSuggestion == nil {
		return nil
	}

	suggestion := *snapshot.PendingTaskSuggestion
	return &suggestion
}
