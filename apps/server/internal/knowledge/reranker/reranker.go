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

type Client struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type Result struct {
	Index          int
	RelevanceScore float64
}

type rerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n"`
	ReturnDocuments bool     `json:"return_documents"`
}

type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

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
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rerank", bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send rerank request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rerank request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
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
