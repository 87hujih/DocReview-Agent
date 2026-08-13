package orchestration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agent_project/apps/server/internal/agent/orchestration"
	agentruntime "agent_project/apps/server/internal/agent/runtime"
)

// TestSupervisorUnderstandGoalProducesTypedDurableContinuation 验证对应场景下的正常路径与失败路径。
func TestSupervisorUnderstandGoalProducesTypedDurableContinuation(t *testing.T) {
	gateway := &scriptedModelGateway{responses: []orchestration.ModelResponse{{
		Output:   json.RawMessage(`{"objective":"review policy document","constraints":["preserve citations"],"expected_output":"validated patch"}`),
		Provider: "test", Model: "semantic-test", PromptVersion: "understand-v1", InputTokens: 20, OutputTokens: 12,
	}}}
	supervisor := mustSupervisor(t, gateway, fakeContextAssembler{}, fakeToolExecutor{})
	result := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-1", StepKey: "understand_goal:1", StepType: "UnderstandGoal",
		InputJSON: json.RawMessage(`{"message":"Please review the policy document"}`), AttemptNumber: 1,
	})
	if result.Err != nil || result.Outcome != agentruntime.OutcomeContinue || len(result.NextSteps) != 1 {
		t.Fatalf("understand result=%#v", result)
	}
	if result.NextSteps[0].StepType != string(orchestration.NodeAssembleContext) || result.Provider != "test" || result.ContextManifestID != "manifest-1" {
		t.Fatalf("understand continuation=%#v", result)
	}
	var envelope orchestration.StepEnvelope
	if err := json.Unmarshal(result.NextSteps[0].InputJSON, &envelope); err != nil {
		t.Fatalf("decode continuation: %v", err)
	}
	if envelope.State.Goal == nil || envelope.State.Goal.Objective != "review policy document" || envelope.State.Sequence != 1 {
		t.Fatalf("typed state=%#v", envelope.State)
	}
}

// TestSupervisorAssemblesContextThenRoutesStrictDecision 验证对应场景下的正常路径与失败路径。
func TestSupervisorAssemblesContextThenRoutesStrictDecision(t *testing.T) {
	state := orchestration.State{Goal: &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"}, Sequence: 1}
	contextAssembler := fakeContextAssembler{snapshot: orchestration.ContextSnapshot{ManifestID: "manifest-42"}}
	decisionGateway := &scriptedModelGateway{responses: []orchestration.ModelResponse{{
		Output:   json.RawMessage(`{"action":"retrieve_evidence","reason":"need evidence","tool_name":"retrieval.search","tool_input":{"resource_id":"resource-1","query":"policy","limit":5},"expected_observation":"evidence set","confidence":0.9}`),
		Provider: "test", Model: "decision-test", PromptVersion: "decision-v1",
	}}}
	supervisor := mustSupervisor(t, decisionGateway, contextAssembler, fakeToolExecutor{})

	assembled := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-context", StepType: string(orchestration.NodeAssembleContext),
		InputJSON: mustEnvelopeJSON(t, state, json.RawMessage(`{}`)),
	})
	if assembled.Err != nil || assembled.Outcome != agentruntime.OutcomeContinue || len(assembled.NextSteps) != 1 ||
		assembled.NextSteps[0].StepType != string(orchestration.NodeDecideNextAction) || assembled.ContextManifestID != "manifest-42" {
		t.Fatalf("assemble result=%#v", assembled)
	}
	var decisionEnvelope orchestration.StepEnvelope
	if err := json.Unmarshal(assembled.NextSteps[0].InputJSON, &decisionEnvelope); err != nil {
		t.Fatal(err)
	}
	decided := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-decision", StepType: string(orchestration.NodeDecideNextAction),
		InputJSON: assembled.NextSteps[0].InputJSON,
	})
	if decided.Err != nil || decided.Outcome != agentruntime.OutcomeContinue || len(decided.NextSteps) != 1 ||
		decided.NextSteps[0].StepType != string(orchestration.NodeRetrieveEvidence) {
		t.Fatalf("decision result=%#v", decided)
	}
	var retrieveEnvelope orchestration.StepEnvelope
	if err := json.Unmarshal(decided.NextSteps[0].InputJSON, &retrieveEnvelope); err != nil {
		t.Fatal(err)
	}
	var toolInput map[string]any
	if err := json.Unmarshal(retrieveEnvelope.NodeInput, &toolInput); err != nil {
		t.Fatal(err)
	}
	if retrieveEnvelope.State.ContextManifestID != "manifest-42" || toolInput["resource_id"] != "resource-1" || toolInput["query"] != "policy" {
		t.Fatalf("retrieve envelope=%#v", retrieveEnvelope)
	}
}

