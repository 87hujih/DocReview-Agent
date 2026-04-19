package assistant

import "testing"

// TestClassifyReadIntentTreatsListRequestAsReadOnly 验证`classifyReadIntentTreatsListRequestAsReadOnly`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsListRequestAsReadOnly(t *testing.T) {
	intent := ClassifyReadIntent("这份简历里有哪些项目")
	if intent.Kind != ReadIntentListSections {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentListSections, intent.Kind)
	}
	if intent.NeedsLLM {
		t.Fatal("list request should not require llm")
	}
	if intent.ShouldEnterTaskFlow {
		t.Fatal("list request must stay in chat lane")
	}
}

// TestClassifyReadIntentTreatsExcerptRequestAsDeterministicRead 验证`classifyReadIntentTreatsExcerptRequestAsDeterministicRead`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsExcerptRequestAsDeterministicRead(t *testing.T) {
	intent := ClassifyReadIntent("把第三个项目先输出一遍")
	if intent.Kind != ReadIntentExcerptSection {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentExcerptSection, intent.Kind)
	}
	if intent.NeedsLLM {
		t.Fatal("excerpt request should not require llm")
	}
	if intent.ShouldEnterTaskFlow {
		t.Fatal("excerpt request must stay in chat lane")
	}
	if !intent.RequiresSectionTarget {
		t.Fatal("excerpt request should require section target")
	}
}

// TestClassifyReadIntentTreatsAnalysisRequestAsSectionAnalysis 验证`classifyReadIntentTreatsAnalysisRequestAsSectionAnalysis`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsAnalysisRequestAsSectionAnalysis(t *testing.T) {
	intent := ClassifyReadIntent("第三个项目的问题是什么")
	if intent.Kind != ReadIntentAnalyzeSection {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentAnalyzeSection, intent.Kind)
	}
	if !intent.NeedsLLM {
		t.Fatal("analysis request should require llm")
	}
	if intent.ShouldEnterTaskFlow {
		t.Fatal("analysis request must stay in chat lane")
	}
	if !intent.RequiresSectionTarget {
		t.Fatal("analysis request should require section target")
	}
}

// TestClassifyReadIntentTreatsTransformRequestAsChatOnlyAnalysis 验证`classifyReadIntentTreatsTransformRequestAsChatOnlyAnalysis`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsTransformRequestAsChatOnlyAnalysis(t *testing.T) {
	intent := ClassifyReadIntent("把第三个项目改强一点")
	if intent.Kind != ReadIntentTransformSection {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentTransformSection, intent.Kind)
	}
	if !intent.NeedsLLM {
		t.Fatal("transform request should require llm")
	}
	if intent.ShouldEnterTaskFlow {
		t.Fatal("transform request should not enter task flow by default")
	}
}

// TestClassifyReadIntentTreatsExplicitRewriteAsExecutionRequest 验证`classifyReadIntentTreatsExplicitRewriteAsExecutionRequest`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsExplicitRewriteAsExecutionRequest(t *testing.T) {
	intent := ClassifyReadIntent("请直接把这份简历改成产品经理版本")
	if intent.Kind != ReadIntentExecutionRequest {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentExecutionRequest, intent.Kind)
	}
	if !intent.NeedsLLM {
		t.Fatal("execution request should require llm planning/reply")
	}
	if !intent.ShouldEnterTaskFlow {
		t.Fatal("execution request should enter task flow")
	}
}

// TestClassifyReadIntentTreatsSectionRevisionAsExecutionRequest 验证`classifyReadIntentTreatsSectionRevisionAsExecutionRequest`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsSectionRevisionAsExecutionRequest(t *testing.T) {
	intent := ClassifyReadIntent("请帮我检查并修订第二章")
	if intent.Kind != ReadIntentExecutionRequest {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentExecutionRequest, intent.Kind)
	}
	if !intent.ShouldEnterTaskFlow {
		t.Fatal("section revision request should enter task flow")
	}
}

// TestClassifyReadIntentTreatsTaskCardRequestAsExecutionRequest 验证`classifyReadIntentTreatsTaskCardRequestAsExecutionRequest`在特定边界条件下的行为，防止同类回归。
func TestClassifyReadIntentTreatsTaskCardRequestAsExecutionRequest(t *testing.T) {
	intent := ClassifyReadIntent("请把第二章整理成任务")
	if intent.Kind != ReadIntentExecutionRequest {
		t.Fatalf("expected read intent kind %q, got %q", ReadIntentExecutionRequest, intent.Kind)
	}
	if !intent.ShouldEnterTaskFlow {
		t.Fatal("task card request should enter task flow")
	}
}
