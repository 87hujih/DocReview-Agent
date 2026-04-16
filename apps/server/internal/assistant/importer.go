package assistant

import (
	"context"

	"agent_project/apps/server/internal/knowledge/ingest"
)

// IngestDocumentImporter 把知识层 ingest 服务适配为助手领域需要的导入接口。
type IngestDocumentImporter struct {
	service *ingest.Service
}

// NewIngestDocumentImporter 创建一个 ingest 适配器。
func NewIngestDocumentImporter(service *ingest.Service) *IngestDocumentImporter {
	return &IngestDocumentImporter{service: service}
}

// ImportDocument 调用知识层导入逻辑，并转成助手领域结果。
func (i *IngestDocumentImporter) ImportDocument(ctx context.Context, input ImportDocumentInput) (*ImportDocumentResult, error) {
	result, err := i.service.ImportDocument(ctx, ingest.ImportDocumentInput{
		FileName: input.FileName,
		Content:  input.Content,
	})
	if err != nil {
		return nil, err
	}

	return &ImportDocumentResult{
		Resource: result.Resource,
		Version:  result.Version,
	}, nil
}
