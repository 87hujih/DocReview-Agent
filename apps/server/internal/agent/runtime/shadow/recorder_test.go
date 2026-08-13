package shadow

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres/agentrun"
)

// TestRecorderCreatesAllowlistedLegacyTaskAsInertDurableRun 验证对应场景下的正常路径与失败路径。
func TestRecorderCreatesAllowlistedLegacyTaskAsInertDurableRun(t *testing.T) {
	store := &fakeStore{}
	recorder, err := NewRecorder(store, []string{"resource-1"})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	runID, recorded, err := recorder.RecordLegacyTask(context.Background(), "task-1", "resource-1", "improve section two")
	if err != nil || !recorded || runID != "run-shadow-1" {
		t.Fatalf("record legacy task: run=%q recorded=%v err=%v", runID, recorded, err)
	}
	if store.calls != 1 || store.run.RequestID == nil || *store.run.RequestID != "legacy-task:task-1" ||
		store.run.TaskID == nil || *store.run.TaskID != "task-1" || store.step.StepType != "UnderstandGoal" {
		t.Fatalf("unexpected shadow persistence envelope: run=%#v step=%#v", store.run, store.step)
	}
}

// TestRecorderIgnoresTaskOutsideExplicitResourceAllowlist 验证对应场景下的正常路径与失败路径。
func TestRecorderIgnoresTaskOutsideExplicitResourceAllowlist(t *testing.T) {
	store := &fakeStore{}
	recorder, err := NewRecorder(store, []string{"resource-allowed"})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if _, recorded, err := recorder.RecordLegacyTask(context.Background(), "task-2", "resource-other", "do not shadow"); err != nil || recorded || store.calls != 0 {
		t.Fatalf("outside cohort must not persist: recorded=%v calls=%d err=%v", recorded, store.calls, err)
	}
}

type fakeStore struct {
	calls int
	run   agentrun.CreateRunParams
	step  agentrun.CreateStepParams
}

// CreateOrGetRunWithInitialStep 按领域约束持久化数据。
func (s *fakeStore) CreateOrGetRunWithInitialStep(_ context.Context, run agentrun.CreateRunParams, step agentrun.CreateStepParams) (*agentrun.Run, *agentrun.Step, bool, error) {
	s.calls++
	s.run = run
	s.step = step
	return &agentrun.Run{ID: "run-shadow-1"}, &agentrun.Step{ID: "step-shadow-1"}, true, nil
}
