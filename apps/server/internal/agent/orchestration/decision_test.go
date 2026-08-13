package orchestration_test

import (
	"encoding/json"
	"testing"

	"agent_project/apps/server/internal/agent/orchestration"
)

// TestDecisionCodecAcceptsOnlyStrictTypedDecision 验证对应场景下的正常路径与失败路径。
func TestDecisionCodecAcceptsOnlyStrictTypedDecision(t *testing.T) {
	codec := orchestration.DecisionCodec{}
	decision, err := codec.Decode(json.RawMessage(`{
		"action":"retrieve_evidence",
		"reason":"need current evidence",
		"tool_name":"retrieval.search",
		"tool_input":{"resource_id":"resource-1","query":"policy","limit":5},
		"expected_observation":"versioned evidence set",
		"confidence":0.8
	}`))
	if err != nil {
		t.Fatalf("decode valid decision: %v", err)
	}
	if decision.Action != orchestration.ActionRetrieveEvidence || decision.ToolName != "retrieval.search" || decision.Confidence != 0.8 {
		t.Fatalf("decision = %#v", decision)
	}

	invalid := []json.RawMessage{
		json.RawMessage(`{"action":"run_shell","reason":"escape","tool_name":"shell","tool_input":{},"expected_observation":"root","confidence":1}`),
		json.RawMessage(`{"action":"finish","reason":"done","tool_name":"","tool_input":{},"expected_observation":"outcome","confidence":1,"approved":true}`),
		json.RawMessage(`{"action":"finish","reason":"done","tool_name":"","tool_input":{},"expected_observation":"outcome","confidence":1} {}`),
		json.RawMessage(`{"action":"finish","reason":"done","tool_name":"","tool_input":[],"expected_observation":"outcome","confidence":1}`),
		json.RawMessage(`{"action":"finish","reason":"done","tool_name":"","tool_input":{},"expected_observation":"outcome","confidence":1.1}`),
	}
	for index, raw := range invalid {
		if _, err := codec.Decode(raw); err == nil {
			t.Fatalf("invalid decision %d was accepted: %s", index, raw)
		}
	}
}

// TestAllRequiredNodeTypesAreStable 验证对应场景下的正常路径与失败路径。
func TestAllRequiredNodeTypesAreStable(t *testing.T) {
	want := []orchestration.NodeType{
		orchestration.NodeUnderstandGoal,
		orchestration.NodeAssembleContext,
		orchestration.NodeDecideNextAction,
		orchestration.NodeRetrieveEvidence,
		orchestration.NodeReadDocumentNodes,
		orchestration.NodeAnalyzeEvidence,
		orchestration.NodeGeneratePatch,
		orchestration.NodeValidatePatch,
		orchestration.NodeRequestApproval,
		orchestration.NodeCommitPatch,
		orchestration.NodeRenderOutcome,
	}
	for _, node := range want {
		if !node.Valid() {
			t.Fatalf("required node %q is invalid", node)
		}
	}
}