// TestSupervisorStopsDecisionLoopAfterRepeatedNoProgressWithoutCallingModel 验证对应场景下的正常路径与失败路径。
func TestSupervisorStopsDecisionLoopAfterRepeatedNoProgressWithoutCallingModel(t *testing.T) {
	state := orchestration.State{
		Goal:              &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"},
		ContextManifestID: "manifest-1", ConsecutiveNoProgress: 3, Sequence: 8,
	}
	gateway := &scriptedModelGateway{}
	supervisor := mustSupervisor(t, gateway, fakeContextAssembler{}, fakeToolExecutor{})
	result := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-decision", StepType: string(orchestration.NodeDecideNextAction),
		InputJSON: mustEnvelopeJSON(t, state, json.RawMessage(`{}`)),
	})
	if result.Err != nil || result.Outcome != agentruntime.OutcomeContinue || len(result.NextSteps) != 1 ||
		result.NextSteps[0].StepType != string(orchestration.NodeRenderOutcome) || gateway.index != 0 {
		t.Fatalf("no-progress stop=%#v model_calls=%d", result, gateway.index)
	}
	var envelope orchestration.StepEnvelope
	if err := json.Unmarshal(result.NextSteps[0].InputJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.State.StopReason != "no_new_information" {
		t.Fatalf("stop state=%#v", envelope.State)
	}
}

// TestSupervisorExecutesTypedToolAndProducesDurableObservation 验证对应场景下的正常路径与失败路径。
func TestSupervisorExecutesTypedToolAndProducesDurableObservation(t *testing.T) {
	state := orchestration.State{
		Goal:              &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"},
		ContextManifestID: "manifest-1", Sequence: 3,
		LastDecision:    &orchestration.Decision{Action: orchestration.ActionRetrieveEvidence, ToolName: "retrieval.search"},
		LastToolVersion: "2.0.0",
	}
	tools := fakeToolExecutor{observation: orchestration.ToolObservation{
		ToolCallID: "tool-call-1", Output: json.RawMessage(`{"evidence_set":{"evidence":[{"evidence_id":"evidence-1"}]}}`),
	}}
	supervisor := mustSupervisor(t, &scriptedModelGateway{}, fakeContextAssembler{}, tools)
	result := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-tool", StepKey: "retrieve_evidence:3",
		StepType: string(orchestration.NodeRetrieveEvidence), IdempotencyKey: "agent-step:step-tool",
		InputJSON: mustEnvelopeJSON(t, state, json.RawMessage(`{"resource_id":"resource-1","query":"policy","limit":5}`)),
	})
	if result.Err != nil || result.Outcome != agentruntime.OutcomeContinue || len(result.NextSteps) != 1 || len(result.Observations) != 1 {
		t.Fatalf("tool result=%#v", result)
	}
	if result.Observations[0].ToolCallID != "tool-call-1" || result.Observations[0].ContentHash == "" || !result.Observations[0].Novel {
		t.Fatalf("durable observation=%#v", result.Observations[0])
	}
	var envelope orchestration.StepEnvelope
	if err := json.Unmarshal(result.NextSteps[0].InputJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.State.Observations) != 1 || envelope.State.ConsecutiveNoProgress != 0 ||
		result.NextSteps[0].StepType != string(orchestration.NodeAssembleContext) {
		t.Fatalf("observed state=%#v", envelope.State)
	}
}

