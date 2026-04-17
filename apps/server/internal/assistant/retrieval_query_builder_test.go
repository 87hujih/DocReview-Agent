package assistant

import (
	"strings"
	"testing"
)

func TestRetrievalQueryBuilderKeepsExplicitQueryUntouched(t *testing.T) {
	builder := RetrievalQueryBuilder{}
	summary := "用户正在整理学生手册第二章考勤规则。"

	got := builder.Build(RetrievalQueryInput{
		CurrentMessage: "请总结学生手册第二章考勤和请假条款",
		RollingSummary: &summary,
		PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
			Instruction: "请整理第二章为正式修订任务。",
		},
	})

	if got != "请总结学生手册第二章考勤和请假条款" {
		t.Fatalf("expected explicit query to stay unchanged, got %q", got)
	}
}

func TestRetrievalQueryBuilderExpandsShortAnaphoraQuery(t *testing.T) {
	builder := RetrievalQueryBuilder{}
	summary := "用户正在优化学生手册第二章考勤规则，希望保留按天登记和节假日例外。"

	got := builder.Build(RetrievalQueryInput{
		CurrentMessage: "继续改第二个",
		RollingSummary: &summary,
	})

	if got == "继续改第二个" {
		t.Fatal("expected short anaphora query to be expanded")
	}
	if !strings.Contains(got, "当前问题：继续改第二个") {
		t.Fatalf("expected expanded query to include current message, got %q", got)
	}
	if !strings.Contains(got, "会话摘要："+summary) {
		t.Fatalf("expected expanded query to include rolling summary, got %q", got)
	}
}

func TestRetrievalQueryBuilderIncludesPendingTaskSuggestionWhenExpanding(t *testing.T) {
	builder := RetrievalQueryBuilder{}

	got := builder.Build(RetrievalQueryInput{
		CurrentMessage: "继续",
		PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
			Instruction: "请整理第二章为正式修订任务。",
		},
	})

	if !strings.Contains(got, "待确认任务：请整理第二章为正式修订任务。") {
		t.Fatalf("expected expanded query to include pending task suggestion, got %q", got)
	}
}

func TestRetrievalQueryBuilderKeepsResolvedTargetUntouched(t *testing.T) {
	builder := RetrievalQueryBuilder{}
	summary := "用户刚刚列出了两个项目。"

	got := builder.Build(RetrievalQueryInput{
		CurrentMessage:    "针对第一个项目，给出修改示例",
		RollingSummary:    &summary,
		ResolvedReference: &ResolvedReference{SectionID: "section-campushub", EntityName: "CampusHub"},
	})

	if got != "针对第一个项目，给出修改示例" {
		t.Fatalf("expected resolved target query to stay unchanged, got %q", got)
	}
}
