package searchindex

import (
	"context"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

const syncBackendOpenSearchBM25 = "opensearch_bm25"

type versionChunkReader interface {
	ListChunksByVersion(ctx context.Context, versionID string) ([]postgres.ResourceChunk, error)
}

type chunkIndexClient interface {
	EnsureChunkIndex(ctx context.Context) error
	DeleteVersionDocuments(ctx context.Context, versionID string) error
	BulkUpsertChunkDocuments(ctx context.Context, documents []ChunkDocument) error
}

// Service 统一封装按资源版本同步 OpenSearch 文档投影的流程。
type Service struct {
	repo    versionChunkReader
	client  chunkIndexClient
	enabled bool
}

// NewSyncService 根据当前检索后端配置创建版本级同步服务。
func NewSyncService(repo versionChunkReader, client chunkIndexClient, searchBackend string) *Service {
	return &Service{
		repo:    repo,
		client:  client,
		enabled: repo != nil && client != nil && normalizeSyncBackend(searchBackend) == syncBackendOpenSearchBM25,
	}
}

// SyncVersion 统一执行“读取真源 chunk -> 删除旧文档 -> bulk 写入当前版本”的同步流程。
func (s *Service) SyncVersion(ctx context.Context, resourceID string, versionID string) error {
	_ = resourceID
	if !s.enabled {
		return nil
	}

	chunks, err := s.repo.ListChunksByVersion(ctx, versionID)
	if err != nil {
		return err
	}

	if err := s.client.EnsureChunkIndex(ctx); err != nil {
		return err
	}
	if err := s.client.DeleteVersionDocuments(ctx, versionID); err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}

	return s.client.BulkUpsertChunkDocuments(ctx, buildChunkDocuments(chunks))
}

func normalizeSyncBackend(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
