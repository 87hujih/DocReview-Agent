package embedder

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	openaiembedding "github.com/cloudwego/eino-ext/components/embedding/openai"
)

// Embedder 封装兼容 OpenAI 协议的 embedding 客户端，并校验向量维度。
type Embedder struct {
	client *openaiembedding.Embedder
	dim    int
}

// New 构造一个始终带非空 HTTP client 的 embedder，避免上游 typed nil 导致 panic。
func New(ctx context.Context, baseURL string, apiKey string, model string, dim int) (*Embedder, error) {
	config := &openaiembedding.EmbeddingConfig{
		APIKey: apiKey,
		Model:  model,
		// 官方 embedding 组件底层仍会转发到 ACL client，这里继续显式传入真实 client，避免 typed nil 绕过空判断。
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
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
		dim:    dim,
	}, nil
}

// Embed 把服务商返回结果转换为可直接写入 pgvector 的 float32 切片。
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	embeddings, err := e.client.EmbedStrings(ctx, texts)
	if err != nil {
		return nil, err
	}

	vectors := make([][]float32, len(embeddings))
	for index, embedding := range embeddings {
		if e.dim > 0 && len(embedding) != e.dim {
			return nil, fmt.Errorf("unexpected embedding dimension %d, want %d", len(embedding), e.dim)
		}

		vector := make([]float32, len(embedding))
		for valueIndex, value := range embedding {
			vector[valueIndex] = float32(value)
		}

		vectors[index] = vector
	}

	return vectors, nil
}
