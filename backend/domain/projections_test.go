package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHomeViewUsesLocalMinuteAndProjectsAllAttentionSources(t *testing.T) {
	now := "2026-09-30T09:15"
	state := DomainState{
		Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 2, ContextID: 1, Name: "Default"}},
		Items: []Item{{ID: 3, HumanIdentifier: "MC-4", Title: "Reminder", ProjectID: 2, Status: StatusActive, Reminders: []Reminder{{ID: 8, RemindAt: "2026-09-30T09:15"}}}, {ID: 4, HumanIdentifier: "MC-5", Title: "Inbox", ProjectID: 2, Status: StatusInbox, Reminders: []Reminder{}}},
		Runs:  []Run{{ID: 9, ItemID: 3, State: RunBlocked}},
	}
	view := HomeViewFor(state, nil, now)
	if len(view.NeedsAttention) != 2 || len(view.Due) != 1 || len(view.Running) != 1 || len(view.AttentionEntries) != 2 {
		t.Fatalf("home projection = %#v", view)
	}
	if view.AttentionEntries[0].Kind != AttentionReminder || view.AttentionEntries[0].Summary != "Reminder due at 2026-09-30T09:15" || view.AttentionEntries[1].Kind != AttentionBlockedRun {
		t.Fatalf("attention entries = %#v", view.AttentionEntries)
	}
}

func TestAttentionEntriesAggregateUnreviewedActivitiesPerLinkAndHonorLocalWatchMinute(t *testing.T) {
	now := "2026-09-30T09:15"
	state := DomainState{Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 2, ContextID: 1, Name: "Default"}}, Items: []Item{{ID: 3, ProjectID: 2, HumanIdentifier: "MC-4", Title: "x", Reminders: []Reminder{}}},
		ExternalObjects: []ExternalObject{{ID: 5, Provider: ProviderGitHub, Kind: ObjectIssue, CanonicalURL: "https://github.com/o/r/issues/1"}},
		Snapshots:       []ExternalSnapshot{{ExternalObjectID: 5, Title: "Fresh title"}}, Links: []Link{{ID: 7, ItemID: 3, ExternalObjectID: 5, ReviewedActivityID: 10, ReviewAt: &now}},
		Activities: []Activity{{ID: 11, ExternalObjectID: 5, Changes: []ExternalChange{{Kind: "title", Previous: stringPointer("old"), Current: stringPointer("new")}}}, {ID: 12, ExternalObjectID: 5, Changes: []ExternalChange{{Kind: "state", Previous: stringPointer("open"), Current: stringPointer("closed")}}}},
	}
	entries := AttentionEntries(state, nil, now)
	if len(entries) != 2 || len(entries[0].Activities) != 2 || entries[0].SourceTitle != "Fresh title" || entries[0].Summary != "Title changed from old to new; State changed from open to closed" || entries[1].Kind != AttentionReview || entries[1].Summary != "Review scheduled for 2026-09-30T09:15" {
		t.Fatalf("entries = %#v", entries)
	}
	state.Links[0].WatchUntil = &now
	if got := AttentionEntries(state, nil, now); len(got) != 1 || got[0].Kind != AttentionReview {
		t.Fatalf("watch ending at current minute still produced attention: %#v", got)
	}
}

func TestSearchItemsFiltersByContextAndSearchesDomainFields(t *testing.T) {
	state := DomainState{Contexts: []Context{{ID: 1, Name: "Personal"}, {ID: 2, Name: "Work"}}, Projects: []Project{{ID: 3, ContextID: 1, Name: "Default"}, {ID: 4, ContextID: 2, Name: "Research"}}, Items: []Item{{ID: 5, HumanIdentifier: "MC-1", Title: "Write docs", ProjectID: 3, Notes: "alpha", Reminders: []Reminder{}}, {ID: 6, HumanIdentifier: "MC-2", Title: "Code", ProjectID: 4, Notes: "beta", Reminders: []Reminder{}}}}
	id := int64(2)
	got := SearchItems(state, "research", &id)
	if len(got) != 1 || got[0].Item.ID != 6 {
		t.Fatalf("search = %#v", got)
	}
}

