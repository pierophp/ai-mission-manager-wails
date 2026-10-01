package domain

import (
	"strings"
	"testing"
)

func linkEventState() DomainState {
	return DomainState{
		Contexts: []Context{{ID: 1}}, Projects: []Project{{ID: 2, ContextID: 1}}, Items: []Item{{ID: 3, ProjectID: 2}},
		ExternalObjects: []ExternalObject{{ID: 5, Provider: ProviderGitHub, Kind: ObjectIssue, ExternalKey: "issue:o/r#5", CanonicalURL: "https://github.com/o/r/issues/5"}},
		Links:           []Link{{ID: 7, ItemID: 3, ExternalObjectID: 5}},
		Activities:      []Activity{{ID: 10, ExternalObjectID: 5}, {ID: 11, ExternalObjectID: 5}},
	}
}

func TestLinkAttentionMutationsPreserveNullableInheritanceAndReviewProgress(t *testing.T) {
	state := linkEventState()
	title, stateValue, metadata := true, false, true
	policy := LinkAttentionOverrides{TitleAttention: &title, StateAttention: &stateValue, MetadataAttention: &metadata}
	changed, err := Decide(state, Event{Kind: "set_link_attention_policy", LinkID: 7, AttentionOverrides: &policy})
	if err != nil {
		t.Fatal(err)
	}
	if changed.State.Links[0].AttentionPolicy == nil || !changed.State.Links[0].AttentionPolicy.Title || changed.State.Links[0].AttentionPolicy.State {
		t.Fatalf("policy = %#v", changed.State.Links[0])
	}
	inherit, err := Decide(changed.State, Event{Kind: "set_link_attention_policy", LinkID: 7})
	if err != nil || inherit.State.Links[0].AttentionPolicy != nil {
		t.Fatalf("inherited policy = %#v, err %v", inherit, err)
	}
	date := "2026-10-01T09:00"
	changed, err = Decide(inherit.State, Event{Kind: "set_link_review_at", LinkID: 7, Timestamp: date})
	if err != nil || changed.State.Links[0].ReviewAt == nil || *changed.State.Links[0].ReviewAt != date {
		t.Fatalf("review date = %#v, err %v", changed.State.Links, err)
	}
	changed, err = Decide(changed.State, Event{Kind: "mark_link_reviewed", LinkID: 7})
	if err != nil || changed.State.Links[0].ReviewedActivityID != 11 || changed.State.Links[0].ReviewAt != nil {
		t.Fatalf("reviewed Link = %#v, err %v", changed.State.Links, err)
	}
}

func TestSetLinkAttentionPolicyCanInheritOneDimension(t *testing.T) {
	state := linkEventState()
	state.AttentionDefaults = []ContextAttentionDefault{{ContextID: 1, ObjectKind: ObjectIssue, Policy: ExternalChangePolicy{Title: true, State: false, Metadata: true}}}
	metadata := false
	policy := LinkAttentionOverrides{TitleAttention: nil, StateAttention: nil, MetadataAttention: &metadata}
	decision, err := Decide(state, Event{Kind: "set_link_attention_policy", LinkID: 7, AttentionOverrides: &policy})
	if err != nil {
		t.Fatal(err)
	}
	link := decision.State.Links[0]
	if link.TitleAttention != nil || link.StateAttention != nil || link.MetadataAttention == nil || *link.MetadataAttention {
		t.Fatalf("overrides=%+v", link.LinkAttentionOverrides)
	}
	if effective := effectiveAttentionPolicy(decision.State, link, decision.State.ExternalObjects[0]); effective != (ExternalChangePolicy{Title: true, State: false, Metadata: false}) {
		t.Fatalf("effective policy=%+v", effective)
	}
}

func TestLinkMutationPreservesPartiallyInheritedAttentionColumns(t *testing.T) {
	state := linkEventState()
	state.AttentionDefaults = []ContextAttentionDefault{{ContextID: 1, ObjectKind: ObjectIssue, Policy: ExternalChangePolicy{Title: true, State: false, Metadata: true}}}
	state.Links[0].StateAttention = linkEventBoolPointer(false)
	state.Links[0].MetadataAttention = linkEventBoolPointer(true)
	changed, err := Decide(state, Event{Kind: "set_link_watch_until", LinkID: 7, Timestamp: "2026-10-01T09:00"})
	if err != nil {
		t.Fatal(err)
	}
	link := changed.State.Links[0]
	if link.TitleAttention != nil || link.StateAttention == nil || *link.StateAttention || link.MetadataAttention == nil || !*link.MetadataAttention {
		t.Fatalf("nullable attention columns were lost: %#v", link)
	}
	if policy := effectiveAttentionPolicy(changed.State, link, changed.State.ExternalObjects[0]); policy != (ExternalChangePolicy{Title: true, State: false, Metadata: true}) {
		t.Fatalf("effective policy = %+v", policy)
	}
}

