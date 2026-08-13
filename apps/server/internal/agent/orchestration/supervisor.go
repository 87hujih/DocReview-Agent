package orchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	agentcontext "agent_project/apps/server/internal/agent/context"
	agentruntime "agent_project/apps/server/internal/agent/runtime"
	documentvalidation "agent_project/apps/server/internal/document/validation"
)

type StepEnvelope struct {
	State     State           `json:"state"`
	NodeInput json.RawMessage `json:"node_input"`
}

type ModelRequest struct {
	RunID                string
	StepID               string
	TraceID              string
	Node                 NodeType
	State                State
	Input                json.RawMessage
	ContextManifestID    string
	ContextItems         []agentcontext.Item
	ExpectedOutputSchema string
}

type ModelResponse struct {
	Output        json.RawMessage
	Provider      string
	Model         string
	PromptVersion string
	Temperature   *float64
	InputTokens   int64
	OutputTokens  int64
	Cost          float64
	RetryCount    int
	FinishReason  string
}

type ModelGateway interface {
	Invoke(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

type ModelFailure struct {
	Category agentruntime.ErrorCategory
	Message  string
	Cause    error
}

// 错误执行该函数负责的核心处理逻辑。
func (failure *ModelFailure) Error() string {
	if failure == nil {
		return ""
	}
	return failure.Message
}

// Unwrap 执行该函数负责的核心处理逻辑。
func (failure *ModelFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

type ContextRequest struct {
	RunID    string
	StepID   string
	Node     NodeType
	State    State
	RawInput json.RawMessage
}

type ContextSnapshot struct {
	ManifestID string
	Items      []agentcontext.Item
}

type ContextAssembler interface {
	Assemble(ctx context.Context, request ContextRequest) (ContextSnapshot, error)
	Load(ctx context.Context, manifestID string) (ContextSnapshot, error)
}

type ToolRequest struct {
	RunID          string
	StepID         string
	TraceID        string
	ToolName       string
	ToolVersion    string
	Input          json.RawMessage
	IdempotencyKey string
	ApprovalID     string
}

type ToolObservation struct {
	ToolCallID string
	Output     json.RawMessage
}

type ToolFailure struct {
	Category agentruntime.ErrorCategory
	Message  string
	Cause    error
}

// 错误执行该函数负责的核心处理逻辑。
func (failure *ToolFailure) Error() string {
	if failure == nil {
		return ""
	}
	return failure.Message
}

// Unwrap 执行该函数负责的核心处理逻辑。
func (failure *ToolFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

type ToolExecutor interface {
	Execute(ctx context.Context, request ToolRequest) (ToolObservation, error)
}

type SupervisorConfig struct {
	MaxNoProgress        int
	MaxStateObservations int
}

type Supervisor struct {
	cfg       SupervisorConfig
	models    ModelGateway
	contexts  ContextAssembler
	tools     ToolExecutor
	validator *ActionValidator
	codec     DecisionCodec
}

var _ agentruntime.Executor = (*Supervisor)(nil)

// NewSupervisor 校验依赖并创建对应实例。
func NewSupervisor(cfg SupervisorConfig, models ModelGateway, contexts ContextAssembler, tools ToolExecutor, validator *ActionValidator) (*Supervisor, error) {
	if cfg.MaxNoProgress <= 0 || cfg.MaxStateObservations <= 0 {
		return nil, fmt.Errorf("监督器边界必须为正数")
	}
	if models == nil || contexts == nil || tools == nil || validator == nil {
		return nil, fmt.Errorf("监督器模型、上下文、工具、和校验器依赖不能为空")
	}
	return &Supervisor{cfg: cfg, models: models, contexts: contexts, tools: tools, validator: validator}, nil
}

// Execute 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) Execute(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	node := NodeType(strings.TrimSpace(input.StepType))
	if !node.Valid() {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "未知的类型化节点："+input.StepType)
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch node {
	case NodeUnderstandGoal:
		return supervisor.understandGoal(ctx, input)
	case NodeAssembleContext:
		return supervisor.assembleContext(ctx, input)
	case NodeDecideNextAction:
		return supervisor.decideNextAction(ctx, input)
	case NodeRetrieveEvidence:
		return supervisor.executeObservationTool(ctx, input, node, ActionRetrieveEvidence, "retrieval.search")
	case NodeReadDocumentNodes:
		return supervisor.executeObservationTool(ctx, input, node, ActionReadNodes, "document.read_nodes")
	case NodeAnalyzeEvidence:
		return supervisor.analyzeEvidence(ctx, input)
	case NodeGeneratePatch:
		return supervisor.generatePatch(ctx, input)
	case NodeValidatePatch:
		return supervisor.validatePatch(ctx, input)
	case NodeRequestApproval:
		return supervisor.requestApproval(ctx, input)
	case NodeCommitPatch:
		return supervisor.commitPatch(ctx, input)
	case NodeRenderOutcome:
		return supervisor.renderOutcome(ctx, input)
	default:
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "类型化节点尚未实现："+string(node))
	}
}

// commitPatch 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) commitPatch(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 CommitPatch 输入失败："+err.Error())
	}
	state := envelope.State
	if state.Patch == nil || !state.Patch.Valid || strings.TrimSpace(state.Patch.TargetIdempotencyKey) == "" || strings.TrimSpace(state.ApprovalID) == "" {
		return executionFailure(agentruntime.ErrorCategoryPolicyBlocked, "CommitPatch 必须提供已校验补丁、目标幂等键和外部审批")
	}
	if !sameJSONObject(state.Patch.PatchInput, envelope.NodeInput) {
		return executionFailure(agentruntime.ErrorCategoryConflict, "CommitPatch 输入与已审批补丁不一致")
	}
	observation, fullPayload, domainOutput, failure := supervisor.executeBoundTool(
		ctx, input, "patch.commit", "1.0.0", state.Patch.PatchInput, state.Patch.TargetIdempotencyKey, state.ApprovalID,
	)
	if failure != nil {
		return *failure
	}
	var commitOutput struct {
		Commit struct {
			ResourceID string `json:"resource_id"`
			VersionID  string `json:"version_id"`
			OutboxID   string `json:"outbox_id"`
		} `json:"commit"`
	}
	if err := decodeStrict(domainOutput, &commitOutput); err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "patch.commit 结果无效："+err.Error())
	}
	if strings.TrimSpace(commitOutput.Commit.ResourceID) == "" || strings.TrimSpace(commitOutput.Commit.VersionID) == "" || strings.TrimSpace(commitOutput.Commit.OutboxID) == "" {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "patch.commit 结果必须包含资源、版本和发件箱标识")
	}
	state, spec := supervisor.recordToolObservation(state, input, NodeCommitPatch, "commit_patch", observation.ToolCallID, fullPayload)
	state.StopReason = "goal_achieved"
	state.Sequence++
	next, err := nextStep(state, NodeAssembleContext, json.RawMessage(`{}`))
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	output, _ := json.Marshal(map[string]any{"commit": commitOutput.Commit, "tool_call_id": observation.ToolCallID})
	return agentruntime.ExecutionResult{
		Outcome: agentruntime.OutcomeContinue, OutputJSON: output,
		NextSteps: []agentruntime.StepSpec{next}, Observations: []agentruntime.ObservationSpec{spec},
		ContextManifestID: state.ContextManifestID,
	}
}

