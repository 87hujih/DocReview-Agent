package main

import "testing"

// TestMainWiresAssistantRuntimeEventServiceAndProjector 验证 server main 会显式装配 assistant runtime learning 依赖。
func TestMainWiresAssistantRuntimeEventServiceAndProjector(t *testing.T) {
	deps := buildAssistantRuntimeLearning(nil)
	if deps == nil {
		t.Fatal("expected runtime learning deps to be wired")
	}
	if deps.eventRepo == nil {
		t.Fatal("expected runtime event repo to be wired")
	}
	if deps.sampleRepo == nil {
		t.Fatal("expected runtime sample repo to be wired")
	}
	if deps.eventService == nil {
		t.Fatal("expected runtime event service to be wired")
	}
	if deps.projector == nil {
		t.Fatal("expected runtime learning projector to be wired")
	}
}
