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
			SessionID:      "session-3",
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

// TestRuntimeStateBuilderCarriesCurrentDocument 验证 builder 会把 current document 一并带入运行时状态。
func TestRuntimeStateBuilderCarriesCurrentDocument(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		CurrentDocument: &CurrentDocument{
			ResourceID: "resource-1",
			VersionID:  "version-3",
			Title:      "产品经理简历",
			SourceType: "upload",
			FullText:   "这是当前文件全文。",
			Sections: []postgres.ResourceSection{
				{ID: "section-1", ResourceID: "resource-1", VersionID: "version-3", SectionType: "project", SectionOrder: 1, Title: "CampusHub"},
			},
			Ready: true,
		},
	}

	state := builder.Build("结合全文分析第三个项目", replyContext)

	if state.CurrentDocument == nil {
		t.Fatal("expected current document to be carried")
	}
	if !state.CurrentDocument.Ready {
		t.Fatal("expected current document ready flag to be preserved")
	}
	if state.CurrentDocument.FullText != "这是当前文件全文。" {
		t.Fatalf("expected current document full text to be carried, got %q", state.CurrentDocument.FullText)
	}
	if len(state.CurrentDocument.Sections) != 1 || state.CurrentDocument.Sections[0].ID != "section-1" {
		t.Fatalf("expected current document sections to be carried, got %#v", state.CurrentDocument.Sections)
	}
}

// TestRuntimeStateBuilderCarriesAdvisorState 验证 builder 会把 advisor state 一并带入运行时状态。
func TestRuntimeStateBuilderCarriesAdvisorState(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		Snapshot: &SessionContextSnapshot{
			SessionID: "session-5",
		},
	}
	mustSetPointerStructField(t, replyContext.Snapshot, "PendingClarification", map[string]any{
		"Kind":           "execution_confirmation",
		"Question":       "要不要按这个方向直接改？",
		"AskedMessageID": "message-clarify-1",
		"Options":        []string{"先分析", "直接修改"},
	})
	mustSetPointerStructField(t, replyContext.Snapshot, "AdvisoryContext", map[string]any{
		"Diagnosis":          "第三个项目缺少结果。",
		"Recommendations":    []string{"补结果", "补指标"},
		"PreferredDirection": "按结果导向重写",
		"SourceMessageID":    "message-advice-1",
	})
	mustSetPointerStructField(t, replyContext.Snapshot, "PendingProposal", map[string]any{
		"ProposalID":                    "proposal-1",
		"Instruction":                   "把第三个项目改成问题-动作-结果结构",
		"PlanGoal":                      "产出可执行的简历改写任务",
		"ProposedMessageID":             "message-proposal-1",
		"RequiresExplicitAuthorization": true,
	})
	mustSetPointerStructField(t, replyContext.Snapshot, "AuthorizationState", map[string]any{
		"Status":               "pending",
		"GrantedForProposalID": "proposal-1",
		"GrantedByMessageID":   "message-authorize-1",
	})

	state := builder.Build("按你的建议改", replyContext)

	if got := mustReadStringField(t, mustReadPointerStructField(t, &state, "PendingClarification"), "Question"); got != "要不要按这个方向直接改？" {
		t.Fatalf("expected pending clarification on runtime state, got %q", got)
	}
	if got := mustReadStringField(t, mustReadPointerStructField(t, &state, "AdvisoryContext"), "Diagnosis"); got != "第三个项目缺少结果。" {
		t.Fatalf("expected advisory context on runtime state, got %q", got)
	}
	if got := mustReadStringField(t, mustReadPointerStructField(t, &state, "PendingProposal"), "ProposalID"); got != "proposal-1" {
		t.Fatalf("expected pending proposal on runtime state, got %q", got)
	}
	if got := mustReadStringField(t, mustReadPointerStructField(t, &state, "AuthorizationState"), "Status"); got != "pending" {
		t.Fatalf("expected authorization state on runtime state, got %q", got)
	}
}

// TestRuntimeStateBuilderCarriesNodeAwareSnapshotState 验证 builder 会把 node-aware 快照字段显式带入运行时状态。
func TestRuntimeStateBuilderCarriesNodeAwareSnapshotState(t *testing.T) {
	builder := RuntimeStateBuilder{}
	replyContext := &ReplyContext{
		Snapshot: &SessionContextSnapshot{
			SessionID: "session-node-1",
			ActiveNode: &SnapshotActiveNode{
				ID:   "project-3",
				Kind: string(OutlineNodeProjectItem),
			},
			NodeReferenceFrame: []NodeReference{
				{Ordinal: 3, NodeID: "project-3", NodeKind: string(OutlineNodeProjectItem), EntityName: "慢跑计划"},
			},
		},
	}

	state := builder.Build("这个项目的问题是什么", replyContext)

	if state.ActiveNode == nil || state.ActiveNode.ID != "project-3" || state.ActiveNode.Kind != string(OutlineNodeProjectItem) {
		t.Fatalf("expected active node on runtime state, got %#v", state.ActiveNode)
	}
	if len(state.NodeReferenceFrame) != 1 || state.NodeReferenceFrame[0].NodeID != "project-3" {
		t.Fatalf("expected node reference frame on runtime state, got %#v", state.NodeReferenceFrame)
	}
}
