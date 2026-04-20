package assistant

import "testing"

// TestClassifyDocumentOperationTreatsExcerptAsRead 验证读取型问法会进入 read 分支。
func TestClassifyDocumentOperationTreatsExcerptAsRead(t *testing.T) {
	operation := ClassifyDocumentOperation(DocumentOperationInput{
		Message:         "把第三个项目先输出一遍",
		CurrentDocument: &CurrentDocument{Ready: true},
	})

	if operation != DocumentOperationRead {
		t.Fatalf("expected document operation %q, got %q", DocumentOperationRead, operation)
	}
}

// TestClassifyDocumentOperationTreatsNaturalQuestionAsAnalyze 验证自然分析问法不会因为模板不精确而掉回 discussion。
func TestClassifyDocumentOperationTreatsNaturalQuestionAsAnalyze(t *testing.T) {
	operation := ClassifyDocumentOperation(DocumentOperationInput{
		Message:         "结合我刚上传的简历，详细分析第三个项目的问题",
		CurrentDocument: &CurrentDocument{Ready: true},
	})

	if operation != DocumentOperationAnalyze {
		t.Fatalf("expected document operation %q, got %q", DocumentOperationAnalyze, operation)
	}
}

// TestClassifyDocumentOperationTreatsRewriteExampleAsTransformInChat 验证聊天内改写问法会进入 transform-in-chat。
func TestClassifyDocumentOperationTreatsRewriteExampleAsTransformInChat(t *testing.T) {
	operation := ClassifyDocumentOperation(DocumentOperationInput{
		Message:         "先给我一版第三个项目的更强写法，但先不要创建任务",
		CurrentDocument: &CurrentDocument{Ready: true},
	})

	if operation != DocumentOperationTransformInChat {
		t.Fatalf("expected document operation %q, got %q", DocumentOperationTransformInChat, operation)
	}
}

// TestClassifyDocumentOperationTreatsExplicitModifyAsWorkflow 验证明确执行问法会进入 workflow。
func TestClassifyDocumentOperationTreatsExplicitModifyAsWorkflow(t *testing.T) {
	operation := ClassifyDocumentOperation(DocumentOperationInput{
		Message:         "直接帮我改第三个项目，开始执行",
		CurrentDocument: &CurrentDocument{Ready: true},
	})

	if operation != DocumentOperationWorkflow {
		t.Fatalf("expected document operation %q, got %q", DocumentOperationWorkflow, operation)
	}
}
