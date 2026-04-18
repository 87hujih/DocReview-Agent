package assistant

import "testing"

func TestClassifyReadIntentTreatsListRequestAsSectionList(t *testing.T) {
	intent := ClassifyReadIntent("有哪些项目")
	if intent.Kind != ReadIntentListSections {
		t.Fatalf("expected read intent %q, got %#v", ReadIntentListSections, intent)
	}
	if intent.RequiresLLM {
		t.Fatalf("expected list intent to skip llm, got %#v", intent)
	}
	if intent.ShouldTriggerTaskFlow {
		t.Fatalf("expected list intent to stay in chat lane, got %#v", intent)
	}
}

func TestClassifyReadIntentTreatsExcerptAsDeterministicRead(t *testing.T) {
	intent := ClassifyReadIntent("第三个项目先输出一遍")
	if intent.Kind != ReadIntentExcerptSection {
		t.Fatalf("expected read intent %q, got %#v", ReadIntentExcerptSection, intent)
	}
	if intent.RequiresLLM {
		t.Fatalf("expected excerpt intent to skip llm, got %#v", intent)
	}
	if intent.ShouldTriggerTaskFlow {
		t.Fatalf("expected excerpt intent to stay in chat lane, got %#v", intent)
	}
}

func TestClassifyReadIntentTreatsOptimizationAsTransform(t *testing.T) {
	intent := ClassifyReadIntent("第三个项目怎么优化")
	if intent.Kind != ReadIntentTransformSection {
		t.Fatalf("expected read intent %q, got %#v", ReadIntentTransformSection, intent)
	}
	if !intent.RequiresLLM {
		t.Fatalf("expected transform intent to require llm, got %#v", intent)
	}
	if intent.ShouldTriggerTaskFlow {
		t.Fatalf("expected transform intent to stay in chat lane, got %#v", intent)
	}
}

func TestClassifyReadIntentTreatsExplicitRewriteAsExecutionRequest(t *testing.T) {
	intent := ClassifyReadIntent("请直接把这份简历改成产品经理版本")
	if intent.Kind != ReadIntentExecutionRequest {
		t.Fatalf("expected read intent %q, got %#v", ReadIntentExecutionRequest, intent)
	}
	if intent.RequiresLLM {
		t.Fatalf("expected execution intent to skip llm reply classification, got %#v", intent)
	}
	if !intent.ShouldTriggerTaskFlow {
		t.Fatalf("expected execution intent to trigger task flow, got %#v", intent)
	}
}
