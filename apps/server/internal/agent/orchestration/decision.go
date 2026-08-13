// Package orchestration defines the 类型化的、有界的 Agent graph. 模型输出
// 为 data 位于 this boundary; deterministic validators own every transition 和
// 工具 selection.
package orchestration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type NodeType string

const (
	NodeUnderstandGoal    NodeType = "UnderstandGoal"
	NodeAssembleContext   NodeType = "AssembleContext"
	NodeDecideNextAction  NodeType = "DecideNextAction"
	NodeRetrieveEvidence  NodeType = "RetrieveEvidence"
	NodeReadDocumentNodes NodeType = "ReadDocumentNodes"
	NodeAnalyzeEvidence   NodeType = "AnalyzeEvidence"
	NodeGeneratePatch     NodeType = "GeneratePatch"
	NodeValidatePatch     NodeType = "ValidatePatch"
	NodeRequestApproval   NodeType = "RequestApproval"
	NodeCommitPatch       NodeType = "CommitPatch"
	NodeRenderOutcome     NodeType = "RenderOutcome"
)

// Valid 有效的执行该函数负责的核心处理逻辑。
func (node NodeType) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch node {
	case NodeUnderstandGoal, NodeAssembleContext, NodeDecideNextAction,
		NodeRetrieveEvidence, NodeReadDocumentNodes, NodeAnalyzeEvidence,
		NodeGeneratePatch, NodeValidatePatch, NodeRequestApproval,
		NodeCommitPatch, NodeRenderOutcome:
		return true
	default:
		return false
	}
}

type DecisionAction string

const (
	ActionRetrieveEvidence DecisionAction = "retrieve_evidence"
	ActionReadNodes        DecisionAction = "read_nodes"
	ActionAnalyze          DecisionAction = "analyze"
	ActionGeneratePatch    DecisionAction = "generate_patch"
	ActionRequestUserInput DecisionAction = "request_user_input"
	ActionRequestApproval  DecisionAction = "request_approval"
	ActionFinish           DecisionAction = "finish"
)

// Valid 有效的执行该函数负责的核心处理逻辑。
func (action DecisionAction) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch action {
	case ActionRetrieveEvidence, ActionReadNodes, ActionAnalyze, ActionGeneratePatch,
		ActionRequestUserInput, ActionRequestApproval, ActionFinish:
		return true
	default:
		return false
	}
}

type Decision struct {
	Action              DecisionAction  `json:"action"`
	Reason              string          `json:"reason"`
	ToolName            string          `json:"tool_name"`
	ToolInput           json.RawMessage `json:"tool_input"`
	ExpectedObservation string          `json:"expected_observation"`
	Confidence          float64         `json:"confidence"`
}

// DecisionCodec 为 the only decoder 用于模型 planning 输出. It rejects
// unknown fields、重复的 keys、末尾存在多余的值、non-对象工具输入、和
// 值 outside the closed 动作 enum.
type DecisionCodec struct{}

// Decode 解析解析输入并返回类型化结果。
func (DecisionCodec) Decode(raw json.RawMessage) (Decision, error) {
	if err := validateUniqueJSON(raw); err != nil {
		return Decision{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var decision Decision
	if err := decoder.Decode(&decision); err != nil {
		return Decision{}, fmt.Errorf("解析决策：%w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Decision{}, err
	}
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.ToolName = strings.TrimSpace(decision.ToolName)
	decision.ExpectedObservation = strings.TrimSpace(decision.ExpectedObservation)
	if !decision.Action.Valid() || decision.Reason == "" || decision.ExpectedObservation == "" ||
		decision.Confidence < 0 || decision.Confidence > 1 {
		return Decision{}, fmt.Errorf("决策动作、原因、预期的观察结果、或置信度无效")
	}
	var toolInput map[string]any
	if err := json.Unmarshal(decision.ToolInput, &toolInput); err != nil || toolInput == nil {
		return Decision{}, fmt.Errorf("决策 tool_input 必须是 JSON 对象")
	}
	normalized, _ := json.Marshal(toolInput)
	decision.ToolInput = normalized
	return decision, nil
}

// validateUniqueJSON 校验输入及领域约束。
func validateUniqueJSON(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := readUniqueValue(decoder, "$", 0); err != nil {
		return err
	}
	return requireEOF(decoder)
}

// readUniqueValue 执行该函数负责的核心处理逻辑。
func readUniqueValue(decoder *json.Decoder, path string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON 超过最大深度位于 %s", path)
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("解析 JSON 位于 %s：%w", path, err)
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("对象键位于 %s 不是字符串", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("重复的 JSON 键 %q 位于 %s", key, path)
			}
			seen[key] = struct{}{}
			if err := readUniqueValue(decoder, path+"."+key, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("未正确结束的对象位于 %s", path)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := readUniqueValue(decoder, fmt.Sprintf("%s[%d]", path, index), depth+1); err != nil {
				return err
			}
			index++
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("未正确结束的数组位于 %s", path)
		}
	default:
		return fmt.Errorf("非预期的 JSON 分隔符位于 %s", path)
	}
	return nil
}

// requireEOF 执行该函数负责的核心处理逻辑。
func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errorsIsEOF(err) {
		return fmt.Errorf("JSON 必须且只能包含一个值")
	}
	return nil
}

// errorsIsEOF 执行该函数负责的核心处理逻辑。
func errorsIsEOF(err error) bool { return err == io.EOF }
