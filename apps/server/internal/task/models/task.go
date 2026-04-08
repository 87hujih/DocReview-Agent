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
	StatusDrafting:         {StatusAwaitingApproval: true, StatusFailed: true},
	StatusAwaitingApproval: {StatusExecuting: true, StatusFailed: true},
	StatusExecuting:        {StatusCompleted: true, StatusFailed: true},
}

func Transition(from string, to string) error {
	targets, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("invalid transition: %s is a terminal state", from)
	}

	if !targets[to] {
		return fmt.Errorf("invalid transition: %s -> %s", from, to)
	}

	return nil
}
