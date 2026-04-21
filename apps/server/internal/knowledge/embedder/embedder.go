package embedder

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	openaiembedding "github.com/cloudwego/eino-ext/components/embedding/openai"
)

// Embedder 封装兼容 OpenAI 协议的 embedding 客户端，并校验向量维度。
type Embedder struct {
	client *openaiembedding.Embedder
	model  string
	dim    int
}

// embedTraceCapture 保存单次 embeddings 请求返回的 trace 信息，便于失败时拼出诊断上下文。
type embedTraceCapture struct {
	traceID string
}

// embedTraceCaptureContextKey 是 embeddings trace 捕获器挂到上下文里的私有键。
type embedTraceCaptureContextKey struct{}

// embeddingRequestStats 汇总本次 embeddings 请求的输入规模，避免直接打印原文内容。
type embeddingRequestStats struct {
	inputCount int
	totalRunes int
	maxRunes   int
}

// embedTraceTransport 在不改第三方 SDK 的前提下抓取响应头里的 trace id。
type embedTraceTransport struct {
	base http.RoundTripper
}

// New 构造一个始终带非空 HTTP client 的 embedder，避免上游 typed nil 导致 panic。
func New(ctx context.Context, baseURL string, apiKey string, model string, dim int) (*Embedder, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: embedTraceTransport{
			base: http.DefaultTransport,
		},
	}
	config := &openaiembedding.EmbeddingConfig{
		APIKey: apiKey,
		Model:  model,
		// 官方 embedding 组件底层仍会转发到 ACL client，这里继续显式传入真实 client，避免 typed nil 绕过空判断。
		HTTPClient: httpClient,
	}
	if strings.TrimSpace(baseURL) != "" {
		config.BaseURL = baseURL
	}
	if dim > 0 {
		config.Dimensions = &dim
	}

	client, err := openaiembedding.NewEmbedder(ctx, config)
	if err != nil {
		return nil, err
	}

	return &Embedder{
		client: client,
		model:  strings.TrimSpace(model),
		dim:    dim,
	}, nil
}

// Embed 把服务商返回结果转换为可直接写入 pgvector 的 float32 切片。
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	stats := summarizeEmbeddingRequest(texts)
	traceCapture := &embedTraceCapture{}
	ctx = context.WithValue(ctx, embedTraceCaptureContextKey{}, traceCapture)

	embeddings, err := e.client.EmbedStrings(ctx, texts)
	if err != nil {
		wrappedErr := fmt.Errorf(
			"embedding 请求失败：model=%s dimensions=%d input_count=%d total_runes=%d max_runes=%d trace_id=%s: %w",
			e.model,
			e.dim,
			stats.inputCount,
			stats.totalRunes,
			stats.maxRunes,
			traceCapture.normalizedTraceID(),
			err,
		)
		log.Printf("警告：%v", wrappedErr)
		return nil, wrappedErr
	}

	vectors := make([][]float32, len(embeddings))
	for index, embedding := range embeddings {
		if e.dim > 0 && len(embedding) != e.dim {
			return nil, fmt.Errorf("embedding 维度异常：实际 %d，期望 %d", len(embedding), e.dim)
		}

		vector := make([]float32, len(embedding))
		for valueIndex, value := range embedding {
			vector[valueIndex] = float32(value)
		}

		vectors[index] = vector
	}

	return vectors, nil
}

// RoundTrip 抓取 embeddings 响应头里的 trace id，供调用方在失败时补齐诊断信息。
func (t embedTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(req)
	if capture, ok := req.Context().Value(embedTraceCaptureContextKey{}).(*embedTraceCapture); ok && capture != nil && resp != nil {
		capture.traceID = strings.TrimSpace(resp.Header.Get("x-siliconcloud-trace-id"))
	}

	return resp, err
}

// normalizedTraceID 统一 trace id 的空值表现，避免错误信息里出现空字段。
func (c *embedTraceCapture) normalizedTraceID() string {
	if c == nil || strings.TrimSpace(c.traceID) == "" {
		return "unknown"
	}

	return strings.TrimSpace(c.traceID)
}

// summarizeEmbeddingRequest 汇总输入规模，方便在不暴露正文的情况下定位上游参数问题。
func summarizeEmbeddingRequest(texts []string) embeddingRequestStats {
	stats := embeddingRequestStats{
		inputCount: len(texts),
	}
	for _, text := range texts {
		runeCount := len([]rune(text))
		stats.totalRunes += runeCount
		if runeCount > stats.maxRunes {
			stats.maxRunes = runeCount
		}
	}

	return stats
}
