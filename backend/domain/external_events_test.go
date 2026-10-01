package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecideLinksExternalObjectByCanonicalIdentity(t *testing.T) {
	input, err := ClassifyExternalURL("https://github.com/Acme/App/issues/7?source=copy")
	if err != nil {
		t.Fatal(err)
	}
	state := DomainState{NextExternalObjectID: 3, NextLinkID: 4, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}, Items: []Item{{ID: 2, ProjectID: 1, Title: "Track"}}}
	snapshot := ExternalSnapshot{Title: "Spec", State: "OPEN", Metadata: []ExternalMetadata{{Key: "number", Value: "7"}}, FetchedAt: 100}
	decision, err := Decide(state, Event{Kind: "link_external_object", ItemID: 2, ExternalObject: &input, ExternalSnapshot: &snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ExternalObjects) != 0 || len(state.Links) != 0 {
		t.Fatal("Decide mutated its input")
	}
	if len(decision.State.ExternalObjects) != 1 || decision.State.ExternalObjects[0].ID != 3 || len(decision.State.Links) != 1 || decision.State.Links[0].ID != 4 || len(decision.State.Snapshots) != 1 || decision.State.Snapshots[0].ExternalObjectID != 3 {
		t.Fatalf("linked state %#v", decision.State)
	}
	if len(decision.Effects) != 3 || decision.Effects[0].Kind != "persist_external_object" || decision.Effects[1].Kind != "persist_external_link" || decision.Effects[2].Kind != "persist_external_snapshot" {
		t.Fatalf("effects %#v", decision.Effects)
	}
	duplicate, err := Decide(decision.State, Event{Kind: "link_external_object", ItemID: 2, ExternalObject: &input})
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicate.State.ExternalObjects) != 1 || len(duplicate.State.Links) != 1 || len(duplicate.Effects) != 0 {
		t.Fatalf("duplicate link was not idempotent: %#v", duplicate)
	}
}

func TestDecideRefreshesExternalSnapshotIntoActivityWithExplicitNulls(t *testing.T) {
	state := DomainState{NextActivityID: 9, ExternalObjects: []ExternalObject{{ID: 3, Provider: ProviderGitHub, Kind: ObjectIssue, ExternalKey: "issue:acme/app#7", CanonicalURL: "https://github.com/acme/app/issues/7"}}, Snapshots: []ExternalSnapshot{{ExternalObjectID: 3, Title: "Before", State: "OPEN", Metadata: []ExternalMetadata{{Key: "labels", Value: "ready"}}, FetchedAt: 100}}}
	snapshot := ExternalSnapshot{Title: "After", State: "OPEN", Metadata: []ExternalMetadata{}, FetchedAt: 101}
	decision, err := Decide(state, Event{Kind: "refresh_external_object", ExternalObjectID: 3, ExternalSnapshot: &snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.State.Activities) != 1 || decision.State.Activities[0].ID != 9 || len(decision.State.Activities[0].Changes) != 2 {
		t.Fatalf("Activities %#v", decision.State.Activities)
	}
	raw, err := json.Marshal(decision.State.Activities[0].Changes[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"kind":"title","key":null,"previous":"Before","current":"After"}` {
		t.Fatalf("Activity change JSON = %s", raw)
	}
	metadata, err := json.Marshal(decision.State.Activities[0].Changes[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(metadata) != `{"kind":"metadata","key":"labels","previous":"ready","current":null}` {
		t.Fatalf("metadata change JSON = %s", metadata)
	}
	if len(decision.Effects) != 3 || decision.Effects[0].Kind != "persist_external_activity" || decision.Effects[1].Kind != "persist_external_snapshot" || decision.Effects[2].Kind != "external_object_refreshed" {
		t.Fatalf("effects %#v", decision.Effects)
	}
	if len(state.Activities) != 0 || state.Snapshots[0].Title != "Before" {
		t.Fatal("Decide mutated its input")
	}
}

func TestAzureDevOpsKeyCollisionFailsExplicitly(t *testing.T) {
	urls := []string{
		"https://dev.azure.com/acme/app/_workitems/edit/42",
		"https://dev.azure.com/acme/app/_git/frontend/pullrequest/42",
	}
	state := DomainState{NextExternalObjectID: 1, NextLinkID: 1, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}, Items: []Item{{ID: 1, ProjectID: 1, Title: "Track"}}}
	first, err := ClassifyExternalURL(urls[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := ClassifyExternalURL(urls[1])
	if err != nil {
		t.Fatal(err)
	}
	if first.ExternalKey != second.ExternalKey {
		t.Fatalf("Azure classifier key parity changed: work item %q, pull request %q", first.ExternalKey, second.ExternalKey)
	}
	linked, err := Decide(state, Event{Kind: "link_external_object", ItemID: 1, ExternalObject: &first})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decide(linked.State, Event{Kind: "link_external_object", ItemID: 1, ExternalObject: &second}); err == nil || !strings.Contains(err.Error(), "identity collision") {
		t.Fatalf("colliding Azure identity error = %v", err)
	}
}
