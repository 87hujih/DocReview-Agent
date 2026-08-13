package orchestration

import "encoding/json"

type GoalState struct {
	Objective      string   `json:"objective"`
	Constraints    []string `json:"constraints"`
	ExpectedOutput string   `json:"expected_output"`
}

type ObservationRef struct {
	ID          string `json:"id,omitempty"`
	Kind        string `json:"kind"`
	ContentHash string `json:"content_hash"`
	Novel       bool   `json:"novel"`
}

type Finding struct {
	FindingID   string   `json:"finding_id"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids"`
	Confidence  float64  `json:"confidence"`
}

type PatchState struct {
	Generated            bool            `json:"generated"`
	Valid                bool            `json:"valid"`
	PatchInput           json.RawMessage `json:"patch_input,omitempty"`
	TargetIdempotencyKey string          `json:"target_idempotency_key,omitempty"`
}

// 状态为 carried 位于 each 持久化的节点输入. It 包含 only 有界的
// working 状态和引用; full 观察结果和 large 内容 remain 位于
// 持久化的观察结果/制品 storage.
type State struct {
	Goal                  *GoalState       `json:"goal,omitempty"`
	ContextManifestID     string           `json:"context_manifest_id,omitempty"`
	Observations          []ObservationRef `json:"observations,omitempty"`
	Findings              []Finding        `json:"findings,omitempty"`
	Patch                 *PatchState      `json:"patch,omitempty"`
	LastDecision          *Decision        `json:"last_decision,omitempty"`
	LastToolVersion       string           `json:"last_tool_version,omitempty"`
	ApprovalID            string           `json:"approval_id,omitempty"`
	ConsecutiveNoProgress int              `json:"consecutive_no_progress"`
	StopReason            string           `json:"stop_reason,omitempty"`
	Sequence              int              `json:"sequence"`
}

type ApprovalWaitOutput struct {
	ApprovalID   string       `json:"approval_id"`
	Status       string       `json:"status"`
	Continuation StepEnvelope `json:"continuation"`
}