// TestSupervisorPropagatesAttemptTraceToModelsAndTools 验证对应场景下的正常路径与失败路径。
func TestSupervisorPropagatesAttemptTraceToModelsAndTools(t *testing.T) {
	gateway := &scriptedModelGateway{responses: []orchestration.ModelResponse{{
		Output: json.RawMessage(`{"objective":"review policy","constraints":[],"expected_output":"patch"}`),
	}}}
	supervisor := mustSupervisor(t, gateway, fakeContextAssembler{}, fakeToolExecutor{})
	supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-understand", TraceID: "trace-1", StepType: string(orchestration.NodeUnderstandGoal),
		InputJSON: json.RawMessage(`{"message":"review policy"}`),
	})
	if len(gateway.requests) != 1 || gateway.requests[0].TraceID != "trace-1" {
		t.Fatalf("model trace was not propagated: %#v", gateway.requests)
	}

	state := orchestration.State{
		Goal: &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"}, ContextManifestID: "manifest-1",
		LastDecision:    &orchestration.Decision{Action: orchestration.ActionRetrieveEvidence, ToolName: "retrieval.search"},
		LastToolVersion: "2.0.0",
	}
	tools := &capturingToolExecutor{observation: orchestration.ToolObservation{ToolCallID: "tool-1", Output: json.RawMessage(`{"evidence_set":{"evidence":[]}}`)}}
	supervisor = mustSupervisor(t, &scriptedModelGateway{}, fakeContextAssembler{}, tools)
	supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-retrieve", TraceID: "trace-2", StepType: string(orchestration.NodeRetrieveEvidence),
		IdempotencyKey: "agent-step:step-retrieve", InputJSON: mustEnvelopeJSON(t, state, json.RawMessage(`{"query":"policy"}`)),
	})
	if tools.request.TraceID != "trace-2" || tools.request.ToolVersion != "2.0.0" {
		t.Fatalf("tool trace was not propagated: %#v", tools.request)
	}
}

// TestSupervisorRejectsPersistedRetrievalToolVersionDrift 验证对应场景下的正常路径与失败路径。
func TestSupervisorRejectsPersistedRetrievalToolVersionDrift(t *testing.T) {
	state := orchestration.State{
		Goal: &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"}, ContextManifestID: "manifest-1",
		LastDecision:    &orchestration.Decision{Action: orchestration.ActionRetrieveEvidence, ToolName: "retrieval.search"},
		LastToolVersion: "1.0.0",
	}
	tools := &capturingToolExecutor{observation: orchestration.ToolObservation{ToolCallID: "tool-1", Output: json.RawMessage(`{"evidence_set":{}}`)}}
	supervisor := mustSupervisor(t, &scriptedModelGateway{}, fakeContextAssembler{}, tools)
	result := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-retrieve", StepType: string(orchestration.NodeRetrieveEvidence),
		InputJSON: mustEnvelopeJSON(t, state, json.RawMessage(`{"resource_id":"resource-1","query":"policy","limit":5}`)),
	})
	if result.Err == nil || result.Err.Category != agentruntime.ErrorCategoryPolicyBlocked || tools.request.ToolName != "" {
		t.Fatalf("version drift was not blocked: result=%#v request=%#v", result, tools.request)
	}
}

