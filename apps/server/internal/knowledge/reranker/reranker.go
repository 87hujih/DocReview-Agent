package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 承载客户端相关状态，明确重排链路中的数据边界。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// Result 承载目标数据的处理结果，方便上游统一消费。
type Result struct {
	Index          int
	RelevanceScore float64
}

// rerankRequest 定义重排接口接收的 JSON 请求体，收口当前接口需要的输入字段。
type rerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n"`
	ReturnDocuments bool     `json:"return_documents"`
}

// rerankResponse 定义重排接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// New 创建New，并补齐重排链路需要的默认依赖和缺省行为。
func New(baseURL string, apiKey string, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Rerank 对 `Rerank` 重排，统一排序策略和分数处理。
func (c *Client) Rerank(ctx context.Context, query string, documents []string, topN int) ([]Result, error) {
	if len(documents) == 0 {
		return []Result{}, nil
	}

	if topN <= 0 || topN > len(documents) {
		topN = len(documents)
	}

	requestBody, err := json.Marshal(rerankRequest{
		Model:           c.model,
		Query:           strings.TrimSpace(query),
		Documents:       documents,
		TopN:            topN,
		ReturnDocuments: false,
	})
	if err != nil {
		return nil, fmt.Errorf("序列化 rerank 请求失败：%w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rerank", bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("创建 rerank 请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送 rerank 请求失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rerank 请求失败：状态码 %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析 rerank 响应失败：%w", err)
	}

	results := make([]Result, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		results = append(results, Result{
			Index:          item.Index,
			RelevanceScore: item.RelevanceScore,
		})
	}

	return results, nil
}
