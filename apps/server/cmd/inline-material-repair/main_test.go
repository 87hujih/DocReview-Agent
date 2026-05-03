package main

import "testing"

// TestParseRepairModeDefaultsToDryRun 验证命令默认进入 dry-run，防止误把一次性修复工具做成默认写库。
func TestParseRepairModeDefaultsToDryRun(t *testing.T) {
	mode, err := parseRepairMode(nil)
	if err != nil {
		t.Fatalf("parse default mode: %v", err)
	}

	if mode.Apply {
		t.Fatal("expected default mode to be dry-run")
	}
	if mode.ResourceID != "" {
		t.Fatalf("expected empty resource id, got %q", mode.ResourceID)
	}
}

// TestParseRepairModeAcceptsApply 验证显式 apply 模式能被正确识别。
func TestParseRepairModeAcceptsApply(t *testing.T) {
	mode, err := parseRepairMode([]string{"--apply"})
	if err != nil {
		t.Fatalf("parse apply mode: %v", err)
	}

	if !mode.Apply {
		t.Fatal("expected apply mode")
	}
}

// TestParseRepairModeAcceptsResourceID 验证命令支持按资源收敛修复范围，便于先小范围验证。
func TestParseRepairModeAcceptsResourceID(t *testing.T) {
	mode, err := parseRepairMode([]string{"--resource-id", "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatalf("parse resource filter mode: %v", err)
	}

	if mode.ResourceID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected resource id filter, got %#v", mode)
	}
}

// TestParseRepairModeRejectsInvalidResourceID 验证非法 UUID 会在入口直接被拦下，避免脚本误扫描错误范围。
func TestParseRepairModeRejectsInvalidResourceID(t *testing.T) {
	if _, err := parseRepairMode([]string{"--resource-id", "not-a-uuid"}); err == nil {
		t.Fatal("expected invalid resource id to fail")
	}
}
