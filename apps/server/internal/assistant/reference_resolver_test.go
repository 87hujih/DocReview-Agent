package assistant

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

func TestSessionContextSnapshotFromRecordIncludesGroundingState(t *testing.T) {
	snapshot, err := SessionContextSnapshotFromRecord(&postgres.SessionContextSnapshotRecord{
		SessionID:                  "session-1",
		ConfirmedConstraintsJSON:   []byte("[]"),
		ActiveSectionID:            stringPointer("section-campushub"),
		ActiveSectionType:          stringPointer("project"),
		ActiveEntityName:           stringPointer("CampusHub"),
		LastCitationWindowsJSON:    []byte(`[{"section_id":"section-campushub","section_type":"project","window_group_id":"project-1"}]`),
		LastEnumeratedEntitiesJSON: []byte(`[{"section_id":"section-campushub","section_type":"project","entity_name":"CampusHub","ordinal":1}]`),
		OrdinalReferenceFrameJSON:  []byte(`[{"ordinal":1,"section_id":"section-campushub","section_type":"project","entity_name":"CampusHub"}]`),
	})
	if err != nil {
		t.Fatalf("snapshot from record: %v", err)
	}

	if snapshot.ActiveSection == nil || snapshot.ActiveSection.ID != "section-campushub" {
		t.Fatalf("expected active section to load, got %#v", snapshot.ActiveSection)
	}
	if snapshot.ActiveEntityName == nil || *snapshot.ActiveEntityName != "CampusHub" {
		t.Fatalf("expected active entity name to load, got %#v", snapshot.ActiveEntityName)
	}
	if len(snapshot.LastEnumeratedEntities) != 1 || snapshot.LastEnumeratedEntities[0].EntityName != "CampusHub" {
		t.Fatalf("expected last enumerated entities to load, got %#v", snapshot.LastEnumeratedEntities)
	}
	if len(snapshot.OrdinalReferenceFrame) != 1 || snapshot.OrdinalReferenceFrame[0].SectionID != "section-campushub" {
		t.Fatalf("expected ordinal frame to load, got %#v", snapshot.OrdinalReferenceFrame)
	}
}

func TestReferenceResolverResolvesFirstProjectFromOrdinalFrame(t *testing.T) {
	resolver := ReferenceResolver{}
	snapshot := &SessionContextSnapshot{
		OrdinalReferenceFrame: []OrdinalReference{
			{Ordinal: 1, SectionID: "section-campushub", SectionType: "project", EntityName: "CampusHub"},
		},
	}

	result := resolver.Resolve("针对第一个项目，给出修改示例", snapshot)
	if result == nil || result.SectionID != "section-campushub" {
		t.Fatalf("expected first project to resolve to CampusHub, got %#v", result)
	}
}

func TestReferenceResolverPrefersExplicitEntityName(t *testing.T) {
	resolver := ReferenceResolver{}
	snapshot := &SessionContextSnapshot{
		LastEnumeratedEntities: []EnumeratedEntity{
			{SectionID: "section-campushub", SectionType: "project", EntityName: "CampusHub", Ordinal: 1},
			{SectionID: "section-attendance", SectionType: "project", EntityName: "智能考勤", Ordinal: 2},
		},
		OrdinalReferenceFrame: []OrdinalReference{
			{Ordinal: 1, SectionID: "section-campushub", SectionType: "project", EntityName: "CampusHub"},
		},
	}

	result := resolver.Resolve("CampusHub 做了什么", snapshot)
	if result == nil || result.SectionID != "section-campushub" {
		t.Fatalf("expected explicit entity name to resolve CampusHub, got %#v", result)
	}
}

func TestReferenceResolverFallsBackToActiveSectionForAnaphora(t *testing.T) {
	resolver := ReferenceResolver{}
	snapshot := &SessionContextSnapshot{
		ActiveSection:    &SnapshotActiveSection{ID: "section-campushub", Type: "project"},
		ActiveEntityName: stringPointer("CampusHub"),
	}

	result := resolver.Resolve("那个项目可以怎么改", snapshot)
	if result == nil || result.SectionID != "section-campushub" {
		t.Fatalf("expected anaphora to resolve active section, got %#v", result)
	}
}

func TestContextLoaderLoadsGroundingState(t *testing.T) {
	loader := NewContextLoader(&fakeSessionContextSnapshotReader{
		record: &postgres.SessionContextSnapshotRecord{
			SessionID:                  "session-1",
			ActiveResourceID:           stringPointer("resource-1"),
			ActiveResourceTitle:        stringPointer("简历"),
			ActiveResourceSourceType:   stringPointer("upload"),
			ConfirmedConstraintsJSON:   []byte("[]"),
			ActiveSectionID:            stringPointer("section-campushub"),
			ActiveSectionType:          stringPointer("project"),
			ActiveEntityName:           stringPointer("CampusHub"),
			LastEnumeratedEntitiesJSON: []byte(`[{"section_id":"section-campushub","section_type":"project","entity_name":"CampusHub","ordinal":1}]`),
			OrdinalReferenceFrameJSON:  []byte(`[{"ordinal":1,"section_id":"section-campushub","section_type":"project","entity_name":"CampusHub"}]`),
		},
	}, &fakeResourceCitationRetriever{})

	replyContext, err := loader.LoadForReply(context.Background(), "session-1", nil, "第一个项目做了什么")
	if err != nil {
		t.Fatalf("load for reply: %v", err)
	}
	if replyContext.Snapshot == nil || replyContext.Snapshot.ActiveSection == nil {
		t.Fatalf("expected grounding state to be loaded into snapshot, got %#v", replyContext.Snapshot)
	}
	if len(replyContext.Snapshot.OrdinalReferenceFrame) != 1 {
		t.Fatalf("expected ordinal frame to be available, got %#v", replyContext.Snapshot.OrdinalReferenceFrame)
	}
}
