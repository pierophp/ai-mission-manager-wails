package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQueueCommandsCheckSkipAndCancelCurrentEntry(t *testing.T) {
	state := DomainState{ImplementationQueues: []ImplementationQueue{{ID: 9, Active: true, PausedReason: &ImplementationQueuePauseReason{Kind: "ticket_still_open"}, Entries: []ImplementationQueueEntry{{Position: 1, TicketNumber: 21}, {Position: 2, TicketNumber: 22}}}}}
	checked, err := ApplyImplementationQueueAction(state, 9, "check")
	if err != nil || checked.ImplementationQueues[0].PausedReason != nil || !checked.ImplementationQueues[0].Active {
		t.Fatalf("check = %#v, %v", checked.ImplementationQueues, err)
	}
	skipped, err := ApplyImplementationQueueAction(state, 9, "skip")
	if err != nil || !skipped.ImplementationQueues[0].Entries[0].Skipped || skipped.ImplementationQueues[0].Entries[1].Skipped {
		t.Fatalf("skip = %#v, %v", skipped.ImplementationQueues, err)
	}
	cancelled, err := ApplyImplementationQueueAction(state, 9, "cancel")
	if err != nil || cancelled.ImplementationQueues[0].Active || cancelled.ImplementationQueues[0].PausedReason != nil {
		t.Fatalf("cancel = %#v, %v", cancelled.ImplementationQueues, err)
	}
	if state.ImplementationQueues[0].Entries[0].Skipped || state.ImplementationQueues[0].PausedReason == nil {
		t.Fatalf("input state mutated: %#v", state.ImplementationQueues[0])
	}
}

func TestCreateImplementationQueueValidatesAndCapturesRustContract(t *testing.T) {
	state := implementationQueueDomainFixture()
	start := ImplementationQueueStart{SpecExternalObjectID: 5, SpecURL: "https://acme.atlassian.net/browse/PROJ-7", Entries: []ImplementationQueueEntry{
		{Position: 1, TicketNumber: 22, TicketTitle: "Second", TicketURL: "https://acme.atlassian.net/browse/PROJ-22", TicketState: "OPEN", RunID: int64Ptr(88), Done: true, Skipped: true},
		{Position: 0, TicketNumber: 21, TicketTitle: "First", TicketURL: "https://github.com/acme/app/issues/21", TicketState: "OPEN"},
	}}
	queue, err := CreateImplementationQueue(state, 42, 3, 4, 6, GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, false, true, start)
	if err != nil {
		t.Fatal(err)
	}
	if queue.ID != 42 || queue.ItemID != 3 || queue.SpecExternalObjectID != 5 || queue.SpecURL != start.SpecURL || !queue.Active || !queue.AllowSharedCheckouts || len(queue.Entries) != 2 {
		t.Fatalf("queue identity/config = %#v", queue)
	}
	if queue.Entries[0].TicketNumber != 21 || queue.Entries[1].TicketNumber != 22 || queue.Entries[1].RunID != nil || queue.Entries[1].Done || queue.Entries[1].Skipped {
		t.Fatalf("queue entries were not sorted/sanitized: %#v", queue.Entries)
	}
	if state.ImplementationQueues != nil || start.Entries[0].RunID == nil {
		t.Fatal("queue creation mutated source state or input")
	}
}

