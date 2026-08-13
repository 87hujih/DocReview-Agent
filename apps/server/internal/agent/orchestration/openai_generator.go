package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	openaiacl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/schema"
)

type OpenAIChatConfig struct {
	APIKey      string
	BaseURL     string
	Model       string
	Timeout     time.Duration
	Temperature float64
}

type OpenAIChatGenerator struct {
	client *openaiacl.Client
}

// NewOpenAIChatGenerator 校验依赖并创建对应实例。
func NewOpenAIChatGenerator(ctx context.Context, cfg OpenAIChatConfig) (*OpenAIChatGenerator, error) {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.APIKey == "" || cfg.Model == "" || cfg.Timeout <= 0 || cfg.Temperature < 0 || cfg.Temperature > 2 {
		return nil, fmt.Errorf("OpenAI-兼容类型化的模型配置无效")
	}
	temperature := float32(cfg.Temperature)
	clientConfig := &openaiacl.Config{
		APIKey: cfg.APIKey, Model: cfg.Model, Temperature: &temperature,
		HTTPClient:     &http.Client{Timeout: cfg.Timeout},
		ResponseFormat: &openaiacl.ChatCompletionResponseFormat{Type: openaiacl.ChatCompletionResponseFormatTypeJSONObject},
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		clientConfig.BaseURL = strings.TrimSpace(cfg.BaseURL)
	}
	client, err := openaiacl.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, err
	}
	return &OpenAIChatGenerator{client: client}, nil
}

// Generate 执行该函数负责的核心处理逻辑。
func (generator *OpenAIChatGenerator) Generate(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if generator == nil || generator.client == nil {
		return ChatResponse{}, fmt.Errorf("类型化的模型客户端不可用")
	}
	response, err := generator.client.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: request.System},
		{Role: schema.User, Content: request.User},
	})
	if err != nil {
		return ChatResponse{}, err
	}
	output := json.RawMessage(strings.TrimSpace(response.Content))
	if !json.Valid(output) {
		return ChatResponse{}, fmt.Errorf("类型化的模型返回了无效的 JSON")
	}
	return ChatResponse{Output: output, FinishReason: "stop"}, nil
}

var _ ChatGenerator = (*OpenAIChatGenerator)(nil)
