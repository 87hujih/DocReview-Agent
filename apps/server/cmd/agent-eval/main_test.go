package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRunWritesPassingReportAndReturnsNonzeroForGateFailure 验证对应场景下的正常路径与失败路径。
func TestRunWritesPassingReportAndReturnsNonzeroForGateFailure(t *testing.T) {
	datasetPath := filepath.Join("..", "..", "internal", "agent", "evaluation", "testdata", "agent_runtime_eval_v1.json")
	candidatePath := filepath.Join("..", "..", "internal", "agent", "evaluation", "testdata", "agent_runtime_candidate_v1.json")
	reportPath := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-dataset", datasetPath, "-candidate", candidatePath, "-report", reportPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("passing gate exit=%d stderr=%s", code, stderr.String())
	}
	report, err := os.ReadFile(reportPath)
	if err != nil || !bytes.Contains(report, []byte(`"status": "passed"`)) {
		t.Fatalf("passing report missing: err=%v report=%s", err, report)
	}

	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	failingCandidate := bytes.Replace(candidate, []byte("sha256:control-v1"), []byte("sha256:changed"), 1)
	failingPath := filepath.Join(t.TempDir(), "candidate-failing.json")
	if err := os.WriteFile(failingPath, failingCandidate, 0o600); err != nil {
		t.Fatal(err)
	}
	failingReportPath := filepath.Join(t.TempDir(), "report-failing.json")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-dataset", datasetPath, "-candidate", failingPath, "-report", failingReportPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("failing gate exit=%d stderr=%s", code, stderr.String())
	}
	failingReport, err := os.ReadFile(failingReportPath)
	if err != nil || !bytes.Contains(failingReport, []byte(`"status": "failed"`)) || !bytes.Contains(failingReport, []byte(`"run_id": "eval-run-injection"`)) {
		t.Fatalf("failure report is not traceable: err=%v report=%s", err, failingReport)
	}
}
