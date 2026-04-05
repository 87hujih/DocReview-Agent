package embedder

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
)

type Embedder struct {
	client *openaiacl.EmbeddingClient
	dim    int
}

func New(ctx context.Context, baseURL string, apiKey string, model string, dim int) (*Embedder, error) {
	config := &openaiacl.EmbeddingConfig{
		APIKey:     apiKey,
		Model:      model,
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
