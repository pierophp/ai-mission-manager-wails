package domain

import "testing"

func TestDecideCreatesContextAndDefaultProjectWithoutMutatingInput(t *testing.T) {
	initial := DomainState{NextContextID: 2, NextProjectID: 3, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}}
	decision, err := Decide(initial, Event{Kind: "create_context", Name: "  Research  "})
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Contexts) != 1 || initial.NextContextID != 2 {
		t.Fatalf("Decide mutated its input: %#v", initial)
	}
	if got := decision.State.Contexts[1]; got.ID != 2 || got.Name != "Research" || !got.CheckDirtyCheckouts || got.DefaultWorkflow != WorkflowMattPocock {
		t.Fatalf("Context = %#v", got)
	}
	if got := decision.State.Projects[1]; got.ID != 3 || got.Name != "Default" || got.ContextID != 2 || got.Defaults.ItemStatus != StatusInbox || got.Defaults.ExecutionMode != ExecutionWorktree {
		t.Fatalf("default Project = %#v", got)
	}
	if len(decision.Effects) != 2 || decision.Effects[0].Kind != "persist_context" || decision.Effects[1].Kind != "persist_project" {
		t.Fatalf("effects = %#v", decision.Effects)
	}
}

func TestDecideValidatesContextAndProjectNamesAndOwnership(t *testing.T) {
	state := DomainState{NextContextID: 2, NextProjectID: 2, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}}
	for _, test := range []struct {
		event Event
		want  string
	}{
		{Event{Kind: "create_context", Name: " "}, "a Context name cannot be blank"},
		{Event{Kind: "create_context", Name: "Personal"}, "Context name already exists: Personal"},
		{Event{Kind: "create_project", ContextID: 9, Name: "Research", Defaults: ProjectDefaults{ItemStatus: StatusInbox, ExecutionMode: ExecutionWorktree}}, "Context 9 does not exist"},
		{Event{Kind: "create_project", ContextID: 1, Name: "Default", Defaults: ProjectDefaults{ItemStatus: StatusInbox, ExecutionMode: ExecutionWorktree}}, "Project name already exists in Context 1: Default"},
	} {
		_, err := Decide(state, test.event)
		if err == nil || err.Error() != test.want {
			t.Errorf("Decide(%+v) error = %v, want %q", test.event, err, test.want)
		}
	}
}

func TestDecideUpdatesContextAttentionDefaultAndProjectDefaults(t *testing.T) {
	state := DomainState{NextProjectID: 2, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}, AttentionDefaults: []ContextAttentionDefault{{ContextID: 1, ObjectKind: ObjectIssue, Policy: ExternalChangePolicy{Title: true, State: true, Metadata: true}}}}
	policy := ExternalChangePolicy{Title: true, State: false, Metadata: true}
	decision, err := Decide(state, Event{Kind: "set_context_attention_default", ContextID: 1, ObjectKind: ObjectIssue, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.State.AttentionDefaults) != 1 || decision.State.AttentionDefaults[0].Policy != policy {
		t.Fatalf("attention defaults = %#v", decision.State.AttentionDefaults)
	}
	project, err := Decide(decision.State, Event{Kind: "update_project", ProjectID: 1, Name: "  Archive  ", Defaults: ProjectDefaults{ItemStatus: StatusActive, ExecutionMode: ExecutionDirect}})
	if err != nil {
		t.Fatal(err)
	}
	if got := project.State.Projects[0]; got.Name != "Archive" || got.Defaults.ItemStatus != StatusActive || got.Defaults.ExecutionMode != ExecutionDirect {
		t.Fatalf("Project = %#v", got)
	}
}

func TestDecideCreatesAndUpdatesCompleteContextConfiguration(t *testing.T) {
	initial := DomainState{NextContextID: 2, NextProjectID: 2, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}}
	configuration := NewContextConfiguration()
	configuration.Name = "  Research  "
	configuration.CheckDirtyCheckouts = false
	configuration.DefaultWorkflow = WorkflowPstack
	configuration.AttentionDefaults[0].Policy = ExternalChangePolicy{Title: true, State: false, Metadata: true}
	created, err := Decide(initial, Event{Kind: "create_context_configuration", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.State.Contexts) != 2 || created.State.Contexts[1].Name != "Research" || created.State.Contexts[1].DefaultWorkflow != WorkflowPstack || created.State.Contexts[1].CheckDirtyCheckouts {
		t.Fatalf("configured Context = %#v", created.State.Contexts)
	}
	if len(created.State.Projects) != 2 || created.State.Projects[1].ContextID != 2 || created.State.Projects[1].Name != "Default" || len(created.State.AttentionDefaults) != 3 || len(created.Effects) != 3 {
		t.Fatalf("Context configuration transition = %#v", created)
	}
	updatedConfiguration := NewContextConfiguration()
	updatedConfiguration.Name = " Renamed "
	updatedConfiguration.GrillDefaults = GrillConfiguration{Agent: AgentCodex, Model: "gpt-6-sol", Effort: "high"}
	for i := range updatedConfiguration.AttentionDefaults {
		updatedConfiguration.AttentionDefaults[i].ContextID = 2
	}
	updatedConfiguration.AttentionDefaults[1].Policy = ExternalChangePolicy{Title: false, State: true, Metadata: false}
	updated, err := Decide(created.State, Event{Kind: "update_context_configuration", ContextID: 2, Configuration: updatedConfiguration})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State.Contexts[1].Name != "Renamed" || updated.State.Contexts[1].GrillDefaults.Agent != AgentCodex || updated.State.AttentionDefaults[1].Policy != updatedConfiguration.AttentionDefaults[1].Policy {
		t.Fatalf("updated Context configuration = %#v", updated.State)
	}
}