// TestSupervisorAnalysisAndPatchGenerationRemainTyped 验证对应场景下的正常路径与失败路径。
func TestSupervisorAnalysisAndPatchGenerationRemainTyped(t *testing.T) {
	gateway := &scriptedModelGateway{responses: []orchestration.ModelResponse{
		{Output: json.RawMessage(`{"findings":[{"finding_id":"finding-1","summary":"policy is outdated","evidence_ids":["evidence-1"],"confidence":0.9}]}`), PromptVersion: "analyze-v1"},
		{Output: json.RawMessage(`{"patch_input":{"resource_id":"resource-1","base_version_id":"version-1","operations":[{"op":"replace_node","node_id":"node-1","expected_hash":"sha256:old","content":"updated"}],"evidence_refs":["evidence-1"],"reason":"update policy"}}`), PromptVersion: "patch-v1"},
	}}
	state := orchestration.State{
		Goal:              &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"},
		ContextManifestID: "manifest-1", Sequence: 4,
		Observations: []orchestration.ObservationRef{{ID: "observation-1", Kind: "RetrieveEvidence", ContentHash: "sha256:evidence", Novel: true}},
	}
	supervisor := mustSupervisor(t, gateway, fakeContextAssembler{}, fakeToolExecutor{})
	analyzed := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-analyze", StepType: string(orchestration.NodeAnalyzeEvidence),
		InputJSON: mustEnvelopeJSON(t, state, json.RawMessage(`{}`)),
	})
	if analyzed.Err != nil || len(analyzed.NextSteps) != 1 || analyzed.NextSteps[0].StepType != string(orchestration.NodeAssembleContext) {
		t.Fatalf("analyze result=%#v", analyzed)
	}
	var afterAnalysis orchestration.StepEnvelope
	if err := json.Unmarshal(analyzed.NextSteps[0].InputJSON, &afterAnalysis); err != nil {
		t.Fatal(err)
	}
	if len(afterAnalysis.State.Findings) != 1 || afterAnalysis.State.Findings[0].FindingID != "finding-1" {
		t.Fatalf("findings=%#v", afterAnalysis.State.Findings)
	}

	generated := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-generate", StepType: string(orchestration.NodeGeneratePatch),
		InputJSON: mustEnvelopeJSON(t, afterAnalysis.State, json.RawMessage(`{}`)),
	})
	if generated.Err != nil || len(generated.NextSteps) != 1 || generated.NextSteps[0].StepType != string(orchestration.NodeValidatePatch) {
		t.Fatalf("generate result=%#v", generated)
	}
	var afterPatch orchestration.StepEnvelope
	if err := json.Unmarshal(generated.NextSteps[0].InputJSON, &afterPatch); err != nil {
		t.Fatal(err)
	}
	if afterPatch.State.Patch == nil || !afterPatch.State.Patch.Generated || afterPatch.State.Patch.Valid {
		t.Fatalf("patch state=%#v", afterPatch.State.Patch)
	}
}

// TestSupervisorValidatesPatchBeforeCreatingBoundApprovalContinuation 验证对应场景下的正常路径与失败路径。
func TestSupervisorValidatesPatchBeforeCreatingBoundApprovalContinuation(t *testing.T) {
	patchInput := json.RawMessage(`{"resource_id":"resource-1","base_version_id":"version-1","operations":[{"op":"replace_node","node_id":"node-1","expected_hash":"sha256:old","content":"updated"}],"evidence_refs":["evidence-1"],"reason":"update policy"}`)
	state := orchestration.State{
		Goal:              &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"},
		ContextManifestID: "manifest-1", Sequence: 6,
		Patch: &orchestration.PatchState{Generated: true, PatchInput: patchInput},
	}
	validatorTools := fakeToolExecutor{observation: orchestration.ToolObservation{
		ToolCallID: "tool-validate", Output: json.RawMessage(`{"validation":{"valid":true,"errors":[]}}`),
	}}
	supervisor := mustSupervisor(t, &scriptedModelGateway{}, fakeContextAssembler{}, validatorTools)
	validated := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-validate", StepType: string(orchestration.NodeValidatePatch),
		IdempotencyKey: "agent-step:step-validate", InputJSON: mustEnvelopeJSON(t, state, patchInput),
	})
	if validated.Err != nil || len(validated.NextSteps) != 1 || validated.NextSteps[0].StepType != string(orchestration.NodeRequestApproval) {
		t.Fatalf("validate result=%#v", validated)
	}
	var afterValidation orchestration.StepEnvelope
	if err := json.Unmarshal(validated.NextSteps[0].InputJSON, &afterValidation); err != nil {
		t.Fatal(err)
	}
	if afterValidation.State.Patch == nil || !afterValidation.State.Patch.Valid || afterValidation.State.Patch.TargetIdempotencyKey == "" {
		t.Fatalf("validated patch=%#v", afterValidation.State.Patch)
	}

	approvalTools := fakeToolExecutor{observation: orchestration.ToolObservation{
		ToolCallID: "tool-approval", Output: json.RawMessage(`{"approval":{"id":"approval-1","status":"pending"}}`),
	}}
	supervisor = mustSupervisor(t, &scriptedModelGateway{}, fakeContextAssembler{}, approvalTools)
	waiting := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-approval", StepType: string(orchestration.NodeRequestApproval),
		IdempotencyKey: "agent-step:step-approval", InputJSON: mustEnvelopeJSON(t, afterValidation.State, json.RawMessage(`{}`)),
	})
	if waiting.Err != nil || waiting.Outcome != agentruntime.OutcomeWaitApproval || len(waiting.NextSteps) != 0 {
		t.Fatalf("approval result=%#v", waiting)
	}
	var output struct {
		ApprovalID   string                     `json:"approval_id"`
		Continuation orchestration.StepEnvelope `json:"continuation"`
	}
	if err := json.Unmarshal(waiting.OutputJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output.ApprovalID != "approval-1" || output.Continuation.State.ApprovalID != "approval-1" ||
		output.Continuation.State.Patch.TargetIdempotencyKey == "" {
		t.Fatalf("approval continuation=%#v", output)
	}
}

