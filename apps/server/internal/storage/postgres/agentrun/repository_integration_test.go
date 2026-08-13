package agentrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentruntime "agent_project/apps/server/internal/agent/runtime"
	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/storage/postgres/agentrun"
	"agent_project/apps/server/internal/storage/postgres/outbox"
	"agent_project/apps/server/internal/testsupport/postgrestest"
)

// TestRepositoryIdempotencyClaimLeaseAndManifestRoundTrip 验证对应场景下的正常路径与失败路径。
func TestRepositoryIdempotencyClaimLeaseAndManifestRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "agentrun_repository", postgres.NewPool, postgres.RunMigrations)
	repo := agentrun.NewRepository(pool)
	requestID := "request-durable-runtime-roundtrip"
	deadline := time.Now().UTC().Add(time.Hour)

	run, created, err := repo.CreateOrGetRun(ctx, agentrun.CreateRunParams{
		RequestID:    &requestID,
		Objective:    "verify durable runtime persistence",
		MaxSteps:     8,
		MaxToolCalls: 4,
		DeadlineAt:   &deadline,
	})
	if err != nil || !created {
		t.Fatalf("create run: created=%v err=%v", created, err)
	}
	duplicate, created, err := repo.CreateOrGetRun(ctx, agentrun.CreateRunParams{
		RequestID:    &requestID,
		Objective:    "verify durable runtime persistence",
		MaxSteps:     8,
		MaxToolCalls: 4,
		DeadlineAt:   &deadline,
	})
	if err != nil || created || duplicate.ID != run.ID {
		t.Fatalf("idempotent run: created=%v duplicate=%#v err=%v", created, duplicate, err)
	}

	step, created, err := repo.CreateOrGetStep(ctx, agentrun.CreateStepParams{
		RunID:       run.ID,
		StepKey:     "understand_goal:1",
		StepType:    "UnderstandGoal",
		InputJSON:   json.RawMessage(`{"objective":"verify"}`),
		MaxAttempts: 3,
	})
	if err != nil || !created {
		t.Fatalf("create step: created=%v err=%v", created, err)
	}

	now := time.Now().UTC()
	claimed, err := repo.ClaimNextStep(ctx, now, "worker-test", time.Minute)
	if err != nil || claimed == nil || claimed.ID != step.ID || claimed.LeaseGeneration != 1 {
		t.Fatalf("claim step: claimed=%#v err=%v", claimed, err)
	}
	if err := repo.HeartbeatStep(ctx, agentrun.HeartbeatParams{
		StepID:          claimed.ID,
		WorkerID:        "worker-test",
		LeaseGeneration: claimed.LeaseGeneration,
		Now:             now.Add(10 * time.Second),
		LeaseDuration:   time.Minute,
	}); err != nil {
		t.Fatalf("heartbeat step: %v", err)
	}

	manifest, err := repo.CreateContextManifest(ctx, agentrun.CreateContextManifestParams{
		RunID:                run.ID,
		StepID:               step.ID,
		TokenBudget:          1000,
		ReservedOutputTokens: 200,
		Tokenizer:            "test-tokenizer-v1",
		ItemsJSON:            json.RawMessage(`[{"item_type":"task","token_count":10}]`),
		TotalTokens:          10,
		ContentHash:          "sha256:test-manifest",
	})
	if err != nil || manifest.ID == "" {
		t.Fatalf("create context manifest: %#v err=%v", manifest, err)
	}
	persistedManifest, err := repo.GetContextManifest(ctx, manifest.ID)
	if err != nil || persistedManifest == nil || string(persistedManifest.ItemsJSON) != string(manifest.ItemsJSON) ||
		persistedManifest.ContentHash != manifest.ContentHash {
		t.Fatalf("reproduce exact context manifest: %#v err=%v", persistedManifest, err)
	}

	call, created, err := repo.CreateOrGetToolCall(ctx, agentrun.CreateToolCallParams{
		RunID:          run.ID,
		StepID:         step.ID,
		ToolName:       "document.read_nodes",
		ToolVersion:    "1.0.0",
		InputJSON:      json.RawMessage(`{"node_ids":["node-1"]}`),
		IdempotencyKey: stringPointer("tool-call-1"),
	})
	if err != nil || !created || call.ID == "" {
		t.Fatalf("create tool call: %#v created=%v err=%v", call, created, err)
	}

	if err := repo.CompleteStep(ctx, agentrun.CompleteStepParams{
		StepID:          claimed.ID,
		WorkerID:        "worker-test",
		LeaseGeneration: claimed.LeaseGeneration,
		OutputJSON:      json.RawMessage(`{"understood":true}`),
		CompletedAt:     now.Add(20 * time.Second),
	}); err != nil {
		t.Fatalf("complete step: %v", err)
	}
	comparisonParams, err := agentrun.CompareShadowOutputs(
		json.RawMessage(`{"status":"complete","version_id":"version-2"}`),
		json.RawMessage(`{"version_id":"version-2","status":"complete"}`),
	)
	if err != nil {
		t.Fatalf("compare shadow outputs: %v", err)
	}
	comparisonParams.RunID = run.ID
	comparison, err := repo.RecordShadowComparison(ctx, comparisonParams)
	if err != nil || comparison.Status != agentrun.ShadowMatched {
		t.Fatalf("record shadow comparison: comparison=%#v err=%v", comparison, err)
	}
	if replay, err := repo.RecordShadowComparison(ctx, comparisonParams); err != nil || replay.ID != comparison.ID {
		t.Fatalf("replay shadow comparison: comparison=%#v err=%v", replay, err)
	}
	conflictComparison := comparisonParams
	conflictComparison.Status = agentrun.ShadowDiverged
	differentHash := "sha256:different"
	conflictComparison.TypedOutputHash = &differentHash
	if _, err := repo.RecordShadowComparison(ctx, conflictComparison); !errors.Is(err, agentrun.ErrIdempotencyConflict) {
		t.Fatalf("changed shadow comparison must conflict, got %v", err)
	}
}

