package embedder

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
)

// Embedder 封装兼容 OpenAI 协议的 embedding 客户端，并校验向量维度。
type Embedder struct {
	client *openaiacl.EmbeddingClient
	dim    int
}

// New 构造一个始终带非空 HTTP client 的 embedder，避免上游 typed nil 导致 panic。
func New(ctx context.Context, baseURL string, apiKey string, model string, dim int) (*Embedder, error) {
	config := &openaiacl.EmbeddingConfig{
		APIKey: apiKey,
		Model:  model,
		// 上游配置里的 HTTP client 是接口类型，这里显式传入真实 client，避免 typed nil 绕过空判断。
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
	if strings.TrimSpace(baseURL) != "" {
		config.BaseURL = baseURL
	}
	if dim > 0 {
		config.Dimensions = &dim
	}

	client, err := openaiacl.NewEmbeddingClient(ctx, config)
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
