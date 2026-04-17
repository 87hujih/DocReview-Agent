package searchindex

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ClientOptions 描述 OpenSearch 客户端初始化参数。
type ClientOptions struct {
	BaseURL     string
	IndexChunks string
	Username    string
	Password    string
	TLSInsecure bool
	HTTPClient  *http.Client
}

// Client 负责维护 OpenSearch chunk 索引及其文档投影。
type Client struct {
	baseURL     string
	indexChunks string
	username    string
	password    string
	httpClient  *http.Client
}

type chunkIndexRequest struct {
	Settings map[string]any `json:"settings"`
	Mappings map[string]any `json:"mappings"`
}

type bulkAction struct {
	Index bulkActionMeta `json:"index"`
}

type bulkActionMeta struct {
	Index string `json:"_index"`
	ID    string `json:"_id"`
}

type deleteByQueryRequest struct {
	Query map[string]map[string]string `json:"query"`
}

type bulkResponse struct {
	Errors bool               `json:"errors"`
	Items  []bulkResponseItem `json:"items"`
}

type bulkResponseItem struct {
	Index bulkResponseStatus `json:"index"`
}

type bulkResponseStatus struct {
	Status int             `json:"status"`
	Error  json.RawMessage `json:"error"`
}

// NewClient 根据配置创建 OpenSearch HTTP 客户端；若关键信息缺失则返回 nil。
func NewClient(options ClientOptions) *Client {
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

	return &Client{
		baseURL:     baseURL,
		indexChunks: indexChunks,
		username:    strings.TrimSpace(options.Username),
		password:    options.Password,
		httpClient:  httpClient,
	}
}

// EnsureChunkIndex 确保 chunk 索引存在，便于后续 delete/upsert 正常执行。
func (c *Client) EnsureChunkIndex(ctx context.Context) error {
	existsReq, err := c.newRequest(ctx, http.MethodHead, "/"+c.indexChunks, nil, "")
	if err != nil {
		return err
	}

	existsResp, err := c.httpClient.Do(existsReq)
	if err != nil {
		return err
	}
	existsResp.Body.Close()

	switch existsResp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
	default:
		return unexpectedStatus("检查 chunk 索引是否存在", existsResp.StatusCode, nil)
	}

	body, err := json.Marshal(defaultChunkIndexRequest())
	if err != nil {
		return err
	}

	createReq, err := c.newRequest(ctx, http.MethodPut, "/"+c.indexChunks, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}

	createResp, err := c.httpClient.Do(createReq)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusOK && createResp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(createResp.Body)
		return unexpectedStatus("创建 chunk 索引", createResp.StatusCode, responseBody)
	}

	return nil
}

// DeleteVersionDocuments 删除某个资源版本的旧文档投影。
func (c *Client) DeleteVersionDocuments(ctx context.Context, versionID string) error {
	payload := deleteByQueryRequest{
		Query: map[string]map[string]string{
			"term": {
				"version_id": strings.TrimSpace(versionID),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := c.newRequest(
		ctx,
		http.MethodPost,
		"/"+c.indexChunks+"/_delete_by_query",
		bytes.NewReader(body),
		"application/json",
	)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return unexpectedStatus("删除版本旧文档", resp.StatusCode, responseBody)
	}

	return nil
}

// BulkUpsertChunkDocuments 通过 bulk API 批量写入当前版本 chunk 文档。
func (c *Client) BulkUpsertChunkDocuments(ctx context.Context, documents []ChunkDocument) error {
	if len(documents) == 0 {
		return nil
	}

	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, document := range documents {
		if err := encoder.Encode(bulkAction{
			Index: bulkActionMeta{
				Index: c.indexChunks,
				ID:    document.DocumentID,
			},
		}); err != nil {
			return err
		}
		if err := encoder.Encode(document); err != nil {
			return err
		}
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/_bulk", &body, "application/x-ndjson")
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return unexpectedStatus("bulk 写入 chunk 文档", resp.StatusCode, responseBody)
	}

	var parsed bulkResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return fmt.Errorf("解析 OpenSearch bulk 响应失败: %w", err)
	}
	if parsed.Errors {
		return fmt.Errorf("OpenSearch bulk 写入返回 errors=true: %s", strings.TrimSpace(string(responseBody)))
	}
	for _, item := range parsed.Items {
		if item.Index.Status >= 300 {
			return fmt.Errorf("OpenSearch bulk 写入项失败，status=%d error=%s", item.Index.Status, strings.TrimSpace(string(item.Index.Error)))
		}
	}

	return nil
}

func (c *Client) newRequest(ctx context.Context, method string, path string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}

	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return req, nil
}

func defaultChunkIndexRequest() chunkIndexRequest {
	return chunkIndexRequest{
		Settings: map[string]any{
			"index": map[string]any{
				"number_of_shards":   1,
				"number_of_replicas": 0,
			},
		},
		Mappings: map[string]any{
			"dynamic": false,
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "keyword"},
				"version_id":  map[string]any{"type": "keyword"},
				"section_id":  map[string]any{"type": "keyword"},
				"section_type": map[string]any{
					"type": "keyword",
				},
				"chunk_id": map[string]any{"type": "keyword"},
				"chunk_role": map[string]any{
					"type": "keyword",
				},
				"chunk_index": map[string]any{"type": "integer"},
				"section_title": map[string]any{
					"type": "text",
					"fields": map[string]any{
						"keyword": map[string]any{
							"type":         "keyword",
							"ignore_above": 256,
						},
					},
				},
				"content": map[string]any{"type": "text"},
				"window_group_id": map[string]any{
					"type": "keyword",
				},
				"page_start": map[string]any{"type": "integer"},
				"page_end":   map[string]any{"type": "integer"},
				"metadata": map[string]any{
					"type":    "object",
					"enabled": false,
				},
			},
		},
	}
}

func unexpectedStatus(action string, statusCode int, responseBody []byte) error {
	body := strings.TrimSpace(string(responseBody))
	if body == "" {
		return fmt.Errorf("%s失败，status=%d", action, statusCode)
	}

	return fmt.Errorf("%s失败，status=%d body=%s", action, statusCode, body)
}