// renderOutcome 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) renderOutcome(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 RenderOutcome 输入失败："+err.Error())
	}
	response, failure := supervisor.invokeWithManifest(ctx, input, NodeRenderOutcome, envelope, "outcome.v1")
	if failure != nil {
		return *failure
	}
	var output struct {
		Message string `json:"message"`
	}
	if err := decodeStrict(response.Output, &output); err != nil {
		return modelFailure(response, "invalid rendered outcome: "+err.Error())
	}
	output.Message = strings.TrimSpace(output.Message)
	if output.Message == "" {
		return modelFailure(response, "rendered outcome message is required")
	}
	canonical, _ := json.Marshal(output)
	result := modelResult(response)
	result.Outcome = agentruntime.OutcomeSucceed
	result.OutputJSON = canonical
	result.ContextManifestID = envelope.State.ContextManifestID
	return result
}

// validatePatch 校验输入及领域约束。
func (supervisor *Supervisor) validatePatch(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 ValidatePatch 输入失败："+err.Error())
	}
	if envelope.State.Patch == nil || !envelope.State.Patch.Generated || envelope.State.Patch.Valid {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "ValidatePatch 必须提供一个尚未校验的已生成补丁")
	}
	if !sameJSONObject(envelope.State.Patch.PatchInput, envelope.NodeInput) {
		return executionFailure(agentruntime.ErrorCategoryConflict, "ValidatePatch 输入与已持久化的生成补丁不一致")
	}
	observation, fullPayload, domainOutput, failure := supervisor.executeBoundTool(ctx, input, "patch.validate", "1.0.0", envelope.NodeInput, input.IdempotencyKey, "")
	if failure != nil {
		return *failure
	}
	var validation struct {
		Validation documentvalidation.Result `json:"validation"`
	}
	if err := decodeStrict(domainOutput, &validation); err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "patch.validate 结果无效："+err.Error())
	}
	state, spec := supervisor.recordToolObservation(envelope.State, input, NodeValidatePatch, "validate_patch", observation.ToolCallID, fullPayload)
	state.Patch.Valid = validation.Validation.Valid
	if validation.Validation.Valid {
		digest := sha256.Sum256(state.Patch.PatchInput)
		state.Patch.TargetIdempotencyKey = fmt.Sprintf("patch-commit:%s:%x", input.RunID, digest[:12])
		state.LastDecision = &Decision{
			Action: ActionRequestApproval, Reason: "validated patch requires external approval",
			ToolName: "workflow.request_approval", ToolInput: json.RawMessage(`{}`),
			ExpectedObservation: "pending external approval", Confidence: 1,
		}
	}
	state.Sequence++
	nextNode := NodeAssembleContext
	if validation.Validation.Valid {
		nextNode = NodeRequestApproval
	}
	next, err := nextStep(state, nextNode, json.RawMessage(`{}`))
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	output, _ := json.Marshal(map[string]any{"validation": validation.Validation, "tool_call_id": observation.ToolCallID})
	return agentruntime.ExecutionResult{
		Outcome: agentruntime.OutcomeContinue, OutputJSON: output,
		NextSteps: []agentruntime.StepSpec{next}, Observations: []agentruntime.ObservationSpec{spec},
		ContextManifestID: state.ContextManifestID,
	}
}

