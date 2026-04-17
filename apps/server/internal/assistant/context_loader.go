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
	retriever resourceCitationRetriever
}

// NewContextLoader 构造回复上下文装载器。
func NewContextLoader(snapshots sessionContextSnapshotReader, retriever resourceCitationRetriever) *ContextLoader {
	return &ContextLoader{
		snapshots: snapshots,
		retriever: retriever,
	}
}

// LoadForReply 优先读取快照；快照不可用时回退到历史扫描。
func (l *ContextLoader) LoadForReply(
	ctx context.Context,
	sessionID string,
	history []postgres.AssistantMessage,
	currentMessage string,
) (*ReplyContext, error) {
	replyContext := &ReplyContext{
		History: append([]postgres.AssistantMessage(nil), history...),
	}

	snapshot, err := l.loadSnapshot(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	replyContext.Snapshot = snapshot

	activeResource, err := l.resolveActiveResource(snapshot, history)
	if err != nil {
		return nil, err
	}
	replyContext.ActiveResource = activeResource

	citations, err := l.loadResourceCitations(ctx, activeResource, currentMessage)
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
