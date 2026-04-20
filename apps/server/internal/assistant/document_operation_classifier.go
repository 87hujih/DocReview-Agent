package assistant

import "strings"

// DocumentOperation 表示当前文件已可见前提下，本轮消息要如何使用该文件。
type DocumentOperation string

const (
	DocumentOperationUnknown         DocumentOperation = "unknown"
	DocumentOperationRead            DocumentOperation = "read"
	DocumentOperationAnalyze         DocumentOperation = "analyze"
	DocumentOperationTransformInChat DocumentOperation = "transform_in_chat"
	DocumentOperationWorkflow        DocumentOperation = "workflow"
)

// DocumentOperationInput 归拢文档操作分类所需输入，避免调用方重复拼装条件。
type DocumentOperationInput struct {
	Message         string
	CurrentDocument *CurrentDocument
}

// ClassifyDocumentOperation 在“当前文件已可见”前提下判断本轮消息要如何使用文档。
func ClassifyDocumentOperation(input DocumentOperationInput) DocumentOperation {
	if input.CurrentDocument == nil || !input.CurrentDocument.Ready {
		return DocumentOperationUnknown
	}

	message := strings.TrimSpace(input.Message)
	if message == "" {
		return DocumentOperationUnknown
	}
	if blocksWorkflow(message) {
		if isTransformInChatRequest(message) {
			return DocumentOperationTransformInChat
		}
		if isDocumentReadRequest(message) {
			return DocumentOperationRead
		}

		return DocumentOperationAnalyze
	}
	if isExplicitExecutionRequest(message) {
		return DocumentOperationWorkflow
	}
	if isTransformInChatRequest(message) {
		return DocumentOperationTransformInChat
	}
	if isDocumentReadRequest(message) {
		return DocumentOperationRead
	}
	if isDocumentAnalyzeRequest(message) {
		return DocumentOperationAnalyze
	}
	if referencesCurrentFileTarget(message) || referencesWholeDocument(message) {
		return DocumentOperationAnalyze
	}

	return DocumentOperationUnknown
}

// blocksWorkflow 判断消息是否显式要求“先不要创建任务/执行”，避免误进 workflow。
func blocksWorkflow(message string) bool {
	return containsAny(message, []string{
		"不要创建任务",
		"先不要创建任务",
		"别创建任务",
		"不用创建任务",
		"不要执行",
		"先不要执行",
		"先别执行",
	})
}

// isTransformInChatRequest 判断消息是否在聊天里索要改写建议，而不是直接执行。
func isTransformInChatRequest(message string) bool {
	if isTransformSectionRequest(message) {
		return true
	}

	if !containsAny(message, []string{
		"写法",
		"改写",
		"改写建议",
		"示例",
		"版本",
	}) {
		return false
	}

	return referencesCurrentFileTarget(message) || referencesWholeDocument(message)
}

// isDocumentReadRequest 判断消息是否要求读取当前文件内容，而不是分析或执行。
func isDocumentReadRequest(message string) bool {
	if isExcerptSectionRequest(message) || isListSectionsRequest(message) || isAggregateAttributeRequest(message) {
		return true
	}

	if !containsAny(message, []string{
		"输出",
		"列出来",
		"摘录",
		"提取",
		"复述",
		"全文",
		"文件内容",
	}) {
		return false
	}

	return referencesCurrentFileTarget(message) || referencesWholeDocument(message)
}

// isDocumentAnalyzeRequest 判断消息是否在要求基于当前文件内容进行分析。
func isDocumentAnalyzeRequest(message string) bool {
	if isAnalyzeSectionRequest(message) {
		return true
	}

	if !containsAny(message, []string{
		"分析",
		"点评",
		"评价",
		"看看",
		"问题",
		"不足",
		"显得弱",
		"为什么弱",
	}) {
		return false
	}

	return referencesCurrentFileTarget(message) || referencesWholeDocument(message)
}
