package builtin

import (
	"context"
	"errors"

	agenttools "agent_project/apps/server/internal/agent/tools"
	assistantweb "agent_project/apps/server/internal/assistant/websearch"
)

// 处理失败： ProviderWebBackend adapts the existing web 提供方 behind ToolRuntime. The
// registry 配置 still pins the 预期的 提供方 name 和 whether it
// 为 mock 或 production, so 一个 mock 响应 不能 be represented as 一个
// 处理失败： production capability.
type ProviderWebBackend struct {
	Provider assistantweb.WebSearchProvider
}

// Search 执行该函数负责的核心处理逻辑。
func (backend ProviderWebBackend) Search(ctx context.Context, input WebSearchInput, traceID string) (WebSearchOutput, error) {
	if backend.Provider == nil {
		return WebSearchOutput{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "Web 提供方不可用"}
	}
	response, err := backend.Provider.Search(ctx, input.Query, assistantweb.SearchOptions{Limit: input.Limit, TraceID: traceID})
	if err != nil {
		return WebSearchOutput{}, classifyWebProviderError(err)
	}
	if response == nil || response.Provider == "" {
		return WebSearchOutput{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "Web 提供方返回了无效响应"}
	}
	results := make([]WebResult, 0, len(response.Results))
	for _, result := range response.Results {
		results = append(results, WebResult{
			Title: result.Title, URL: result.URL, Snippet: result.Snippet,
			Publisher: result.Source, PublishedAt: result.PublishedAt,
		})
	}
	return WebSearchOutput{Provider: response.Provider, Results: results}, nil
}

// classifyWebProviderError 执行该函数负责的核心处理逻辑。
func classifyWebProviderError(err error) error {
	var providerErr *assistantweb.ProviderError
	if !errors.As(err, &providerErr) {
		return &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "Web 提供方调用失败", Cause: err}
	}
	category := agenttools.ErrorTerminalUpstream
	// 根据当前状态或类型选择对应的处理分支。
	switch providerErr.Code {
	case assistantweb.ErrorInvalidRequest:
		category = agenttools.ErrorInvalidInput
	case assistantweb.ErrorProviderTimeout:
		category = agenttools.ErrorTimeout
	case assistantweb.ErrorProviderUnavailable:
		category = agenttools.ErrorRetryableUpstream
	case assistantweb.ErrorInvalidResponse:
		category = agenttools.ErrorTerminalUpstream
	}
	return &agenttools.ToolError{Category: category, Message: providerErr.Error(), Cause: providerErr}
}
