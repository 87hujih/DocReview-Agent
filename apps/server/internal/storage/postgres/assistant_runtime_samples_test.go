package postgres

import (
	"context"
	"testing"
	"time"
)

// TestAssistantRuntimeSampleRepoUpsertAndGetByDecisionMessage 验证`assistantRuntimeSampleRepoUpsertAndGetByDecisionMessage`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeSampleRepoUpsertAndGetByDecisionMessage(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	sampleRepo := NewAssistantRuntimeSampleRepo(pool)
	ctx := testContext(t)

	session, messages, err := assistantRepo.CreateSessionWithMessages(ctx, "assistant runtime 样本会话", []AssistantMessageInput{
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"这是用于折叠样本的回复"}`),
	})
	if err != nil {
		t.Fatalf("create assistant session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup assistant session: %v", err)
		}
	})
	if len(messages) != 1 {
		t.Fatalf("expected exactly 1 seed message, got %d", len(messages))
	}

	err = sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:             session.ID,
		DecisionMessageID:     messages[0].ID,
		RequestKind:           stringPointer("workflow_command"),
		ResponseMode:          stringPointer("answer_then_task_card"),
		PlannerUsed:           boolPointer(true),
		VerifierUsed:          boolPointer(true),
		TaskSuggestionCreated: boolPointer(true),
		PromotedToWorkflow:    boolPointer(true),
		FinalOutcome:          stringPointer("task_suggestion_created"),
		Payload:               []byte(`{"source":"unit-test"}`),
	})
	if err != nil {
		t.Fatalf("upsert runtime sample: %v", err)
	}

	record, err := sampleRepo.GetByDecisionMessage(ctx, messages[0].ID)
	if err != nil {
		t.Fatalf("get runtime sample by decision message: %v", err)
	}
	if record == nil {
		t.Fatal("expected runtime sample record, got nil")
	}
	if record.SessionID != session.ID {
		t.Fatalf("expected session id %q, got %q", session.ID, record.SessionID)
	}
	if record.DecisionMessageID == nil || *record.DecisionMessageID != messages[0].ID {
		t.Fatalf("expected decision message id %q, got %#v", messages[0].ID, record.DecisionMessageID)
	}
	if record.RequestKind != "workflow_command" {
		t.Fatalf("expected request kind %q, got %q", "workflow_command", record.RequestKind)
	}
	if record.ResponseMode != "answer_then_task_card" {
		t.Fatalf("expected response mode %q, got %q", "answer_then_task_card", record.ResponseMode)
	}
	if !record.PlannerUsed {
		t.Fatal("expected planner_used to be true")
	}
	if !record.VerifierUsed {
		t.Fatal("expected verifier_used to be true")
	}
	if !record.TaskSuggestionCreated {
		t.Fatal("expected task_suggestion_created to be true")
	}
	if !record.PromotedToWorkflow {
		t.Fatal("expected promoted_to_workflow to be true")
	}
	if record.FinalOutcome != "task_suggestion_created" {
		t.Fatalf("expected final outcome %q, got %q", "task_suggestion_created", record.FinalOutcome)
	}
	if !jsonEqual(record.Payload, []byte(`{"source":"unit-test"}`)) {
		t.Fatalf("expected payload to persist, got %s", string(record.Payload))
	}
}

// TestAssistantRuntimeSampleRepoUpdatesOutcomeWithoutLosingExistingDecisionFields 验证`assistantRuntimeSampleRepoUpdatesOutcomeWithoutLosingExistingDecisionFields`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeSampleRepoUpdatesOutcomeWithoutLosingExistingDecisionFields(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	sampleRepo := NewAssistantRuntimeSampleRepo(pool)
	ctx := testContext(t)

	session, messages, err := assistantRepo.CreateSessionWithMessages(ctx, "assistant runtime 样本更新会话", []AssistantMessageInput{
		mustAssistantMessageInput(t, "assistant", "task_suggestion", `{"instruction":"请开始整理第三章"}`),
	})
	if err != nil {
		t.Fatalf("create assistant session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup assistant session: %v", err)
		}
	})
	if len(messages) != 1 {
		t.Fatalf("expected exactly 1 seed message, got %d", len(messages))
	}

	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:             session.ID,
		DecisionMessageID:     messages[0].ID,
		RequestKind:           stringPointer("workflow_command"),
		ResponseMode:          stringPointer("answer_then_task_card"),
		PlannerUsed:           boolPointer(true),
		TaskSuggestionCreated: boolPointer(true),
		Payload:               []byte(`{"decision":true}`),
	}); err != nil {
		t.Fatalf("seed runtime sample: %v", err)
	}

	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:               session.ID,
		DecisionMessageID:       messages[0].ID,
		TaskSuggestionConfirmed: boolPointer(true),
		FinalOutcome:            stringPointer("task_suggestion_confirmed"),
		Payload:                 []byte(`{"outcome":true}`),
	}); err != nil {
		t.Fatalf("update runtime sample outcome: %v", err)
	}

	record, err := sampleRepo.GetByDecisionMessage(ctx, messages[0].ID)
	if err != nil {
		t.Fatalf("get updated runtime sample: %v", err)
	}
	if record == nil {
		t.Fatal("expected updated runtime sample record, got nil")
	}
	if record.RequestKind != "workflow_command" {
		t.Fatalf("expected request kind to be preserved, got %q", record.RequestKind)
	}
	if record.ResponseMode != "answer_then_task_card" {
		t.Fatalf("expected response mode to be preserved, got %q", record.ResponseMode)
	}
	if !record.PlannerUsed {
		t.Fatal("expected planner_used to stay true")
	}
	if !record.TaskSuggestionCreated {
		t.Fatal("expected task_suggestion_created to stay true")
	}
	if !record.TaskSuggestionConfirmed {
		t.Fatal("expected task_suggestion_confirmed to become true")
	}
	if record.FinalOutcome != "task_suggestion_confirmed" {
		t.Fatalf("expected final outcome %q, got %q", "task_suggestion_confirmed", record.FinalOutcome)
	}
	if !jsonEqual(record.Payload, []byte(`{"decision":true,"outcome":true}`)) {
		t.Fatalf("expected payload update to merge json object, got %s", string(record.Payload))
	}
}

// TestAssistantRuntimeSampleRepoSummaryCountsSuggestionConfirmRate 验证`assistantRuntimeSampleRepoSummaryCountsSuggestionConfirmRate`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeSampleRepoSummaryCountsSuggestionConfirmRate(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	sampleRepo := NewAssistantRuntimeSampleRepo(pool)
	ctx := testContext(t)

	session, messages := seedAssistantRuntimeSampleSummarySession(t, assistantRepo, ctx, "assistant runtime summary confirm rate", 3)

	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:               session.ID,
		DecisionMessageID:       messages[0].ID,
		TaskSuggestionCreated:   boolPointer(true),
		TaskSuggestionConfirmed: boolPointer(true),
		FinalOutcome:            stringPointer("task_suggestion_confirmed"),
	}); err != nil {
		t.Fatalf("seed confirmed sample: %v", err)
	}
	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:             session.ID,
		DecisionMessageID:     messages[1].ID,
		TaskSuggestionCreated: boolPointer(true),
		TaskSuggestionIgnored: boolPointer(true),
		FinalOutcome:          stringPointer("task_suggestion_ignored"),
	}); err != nil {
		t.Fatalf("seed ignored sample: %v", err)
	}
	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:         session.ID,
		DecisionMessageID: messages[2].ID,
		RequestKind:       stringPointer("readback"),
		ResponseMode:      stringPointer("answer_with_grounding"),
	}); err != nil {
		t.Fatalf("seed plain sample: %v", err)
	}

	summary, err := sampleRepo.Summary(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("summary runtime samples: %v", err)
	}
	if summary.TotalSamples != 3 {
		t.Fatalf("expected total samples %d, got %d", 3, summary.TotalSamples)
	}
	if summary.TaskSuggestionCreated != 2 || summary.TaskSuggestionConfirmed != 1 || summary.TaskSuggestionIgnored != 1 {
		t.Fatalf("expected suggestion summary 2/1/1, got %#v", summary)
	}
}

// TestAssistantRuntimeSampleRepoSummaryCountsClarificationOutcomes 验证`assistantRuntimeSampleRepoSummaryCountsClarificationOutcomes`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeSampleRepoSummaryCountsClarificationOutcomes(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	sampleRepo := NewAssistantRuntimeSampleRepo(pool)
	ctx := testContext(t)

	session, messages := seedAssistantRuntimeSampleSummarySession(t, assistantRepo, ctx, "assistant runtime summary clarification", 2)

	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:            session.ID,
		DecisionMessageID:    messages[0].ID,
		ClarificationAsked:   boolPointer(true),
		ClarificationOutcome: stringPointer("resolved_to_chat"),
		FinalOutcome:         stringPointer("clarification_resolved_to_chat"),
	}); err != nil {
		t.Fatalf("seed clarification-to-chat sample: %v", err)
	}
	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:            session.ID,
		DecisionMessageID:    messages[1].ID,
		ClarificationAsked:   boolPointer(true),
		ClarificationOutcome: stringPointer("resolved_to_workflow"),
		FinalOutcome:         stringPointer("clarification_resolved_to_workflow"),
	}); err != nil {
		t.Fatalf("seed clarification-to-workflow sample: %v", err)
	}

	summary, err := sampleRepo.Summary(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("summary runtime samples: %v", err)
	}
	if summary.ClarificationAsked != 2 || summary.ClarificationResolvedChat != 1 || summary.ClarificationResolvedFlow != 1 {
		t.Fatalf("expected clarification summary 2/1/1, got %#v", summary)
	}
}

// TestAssistantRuntimeSampleRepoSummaryCountsUserCorrections 验证`assistantRuntimeSampleRepoSummaryCountsUserCorrections`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeSampleRepoSummaryCountsUserCorrections(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	sampleRepo := NewAssistantRuntimeSampleRepo(pool)
	ctx := testContext(t)

	session, messages := seedAssistantRuntimeSampleSummarySession(t, assistantRepo, ctx, "assistant runtime summary correction", 1)

	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:         session.ID,
		DecisionMessageID: messages[0].ID,
		UserCorrected:     boolPointer(true),
		FinalOutcome:      stringPointer("user_corrected"),
	}); err != nil {
		t.Fatalf("seed corrected sample: %v", err)
	}

	summary, err := sampleRepo.Summary(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("summary runtime samples: %v", err)
	}
	if summary.UserCorrected != 1 {
		t.Fatalf("expected user corrected count %d, got %#v", 1, summary)
	}
}

// TestAssistantRuntimeSampleRepoSummaryCountsWorkflowDowngrades 验证`assistantRuntimeSampleRepoSummaryCountsWorkflowDowngrades`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeSampleRepoSummaryCountsWorkflowDowngrades(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	sampleRepo := NewAssistantRuntimeSampleRepo(pool)
	ctx := testContext(t)

	session, messages := seedAssistantRuntimeSampleSummarySession(t, assistantRepo, ctx, "assistant runtime summary downgrade", 1)

	if err := sampleRepo.Upsert(ctx, AssistantRuntimeSampleUpsertParams{
		SessionID:         session.ID,
		DecisionMessageID: messages[0].ID,
		FinalOutcome:      stringPointer("workflow_downgraded"),
	}); err != nil {
		t.Fatalf("seed workflow downgraded sample: %v", err)
	}

	summary, err := sampleRepo.Summary(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("summary runtime samples: %v", err)
	}
	if summary.WorkflowDowngraded != 1 {
		t.Fatalf("expected workflow downgraded count %d, got %#v", 1, summary)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func seedAssistantRuntimeSampleSummarySession(
	t *testing.T,
	assistantRepo *AssistantRepo,
	ctx context.Context,
	title string,
	messageCount int,
) (*AssistantSession, []AssistantMessage) {
	t.Helper()

	inputs := make([]AssistantMessageInput, 0, messageCount)
	for index := 0; index < messageCount; index++ {
		inputs = append(inputs, mustAssistantMessageInput(t, "assistant", "text", `{"content":"summary seed"}`))
	}

	session, messages, err := assistantRepo.CreateSessionWithMessages(ctx, title, inputs)
	if err != nil {
		t.Fatalf("create summary session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup summary session: %v", err)
		}
	})

	if len(messages) != messageCount {
		t.Fatalf("expected %d seed messages, got %d", messageCount, len(messages))
	}

	return session, messages
}