// TestSupervisorCommitsOnlyWithApprovalThenRendersTypedOutcome 验证对应场景下的正常路径与失败路径。
func TestSupervisorCommitsOnlyWithApprovalThenRendersTypedOutcome(t *testing.T) {
	patchInput := json.RawMessage(`{"resource_id":"resource-1","base_version_id":"version-1","operations":[{"op":"replace_node","node_id":"node-1","expected_hash":"sha256:old","content":"updated"}],"evidence_refs":["evidence-1"],"reason":"update policy"}`)
	state := orchestration.State{
		Goal:              &orchestration.GoalState{Objective: "review policy", ExpectedOutput: "patch"},
		ContextManifestID: "manifest-1", Sequence: 9, ApprovalID: "approval-1",
		Patch: &orchestration.PatchState{Generated: true, Valid: true, PatchInput: patchInput, TargetIdempotencyKey: "patch-commit-1"},
	}
	commitTools := fakeToolExecutor{observation: orchestration.ToolObservation{
		ToolCallID: "tool-commit", Output: json.RawMessage(`{"commit":{"resource_id":"resource-1","version_id":"version-2","outbox_id":"event-1"}}`),
	}}
	gateway := &scriptedModelGateway{responses: []orchestration.ModelResponse{{
		Output: json.RawMessage(`{"message":"The validated patch was approved and committed."}`), PromptVersion: "render-v1",
	}}}
	supervisor := mustSupervisor(t, gateway, fakeContextAssembler{}, commitTools)
	committed := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-commit", StepType: string(orchestration.NodeCommitPatch),
		IdempotencyKey: "agent-step:step-commit", InputJSON: mustEnvelopeJSON(t, state, patchInput),
	})
	if committed.Err != nil || len(committed.NextSteps) != 1 || committed.NextSteps[0].StepType != string(orchestration.NodeAssembleContext) {
		t.Fatalf("commit result=%#v", committed)
	}
	var afterCommit orchestration.StepEnvelope
	if err := json.Unmarshal(committed.NextSteps[0].InputJSON, &afterCommit); err != nil {
		t.Fatal(err)
	}
	if afterCommit.State.StopReason != "goal_achieved" {
		t.Fatalf("commit stop state=%#v", afterCommit.State)
	}

	rendered := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-render", StepType: string(orchestration.NodeRenderOutcome),
		InputJSON: mustEnvelopeJSON(t, afterCommit.State, json.RawMessage(`{}`)),
	})
	if rendered.Err != nil || rendered.Outcome != agentruntime.OutcomeSucceed || string(rendered.OutputJSON) != `{"message":"The validated patch was approved and committed."}` {
		t.Fatalf("render result=%#v", rendered)
	}
}

