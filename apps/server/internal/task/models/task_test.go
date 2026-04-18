package models

import "testing"

// TestValidTransitions 验证`validTransitions`在特定边界条件下的行为，防止同类回归。
func TestValidTransitions(t *testing.T) {
	testCases := []struct {
		name string
		from string
		to   string
	}{
		{name: "pending to planning", from: StatusPending, to: StatusPlanning},
		{name: "planning to retrieving", from: StatusPlanning, to: StatusRetrieving},
		{name: "retrieving to drafting", from: StatusRetrieving, to: StatusDrafting},
		{name: "drafting to awaiting approval", from: StatusDrafting, to: StatusAwaitingApproval},
		{name: "awaiting approval to executing", from: StatusAwaitingApproval, to: StatusExecuting},
		{name: "executing to completed", from: StatusExecuting, to: StatusCompleted},
		{name: "planning to failed", from: StatusPlanning, to: StatusFailed},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := Transition(testCase.from, testCase.to); err != nil {
				t.Fatalf("expected transition %s -> %s to be valid: %v", testCase.from, testCase.to, err)
			}
		})
	}
}

// TestInvalidTransitions 验证`invalidTransitions`在特定边界条件下的行为，防止同类回归。
func TestInvalidTransitions(t *testing.T) {
	testCases := []struct {
		name string
		from string
		to   string
	}{
		{name: "pending to retrieving", from: StatusPending, to: StatusRetrieving},
		{name: "completed to pending", from: StatusCompleted, to: StatusPending},
		{name: "failed to pending", from: StatusFailed, to: StatusPending},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := Transition(testCase.from, testCase.to); err == nil {
				t.Fatalf("expected transition %s -> %s to be invalid", testCase.from, testCase.to)
			}
		})
	}
}