func TestDecideRejectsInvalidContextConfigurationAtomically(t *testing.T) {
	initial := DomainState{NextContextID: 2, NextProjectID: 2, Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}}
	configuration := NewContextConfiguration()
	configuration.Name = "Research"
	configuration.GrillDefaults.Model = "unsupported-model"
	_, err := Decide(initial, Event{Kind: "create_context_configuration", Configuration: configuration})
	if err == nil || err.Error() != "Grill configuration is invalid for Claude: model unsupported-model, effort high" {
		t.Fatalf("error = %v", err)
	}
	if initial.NextContextID != 2 || len(initial.Contexts) != 1 || len(initial.Projects) != 1 {
		t.Fatalf("failed transition mutated input: %#v", initial)
	}
}

func TestDecideRejectsInvalidAttentionObjectKind(t *testing.T) {
	state := DomainState{Contexts: []Context{{ID: 1, Name: "Personal"}}}
	_, err := Decide(state, Event{Kind: "set_context_attention_default", ContextID: 1, ObjectKind: ExternalObjectKind("unknown")})
	if err == nil || err.Error() != "unknown ExternalObjectKind variant \"unknown\"; expected issue, pull_request, document, or generic" {
		t.Fatalf("error = %v", err)
	}
}

func TestDecideBlocksExecutionMachineChangeWhileContextHasAnActiveRun(t *testing.T) {
	currentMachine, nextMachine := int64(1), int64(2)
	state := DomainState{
		Contexts: []Context{{ID: 1, Name: "Personal", ExecutionMachineID: &currentMachine}},
		Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}},
		Items:    []Item{{ID: 1, ProjectID: 1}},
		Machines: []Machine{{ID: 1}, {ID: 2}},
		Runs:     []Run{{ID: 8, ItemID: 1, State: RunWorking}},
	}
	configuration := NewContextConfiguration()
	configuration.Name = "Personal"
	configuration.ExecutionMachineID = &nextMachine
	for i := range configuration.AttentionDefaults {
		configuration.AttentionDefaults[i].ContextID = 1
	}
	_, err := Decide(state, Event{Kind: "update_context_configuration", ContextID: 1, Configuration: configuration})
	if err == nil || err.Error() != "Context 1 has active Runs: [8]" {
		t.Fatalf("error = %v", err)
	}
}

func TestDecideValidatesConfiguredMachineAndCLIProfile(t *testing.T) {
	state := DomainState{Contexts: []Context{{ID: 1, Name: "Personal"}}, Machines: []Machine{{ID: 1}}}
	configuration := NewContextConfiguration()
	configuration.Name = "Personal"
	for i := range configuration.AttentionDefaults {
		configuration.AttentionDefaults[i].ContextID = 1
	}
	missingMachine := int64(7)
	configuration.ExecutionMachineID = &missingMachine
	_, err := Decide(state, Event{Kind: "update_context_configuration", ContextID: 1, Configuration: configuration})
	if err == nil || err.Error() != "Machine 7 does not exist" {
		t.Fatalf("machine error = %v", err)
	}
	machine := int64(1)
	profile := int64(99)
	configuration.ExecutionMachineID = &machine
	configuration.ClaudeProfileID = &profile
	_, err = Decide(state, Event{Kind: "update_context_configuration", ContextID: 1, Configuration: configuration})
	if err == nil || err.Error() != "CLI configuration profile 99 does not exist" {
		t.Fatalf("profile error = %v", err)
	}
}

func TestDecideRejectsInvalidProjectDefaults(t *testing.T) {
	state := DomainState{NextProjectID: 2, Contexts: []Context{{ID: 1, Name: "Personal"}}}
	_, err := Decide(state, Event{Kind: "create_project", ContextID: 1, Name: "Research", Defaults: ProjectDefaults{ItemStatus: ItemStatus("Started"), ExecutionMode: ExecutionWorktree}})
	if err == nil || err.Error() != `unknown projects.default_item_status value "Started"` {
		t.Fatalf("error = %v", err)
	}
}