// TestEngineStoreWaitingResumeAndOutcomeOutboxAreDurable 验证对应场景下的正常路径与失败路径。
func TestEngineStoreWaitingResumeAndOutcomeOutboxAreDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "agentrun_engine_store", postgres.NewPool, postgres.RunMigrations)
	repo := agentrun.NewRepository(pool)
	store := agentrun.NewEngineStore(repo)
	now := time.Now().UTC()
	requestID := "request-engine-store-wait-resume"

	run, _, err := repo.CreateOrGetRun(ctx, agentrun.CreateRunParams{
		RequestID: &requestID, Objective: "wait for approval then finish", MaxSteps: 4, MaxToolCalls: 2,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, _, err := repo.CreateOrGetStep(ctx, agentrun.CreateStepParams{
		RunID: run.ID, StepKey: "request_approval:1", StepType: "RequestApproval",
		InputJSON: json.RawMessage(`{"risk":"high"}`), MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("create step: %v", err)
	}

	work, err := store.Claim(ctx, agentruntime.ClaimRequest{Now: now, WorkerID: "worker-a", LeaseDuration: time.Minute})
	if err != nil || work == nil {
		t.Fatalf("claim waiting step: work=%#v err=%v", work, err)
	}
	waitingOutcome := agentruntime.OutcomeCommit{
		Work: *work, StepStatus: agentruntime.StepStatusWaitingApproval,
		RunStatus: agentruntime.RunStatusWaitingApproval, OutputJSON: json.RawMessage(`{"approval_id":"approval-1"}`),
		Observations: []agentruntime.ObservationSpec{{
			ObservationKey: work.StepID + ":approval", Kind: "RequestApproval", Action: "request_approval",
			PayloadJSON: json.RawMessage(`{"approval_id":"approval-1"}`), ContentHash: "sha256:approval-1", Novel: true,
		}},
		CommittedAt: now.Add(time.Second),
	}
	if err := store.CommitOutcome(ctx, waitingOutcome); err != nil {
		t.Fatalf("commit waiting outcome: %v", err)
	}
	if err := store.CommitOutcome(ctx, waitingOutcome); err != nil {
		t.Fatalf("repeat committed outcome idempotently: %v", err)
	}
	observations, err := repo.ListObservations(ctx, run.ID)
	if err != nil || len(observations) != 1 || observations[0].ObservationKey != work.StepID+":approval" {
		t.Fatalf("durable observation round trip: observations=%#v err=%v", observations, err)
	}
	var observationPayload map[string]any
	if err := json.Unmarshal(observations[0].PayloadJSON, &observationPayload); err != nil || observationPayload["approval_id"] != "approval-1" {
		t.Fatalf("durable observation payload: payload=%s err=%v", observations[0].PayloadJSON, err)
	}
	changedOutcome := waitingOutcome
	changedOutcome.OutputJSON = json.RawMessage(`{"approval_id":"different"}`)
	if err := store.CommitOutcome(ctx, changedOutcome); !errors.Is(err, agentrun.ErrLeaseLost) {
		t.Fatalf("changed replay must not share committed idempotency fact: %v", err)
	}
	persisted, err := repo.GetRun(ctx, run.ID)
	if err != nil || persisted.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("expected durable waiting run, run=%#v err=%v", persisted, err)
	}

	if err := repo.ResumeWaitingStep(ctx, agentrun.ResumeWaitingParams{
		RunID: run.ID, StepID: work.StepID,
		ExpectedRunStatus:  agentruntime.RunStatusWaitingApproval,
		ExpectedStepStatus: agentruntime.StepStatusWaitingApproval,
		At:                 now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("resume waiting step: %v", err)
	}
	resumed, err := store.Claim(ctx, agentruntime.ClaimRequest{Now: now.Add(3 * time.Second), WorkerID: "worker-b", LeaseDuration: time.Minute})
	if err != nil || resumed == nil || resumed.StepID != work.StepID || resumed.AttemptNumber != 2 {
		t.Fatalf("claim resumed step: work=%#v err=%v", resumed, err)
	}
	if err := store.CommitOutcome(ctx, agentruntime.OutcomeCommit{
		Work: *resumed, StepStatus: agentruntime.StepStatusSucceeded,
		RunStatus: agentruntime.RunStatusSucceeded, OutputJSON: json.RawMessage(`{"approved":true}`),
		CommittedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("commit final outcome: %v", err)
	}
	persisted, err = repo.GetRun(ctx, run.ID)
	if err != nil || persisted.Status != agentruntime.RunStatusSucceeded {
		t.Fatalf("expected durable succeeded run, run=%#v err=%v", persisted, err)
	}

	events, err := outbox.NewRepository(pool).Claim(ctx, outbox.ClaimParams{
		Now: now.Add(5 * time.Second), WorkerID: "projection-worker", LeaseDuration: time.Minute, Limit: 10,
	})
	if err != nil || len(events) != 2 {
		t.Fatalf("claim transactional outbox events: count=%d err=%v", len(events), err)
	}
}

// TestEngineStoreRetryCommitIsIdempotent 验证对应场景下的正常路径与失败路径。
func TestEngineStoreRetryCommitIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "agentrun_engine_store_retry", postgres.NewPool, postgres.RunMigrations)
	repo := agentrun.NewRepository(pool)
	store := agentrun.NewEngineStore(repo)
	now := time.Now().UTC()
	requestID := "request-engine-store-retry"

	run, _, err := repo.CreateOrGetRun(ctx, agentrun.CreateRunParams{
		RequestID: &requestID, Objective: "retry idempotently", MaxSteps: 2, MaxToolCalls: 1,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, _, err := repo.CreateOrGetStep(ctx, agentrun.CreateStepParams{
		RunID: run.ID, StepKey: "retrieve:1", StepType: "RetrieveEvidence", InputJSON: json.RawMessage(`{}`), MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("create step: %v", err)
	}
	work, err := store.Claim(ctx, agentruntime.ClaimRequest{Now: now, WorkerID: "worker-retry", LeaseDuration: time.Minute})
	if err != nil || work == nil {
		t.Fatalf("claim step: work=%#v err=%v", work, err)
	}
	retry := agentruntime.RetryCommit{
		Work:        *work,
		Error:       agentruntime.ExecutionError{Category: agentruntime.ErrorCategoryRetryableUpstream, Message: "temporary"},
		NextRetryAt: now.Add(10 * time.Second), CommittedAt: now.Add(time.Second),
	}
	if err := store.ScheduleRetry(ctx, retry); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	if err := store.ScheduleRetry(ctx, retry); err != nil {
		t.Fatalf("repeat retry commit idempotently: %v", err)
	}
	claimedAgain, err := store.Claim(ctx, agentruntime.ClaimRequest{Now: now.Add(11 * time.Second), WorkerID: "worker-retry-2", LeaseDuration: time.Minute})
	if err != nil || claimedAgain == nil || claimedAgain.AttemptNumber != 2 {
		t.Fatalf("claim scheduled retry: work=%#v err=%v", claimedAgain, err)
	}
}

// TestToolAuditExpiredLeaseCanBeReclaimedAndFencesLateFinish 验证对应场景下的正常路径与失败路径。
func TestToolAuditExpiredLeaseCanBeReclaimedAndFencesLateFinish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "tool_audit_recovery", postgres.NewPool, postgres.RunMigrations)
	repo := agentrun.NewRepository(pool)
	requestID := "request-tool-audit-recovery"
	run, _, err := repo.CreateOrGetRun(ctx, agentrun.CreateRunParams{RequestID: &requestID, Objective: "recover tool audit", MaxSteps: 2, MaxToolCalls: 2})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, _, err := repo.CreateOrGetStep(ctx, agentrun.CreateStepParams{RunID: run.ID, StepKey: "read:1", StepType: "ReadDocumentNodes", InputJSON: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	workerA, err := agentrun.NewToolAuditStore(repo, "tool-worker-a", time.Minute)
	if err != nil {
		t.Fatalf("worker A: %v", err)
	}
	workerB, err := agentrun.NewToolAuditStore(repo, "tool-worker-b", time.Minute)
	if err != nil {
		t.Fatalf("worker B: %v", err)
	}
	started := time.Now().UTC()
	call := agenttools.Call{
		RunID: run.ID, StepID: step.ID, ToolName: "document.read_nodes", ToolVersion: "1.0.0",
		Input: json.RawMessage(`{"resource_id":"resource-1"}`), IdempotencyKey: "read-resource-1",
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	first, err := workerA.Begin(ctx, agenttools.AuditStart{Call: call, StartedAt: started})
	if err != nil || !first.Acquired || first.LeaseGeneration != 1 {
		t.Fatalf("first claim: record=%#v err=%v", first, err)
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	reclaimed, err := workerB.Begin(ctx, agenttools.AuditStart{Call: call, StartedAt: started.Add(2 * time.Minute)})
	if err != nil || !reclaimed.Acquired || reclaimed.LeaseGeneration != 2 {
		t.Fatalf("reclaim: record=%#v err=%v", reclaimed, err)
	}
	result := &agenttools.Result{
		Output:     json.RawMessage(`{"nodes":[]}`),
		Provenance: []agenttools.Provenance{{SourceType: "document", SourceID: "resource-1", TrustLevel: "untrusted"}},
	}
	lateFinish := agenttools.AuditFinish{
		ID: first.ID, ClaimedBy: first.ClaimedBy, LeaseGeneration: first.LeaseGeneration,
		Status: agenttools.AuditSucceeded, Result: result, Attempts: 1, CompletedAt: started.Add(2*time.Minute + time.Second),
	}
	if err := workerA.Finish(ctx, lateFinish); !errors.Is(err, agentrun.ErrToolCallLeaseLost) {
		t.Fatalf("late finish must be fenced, got %v", err)
	}
	finish := lateFinish
	finish.ClaimedBy = reclaimed.ClaimedBy
	finish.LeaseGeneration = reclaimed.LeaseGeneration
	if err := workerB.Finish(ctx, finish); err != nil {
		t.Fatalf("reclaimed finish: %v", err)
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	replay, err := workerA.Begin(ctx, agenttools.AuditStart{Call: call, StartedAt: started.Add(3 * time.Minute)})
	if err != nil || replay.Acquired || replay.Status != agenttools.AuditSucceeded || replay.Result == nil {
		t.Fatalf("terminal replay: record=%#v err=%v", replay, err)
	}
}

// stringPointer 执行该函数负责的核心处理逻辑。
func stringPointer(value string) *string {
	return &value
}
