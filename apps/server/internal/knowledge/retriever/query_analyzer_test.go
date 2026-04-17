package retriever

import "testing"

func TestAnalyzeQueryDetectsProjectListingIntent(t *testing.T) {
	intent := AnalyzeQuery("查看一下我简历中的项目内容，都有哪些")
	if intent.Kind != IntentListProjects {
		t.Fatalf("expected list_projects, got %s", intent.Kind)
	}
}

func TestAnalyzeQueryDetectsProjectDetailIntent(t *testing.T) {
	intent := AnalyzeQuery("CampusHub 做了什么")
	if intent.Kind != IntentProjectDetail {
		t.Fatalf("expected project_detail, got %s", intent.Kind)
	}
}

func TestAnalyzeQueryDetectsTechStackIntent(t *testing.T) {
	intent := AnalyzeQuery("用了哪些技术栈")
	if intent.Kind != IntentTechStack {
		t.Fatalf("expected tech_stack, got %s", intent.Kind)
	}
}

func TestAnalyzeQueryFallsBackToGeneralSearch(t *testing.T) {
	intent := AnalyzeQuery("帮我总结这份材料")
	if intent.Kind != IntentGeneralSearch {
		t.Fatalf("expected general_search, got %s", intent.Kind)
	}
}
