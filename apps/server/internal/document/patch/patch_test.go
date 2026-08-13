package patch_test

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/document/patch"
)

// TestParseStrictRejectsDuplicateAndUnknownFields 验证对应场景下的正常路径与失败路径。
func TestParseStrictRejectsDuplicateAndUnknownFields(t *testing.T) {
	limits := patch.DefaultLimits()
	for _, raw := range []string{
		`{"schema_version":"1.0","schema_version":"1.0","resource_id":"r","base_version_id":"v","operations":[],"evidence_refs":[],"reason":"x"}`,
		`{"schema_version":"1.0","resource_id":"r","base_version_id":"v","operations":[],"evidence_refs":[],"reason":"x","surprise":true}`,
	} {
		if _, err := patch.ParseStrict([]byte(raw), limits); err == nil {
			t.Fatalf("expected strict parse rejection for %s", raw)
		}
	}
}

// TestParseStrictEnforcesSizeOperationAndDepthLimits 验证对应场景下的正常路径与失败路径。
func TestParseStrictEnforcesSizeOperationAndDepthLimits(t *testing.T) {
	valid := `{"schema_version":"1.0","resource_id":"r","base_version_id":"v","operations":[{"op":"replace_node","node_id":"n","expected_hash":"sha256:` + strings.Repeat("a", 64) + `","content":"x"}],"evidence_refs":[],"reason":"x"}`
	limits := patch.DefaultLimits()
	limits.MaxBytes = len(valid) - 1
	if _, err := patch.ParseStrict([]byte(valid), limits); err == nil {
		t.Fatal("expected size limit rejection")
	}
	limits = patch.DefaultLimits()
	limits.MaxOperations = 0
	if _, err := patch.ParseStrict([]byte(valid), limits); err == nil {
		t.Fatal("expected operation limit rejection")
	}
	limits = patch.DefaultLimits()
	limits.MaxDepth = 2
	if _, err := patch.ParseStrict([]byte(valid), limits); err == nil {
		t.Fatal("expected depth limit rejection")
	}
}

// TestParseStrictSupportsVersionedNodeOperations 验证对应场景下的正常路径与失败路径。
func TestParseStrictSupportsVersionedNodeOperations(t *testing.T) {
	raw := `{"schema_version":"1.0","resource_id":"r","base_version_id":"v","operations":[` +
		`{"op":"replace_node","node_id":"n1","expected_hash":"sha256:` + strings.Repeat("a", 64) + `","content":"x"},` +
		`{"op":"insert_before","node_id":"n2","expected_hash":"sha256:` + strings.Repeat("b", 64) + `","expected_parent_id":"root","expected_parent_hash":"sha256:` + strings.Repeat("c", 64) + `","node":{"node_id":"new-1","type":"paragraph","attributes":{},"content":"new","children":[],"source_location":{"file_name":"a.md","start_offset":0,"end_offset":0},"page_mapping":[],"metadata":{},"content_hash":""}},` +
		`{"op":"insert_after","node_id":"n2","expected_hash":"sha256:` + strings.Repeat("b", 64) + `","expected_parent_id":"root","expected_parent_hash":"sha256:` + strings.Repeat("c", 64) + `","node":{"node_id":"new-2","type":"paragraph","attributes":{},"content":"new","children":[],"source_location":{"file_name":"a.md","start_offset":0,"end_offset":0},"page_mapping":[],"metadata":{},"content_hash":""}},` +
		`{"op":"delete_node","node_id":"n3","expected_hash":"sha256:` + strings.Repeat("d", 64) + `"},` +
		`{"op":"update_attributes","node_id":"n4","expected_hash":"sha256:` + strings.Repeat("e", 64) + `","attributes":{"level":3}}` +
		`],"evidence_refs":["e1"],"reason":"approved correction"}`
	set, err := patch.ParseStrict([]byte(raw), patch.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Operations) != 5 || set.SchemaVersion != patch.SchemaVersion {
		t.Fatalf("unexpected PatchSet: %#v", set)
	}
}
