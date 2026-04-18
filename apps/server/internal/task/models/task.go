package models

import "fmt"

const (
	StatusPending          = "pending"
	StatusPlanning         = "planning"
	StatusRetrieving       = "retrieving"
	StatusDrafting         = "drafting"
	StatusAwaitingApproval = "awaiting_approval"
	StatusExecuting        = "executing"
	StatusCompleted        = "completed"
	StatusFailed           = "failed"
)

const (
	StepPlanner   = "planner"
	StepRetriever = "retriever"
	StepReviewer  = "reviewer"
	StepEditor    = "editor"
)

var validTransitions = map[string]map[string]bool{
	StatusPending:          {StatusPlanning: true, StatusFailed: true},
	StatusPlanning:         {StatusRetrieving: true, StatusFailed: true},
	StatusRetrieving:       {StatusDrafting: true, StatusFailed: true},
	StatusDrafting:         {StatusAwaitingApproval: true, StatusFailed: true, StatusCompleted: true}, // StatusCompleted 仅用于无需修改分支
	StatusAwaitingApproval: {StatusExecuting: true, StatusFailed: true},
	StatusExecuting:        {StatusCompleted: true, StatusFailed: true},
}

// Transition 定义任务状态迁移时需要记录的目标状态和事件语义，统一状态机分支的输出约定。
func Transition(from string, to string) error {
	targets, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("非法状态流转：%s 是终态", from)
	}

	if !targets[to] {
		return fmt.Errorf("非法状态流转：%s -> %s", from, to)
	}

	return nil
}