// requestApproval 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) requestApproval(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 RequestApproval 输入失败："+err.Error())
	}
	state := envelope.State
	if state.Patch == nil || !state.Patch.Valid || strings.TrimSpace(state.Patch.TargetIdempotencyKey) == "" ||
		state.LastDecision == nil || state.LastDecision.Action != ActionRequestApproval {
		return executionFailure(agentruntime.ErrorCategoryPolicyBlocked, "RequestApproval 必须提供已校验补丁和已持久化的审批决策")
	}
	var patch map[string]any
	if err := json.Unmarshal(state.Patch.PatchInput, &patch); err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析已持久化补丁失败："+err.Error())
	}
	resourceID, _ := patch["resource_id"].(string)
	if strings.TrimSpace(resourceID) == "" {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "审批必须提供补丁 resource_id")
	}
	approvalInput, _ := json.Marshal(map[string]any{
		"run_id": input.RunID, "step_id": input.StepID,
		"tool_name": "patch.commit", "tool_version": "1.0.0",
		"idempotency_key": state.Patch.TargetIdempotencyKey,
		"reason":          state.LastDecision.Reason, "payload": patch,
		"resources": []map[string]any{{"type": "document", "id": resourceID, "access": "write"}},
	})
	observation, fullPayload, domainOutput, failure := supervisor.executeBoundTool(ctx, input, "workflow.request_approval", "1.0.0", approvalInput, input.IdempotencyKey, "")
	if failure != nil {
		return *failure
	}
	var approvalOutput struct {
		Approval struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"approval"`
	}
	if err := decodeStrict(domainOutput, &approvalOutput); err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "审批工具结果无效："+err.Error())
	}
	if strings.TrimSpace(approvalOutput.Approval.ID) == "" || approvalOutput.Approval.Status != "pending" {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "审批工具必须返回待处理审批")
	}
	state, spec := supervisor.recordToolObservation(state, input, NodeRequestApproval, string(ActionRequestApproval), observation.ToolCallID, fullPayload)
	state.ApprovalID = approvalOutput.Approval.ID
	state.Sequence++
	continuation := StepEnvelope{State: state, NodeInput: append(json.RawMessage(nil), state.Patch.PatchInput...)}
	output, _ := json.Marshal(ApprovalWaitOutput{
		ApprovalID: state.ApprovalID, Status: approvalOutput.Approval.Status, Continuation: continuation,
	})
	return agentruntime.ExecutionResult{
		Outcome: agentruntime.OutcomeWaitApproval, OutputJSON: output,
		Observations: []agentruntime.ObservationSpec{spec}, ContextManifestID: state.ContextManifestID,
	}
}

// executeBoundTool 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) executeBoundTool(ctx context.Context, input agentruntime.ExecutionInput, toolName, version string, toolInput json.RawMessage, idempotencyKey, approvalID string) (ToolObservation, json.RawMessage, json.RawMessage, *agentruntime.ExecutionResult) {
	observation, err := supervisor.tools.Execute(ctx, ToolRequest{
		RunID: input.RunID, StepID: input.StepID, TraceID: input.TraceID, ToolName: toolName, ToolVersion: version,
		Input: toolInput, IdempotencyKey: idempotencyKey, ApprovalID: approvalID,
	})
	if err != nil {
		failure := toolExecutionFailure(err)
		return ToolObservation{}, nil, nil, &failure
	}
	if strings.TrimSpace(observation.ToolCallID) == "" {
		failure := executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "工具执行器未返回工具调用标识")
		return ToolObservation{}, nil, nil, &failure
	}
	var full map[string]any
	if err := json.Unmarshal(observation.Output, &full); err != nil || full == nil {
		failure := executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "工具结果必须是 JSON 对象")
		return ToolObservation{}, nil, nil, &failure
	}
	fullPayload, _ := json.Marshal(full)
	domainOutput := fullPayload
	if nested, exists := full["output"]; exists {
		encoded, err := json.Marshal(nested)
		if err != nil {
			failure := executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "工具领域输出无效")
			return ToolObservation{}, nil, nil, &failure
		}
		domainOutput = encoded
	}
	return observation, fullPayload, domainOutput, nil
}

// recordToolObservation 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) recordToolObservation(state State, input agentruntime.ExecutionInput, node NodeType, action, toolCallID string, payload json.RawMessage) (State, agentruntime.ObservationSpec) {
	state, spec := supervisor.recordModelObservation(state, input, node, action, payload)
	spec.ToolCallID = toolCallID
	return state, spec
}

// sameJSONObject 执行该函数负责的核心处理逻辑。
func sameJSONObject(left, right json.RawMessage) bool {
	var leftObject, rightObject map[string]any
	if json.Unmarshal(left, &leftObject) != nil || json.Unmarshal(right, &rightObject) != nil || leftObject == nil || rightObject == nil {
		return false
	}
	leftJSON, _ := json.Marshal(leftObject)
	rightJSON, _ := json.Marshal(rightObject)
	return bytes.Equal(leftJSON, rightJSON)
}

// executeObservationTool 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) executeObservationTool(ctx context.Context, input agentruntime.ExecutionInput, node NodeType, action DecisionAction, toolName string) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 "+string(node)+" 输入失败："+err.Error())
	}
	if envelope.State.LastDecision == nil || envelope.State.LastDecision.Action != action || envelope.State.LastDecision.ToolName != toolName {
		return executionFailure(agentruntime.ErrorCategoryPolicyBlocked, "已持久化的校验决策未授权该工具节点")
	}
	toolVersion := strings.TrimSpace(supervisor.validator.toolVersions[action])
	if toolVersion == "" {
		return executionFailure(agentruntime.ErrorCategoryPolicyBlocked, "工具节点没有确定的注册版本")
	}
	persistedVersion := strings.TrimSpace(envelope.State.LastToolVersion)
	if persistedVersion == "" || persistedVersion != toolVersion {
		return executionFailure(agentruntime.ErrorCategoryPolicyBlocked, "已持久化工具版本与注册动作契约不匹配")
	}
	observation, err := supervisor.tools.Execute(ctx, ToolRequest{
		RunID: input.RunID, StepID: input.StepID, TraceID: input.TraceID, ToolName: toolName, ToolVersion: persistedVersion,
		Input: envelope.NodeInput, IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return toolExecutionFailure(err)
	}
	observation.ToolCallID = strings.TrimSpace(observation.ToolCallID)
	if observation.ToolCallID == "" {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "工具执行器未返回工具调用标识")
	}
	var object map[string]any
	if err := json.Unmarshal(observation.Output, &object); err != nil || object == nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "工具观察输出必须是 JSON 对象")
	}
	canonical, _ := json.Marshal(object)
	digest := sha256.Sum256(canonical)
	contentHash := fmt.Sprintf("sha256:%x", digest[:])
	novel := true
	for _, previous := range envelope.State.Observations {
		if previous.ContentHash == contentHash {
			novel = false
			break
		}
	}
	observationKey := input.StepID + ":observation"
	state := envelope.State
	state.Observations = append(state.Observations, ObservationRef{
		ID: observationKey, Kind: string(node), ContentHash: contentHash, Novel: novel,
	})
	if len(state.Observations) > supervisor.cfg.MaxStateObservations {
		state.Observations = append([]ObservationRef(nil), state.Observations[len(state.Observations)-supervisor.cfg.MaxStateObservations:]...)
	}
	if novel {
		state.ConsecutiveNoProgress = 0
	} else {
		state.ConsecutiveNoProgress++
	}
	state.Sequence++
	next, err := nextStep(state, NodeAssembleContext, json.RawMessage(`{}`))
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	observationSpec := agentruntime.ObservationSpec{
		ObservationKey: observationKey, Kind: string(node), Action: string(action),
		ToolCallID: observation.ToolCallID, PayloadJSON: canonical, ContentHash: contentHash, Novel: novel,
	}
	return agentruntime.ExecutionResult{
		Outcome:    agentruntime.OutcomeContinue,
		OutputJSON: canonical, NextSteps: []agentruntime.StepSpec{next}, Observations: []agentruntime.ObservationSpec{observationSpec},
		ContextManifestID: state.ContextManifestID,
	}
}

// analyzeEvidence 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) analyzeEvidence(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 AnalyzeEvidence 输入失败："+err.Error())
	}
	if len(envelope.State.Observations) == 0 {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "AnalyzeEvidence 必须提供已持久化的观察结果")
	}
	response, failure := supervisor.invokeWithManifest(ctx, input, NodeAnalyzeEvidence, envelope, "findings.v1")
	if failure != nil {
		return *failure
	}
	var output struct {
		Findings []Finding `json:"findings"`
	}
	if err := decodeStrict(response.Output, &output); err != nil {
		return modelFailure(response, "invalid findings: "+err.Error())
	}
	if err := validateFindings(output.Findings); err != nil {
		return modelFailure(response, "invalid findings: "+err.Error())
	}
	canonical, _ := json.Marshal(map[string]any{"findings": output.Findings})
	state, observation := supervisor.recordModelObservation(envelope.State, input, NodeAnalyzeEvidence, string(ActionAnalyze), canonical)
	state.Findings = append([]Finding(nil), output.Findings...)
	state.Sequence++
	next, err := nextStep(state, NodeAssembleContext, json.RawMessage(`{}`))
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	result := modelResult(response)
	result.Outcome = agentruntime.OutcomeContinue
	result.OutputJSON = canonical
	result.NextSteps = []agentruntime.StepSpec{next}
	result.Observations = []agentruntime.ObservationSpec{observation}
	result.ContextManifestID = state.ContextManifestID
	return result
}

// generatePatch 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) generatePatch(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 GeneratePatch 输入失败："+err.Error())
	}
	if len(envelope.State.Findings) == 0 {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "GeneratePatch 必须提供类型化发现项")
	}
	response, failure := supervisor.invokeWithManifest(ctx, input, NodeGeneratePatch, envelope, "patch_input.v1")
	if failure != nil {
		return *failure
	}
	var output struct {
		PatchInput json.RawMessage `json:"patch_input"`
	}
	if err := decodeStrict(response.Output, &output); err != nil {
		return modelFailure(response, "invalid patch output: "+err.Error())
	}
	var patchObject map[string]any
	if err := json.Unmarshal(output.PatchInput, &patchObject); err != nil || patchObject == nil {
		return modelFailure(response, "patch_input must be a JSON object")
	}
	patchInput, _ := json.Marshal(patchObject)
	canonical, _ := json.Marshal(map[string]any{"patch_input": json.RawMessage(patchInput)})
	state, observation := supervisor.recordModelObservation(envelope.State, input, NodeGeneratePatch, string(ActionGeneratePatch), canonical)
	state.Patch = &PatchState{Generated: true, Valid: false, PatchInput: patchInput}
	state.Sequence++
	next, err := nextStep(state, NodeValidatePatch, patchInput)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	result := modelResult(response)
	result.Outcome = agentruntime.OutcomeContinue
	result.OutputJSON = canonical
	result.NextSteps = []agentruntime.StepSpec{next}
	result.Observations = []agentruntime.ObservationSpec{observation}
	result.ContextManifestID = state.ContextManifestID
	return result
}

// invokeWithManifest 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) invokeWithManifest(ctx context.Context, input agentruntime.ExecutionInput, node NodeType, envelope StepEnvelope, schema string) (ModelResponse, *agentruntime.ExecutionResult) {
	snapshot, err := supervisor.contexts.Load(ctx, envelope.State.ContextManifestID)
	if err != nil {
		failure := executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "加载模型上下文失败："+err.Error())
		return ModelResponse{}, &failure
	}
	if snapshot.ManifestID != envelope.State.ContextManifestID {
		failure := executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "已加载上下文清单的标识不匹配")
		return ModelResponse{}, &failure
	}
	response, err := supervisor.models.Invoke(ctx, ModelRequest{
		RunID: input.RunID, StepID: input.StepID, TraceID: input.TraceID, Node: node, State: envelope.State,
		Input: envelope.NodeInput, ContextManifestID: snapshot.ManifestID, ContextItems: snapshot.Items,
		ExpectedOutputSchema: schema,
	})
	if err != nil {
		failure := modelExecutionFailure(err, string(node)+" model call failed")
		return ModelResponse{}, &failure
	}
	return response, nil
}

// recordModelObservation 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) recordModelObservation(state State, input agentruntime.ExecutionInput, node NodeType, action string, payload json.RawMessage) (State, agentruntime.ObservationSpec) {
	digest := sha256.Sum256(payload)
	contentHash := fmt.Sprintf("sha256:%x", digest[:])
	novel := true
	for _, prior := range state.Observations {
		if prior.ContentHash == contentHash {
			novel = false
			break
		}
	}
	key := input.StepID + ":observation"
	state.Observations = append(state.Observations, ObservationRef{ID: key, Kind: string(node), ContentHash: contentHash, Novel: novel})
	if len(state.Observations) > supervisor.cfg.MaxStateObservations {
		state.Observations = append([]ObservationRef(nil), state.Observations[len(state.Observations)-supervisor.cfg.MaxStateObservations:]...)
	}
	if novel {
		state.ConsecutiveNoProgress = 0
	} else {
		state.ConsecutiveNoProgress++
	}
	return state, agentruntime.ObservationSpec{
		ObservationKey: key, Kind: string(node), Action: action,
		PayloadJSON: payload, ContentHash: contentHash, Novel: novel,
	}
}

// validateFindings 校验输入及领域约束。
func validateFindings(findings []Finding) error {
	if len(findings) == 0 || len(findings) > 100 {
		return fmt.Errorf("发现项必须包含 1 到 100 项")
	}
	seen := map[string]struct{}{}
	for index, finding := range findings {
		findingID := strings.TrimSpace(finding.FindingID)
		if findingID == "" || strings.TrimSpace(finding.Summary) == "" || len(finding.EvidenceIDs) == 0 ||
			finding.Confidence < 0 || finding.Confidence > 1 {
			return fmt.Errorf("发现项[%d] 无效", index)
		}
		if _, duplicate := seen[findingID]; duplicate {
			return fmt.Errorf("重复的 finding_id %q", findingID)
		}
		seen[findingID] = struct{}{}
		for _, evidenceID := range finding.EvidenceIDs {
			if strings.TrimSpace(evidenceID) == "" {
				return fmt.Errorf("发现项[%d] 具有空白证据 ID", index)
			}
		}
	}
	return nil
}

// toolExecutionFailure 执行该函数负责的核心处理逻辑。
func toolExecutionFailure(err error) agentruntime.ExecutionResult {
	var failure *ToolFailure
	if errors.As(err, &failure) && failure.Category.Valid() && strings.TrimSpace(failure.Message) != "" {
		return executionFailure(failure.Category, failure.Message)
	}
	return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "工具执行失败："+err.Error())
}

// assembleContext 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) assembleContext(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 AssembleContext 输入失败："+err.Error())
	}
	snapshot, err := supervisor.contexts.Assemble(ctx, ContextRequest{
		RunID: input.RunID, StepID: input.StepID, Node: NodeAssembleContext,
		State: envelope.State, RawInput: envelope.NodeInput,
	})
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "组装上下文失败："+err.Error())
	}
	snapshot.ManifestID = strings.TrimSpace(snapshot.ManifestID)
	if snapshot.ManifestID == "" {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "上下文组装器未返回清单标识")
	}
	state := envelope.State
	state.ContextManifestID = snapshot.ManifestID
	state.Sequence++
	next, err := nextStep(state, NodeDecideNextAction, json.RawMessage(`{}`))
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	output, _ := json.Marshal(map[string]any{"context_manifest_id": snapshot.ManifestID})
	return agentruntime.ExecutionResult{
		Outcome: agentruntime.OutcomeContinue, OutputJSON: output, NextSteps: []agentruntime.StepSpec{next},
		ContextManifestID: snapshot.ManifestID,
	}
}

// decideNextAction 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) decideNextAction(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	envelope, err := decodeEnvelope(input.InputJSON)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 DecideNextAction 输入失败："+err.Error())
	}
	state := envelope.State
	if strings.TrimSpace(state.ContextManifestID) == "" {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "DecideNextAction 必须提供上下文清单")
	}
	if strings.TrimSpace(state.StopReason) != "" {
		state.Sequence++
		next, err := nextStep(state, NodeRenderOutcome, json.RawMessage(`{}`))
		if err != nil {
			return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
		}
		output, _ := json.Marshal(map[string]any{"stop_reason": state.StopReason})
		return agentruntime.ExecutionResult{
			Outcome: agentruntime.OutcomeContinue, OutputJSON: output,
			NextSteps: []agentruntime.StepSpec{next}, ContextManifestID: state.ContextManifestID,
		}
	}
	if state.ConsecutiveNoProgress >= supervisor.cfg.MaxNoProgress {
		state.StopReason = "no_new_information"
		state.Sequence++
		next, err := nextStep(state, NodeRenderOutcome, json.RawMessage(`{}`))
		if err != nil {
			return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
		}
		output, _ := json.Marshal(map[string]any{"stop_reason": state.StopReason})
		return agentruntime.ExecutionResult{
			Outcome: agentruntime.OutcomeContinue, OutputJSON: output,
			NextSteps: []agentruntime.StepSpec{next}, ContextManifestID: state.ContextManifestID,
		}
	}
	snapshot, err := supervisor.contexts.Load(ctx, state.ContextManifestID)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "加载决策上下文失败："+err.Error())
	}
	if snapshot.ManifestID != state.ContextManifestID {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "已加载上下文清单的标识不匹配")
	}
	response, err := supervisor.models.Invoke(ctx, ModelRequest{
		RunID: input.RunID, StepID: input.StepID, TraceID: input.TraceID, Node: NodeDecideNextAction,
		State: state, Input: envelope.NodeInput, ContextManifestID: state.ContextManifestID, ContextItems: snapshot.Items,
		ExpectedOutputSchema: "decision.v1",
	})
	if err != nil {
		return modelExecutionFailure(err, "decision model call failed")
	}
	decision, err := supervisor.codec.Decode(response.Output)
	if err != nil {
		return modelFailure(response, "invalid decision: "+err.Error())
	}
	action, err := supervisor.validator.Validate(state, decision)
	if err != nil {
		return modelFailure(response, "action validation failed: "+err.Error())
	}
	state.LastDecision = &decision
	state.LastToolVersion = action.ToolVersion
	result := modelResult(response)
	result.ContextManifestID = state.ContextManifestID
	result.OutputJSON, _ = json.Marshal(map[string]any{"decision": decision})
	if action.WaitForInput {
		result.Outcome = agentruntime.OutcomeWaitInput
		return result
	}
	state.Sequence++
	next, err := nextStep(state, action.NextNode, action.ToolInput)
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	result.Outcome = agentruntime.OutcomeContinue
	result.NextSteps = []agentruntime.StepSpec{next}
	return result
}

// understandGoal 执行该函数负责的核心处理逻辑。
func (supervisor *Supervisor) understandGoal(ctx context.Context, input agentruntime.ExecutionInput) agentruntime.ExecutionResult {
	var initial struct {
		Message      string `json:"message"`
		Objective    string `json:"objective"`
		ResourceID   string `json:"resource_id,omitempty"`
		LegacyTaskID string `json:"legacy_task_id,omitempty"`
	}
	if err := decodeStrict(input.InputJSON, &initial); err != nil {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "解析 UnderstandGoal 输入失败："+err.Error())
	}
	objective := strings.TrimSpace(initial.Message)
	if objective == "" {
		objective = strings.TrimSpace(initial.Objective)
	}
	if objective == "" {
		return executionFailure(agentruntime.ErrorCategoryInvalidInput, "UnderstandGoal 输入必须包含消息或目标")
	}
	snapshot, err := supervisor.contexts.Assemble(ctx, ContextRequest{
		RunID: input.RunID, StepID: input.StepID, Node: NodeUnderstandGoal, RawInput: input.InputJSON,
	})
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "组装 UnderstandGoal 上下文失败："+err.Error())
	}
	snapshot.ManifestID = strings.TrimSpace(snapshot.ManifestID)
	if snapshot.ManifestID == "" {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, "UnderstandGoal 上下文组装器未返回清单标识")
	}
	response, err := supervisor.models.Invoke(ctx, ModelRequest{
		RunID: input.RunID, StepID: input.StepID, TraceID: input.TraceID, Node: NodeUnderstandGoal,
		Input: input.InputJSON, ContextManifestID: snapshot.ManifestID, ContextItems: snapshot.Items,
		ExpectedOutputSchema: "goal_understanding.v1",
	})
	if err != nil {
		return modelExecutionFailure(err, "understand goal model call failed")
	}
	var goal GoalState
	if err := decodeStrict(response.Output, &goal); err != nil {
		return modelFailure(response, "invalid goal understanding: "+err.Error())
	}
	goal.Objective = strings.TrimSpace(goal.Objective)
	goal.ExpectedOutput = strings.TrimSpace(goal.ExpectedOutput)
	if goal.Objective == "" || goal.ExpectedOutput == "" {
		return modelFailure(response, "goal objective and expected_output are required")
	}
	state := State{Goal: &goal, ContextManifestID: snapshot.ManifestID, Sequence: 1}
	next, err := nextStep(state, NodeAssembleContext, json.RawMessage(`{}`))
	if err != nil {
		return executionFailure(agentruntime.ErrorCategoryTerminalUpstream, err.Error())
	}
	result := modelResult(response)
	result.ContextManifestID = snapshot.ManifestID
	result.Outcome = agentruntime.OutcomeContinue
	result.OutputJSON, _ = json.Marshal(map[string]any{"goal": goal})
	result.NextSteps = []agentruntime.StepSpec{next}
	return result
}

// nextStep 执行该函数负责的核心处理逻辑。
func nextStep(state State, node NodeType, nodeInput json.RawMessage) (agentruntime.StepSpec, error) {
	if !node.Valid() {
		return agentruntime.StepSpec{}, fmt.Errorf("无效的下一个节点 %q", node)
	}
	if len(nodeInput) == 0 {
		nodeInput = json.RawMessage(`{}`)
	}
	envelope, err := json.Marshal(StepEnvelope{State: state, NodeInput: nodeInput})
	if err != nil {
		return agentruntime.StepSpec{}, err
	}
	return agentruntime.StepSpec{
		StepKey: fmt.Sprintf("%s:%d", nodeKey(node), state.Sequence), StepType: string(node), InputJSON: envelope, MaxAttempts: 5,
	}, nil
}

// decodeEnvelope 解析输入并返回类型化结果。
func decodeEnvelope(raw json.RawMessage) (StepEnvelope, error) {
	var envelope StepEnvelope
	if err := decodeStrict(raw, &envelope); err != nil {
		return StepEnvelope{}, err
	}
	if envelope.State.Goal == nil || strings.TrimSpace(envelope.State.Goal.Objective) == "" || envelope.State.Sequence < 0 {
		return StepEnvelope{}, fmt.Errorf("步骤封装需要类型化的目标状态和 non-负数序列号")
	}
	if len(envelope.NodeInput) == 0 {
		envelope.NodeInput = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(envelope.NodeInput, &object); err != nil || object == nil {
		return StepEnvelope{}, fmt.Errorf("步骤 node_input 必须是 JSON 对象")
	}
	return envelope, nil
}

// 处理失败： DecodeStepEnvelope applies the same strict schema boundary used 由 the
// 监督器. Persistence adapters use it 之前 resuming 一个 approved 类型化的
// 续接信息 so 数据库内容不能 bypass 节点 validation.
func DecodeStepEnvelope(raw json.RawMessage) (StepEnvelope, error) {
	return decodeEnvelope(raw)
}

// DecodeApprovalWaitOutput 解析输入并返回类型化结果。
func DecodeApprovalWaitOutput(raw json.RawMessage) (ApprovalWaitOutput, error) {
	var output ApprovalWaitOutput
	if err := decodeStrict(raw, &output); err != nil {
		return ApprovalWaitOutput{}, err
	}
	output.ApprovalID = strings.TrimSpace(output.ApprovalID)
	output.Status = strings.TrimSpace(output.Status)
	if output.ApprovalID == "" || output.Status != "pending" {
		return ApprovalWaitOutput{}, fmt.Errorf("审批等待输出需要待处理的审批标识")
	}
	envelopeJSON, err := json.Marshal(output.Continuation)
	if err != nil {
		return ApprovalWaitOutput{}, err
	}
	output.Continuation, err = decodeEnvelope(envelopeJSON)
	if err != nil {
		return ApprovalWaitOutput{}, err
	}
	if output.Continuation.State.ApprovalID != output.ApprovalID {
		return ApprovalWaitOutput{}, fmt.Errorf("审批续接信息标识不匹配")
	}
	return output, nil
}

// nodeKey 执行该函数负责的核心处理逻辑。
func nodeKey(node NodeType) string {
	keys := map[NodeType]string{
		NodeUnderstandGoal: "understand_goal", NodeAssembleContext: "assemble_context",
		NodeDecideNextAction: "decide_next_action", NodeRetrieveEvidence: "retrieve_evidence",
		NodeReadDocumentNodes: "read_document_nodes", NodeAnalyzeEvidence: "analyze_evidence",
		NodeGeneratePatch: "generate_patch", NodeValidatePatch: "validate_patch",
		NodeRequestApproval: "request_approval", NodeCommitPatch: "commit_patch", NodeRenderOutcome: "render_outcome",
	}
	return keys[node]
}

// decodeStrict 解析输入并返回类型化结果。
func decodeStrict(raw json.RawMessage, target any) error {
	if err := validateUniqueJSON(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("JSON 必须且只能包含一个值")
	}
	return nil
}

// executionFailure 执行该函数负责的核心处理逻辑。
func executionFailure(category agentruntime.ErrorCategory, message string) agentruntime.ExecutionResult {
	return agentruntime.ExecutionResult{Err: &agentruntime.ExecutionError{Category: category, Message: message}}
}

// modelExecutionFailure 执行该函数负责的核心处理逻辑。
func modelExecutionFailure(err error, prefix string) agentruntime.ExecutionResult {
	category := agentruntime.ErrorCategoryTerminalUpstream
	var failure *ModelFailure
	if errors.As(err, &failure) && failure.Category.Valid() {
		category = failure.Category
	}
	return executionFailure(category, prefix+": "+err.Error())
}

// modelFailure 执行该函数负责的核心处理逻辑。
func modelFailure(response ModelResponse, message string) agentruntime.ExecutionResult {
	result := modelResult(response)
	result.Err = &agentruntime.ExecutionError{Category: agentruntime.ErrorCategoryInvalidInput, Message: message}
	return result
}

// modelResult 执行该函数负责的核心处理逻辑。
func modelResult(response ModelResponse) agentruntime.ExecutionResult {
	return agentruntime.ExecutionResult{
		Provider: response.Provider, Model: response.Model, PromptVersion: response.PromptVersion,
		Temperature: response.Temperature, RetryCount: response.RetryCount,
		InputTokens: response.InputTokens, OutputTokens: response.OutputTokens,
		Cost: response.Cost, FinishReason: response.FinishReason,
	}
}
