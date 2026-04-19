package assistant

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestRuntimeLearningProjectorProjectsTaskSuggestionCreated 验证`runtimeLearningProjectorProjectsTaskSuggestionCreated`在特定边界条件下的行为，防止同类回归。
func TestRuntimeLearningProjectorProjectsTaskSuggestionCreated(t *testing.T) {
	repo := &fakeRuntimeSampleRepo{}
	projector := NewRuntimeLearningProjector(repo)
	messageID := "msg-created"

	err := projector.Project(context.Background(), &postgres.AssistantRuntimeEvent{
		SessionID: "session-1",
		MessageID: &messageID,
		Source:    "task_suggestion",
		EventType: RuntimeEventTypeTaskSuggestionCreated,
		Payload:   []byte(`{"instruction":"请开始整理第三章"}`),
	})
	if err != nil {
		t.Fatalf("project task suggestion created: %v", err)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 sample upsert call, got %d", len(repo.calls))
	}
	if repo.calls[0].DecisionMessageID != messageID {
		t.Fatalf("expected decision message id %q, got %q", messageID, repo.calls[0].DecisionMessageID)
	}
	if repo.calls[0].TaskSuggestionCreated == nil || !*repo.calls[0].TaskSuggestionCreated {
		t.Fatalf("expected task_suggestion_created to be true, got %#v", repo.calls[0].TaskSuggestionCreated)
	}
	if repo.calls[0].PromotedToWorkflow == nil || !*repo.calls[0].PromotedToWorkflow {
		t.Fatalf("expected promoted_to_workflow to be true, got %#v", repo.calls[0].PromotedToWorkflow)
	}
	if repo.calls[0].FinalOutcome == nil || *repo.calls[0].FinalOutcome != RuntimeFinalOutcomeTaskSuggestionCreated {
		t.Fatalf("expected final outcome %q, got %#v", RuntimeFinalOutcomeTaskSuggestionCreated, repo.calls[0].FinalOutcome)
	}
}

// TestRuntimeLearningProjectorProjectsTaskSuggestionConfirmed 验证`runtimeLearningProjectorProjectsTaskSuggestionConfirmed`在特定边界条件下的行为，防止同类回归。
func TestRuntimeLearningProjectorProjectsTaskSuggestionConfirmed(t *testing.T) {
	repo := &fakeRuntimeSampleRepo{}
	projector := NewRuntimeLearningProjector(repo)
	messageID := "msg-confirmed"

	err := projector.Project(context.Background(), &postgres.AssistantRuntimeEvent{
		SessionID: "session-1",
		MessageID: &messageID,
		Source:    "task_suggestion",
		EventType: RuntimeEventTypeTaskSuggestionConfirmed,
		Payload:   []byte(`{"task_id":"task-1"}`),
	})
	if err != nil {
		t.Fatalf("project task suggestion confirmed: %v", err)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 sample upsert call, got %d", len(repo.calls))
	}
	if repo.calls[0].TaskSuggestionConfirmed == nil || !*repo.calls[0].TaskSuggestionConfirmed {
		t.Fatalf("expected task_suggestion_confirmed to be true, got %#v", repo.calls[0].TaskSuggestionConfirmed)
	}
	if repo.calls[0].FinalOutcome == nil || *repo.calls[0].FinalOutcome != RuntimeFinalOutcomeTaskSuggestionConfirmed {
		t.Fatalf("expected final outcome %q, got %#v", RuntimeFinalOutcomeTaskSuggestionConfirmed, repo.calls[0].FinalOutcome)
	}
}

// TestRuntimeLearningProjectorProjectsClarificationAsked 验证`runtimeLearningProjectorProjectsClarificationAsked`在特定边界条件下的行为，防止同类回归。
func TestRuntimeLearningProjectorProjectsClarificationAsked(t *testing.T) {
	repo := &fakeRuntimeSampleRepo{}
	projector := NewRuntimeLearningProjector(repo)
	messageID := "msg-clarify"

	err := projector.Project(context.Background(), &postgres.AssistantRuntimeEvent{
		SessionID: "session-1",
		MessageID: &messageID,
		Source:    "clarification",
		EventType: RuntimeEventTypeClarificationPrompted,
		Payload:   []byte(`{"question":"你是想先输出原文，还是直接创建任务？"}`),
	})
	if err != nil {
		t.Fatalf("project clarification prompted: %v", err)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 sample upsert call, got %d", len(repo.calls))
	}
	if repo.calls[0].ClarificationAsked == nil || !*repo.calls[0].ClarificationAsked {
		t.Fatalf("expected clarification_asked to be true, got %#v", repo.calls[0].ClarificationAsked)
	}
}

// TestRuntimeLearningProjectorProjectsExplicitCorrection 验证`runtimeLearningProjectorProjectsExplicitCorrection`在特定边界条件下的行为，防止同类回归。
func TestRuntimeLearningProjectorProjectsExplicitCorrection(t *testing.T) {
	repo := &fakeRuntimeSampleRepo{}
	projector := NewRuntimeLearningProjector(repo)
	messageID := "msg-correction"

	err := projector.Project(context.Background(), &postgres.AssistantRuntimeEvent{
		SessionID: "session-1",
		MessageID: &messageID,
		Source:    "user",
		EventType: RuntimeEventTypeUserCorrected,
		Payload:   []byte(`{"reason":"not_this_intent"}`),
	})
	if err != nil {
		t.Fatalf("project explicit correction: %v", err)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 sample upsert call, got %d", len(repo.calls))
	}
	if repo.calls[0].UserCorrected == nil || !*repo.calls[0].UserCorrected {
		t.Fatalf("expected user_corrected to be true, got %#v", repo.calls[0].UserCorrected)
	}
	if repo.calls[0].FinalOutcome == nil || *repo.calls[0].FinalOutcome != RuntimeFinalOutcomeUserCorrected {
		t.Fatalf("expected final outcome %q, got %#v", RuntimeFinalOutcomeUserCorrected, repo.calls[0].FinalOutcome)
	}
}

type fakeRuntimeSampleRepo struct {
	calls []postgres.AssistantRuntimeSampleUpsertParams
}

// Upsert 实现 runtime 样本仓储测试替身，记录 projector 发出的折叠请求。
func (r *fakeRuntimeSampleRepo) Upsert(_ context.Context, params postgres.AssistantRuntimeSampleUpsertParams) error {
	copied := params
	copied.RequestKind = cloneOptionalString(params.RequestKind)
	copied.ResponseMode = cloneOptionalString(params.ResponseMode)
	copied.ClarificationOutcome = cloneOptionalString(params.ClarificationOutcome)
	copied.FinalOutcome = cloneOptionalString(params.FinalOutcome)
	copied.PlannerUsed = cloneOptionalBool(params.PlannerUsed)
	copied.VerifierUsed = cloneOptionalBool(params.VerifierUsed)
	copied.ClarificationAsked = cloneOptionalBool(params.ClarificationAsked)
	copied.TaskSuggestionCreated = cloneOptionalBool(params.TaskSuggestionCreated)
	copied.TaskSuggestionConfirmed = cloneOptionalBool(params.TaskSuggestionConfirmed)
	copied.TaskSuggestionIgnored = cloneOptionalBool(params.TaskSuggestionIgnored)
	copied.UserCorrected = cloneOptionalBool(params.UserCorrected)
	copied.PromotedToWorkflow = cloneOptionalBool(params.PromotedToWorkflow)
	copied.Payload = append([]byte(nil), params.Payload...)
	r.calls = append(r.calls, copied)
	return nil
}

func cloneOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}
