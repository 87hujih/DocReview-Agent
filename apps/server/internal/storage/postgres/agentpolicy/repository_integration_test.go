package agentpolicy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/orchestration"
	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/storage/postgres/agentpolicy"
	"agent_project/apps/server/internal/testsupport/postgrestest"
)

// TestApprovalRequestDecisionAndVerificationAreDurableAndBound 验证对应场景下的正常路径与失败路径。
func TestApprovalRequestDecisionAndVerificationAreDurableAndBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := postgrestest.NewIsolatedPool(t, ctx, "agentpolicy_approval", postgres.NewPool, postgres.RunMigrations)

	var organizationID, workspaceID, userID, resourceID, runID, stepID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name) VALUES ('approval-org', 'Approval Org') RETURNING id`).Scan(&organizationID); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (organization_id, slug, name) VALUES ($1, 'approval-workspace', 'Approval Workspace') RETURNING id`, organizationID).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (display_name) VALUES ('Approver') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources (title, workspace_id) VALUES ('Approval document', $1) RETURNING id`, workspaceID).Scan(&resourceID); err != nil {
		t.Fatalf("create resource: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runs (workspace_id, objective) VALUES ($1, 'approval test') RETURNING id`, workspaceID).Scan(&runID); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent_steps (run_id, step_key, step_type) VALUES ($1, 'approval:1', 'RequestApproval') RETURNING id`, runID).Scan(&stepID); err != nil {
		t.Fatalf("create step: %v", err)
	}

	security := agenttools.SecurityContext{PrincipalType: "user", PrincipalID: userID, WorkspaceID: workspaceID}
	resources := []agenttools.ResourceRef{{Type: "document", ID: resourceID, Access: agenttools.AccessWrite}}
	store := agentpolicy.NewApprovalStore(pool)
	approval, err := store.RequestApproval(ctx, security, builtin.ApprovalInput{
		RunID: runID, StepID: stepID, ToolName: "patch.commit", ToolVersion: "1.0.0",
		IdempotencyKey: "commit-document-1", Reason: "publish validated patch",
		Payload: json.RawMessage(`{"base_version_id":"version-1"}`), Resources: resources,
	}, "approval-request-1")
	if err != nil || approval.ID == "" || approval.Status != "pending" {
		t.Fatalf("request approval: approval=%#v err=%v", approval, err)
	}
	patchInput := json.RawMessage(`{"resource_id":"` + resourceID + `","base_version_id":"version-1","operations":[],"evidence_refs":[],"reason":"publish"}`)
	waitOutput, err := json.Marshal(orchestration.ApprovalWaitOutput{
		ApprovalID: approval.ID, Status: "pending",
		Continuation: orchestration.StepEnvelope{
			State: orchestration.State{
				Goal:       &orchestration.GoalState{Objective: "approval test", ExpectedOutput: "patch"},
				Patch:      &orchestration.PatchState{Generated: true, Valid: true, PatchInput: patchInput, TargetIdempotencyKey: "commit-document-1"},
				ApprovalID: approval.ID, Sequence: 8,
			},
			NodeInput: patchInput,
		},
	})
	if err != nil {
		t.Fatalf("encode typed approval wait: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_steps SET status = 'waiting_approval', output_json = $2 WHERE id = $1`, stepID, waitOutput); err != nil {
		t.Fatalf("persist waiting approval step: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET status = 'waiting_approval', current_step = 'approval:1' WHERE id = $1`, runID); err != nil {
		t.Fatalf("persist waiting approval run: %v", err)
	}

	resolver := agentpolicy.NewResolver(pool)
	check := agenttools.ApprovalCheck{
		ApprovalID: approval.ID, Principal: security, RunID: runID, StepID: stepID, ToolName: "patch.commit", ToolVersion: "1.0.0",
		IdempotencyKey: "commit-document-1", Resources: resources,
	}
	approved, err := resolver.VerifyApproval(ctx, check)
	if err != nil || approved {
		t.Fatalf("pending approval was accepted: approved=%v err=%v", approved, err)
	}

	decidedAt := time.Now().UTC()
	decision := agentpolicy.DecisionParams{
		ApprovalID: approval.ID, Security: security, Status: "approved", Reason: "reviewed by owner", DecidedAt: decidedAt,
	}
	decided, err := store.DecideApproval(ctx, decision)
	if err != nil || decided.Status != "approved" {
		t.Fatalf("decide approval: decision=%#v err=%v", decided, err)
	}
	if replay, err := store.DecideApproval(ctx, decision); err != nil || replay != decided {
		t.Fatalf("idempotent decision replay: replay=%#v err=%v", replay, err)
	}
	conflict := decision
	conflict.Status = "rejected"
	if _, err := store.DecideApproval(ctx, conflict); !isToolCategory(err, agenttools.ErrorConflict) {
		t.Fatalf("different terminal decision must conflict, got %v", err)
	}
	var commitStepID, commitStepStatus, runStatus string
	if err := pool.QueryRow(ctx, `SELECT id, status FROM agent_steps WHERE run_id = $1 AND step_type = 'CommitPatch'`, runID).Scan(&commitStepID, &commitStepStatus); err != nil {
		t.Fatalf("load approved continuation: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, runID).Scan(&runStatus); err != nil {
		t.Fatalf("load resumed run: %v", err)
	}
	if commitStepStatus != "queued" || runStatus != "queued" {
		t.Fatalf("approval did not atomically resume typed continuation: step=%s run=%s", commitStepStatus, runStatus)
	}
	check.StepID = commitStepID
	approved, err = resolver.VerifyApproval(ctx, check)
	if err != nil || !approved {
		t.Fatalf("persisted matching approval not accepted: approved=%v err=%v", approved, err)
	}
	wrongResources := check
	wrongResources.Resources = []agenttools.ResourceRef{{Type: "document", ID: resources[0].ID, Access: agenttools.AccessRead}}
	approved, err = resolver.VerifyApproval(ctx, wrongResources)
	if err != nil || approved {
		t.Fatalf("approval escaped resource binding: approved=%v err=%v", approved, err)
	}
	wrongRun := check
	wrongRun.RunID = "00000000-0000-0000-0000-000000000999"
	approved, err = resolver.VerifyApproval(ctx, wrongRun)
	if err != nil || approved {
		t.Fatalf("approval escaped run binding: approved=%v err=%v", approved, err)
	}

	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type = 'agent_tool_approval' AND aggregate_id = $1`, approval.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count approval outbox: %v", err)
	}
	if eventCount != 2 {
		t.Fatalf("approval request and decision must commit two outbox facts, got %d", eventCount)
	}

	var rejectedRunID, rejectedStepID string
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runs (workspace_id, objective) VALUES ($1, 'rejected approval test') RETURNING id`, workspaceID).Scan(&rejectedRunID); err != nil {
		t.Fatalf("create rejected run: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent_steps (run_id, step_key, step_type) VALUES ($1, 'approval:reject', 'RequestApproval') RETURNING id`, rejectedRunID).Scan(&rejectedStepID); err != nil {
		t.Fatalf("create rejected step: %v", err)
	}
	rejectedApproval, err := store.RequestApproval(ctx, security, builtin.ApprovalInput{
		RunID: rejectedRunID, StepID: rejectedStepID, ToolName: "patch.commit", ToolVersion: "1.0.0",
		IdempotencyKey: "commit-document-rejected", Reason: "publish rejected patch",
		Payload: json.RawMessage(`{"base_version_id":"version-1"}`), Resources: resources,
	}, "approval-request-rejected")
	if err != nil {
		t.Fatalf("request rejected approval: %v", err)
	}
	rejectedPatch := json.RawMessage(`{"resource_id":"` + resourceID + `","base_version_id":"version-1","operations":[],"evidence_refs":[],"reason":"reject"}`)
	rejectedWaitOutput, err := json.Marshal(orchestration.ApprovalWaitOutput{
		ApprovalID: rejectedApproval.ID, Status: "pending",
		Continuation: orchestration.StepEnvelope{
			State: orchestration.State{
				Goal:       &orchestration.GoalState{Objective: "rejected approval test", ExpectedOutput: "patch"},
				Patch:      &orchestration.PatchState{Generated: true, Valid: true, PatchInput: rejectedPatch, TargetIdempotencyKey: "commit-document-rejected"},
				ApprovalID: rejectedApproval.ID, Sequence: 8,
			},
			NodeInput: rejectedPatch,
		},
	})
	if err != nil {
		t.Fatalf("encode rejected approval wait: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_steps SET status = 'waiting_approval', output_json = $2 WHERE id = $1`, rejectedStepID, rejectedWaitOutput); err != nil {
		t.Fatalf("persist rejected waiting step: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET status = 'waiting_approval', current_step = 'approval:reject' WHERE id = $1`, rejectedRunID); err != nil {
		t.Fatalf("persist rejected waiting run: %v", err)
	}
	if _, err := store.DecideApproval(ctx, agentpolicy.DecisionParams{
		ApprovalID: rejectedApproval.ID, Security: security, Status: "rejected", Reason: "unsafe change", DecidedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("reject approval: %v", err)
	}
	var rejectedStepStatus, rejectedRunStatus string
	var rejectedCommitCount int
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_steps WHERE id = $1`, rejectedStepID).Scan(&rejectedStepStatus); err != nil {
		t.Fatalf("load rejected step: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, rejectedRunID).Scan(&rejectedRunStatus); err != nil {
		t.Fatalf("load rejected run: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_steps WHERE run_id = $1 AND step_type = 'CommitPatch'`, rejectedRunID).Scan(&rejectedCommitCount); err != nil {
		t.Fatalf("count rejected continuations: %v", err)
	}
	if rejectedStepStatus != "failed" || rejectedRunStatus != "failed" || rejectedCommitCount != 0 {
		t.Fatalf("rejected approval escaped terminal transition: step=%s run=%s commits=%d", rejectedStepStatus, rejectedRunStatus, rejectedCommitCount)
	}

	limiter := agentpolicy.NewPostgresRateLimiter(pool, agentpolicy.StaticRateLimitRules{
		Default: agentpolicy.RateLimitRule{Limit: 2, Window: time.Minute},
	})
	limitRequest := agenttools.RateLimitRequest{
		ToolName: "web.search", ToolVersion: "1.0.0", RiskLevel: agenttools.RiskMedium, Security: security,
	}
	for attempt := 1; attempt <= 2; attempt++ {
		decision, err := limiter.Allow(ctx, limitRequest)
		if err != nil || !decision.Allowed {
			t.Fatalf("rate limit attempt %d: decision=%#v err=%v", attempt, decision, err)
		}
	}
	denied, err := limiter.Allow(ctx, limitRequest)
	if err != nil || denied.Allowed || denied.RetryAfter <= 0 || denied.RetryAfter > time.Minute {
		t.Fatalf("durable rate limit denial: decision=%#v err=%v", denied, err)
	}
}

// isToolCategory 执行该函数负责的核心处理逻辑。
func isToolCategory(err error, category agenttools.ErrorCategory) bool {
	var toolErr *agenttools.ToolError
	return errors.As(err, &toolErr) && toolErr.Category == category
}