func TestMissingContextDefaultInheritsTheUnfilteredPolicy(t *testing.T) {
	state := linkEventState()
	state.ExternalObjects[0].Kind = ObjectDocument
	state.AttentionDefaults = []ContextAttentionDefault{{ContextID: 1, ObjectKind: ObjectIssue, Policy: ExternalChangePolicy{}}}
	if got := effectiveAttentionPolicy(state, state.Links[0], state.ExternalObjects[0]); got != (ExternalChangePolicy{Title: true, State: true, Metadata: true}) {
		t.Fatalf("policy without matching default = %+v", got)
	}
}

func linkEventBoolPointer(value bool) *bool { return &value }

func TestExternalObjectDeletionPreviewFingerprintGuardsStateAndDeletesDependents(t *testing.T) {
	state := linkEventState()
	state.Snapshots = []ExternalSnapshot{{ExternalObjectID: 5, Title: "Issue"}}
	state.Links = append(state.Links, Link{ID: 8, ItemID: 4, ExternalObjectID: 5})
	preview, err := PrepareExternalObjectDeletion(state, 5)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Plan.SnapshotCount != 1 || preview.Plan.ActivityCount != 2 || len(preview.Plan.LinkIDs) != 2 || preview.StateFingerprint == "" {
		t.Fatalf("preview = %#v", preview)
	}
	changed := state
	changed.Activities = append([]Activity{}, state.Activities...)
	changed.Activities[0].ObservedAt++
	if _, err = Decide(changed, Event{Kind: "delete_external_object", ExternalObjectID: 5, Confirmed: true, StateFingerprint: preview.StateFingerprint}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale preview error = %v", err)
	}
	deleted, err := Decide(state, Event{Kind: "delete_external_object", ExternalObjectID: 5, Confirmed: true, StateFingerprint: preview.StateFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.State.ExternalObjects) != 0 || len(deleted.State.Links) != 0 || len(deleted.State.Snapshots) != 0 || len(deleted.State.Activities) != 0 {
		t.Fatalf("dependent data remains: %#v", deleted.State)
	}
}

func TestDeletingSpecClearsSurvivingLinkReferenceAndPreviewsIt(t *testing.T) {
	state := linkEventState()
	state.ExternalObjects = append(state.ExternalObjects, ExternalObject{ID: 6, Provider: ProviderGitHub, Kind: ObjectIssue, ExternalKey: "issue:o/r#6", CanonicalURL: "https://github.com/o/r/issues/6"})
	specID := int64(5)
	state.Links = append(state.Links, Link{ID: 8, ItemID: 3, ExternalObjectID: 6, Purpose: "to-tickets", SpecExternalObjectID: &specID})
	preview, err := PrepareExternalObjectDeletion(state, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Plan.ClearedSpecLinkIDs) != 1 || preview.Plan.ClearedSpecLinkIDs[0] != 8 {
		t.Fatalf("cleared spec links = %#v", preview.Plan.ClearedSpecLinkIDs)
	}
	deleted, err := Decide(state, Event{Kind: "delete_external_object", ExternalObjectID: 5, Confirmed: true, StateFingerprint: preview.StateFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.State.Links) != 1 || deleted.State.Links[0].SpecExternalObjectID != nil {
		t.Fatalf("surviving Link retained deleted Spec: %#v", deleted.State.Links)
	}
}

func TestUnlinkDeletesOrphanedExternalObjectButKeepsSharedObject(t *testing.T) {
	state := linkEventState()
	state.Links = append(state.Links, Link{ID: 8, ItemID: 4, ExternalObjectID: 5})
	shared, err := Decide(state, Event{Kind: "unlink_external_link", LinkID: 7, Confirmed: true})
	if err != nil || len(shared.State.ExternalObjects) != 1 || len(shared.State.Links) != 1 {
		t.Fatalf("shared unlink = %#v, err %v", shared, err)
	}
	solo, err := Decide(linkEventState(), Event{Kind: "unlink_external_link", LinkID: 7, Confirmed: true})
	if err != nil || len(solo.State.ExternalObjects) != 0 || len(solo.State.Activities) != 0 {
		t.Fatalf("orphan unlink = %#v, err %v", solo, err)
	}
}
