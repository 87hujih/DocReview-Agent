package shadow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres/agentrun"
)

type Store interface {
	CreateOrGetRunWithInitialStep(context.Context, agentrun.CreateRunParams, agentrun.CreateStepParams) (*agentrun.Run, *agentrun.Step, bool, error)
}

// 记录器 expands 一个 explicitly allowlisted 旧版 task into inert 持久化的
// 运行时 facts. It never starts 一个工作进程或 executes the initial 步骤.
type Recorder struct {
	store              Store
	allowedResourceIDs map[string]struct{}
}

// NewRecorder 校验依赖并创建对应实例。
func NewRecorder(store Store, allowedResourceIDs []string) (*Recorder, error) {
	if store == nil {
		return nil, fmt.Errorf("影子运行时存储不能为空")
	}
	allowed := make(map[string]struct{}, len(allowedResourceIDs))
	for _, resourceID := range allowedResourceIDs {
		if resourceID = strings.TrimSpace(resourceID); resourceID != "" {
			allowed[resourceID] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("影子运行时需要一个明确的资源允许列表")
	}
	return &Recorder{store: store, allowedResourceIDs: allowed}, nil
}

// RecordLegacyTask 按领域约束持久化数据。
func (r *Recorder) RecordLegacyTask(ctx context.Context, taskID, resourceID, instruction string) (string, bool, error) {
	taskID = strings.TrimSpace(taskID)
	resourceID = strings.TrimSpace(resourceID)
	instruction = strings.TrimSpace(instruction)
	if taskID == "" || resourceID == "" || instruction == "" {
		return "", false, fmt.Errorf("旧版 task id、资源 id、和指令不能为空")
	}
	if _, allowed := r.allowedResourceIDs[resourceID]; !allowed {
		return "", false, nil
	}

	requestID := "legacy-task:" + taskID
	stateJSON, err := json.Marshal(map[string]any{
		"mode": "shadow", "legacy_task_id": taskID, "resource_id": resourceID,
	})
	if err != nil {
		return "", false, err
	}
	inputJSON, err := json.Marshal(map[string]any{
		"objective": instruction, "legacy_task_id": taskID, "resource_id": resourceID,
	})
	if err != nil {
		return "", false, err
	}
	run, _, _, err := r.store.CreateOrGetRunWithInitialStep(ctx, agentrun.CreateRunParams{
		TaskID: &taskID, RequestID: &requestID, Objective: instruction,
		MaxSteps: 64, MaxToolCalls: 32, StateJSON: stateJSON,
	}, agentrun.CreateStepParams{
		StepKey: "understand_goal:1", StepType: "UnderstandGoal", InputJSON: inputJSON, MaxAttempts: 5,
	})
	if err != nil {
		return "", false, err
	}
	return run.ID, true, nil
}