// TestDecodeStepEnvelopeRejectsUnknownFields 验证对应场景下的正常路径与失败路径。
func TestDecodeStepEnvelopeRejectsUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"state":{"goal":{"objective":"review","constraints":[],"expected_output":"patch"},"sequence":1},"node_input":{},"model_can_approve":true}`)
	if _, err := orchestration.DecodeStepEnvelope(raw); err == nil {
		t.Fatal("approval continuation accepted an unknown authority field")
	}
}

// TestSupervisorPreservesRetryableModelFailureCategory 验证对应场景下的正常路径与失败路径。
func TestSupervisorPreservesRetryableModelFailureCategory(t *testing.T) {
	supervisor := mustSupervisor(t, failingModelGateway{err: &orchestration.ModelFailure{
		Category: agentruntime.ErrorCategoryRetryableUpstream, Message: "provider unavailable", Cause: errors.New("503"),
	}}, fakeContextAssembler{}, fakeToolExecutor{})
	result := supervisor.Execute(context.Background(), agentruntime.ExecutionInput{
		RunID: "run-1", StepID: "step-understand", StepType: string(orchestration.NodeUnderstandGoal),
		InputJSON: json.RawMessage(`{"message":"review"}`),
	})
	if result.Err == nil || result.Err.Category != agentruntime.ErrorCategoryRetryableUpstream {
		t.Fatalf("model error classification=%#v", result.Err)
	}
}

type scriptedModelGateway struct {
	responses []orchestration.ModelResponse
	requests  []orchestration.ModelRequest
	index     int
}

type failingModelGateway struct{ err error }

// Invoke 执行该函数负责的核心处理逻辑。
func (gateway failingModelGateway) Invoke(context.Context, orchestration.ModelRequest) (orchestration.ModelResponse, error) {
	return orchestration.ModelResponse{}, gateway.err
}

// Invoke 执行该函数负责的核心处理逻辑。
func (gateway *scriptedModelGateway) Invoke(_ context.Context, request orchestration.ModelRequest) (orchestration.ModelResponse, error) {
	gateway.requests = append(gateway.requests, request)
	response := gateway.responses[gateway.index]
	gateway.index++
	return response, nil
}

type fakeContextAssembler struct{ snapshot orchestration.ContextSnapshot }

// Assemble 执行该函数负责的核心处理逻辑。
func (assembler fakeContextAssembler) Assemble(context.Context, orchestration.ContextRequest) (orchestration.ContextSnapshot, error) {
	if assembler.snapshot.ManifestID == "" {
		return orchestration.ContextSnapshot{ManifestID: "manifest-1"}, nil
	}
	return assembler.snapshot, nil
}

// 加载按作用域读取并返回所需数据。
func (assembler fakeContextAssembler) Load(context.Context, string) (orchestration.ContextSnapshot, error) {
	if assembler.snapshot.ManifestID == "" {
		return orchestration.ContextSnapshot{ManifestID: "manifest-1"}, nil
	}
	return assembler.snapshot, nil
}

type fakeToolExecutor struct{ observation orchestration.ToolObservation }

// Execute 执行该函数负责的核心处理逻辑。
func (executor fakeToolExecutor) Execute(context.Context, orchestration.ToolRequest) (orchestration.ToolObservation, error) {
	return executor.observation, nil
}

type capturingToolExecutor struct {
	request     orchestration.ToolRequest
	observation orchestration.ToolObservation
}

// Execute 执行该函数负责的核心处理逻辑。
func (executor *capturingToolExecutor) Execute(_ context.Context, request orchestration.ToolRequest) (orchestration.ToolObservation, error) {
	executor.request = request
	return executor.observation, nil
}

// mustSupervisor 执行该函数负责的核心处理逻辑。
func mustSupervisor(t *testing.T, gateway orchestration.ModelGateway, contexts orchestration.ContextAssembler, tools orchestration.ToolExecutor) *orchestration.Supervisor {
	t.Helper()
	supervisor, err := orchestration.NewSupervisor(orchestration.SupervisorConfig{MaxNoProgress: 3, MaxStateObservations: 16}, gateway, contexts, tools, orchestration.NewActionValidator())
	if err != nil {
		t.Fatalf("new supervisor: %v", err)
	}
	return supervisor
}

// mustEnvelopeJSON 执行该函数负责的核心处理逻辑。
func mustEnvelopeJSON(t *testing.T, state orchestration.State, nodeInput json.RawMessage) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(orchestration.StepEnvelope{State: state, NodeInput: nodeInput})
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return encoded
}
