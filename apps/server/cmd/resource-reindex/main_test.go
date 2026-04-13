package main

import "testing"

func TestParseReindexModeAcceptsResourceID(t *testing.T) {
	mode, err := parseReindexMode([]string{"--resource-id", "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatalf("parse resource id mode: %v", err)
	}

	if mode.ResourceID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected resource id mode, got %#v", mode)
	}
	if mode.MissingCurrent {
		t.Fatalf("expected missing-current mode to be false")
	}
}

func TestParseReindexModeAcceptsMissingCurrent(t *testing.T) {
	mode, err := parseReindexMode([]string{"--missing-current"})
	if err != nil {
		t.Fatalf("parse missing-current mode: %v", err)
	}

	if !mode.MissingCurrent {
		t.Fatalf("expected missing-current mode, got %#v", mode)
	}
	if mode.ResourceID != "" {
		t.Fatalf("expected empty resource id, got %q", mode.ResourceID)
	}
}

func TestParseReindexModeRequiresOneMode(t *testing.T) {
	if _, err := parseReindexMode(nil); err == nil {
		t.Fatal("expected missing mode to fail")
	}

	if _, err := parseReindexMode([]string{
		"--resource-id", "00000000-0000-0000-0000-000000000001",
		"--missing-current",
	}); err == nil {
		t.Fatal("expected conflicting modes to fail")
	}
}

func TestParseReindexModeRejectsInvalidResourceID(t *testing.T) {
	if _, err := parseReindexMode([]string{"--resource-id", "not-a-uuid"}); err == nil {
		t.Fatal("expected invalid resource id to fail")
	}
}
