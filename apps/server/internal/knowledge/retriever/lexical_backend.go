package retriever

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// LexicalBackend 抽象版本范围内的词法候选召回能力。
type LexicalBackend interface {
	Search(ctx context.Context, query string, scope lexicalSearchScope) ([]postgres.ResourceChunk, error)
}

type lexicalSearchScope struct {
	Limit     int
	VersionID string
}

type postgresLexicalBackend struct {
	repo resourceRepository
}

// OpenSearchBM25BackendOptions 描述 OpenSearch BM25 backend 的初始化参数。
type OpenSearchBM25BackendOptions struct {
	BaseURL     string
	IndexChunks string
	Username    string
	Password    string
	TLSInsecure bool
	HTTPClient  *http.Client
}

type openSearchBM25Backend struct {
	baseURL     string
	indexChunks string
	username    string
	password    string
	httpClient  *http.Client
}

type openSearchSearchRequest struct {
	Size   int                 `json:"size"`
	Source []string            `json:"_source"`
	Query  openSearchBoolQuery `json:"query"`
}

type openSearchBoolQuery struct {
	Bool openSearchBoolClause `json:"bool"`
}

type openSearchBoolClause struct {
	Must   []openSearchQueryClause  `json:"must"`
	Filter []openSearchFilterClause `json:"filter,omitempty"`
}

type openSearchQueryClause struct {
	MultiMatch openSearchMultiMatchQuery `json:"multi_match"`
}

type openSearchMultiMatchQuery struct {
	Query  string   `json:"query"`
	Fields []string `json:"fields"`
}

type openSearchFilterClause struct {
	Term map[string]string `json:"term"`
}

type openSearchSearchResponse struct {
	Hits struct {
		Hits []struct {
			Source openSearchChunkSource `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type openSearchChunkSource struct {
	ResourceID    string         `json:"resource_id"`
	VersionID     string         `json:"version_id"`
	SectionID     string         `json:"section_id"`
	SectionType   string         `json:"section_type"`
	ChunkID       string         `json:"chunk_id"`
	ChunkRole     string         `json:"chunk_role"`
	ChunkIndex    int            `json:"chunk_index"`
	SectionTitle  string         `json:"section_title"`
	Content       string         `json:"content"`
	WindowGroupID string         `json:"window_group_id"`
	PageStart     int            `json:"page_start"`
	PageEnd       int            `json:"page_end"`
	Metadata      map[string]any `json:"metadata"`
}

func newPostgresLexicalBackend(repo resourceRepository) LexicalBackend {
	return &postgresLexicalBackend{repo: repo}
}

// NewOpenSearchBM25Backend 创建通过 OpenSearch `_search` API 执行 BM25 查询的 backend。
func NewOpenSearchBM25Backend(options OpenSearchBM25BackendOptions) LexicalBackend {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	indexChunks := strings.TrimSpace(options.IndexChunks)
	if baseURL == "" || indexChunks == "" {
		return nil
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if options.TLSInsecure {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
		httpClient = &http.Client{Transport: transport}
	}

	return &openSearchBM25Backend{
		baseURL:     baseURL,
		indexChunks: indexChunks,
		username:    strings.TrimSpace(options.Username),
		password:    options.Password,
		httpClient:  httpClient,
	}
}

func (b *postgresLexicalBackend) Search(ctx context.Context, query string, scope lexicalSearchScope) ([]postgres.ResourceChunk, error) {
	if scope.Limit <= 0 {
		return []postgres.ResourceChunk{}, nil
	}

	if strings.TrimSpace(scope.VersionID) != "" {
		return b.repo.SearchChunksLexicalByVersion(ctx, query, scope.Limit, scope.VersionID)
	}

	return b.repo.SearchChunksLexical(ctx, query, scope.Limit)
}

func (b *openSearchBM25Backend) Search(ctx context.Context, query string, scope lexicalSearchScope) ([]postgres.ResourceChunk, error) {
	if scope.Limit <= 0 || strings.TrimSpace(query) == "" {
		return []postgres.ResourceChunk{}, nil
	}

	requestBody, err := json.Marshal(buildOpenSearchSearchRequest(query, scope))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		b.baseURL+"/"+b.indexChunks+"/_search",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.username != "" {
		req.SetBasicAuth(b.username, b.password)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenSearch BM25 查询失败，status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var parsed openSearchSearchResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, fmt.Errorf("解析 OpenSearch BM25 查询响应失败：%w", err)
	}

	chunks := make([]postgres.ResourceChunk, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		chunks = append(chunks, postgres.ResourceChunk{
			ID:            hit.Source.ChunkID,
			ResourceID:    hit.Source.ResourceID,
			VersionID:     hit.Source.VersionID,
			ChunkIndex:    hit.Source.ChunkIndex,
			SectionTitle:  hit.Source.SectionTitle,
			Content:       hit.Source.Content,
			SectionID:     hit.Source.SectionID,
			SectionType:   hit.Source.SectionType,
			ChunkRole:     hit.Source.ChunkRole,
			WindowGroupID: hit.Source.WindowGroupID,
			PageStart:     hit.Source.PageStart,
			PageEnd:       hit.Source.PageEnd,
			Metadata:      cloneChunkMetadata(hit.Source.Metadata),
		})
	}

	return chunks, nil
}

func buildOpenSearchSearchRequest(query string, scope lexicalSearchScope) openSearchSearchRequest {
	request := openSearchSearchRequest{
		Size: scope.Limit,
		Source: []string{
			"resource_id",
			"version_id",
			"section_id",
			"section_type",
			"chunk_id",
			"chunk_role",
			"chunk_index",
			"section_title",
			"content",
			"window_group_id",
			"page_start",
			"page_end",
			"metadata",
		},
		Query: openSearchBoolQuery{
			Bool: openSearchBoolClause{
				Must: []openSearchQueryClause{
					{
						MultiMatch: openSearchMultiMatchQuery{
							Query:  strings.TrimSpace(query),
							Fields: []string{"section_title^2", "content"},
						},
					},
				},
			},
		},
	}

	if strings.TrimSpace(scope.VersionID) != "" {
		request.Query.Bool.Filter = []openSearchFilterClause{
			{
				Term: map[string]string{
					"version_id": strings.TrimSpace(scope.VersionID),
				},
			},
		}
	}

	return request
}

func cloneChunkMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}

	return cloned
}