func TestAttentionEntriesScopesImplementationQueueAndSerializesItsRustKind(t *testing.T) {
	state := DomainState{
		Contexts:        []Context{{ID: 1, Name: "Personal"}, {ID: 2, Name: "Work"}},
		Projects:        []Project{{ID: 3, ContextID: 1}, {ID: 4, ContextID: 2}},
		Items:           []Item{{ID: 5, ProjectID: 3}, {ID: 6, ProjectID: 4}},
		ExternalObjects: []ExternalObject{{ID: 7, Provider: ProviderGitHub, Kind: ObjectIssue, CanonicalURL: "https://github.com/o/r/issues/7"}, {ID: 8, Provider: ProviderGitHub, Kind: ObjectIssue, CanonicalURL: "https://github.com/o/r/issues/8"}},
		Links:           []Link{{ID: 9, ItemID: 5, ExternalObjectID: 7}, {ID: 10, ItemID: 6, ExternalObjectID: 8}},
		ImplementationQueues: []ImplementationQueue{
			{ID: 11, ItemID: 5, SpecExternalObjectID: 7, Active: true, PausedReason: &ImplementationQueuePauseReason{Kind: "ticket_still_open"}, Entries: []ImplementationQueueEntry{{TicketNumber: 71, TicketTitle: "Personal ticket", TicketURL: "https://github.com/o/r/issues/71"}}},
			{ID: 12, ItemID: 6, SpecExternalObjectID: 8, Active: true, PausedReason: &ImplementationQueuePauseReason{Kind: "ticket_still_open"}, Entries: []ImplementationQueueEntry{{TicketNumber: 81, TicketTitle: "Work ticket", TicketURL: "https://github.com/o/r/issues/81"}}},
		},
	}
	contextID := int64(1)
	entries := AttentionEntries(state, &contextID, "2026-09-30T09:15")
	if len(entries) != 1 || entries[0].Kind != AttentionImplementationQueue || entries[0].QueueID == nil || *entries[0].QueueID != 11 {
		t.Fatalf("scoped queue attention = %#v", entries)
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"kind":"ImplementationQueue"`) || !strings.Contains(string(encoded), `"queue_id":11`) {
		t.Fatalf("ImplementationQueue JSON = %s", encoded)
	}
}

func TestDoneItemWithAttentionEntryStillAppearsInNeedsAttention(t *testing.T) {
	state := DomainState{Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 2, ContextID: 1, Name: "Default"}}, Items: []Item{{ID: 3, ProjectID: 2, HumanIdentifier: "MC-4", Title: "Done but watched", Status: StatusDone, Reminders: []Reminder{}}}, Runs: []Run{{ID: 9, ItemID: 3, State: RunBlocked}}}
	view := HomeViewFor(state, nil, "2026-09-30T09:15")
	if len(view.NeedsAttention) != 1 || len(view.AttentionEntries) != 1 || view.NeedsAttention[0].Item.Status != StatusDone {
		t.Fatalf("done Item home projection = %#v", view)
	}
}

func TestCanonicalRunViewIncludesDetectedDownstreamReferences(t *testing.T) {
	run := CanonicalRunForView(Run{
		ID:               9,
		ExecutionProfile: ExecutionProfileGrill,
		Transcript:       "AI_MISSION_MANAGER_EVENT {\"event\":\"external.object.created\",\"url\":\"https://github.com/acme/app/issues/14\",\"run_id\":9,\"action\":\"to-tickets\"}",
	})
	if len(run.DownstreamIssueCandidates) != 1 || run.DownstreamIssueCandidates[0].URL != "https://github.com/acme/app/issues/14" {
		t.Fatalf("projected downstream references = %#v", run.DownstreamIssueCandidates)
	}
}

func stringPointer(value string) *string { return &value }
