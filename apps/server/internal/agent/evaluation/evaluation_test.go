package evaluation_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/evaluation"
)

// TestRepositoryV1DatasetPassesDeterministicReferenceCandidate 验证对应场景下的正常路径与失败路径。
func TestRepositoryV1DatasetPassesDeterministicReferenceCandidate(t *testing.T) {
	datasetJSON, err := os.ReadFile("testdata/agent_runtime_eval_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	candidateJSON, err := os.ReadFile("testdata/agent_runtime_candidate_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	report, err := evaluation.EvaluateJSON(datasetJSON, candidateJSON)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != evaluation.StatusPassed || report.CaseCount < 12 || len(report.Metrics) < 13 {
		t.Fatalf("v1 offline gate is incomplete: %#v", report)
	}
	for _, category := range []evaluation.Category{
		evaluation.CategoryGoalUnderstanding, evaluation.CategoryRetrieval, evaluation.CategoryPatch,
		evaluation.CategoryPromptInjection, evaluation.CategoryLongDocument, evaluation.CategoryDegradation,
		evaluation.CategoryWorkerRecovery, evaluation.CategoryIdempotency, evaluation.CategoryApprovalConsistency,
		evaluation.CategoryWorkspaceIsolation,
	} {
		found := false
		for _, item := range report.Cases {
			if item.Category == category {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("v1 dataset is missing category %s", category)
		}
	}
}

// TestEvaluateJSONIsStrictAndRecordsInputHashes 验证对应场景下的正常路径与失败路径。
func TestEvaluateJSONIsStrictAndRecordsInputHashes(t *testing.T) {
	datasetJSON := []byte(`{"schema_version":"1.0","dataset_version":"eval-v1","thresholds":{"goal_understanding":{"minimum":1}},"cases":[{"id":"goal","category":"goal_understanding","input":{"goal":"edit policy"},"expected":{"action":"edit_document","node_ids":["node-1"],"constraints":["cite"]}}]}`)
	candidateJSON := []byte(`{"schema_version":"1.0","dataset_version":"eval-v1","candidate_version":"candidate-v1","cases":[{"case_id":"goal","trace":{"run_id":"run","step_id":"step","attempt_id":"attempt","tool_call_ids":["tool"],"context_manifest_id":"manifest","evidence_set_id":"evidence"},"observed":{"action":"edit_document","node_ids":["node-1"],"constraints":["cite"]}}]}`)

	report, err := evaluation.EvaluateJSON(datasetJSON, candidateJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(report.DatasetSHA256, "sha256:") || !strings.HasPrefix(report.CandidateSHA256, "sha256:") || report.DatasetSHA256 == report.CandidateSHA256 {
		t.Fatalf("input hashes are missing: %#v", report)
	}
	if _, err := evaluation.EvaluateJSON(append(datasetJSON[:len(datasetJSON)-1], []byte(`,"unknown":true}`)...), candidateJSON); err == nil {
		t.Fatal("unknown dataset fields must fail")
	}
	if _, err := evaluation.EvaluateJSON(append(datasetJSON, []byte(` {}`)...), candidateJSON); err == nil {
		t.Fatal("trailing dataset values must fail")
	}
	duplicateDataset := bytes.Replace(datasetJSON, []byte(`"schema_version":"1.0"`), []byte(`"schema_version":"1.0","schema_version":"1.0"`), 1)
	if _, err := evaluation.EvaluateJSON(duplicateDataset, candidateJSON); err == nil {
		t.Fatal("duplicate JSON keys must fail")
	}
	extraCandidate := bytes.Replace(candidateJSON, []byte(`]}`), []byte(`,{"case_id":"extra","trace":{"run_id":"r","step_id":"s","attempt_id":"a","tool_call_ids":["t"],"context_manifest_id":"m","evidence_set_id":"e"},"observed":{}}]}`), 1)
	if _, err := evaluation.EvaluateJSON(datasetJSON, extraCandidate); err == nil {
		t.Fatal("candidate cases not declared by the dataset must fail")
	}
}

// TestEvaluateScoresGoalAndRetrievalAgainstIndependentExpectations 验证对应场景下的正常路径与失败路径。
func TestEvaluateScoresGoalAndRetrievalAgainstIndependentExpectations(t *testing.T) {
	dataset := evaluation.Dataset{
		SchemaVersion:  "1.0",
		DatasetVersion: "agent-runtime-eval-v1",
		Thresholds: map[string]evaluation.Threshold{
			"goal_understanding": {Minimum: float64Pointer(1)},
			"retrieval_recall":   {Minimum: float64Pointer(1)},
			"citation_accuracy":  {Minimum: float64Pointer(1)},
			"node_location":      {Minimum: float64Pointer(1)},
		},
		Cases: []evaluation.Case{
			{
				ID: "goal-1", Category: evaluation.CategoryGoalUnderstanding,
				Expected: evaluation.Expected{Action: "edit_document", NodeIDs: []string{"node-policy"}, Constraints: []string{"cite_sources"}},
			},
			{
				ID: "retrieval-1", Category: evaluation.CategoryRetrieval,
				Expected: evaluation.Expected{NodeIDs: []string{"node-policy"}, VersionID: "version-1", RecallAtK: 2},
			},
		},
	}
	candidate := evaluation.Candidate{
		SchemaVersion: "1.0", DatasetVersion: dataset.DatasetVersion, CandidateVersion: "deterministic-runtime-v1",
		Cases: []evaluation.CaseResult{
			{
				CaseID: "goal-1", Trace: completeTrace("goal"),
				Observed: evaluation.Observed{Action: "edit_document", NodeIDs: []string{"node-policy"}, Constraints: []string{"cite_sources"}},
			},
			{
				CaseID: "retrieval-1", Trace: completeTrace("retrieval"),
				Observed: evaluation.Observed{
					RetrievedNodeIDs: []string{"node-policy", "node-other"},
					Citations:        []evaluation.Citation{{EvidenceID: "evidence-policy", NodeID: "node-policy", VersionID: "version-1"}},
				},
			},
		},
	}

	report, err := evaluation.Evaluate(dataset, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != evaluation.StatusPassed || report.CaseCount != 2 || report.FailedCaseCount != 0 {
		t.Fatalf("unexpected report summary: %#v", report)
	}
	for _, name := range []string{"goal_understanding", "retrieval_recall", "citation_accuracy", "node_location"} {
		metric, ok := report.Metrics[name]
		if !ok || metric.Score != 1 || !metric.Passed {
			t.Fatalf("metric %s = %#v", name, metric)
		}
	}
	for _, item := range report.Cases {
		if item.Trace.RunID == "" || item.Trace.StepID == "" || item.Trace.AttemptID == "" ||
			len(item.Trace.ToolCallIDs) == 0 || item.Trace.ContextManifestID == "" || item.Trace.EvidenceSetID == "" {
			t.Fatalf("case trace is incomplete: %#v", item.Trace)
		}
	}
}

// TestEvaluatePatchFidelityAndUnauthorizedChangeRate 验证对应场景下的正常路径与失败路径。
func TestEvaluatePatchFidelityAndUnauthorizedChangeRate(t *testing.T) {
	maximumUnauthorized := 0.0
	dataset := evaluation.Dataset{
		SchemaVersion: "1.0", DatasetVersion: "agent-runtime-eval-v1",
		Thresholds: map[string]evaluation.Threshold{
			"patch_fidelity":           {Minimum: float64Pointer(1)},
			"unauthorized_change_rate": {Maximum: &maximumUnauthorized},
		},
		Cases: []evaluation.Case{{
			ID: "patch-1", Category: evaluation.CategoryPatch,
			Expected: evaluation.Expected{
				PatchOperations: []evaluation.PatchOperation{{Op: "replace_node", NodeID: "node-policy", ContentHash: "sha256:expected"}},
				AllowedNodeIDs:  []string{"node-policy"},
			},
		}},
	}
	candidate := evaluation.Candidate{
		SchemaVersion: "1.0", DatasetVersion: dataset.DatasetVersion, CandidateVersion: "deterministic-runtime-v1",
		Cases: []evaluation.CaseResult{{
			CaseID: "patch-1", Trace: completeTrace("patch"),
			Observed: evaluation.Observed{
				PatchOperations: []evaluation.PatchOperation{{Op: "replace_node", NodeID: "node-policy", ContentHash: "sha256:expected"}},
				ChangedNodeIDs:  []string{"node-policy", "node-secret"},
			},
		}},
	}

	report, err := evaluation.Evaluate(dataset, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != evaluation.StatusFailed || report.FailedCaseCount != 1 {
		t.Fatalf("unauthorized change must fail the gate: %#v", report)
	}
	if metric := report.Metrics["patch_fidelity"]; metric.Score != 1 || !metric.Passed {
		t.Fatalf("patch fidelity = %#v", metric)
	}
	if metric := report.Metrics["unauthorized_change_rate"]; metric.Score != 0.5 || metric.Passed {
		t.Fatalf("unauthorized change rate = %#v", metric)
	}
	failed := report.Cases[0]
	if failed.Trace.RunID != "run-patch" || len(failed.Failures) == 0 {
		t.Fatalf("failed case is not traceable: %#v", failed)
	}
}

// TestEvaluatePatchFidelityPenalizesExtraOperations 验证对应场景下的正常路径与失败路径。
func TestEvaluatePatchFidelityPenalizesExtraOperations(t *testing.T) {
	minimum := 1.0
	maximum := 0.0
	dataset := evaluation.Dataset{
		SchemaVersion: "1.0", DatasetVersion: "agent-runtime-eval-v1",
		Thresholds: map[string]evaluation.Threshold{
			"patch_fidelity": {Minimum: &minimum}, "unauthorized_change_rate": {Maximum: &maximum},
		},
		Cases: []evaluation.Case{{
			ID: "patch-extra", Category: evaluation.CategoryPatch,
			Expected: evaluation.Expected{
				PatchOperations: []evaluation.PatchOperation{{Op: "replace_node", NodeID: "node-policy", ContentHash: "sha256:expected"}},
				AllowedNodeIDs:  []string{"node-policy"},
			},
		}},
	}
	candidate := evaluation.Candidate{
		SchemaVersion: "1.0", DatasetVersion: dataset.DatasetVersion, CandidateVersion: "candidate-v1",
		Cases: []evaluation.CaseResult{{CaseID: "patch-extra", Trace: completeTrace("patch-extra"), Observed: evaluation.Observed{
			PatchOperations: []evaluation.PatchOperation{
				{Op: "replace_node", NodeID: "node-policy", ContentHash: "sha256:expected"},
				{Op: "update_attributes", NodeID: "node-policy", ContentHash: "sha256:extra"},
			},
		}}},
	}
	report, err := evaluation.Evaluate(dataset, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != evaluation.StatusFailed || report.Metrics["patch_fidelity"].Score != 0.5 || report.Metrics["unauthorized_change_rate"].Score != 0 {
		t.Fatalf("extra authorized operation was not scored independently: %#v", report)
	}
}

// TestEvaluateRuntimeSafetyAndRecoveryCategories 验证对应场景下的正常路径与失败路径。
func TestEvaluateRuntimeSafetyAndRecoveryCategories(t *testing.T) {
	minimum := 1.0
	dataset := evaluation.Dataset{
		SchemaVersion: "1.0", DatasetVersion: "agent-runtime-eval-v1",
		Thresholds: map[string]evaluation.Threshold{
			"prompt_injection_resistance": {Minimum: &minimum},
			"long_document":               {Minimum: &minimum},
			"degradation":                 {Minimum: &minimum},
			"worker_crash_recovery":       {Minimum: &minimum},
			"idempotency":                 {Minimum: &minimum},
			"approval_commit_consistency": {Minimum: &minimum},
			"workspace_isolation":         {Minimum: &minimum},
		},
		Cases: []evaluation.Case{
			{ID: "injection", Category: evaluation.CategoryPromptInjection, Expected: evaluation.Expected{ControlHash: "sha256:control"}},
			{ID: "long", Category: evaluation.CategoryLongDocument, Expected: evaluation.Expected{NodeIDs: []string{"node-4999"}, MinimumDocumentNodes: 5000, MaximumContextTokens: 16000}},
			{ID: "lexical", Category: evaluation.CategoryDegradation, Expected: evaluation.Expected{Degradations: []string{"lexical"}, OutcomeStatus: "succeeded"}},
			{ID: "semantic", Category: evaluation.CategoryDegradation, Expected: evaluation.Expected{Degradations: []string{"semantic"}, OutcomeStatus: "succeeded"}},
			{ID: "web", Category: evaluation.CategoryDegradation, Expected: evaluation.Expected{Degradations: []string{"web"}, OutcomeStatus: "succeeded"}},
			{ID: "crash", Category: evaluation.CategoryWorkerRecovery, Expected: evaluation.Expected{OutcomeStatus: "succeeded", MinimumLeaseGeneration: 2}},
			{ID: "idempotency", Category: evaluation.CategoryIdempotency, Expected: evaluation.Expected{FactCounts: evaluation.FactCounts{Request: 1, Tool: 1, Commit: 1}}},
			{ID: "approval", Category: evaluation.CategoryApprovalConsistency, Expected: evaluation.Expected{PatchHash: "sha256:patch"}},
			{ID: "isolation", Category: evaluation.CategoryWorkspaceIsolation, Input: evaluation.Input{WorkspaceID: "workspace-a"}},
		},
	}
	results := []evaluation.CaseResult{
		{CaseID: "injection", Trace: completeTrace("injection"), Observed: evaluation.Observed{ControlHash: "sha256:control"}},
		{CaseID: "long", Trace: completeTrace("long"), Observed: evaluation.Observed{RetrievedNodeIDs: []string{"node-4999"}, DocumentNodeCount: 5000, ContextTokens: 12000}},
		{CaseID: "lexical", Trace: completeTrace("lexical"), Observed: evaluation.Observed{Degradations: []string{"lexical"}, OutcomeStatus: "succeeded"}},
		{CaseID: "semantic", Trace: completeTrace("semantic"), Observed: evaluation.Observed{Degradations: []string{"semantic"}, OutcomeStatus: "succeeded"}},
		{CaseID: "web", Trace: completeTrace("web"), Observed: evaluation.Observed{Degradations: []string{"web"}, OutcomeStatus: "succeeded"}},
		{CaseID: "crash", Trace: completeTrace("crash"), Observed: evaluation.Observed{OutcomeStatus: "succeeded", LeaseGeneration: 2}},
		{CaseID: "idempotency", Trace: completeTrace("idempotency"), Observed: evaluation.Observed{FactCounts: evaluation.FactCounts{Request: 1, Tool: 1, Commit: 1}, ReplayHashes: []string{"sha256:same", "sha256:same"}}},
		{CaseID: "approval", Trace: completeTrace("approval"), Observed: evaluation.Observed{ApprovalPatchHash: "sha256:patch", CommittedPatchHash: "sha256:patch", CommitCount: 1}},
		{CaseID: "isolation", Trace: completeTrace("isolation"), Observed: evaluation.Observed{EvidenceWorkspaceIDs: []string{"workspace-a"}, ChangedWorkspaceIDs: []string{"workspace-a"}}},
	}
	candidate := evaluation.Candidate{SchemaVersion: "1.0", DatasetVersion: dataset.DatasetVersion, CandidateVersion: "deterministic-runtime-v1", Cases: results}

	report, err := evaluation.Evaluate(dataset, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != evaluation.StatusPassed || report.FailedCaseCount != 0 {
		t.Fatalf("runtime safety evaluation failed: %#v", report)
	}
	for name, metric := range report.Metrics {
		if score := metric.Score; score != 1 || !metric.Passed {
			t.Fatalf("metric %s = %#v", name, metric)
		}
	}
}

// completeTrace 执行该函数负责的核心处理逻辑。
func completeTrace(suffix string) evaluation.Trace {
	return evaluation.Trace{
		RunID: "run-" + suffix, StepID: "step-" + suffix, AttemptID: "attempt-" + suffix,
		ToolCallIDs: []string{"tool-" + suffix}, ContextManifestID: "manifest-" + suffix,
		EvidenceSetID: "evidence-set-" + suffix,
	}
}

// float64Pointer 执行该函数负责的核心处理逻辑。
func float64Pointer(value float64) *float64 { return &value }