func TestCreateImplementationQueueRejectsInvalidSpecAndTickets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DomainState, *ImplementationQueueStart)
	}{
		{"spec is not linked as spec", func(state *DomainState, _ *ImplementationQueueStart) {
			state.Links[0].Purpose = LinkPurpose("ordinary")
		}},
		{"spec url is not canonical", func(_ *DomainState, start *ImplementationQueueStart) { start.SpecURL += "?x=1" }},
		{"unsupported spec", func(state *DomainState, _ *ImplementationQueueStart) {
			state.ExternalObjects[0].Provider = ProviderAzureDevOps
		}},
		{"tickets are not contiguous", func(_ *DomainState, start *ImplementationQueueStart) { start.Entries[1].Position = 2 }},
		{"ticket is closed", func(_ *DomainState, start *ImplementationQueueStart) { start.Entries[0].TicketState = "CLOSED" }},
		{"ticket is removed", func(_ *DomainState, start *ImplementationQueueStart) { start.Entries[0].TicketState = "REMOVED" }},
		{"ticket urls duplicate", func(_ *DomainState, start *ImplementationQueueStart) {
			start.Entries[1].TicketURL = start.Entries[0].TicketURL
		}},
		{"ticket numbers duplicate", func(_ *DomainState, start *ImplementationQueueStart) {
			start.Entries[1].TicketNumber = start.Entries[0].TicketNumber
		}},
		{"unsupported ticket url", func(_ *DomainState, start *ImplementationQueueStart) {
			start.Entries[0].TicketURL = "https://dev.azure.com/acme/project/_workitems/edit/21"
		}},
		{"active queue exists", func(state *DomainState, _ *ImplementationQueueStart) {
			state.ImplementationQueues = []ImplementationQueue{{ItemID: 3, Active: true}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := implementationQueueDomainFixture()
			start := implementationQueueStartFixture()
			test.mutate(&state, &start)
			if _, err := CreateImplementationQueue(state, 42, 3, 4, 6, GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, false, false, start); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCreateImplementationQueueAcceptsLocalSpecAndTicket(t *testing.T) {
	state := DomainState{ExternalObjects: []ExternalObject{{ID: 5, Provider: ProviderGeneric, Kind: ObjectIssue, ExternalKey: "local:6#spec.md", CanonicalURL: "local:6#spec.md"}}, Links: []Link{{ItemID: 3, ExternalObjectID: 5, Purpose: LinkPurpose("to-spec")}}}
	start := ImplementationQueueStart{SpecExternalObjectID: 5, SpecURL: "local:6#spec.md", Entries: []ImplementationQueueEntry{{Position: 0, TicketNumber: 1, TicketTitle: "Local", TicketURL: "local:6#issues/1.md", TicketState: "OPEN"}}}
	if _, err := CreateImplementationQueue(state, 42, 3, 4, 6, GrillConfiguration{Agent: AgentCodex, Model: "gpt-5", Effort: "high"}, false, false, start); err != nil {
		t.Fatal(err)
	}
}

func TestImplementationQueuePromptSelectsProviderReadAndCompletionInstructions(t *testing.T) {
	for _, test := range []struct{ url, command, completion string }{
		{"https://github.com/acme/app/issues/21", "gh issue view 21 --comments", "Never close the parent spec or any other issue."},
		{"https://acme.atlassian.net/browse/PROJ-21", "twg jira workitem get PROJ-21 --site https://acme.atlassian.net --output json", "mark this ticket complete in its provider"},
		{"local:6#issues/21.md", "cat 'issues/21.md'", "set this ticket's `Status:` line to `Closed`"},
		{"file:///repo/issues/21-my%20ticket.md", "cat '/repo/issues/21-my ticket.md'", "set this ticket's `Status:` line to `Closed`"},
	} {
		prompt := ComposeImplementationQueuePrompt(21, test.url, "https://github.com/acme/app/issues/7")
		if !strings.Contains(prompt, test.command) || !strings.Contains(prompt, test.completion) || !strings.Contains(prompt, "Ticket #21") {
			t.Errorf("prompt for %q is missing contract text: %s", test.url, prompt)
		}
	}
}

func implementationQueueDomainFixture() DomainState {
	return DomainState{ExternalObjects: []ExternalObject{{ID: 5, Provider: ProviderAtlassian, Kind: ObjectIssue, ExternalKey: "jira:acme#PROJ-7", CanonicalURL: "https://acme.atlassian.net/browse/PROJ-7"}}, Links: []Link{{ItemID: 3, ExternalObjectID: 5, Purpose: LinkPurpose("to-spec")}}}
}

func implementationQueueStartFixture() ImplementationQueueStart {
	return ImplementationQueueStart{SpecExternalObjectID: 5, SpecURL: "https://acme.atlassian.net/browse/PROJ-7", Entries: []ImplementationQueueEntry{
		{Position: 0, TicketNumber: 21, TicketTitle: "First", TicketURL: "https://github.com/acme/app/issues/21", TicketState: "OPEN"},
		{Position: 1, TicketNumber: 22, TicketTitle: "Second", TicketURL: "https://acme.atlassian.net/browse/PROJ-22", TicketState: "OPEN"},
	}}
}

func TestImplementationQueueUsesCamelCaseAndAdjacentPauseTag(t *testing.T) {
	queue := ImplementationQueue{ID: 21, ItemID: 4, SpecExternalObjectID: 7, SpecURL: "https://github.com/acme/repo/issues/7", WorkspaceID: 2, RepositoryID: 3, Configuration: GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet", Effort: "high"}, Active: true, Entries: []ImplementationQueueEntry{{Position: 1, TicketNumber: 22, TicketTitle: "One", TicketURL: "https://github.com/acme/repo/issues/22", TicketState: "OPEN", RunID: int64Ptr(21)}}, PausedReason: &ImplementationQueuePauseReason{Kind: "launch_failed", Message: stringPtr("offline")}}
	raw, err := json.Marshal(queue)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, field := range []string{`"itemId"`, `"specExternalObjectId"`, `"workspaceId"`, `"ticketNumber"`, `"ticketUrl"`, `"runId"`, `"pausedReason":{"kind":"launch_failed","message":"offline"}`} {
		if !strings.Contains(text, field) {
			t.Errorf("queue JSON missing %s: %s", field, text)
		}
	}
	if strings.Contains(text, `"item_id"`) || strings.Contains(text, `"ticket_number"`) {
		t.Fatalf("queue JSON is not camelCase: %s", text)
	}
}

func stringPtr(v string) *string { return &v }
