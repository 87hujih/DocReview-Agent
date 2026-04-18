package assistant

import (
	"testing"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
)

// TestRuntimeStateBuilderIncludesActiveResourceAndGrounding 验证 builder 会组装活跃资源、引用证据与 grounding 目标。
func TestRuntimeStateBuilderIncludesActiveResourceAndGrounding(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		Snapshot: &SessionContextSnapshot{
			SessionID: "session-1",
		},
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "产品经理简历",
			Source: "upload",
		},
		Citations: []citation.Citation{
			{
				CitationID:   "cite_1",
				ResourceID:   "resource-1",
				SectionID:    "section-3",
				SectionType:  "project",
				SectionTitle: "CampusHub",
				Snippet:      "负责活动发布、报名与签到全流程。",
			},
		},
		GroundedTarget: &ResolvedReference{
			SectionID:   "section-3",
			SectionType: "project",
			EntityName:  "CampusHub",
			Reason:      "ordinal_reference",
		},
	}

	state := builder.Build("把第三个项目先输出一遍", replyContext)

	if state.Message != "把第三个项目先输出一遍" {
		t.Fatalf("expected message to be preserved, got %q", state.Message)
	}
	if state.Snapshot == nil || state.Snapshot.SessionID != "session-1" {
		t.Fatalf("expected snapshot to be carried, got %#v", state.Snapshot)
	}
	if state.ActiveResource == nil || state.ActiveResource.ID != "resource-1" {
		t.Fatalf("expected active resource to be carried, got %#v", state.ActiveResource)
	}
	if len(state.Citations) != 1 || state.Citations[0].CitationID != "cite_1" {
		t.Fatalf("expected citations to be carried, got %#v", state.Citations)
	}
	if state.GroundedTarget == nil || state.GroundedTarget.SectionID != "section-3" {
		t.Fatalf("expected grounded target to be carried, got %#v", state.GroundedTarget)
	}
}

// TestRuntimeStateBuilderIncludesPendingTaskSuggestionAndLatestTask 验证 builder 会从快照里提取待确认任务与最近任务。
func TestRuntimeStateBuilderIncludesPendingTaskSuggestionAndLatestTask(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		Snapshot: &SessionContextSnapshot{
			SessionID: "session-2",
			PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
				MessageID:   "message-suggestion",
				Instruction: "把第三个项目改成产品经理版本",
			},
			LatestTask: &SnapshotLatestTask{
				ID:              "task-1",
				Status:          "running",
				SourceMessageID: "message-task",
			},
		},
	}

	state := builder.Build("直接开始改第三个项目", replyContext)

	if state.PendingTaskSuggestion == nil || state.PendingTaskSuggestion.MessageID != "message-suggestion" {
		t.Fatalf("expected pending task suggestion to be carried, got %#v", state.PendingTaskSuggestion)
	}
	if state.LatestTask == nil || state.LatestTask.ID != "task-1" || state.LatestTask.Status != "running" {
		t.Fatalf("expected latest task to be carried, got %#v", state.LatestTask)
	}
}

// TestRuntimeStateBuilderCarriesConfirmedConstraintsAndRollingSummary 验证 builder 会暴露滚动摘要与已确认约束。
func TestRuntimeStateBuilderCarriesConfirmedConstraintsAndRollingSummary(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		Snapshot: &SessionContextSnapshot{
			SessionID: "session-3",
			RollingSummary: stringPointer("用户已经确认要保留校园项目经历，并强调表达要更偏产品。"),
			ConfirmedConstraints: []ConfirmedConstraint{
				{Label: "目标岗位", Value: "产品经理"},
				{Label: "语气", Value: "克制专业"},
			},
		},
	}

	state := builder.Build("继续往产品经理方向改", replyContext)

	if state.RollingSummary == nil || *state.RollingSummary != "用户已经确认要保留校园项目经历，并强调表达要更偏产品。" {
		t.Fatalf("expected rolling summary to be carried, got %#v", state.RollingSummary)
	}
	if len(state.ConfirmedConstraints) != 2 {
		t.Fatalf("expected confirmed constraints to be carried, got %#v", state.ConfirmedConstraints)
	}
	if state.ConfirmedConstraints[0].Label != "目标岗位" || state.ConfirmedConstraints[1].Value != "克制专业" {
		t.Fatalf("unexpected confirmed constraints: %#v", state.ConfirmedConstraints)
	}
}

// TestRuntimeStateBuilderKeepsCurrentMessageSeparateFromRecentHistory 验证 builder 不会把当前消息混进 recent history。
func TestRuntimeStateBuilderKeepsCurrentMessageSeparateFromRecentHistory(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		History: []postgres.AssistantMessage{
			{
				ID:         "message-1",
				SessionID:  "session-4",
				Role:       RoleUser,
				Kind:       KindText,
				SequenceNo: 1,
				Payload:    mustJSON(t, TextPayload{Content: "先看看这份简历"}),
				CreatedAt:  time.Now(),
			},
			{
				ID:         "message-2",
				SessionID:  "session-4",
				Role:       RoleAssistant,
				Kind:       KindText,
				SequenceNo: 2,
				Payload:    mustJSON(t, TextPayload{Content: "可以，我先按项目经历来读。"}),
				CreatedAt:  time.Now(),
			},
		},
	}

	state := builder.Build("把第三个项目先输出一遍", replyContext)

	if state.Message != "把第三个项目先输出一遍" {
		t.Fatalf("expected current message to stay on runtime state, got %q", state.Message)
	}
	if len(state.History) != 2 {
		t.Fatalf("expected history length to stay 2, got %d", len(state.History))
	}

	lastPayload, err := unmarshalTextPayload(state.History[len(state.History)-1].Payload)
	if err != nil {
		t.Fatalf("unmarshal history payload: %v", err)
	}
	if lastPayload.Content != "可以，我先按项目经历来读。" {
		t.Fatalf("expected history to remain unchanged, got %q", lastPayload.Content)
	}
}
