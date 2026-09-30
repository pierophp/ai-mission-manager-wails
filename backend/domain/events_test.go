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

func TestDecideRegistersAndUpdatesRepositoryAndMachineLocation(t *testing.T) {
	state := DomainState{
		NextRepositoryID: 4,
		Projects:         []Project{{ID: 2, ContextID: 1}},
		Machines:         []Machine{{ID: 3, ContextID: 1}},
	}
	registered, err := Decide(state, Event{Kind: "register_repository_at_location", ProjectID: 2, Name: " app ", RemoteURL: " git@example.com:team/app.git ", BaseBranch: " trunk ", MachineID: 3, CheckoutPath: "/src/app", WorktreeRoot: "/src/worktrees"})
	if err != nil {
		t.Fatal(err)
	}
	if got := registered.State.Repositories[0]; got.ID != 4 || got.Name != "app" || got.BaseBranch != "trunk" {
		t.Fatalf("Repository = %#v", got)
	}
	if got := registered.State.RepositoryLocations[0]; got.RepositoryID != 4 || got.MachineID != 3 || got.CheckoutPath != "/src/app" {
		t.Fatalf("Repository location = %#v", got)
	}
	if registered.State.NextRepositoryID != 5 || len(registered.Effects) != 1 || registered.Effects[0].Kind != "persist_repository_at_location" {
		t.Fatalf("registration transition = %#v", registered)
	}
	updated, err := Decide(registered.State, Event{Kind: "update_repository_location", RepositoryID: 4, PreviousMachineID: int64Ptr(3), MachineID: 3, CheckoutPath: "/src/new-app", WorktreeRoot: "/src/new-worktrees"})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.State.RepositoryLocations[0]; got.CheckoutPath != "/src/new-app" || got.WorktreeRoot != "/src/new-worktrees" {
		t.Fatalf("updated location = %#v", got)
	}
}

func TestDecideEnsuresProjectWorkspacesFromRepositoriesIdempotently(t *testing.T) {
	state := DomainState{
		NextWorkspaceID: 2,
		Items:           []Item{{ID: 1, ProjectID: 7, HumanIdentifier: "MC-4"}},
		Repositories:    []Repository{{ID: 2, ProjectID: 7, BaseBranch: "trunk"}},
		Workspaces:      []Workspace{{ID: 1, ItemID: 1, PreparationState: WorkspaceReady, Repositories: []WorkspaceRepository{{RepositoryID: 2, Branch: "custom-branch", BaseBranch: "main"}}}},
	}
	decision, err := Decide(state, Event{Kind: "ensure_project_workspaces"})
	if err != nil {
		t.Fatal(err)
	}
	if got := decision.State.Workspaces[0]; got.PreparationState != WorkspaceReady || len(got.Repositories) != 1 || got.Repositories[0] != (WorkspaceRepository{RepositoryID: 2, Branch: "custom-branch", BaseBranch: "trunk"}) {
		t.Fatalf("reconciled Workspace = %#v", got)
	}
	if len(decision.Effects) != 1 || decision.Effects[0].Kind != "update_workspace_repositories" {
		t.Fatalf("effects = %#v", decision.Effects)
	}
	created, err := Decide(DomainState{NextWorkspaceID: 4, Items: state.Items, Repositories: state.Repositories}, Event{Kind: "ensure_project_workspaces"})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.State.Workspaces[0]; got.ID != 4 || got.PreparationState != WorkspacePending || got.Repositories[0].Branch != "mission-MC-4" || created.State.NextWorkspaceID != 5 {
		t.Fatalf("new Workspace = %#v", created.State)
	}
	again, err := Decide(created.State, Event{Kind: "ensure_project_workspaces"})
	if err != nil || len(again.Effects) != 0 {
		t.Fatalf("second ensure effects = %#v, err=%v", again.Effects, err)
	}
}

func int64Ptr(value int64) *int64 { return &value }

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

func TestDecideManagesMachinesAndMachineOwnedProviderProfiles(t *testing.T) {
	host := "build.example.com"
	state := DomainState{NextMachineID: 1, NextCLIProfileID: 1, Contexts: []Context{{ID: 1, Name: "Personal"}, {ID: 2, Name: "Shared"}}}
	transport := MachineTransport{Kind: TransportSSH, Host: &host}
	registered, err := Decide(state, Event{Kind: "register_machine", ContextID: 1, Name: "Build", SocketName: "mission", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	machine := registered.State.Machines[0]
	if machine.ID != 1 || machine.LastObserved != "unknown" || registered.State.NextMachineID != 2 {
		t.Fatalf("Machine = %#v", machine)
	}
	if _, err := Decide(registered.State, Event{Kind: "register_machine", ContextID: 1, Name: " Build ", SocketName: "other", Transport: transport}); err == nil {
		t.Fatal("expected duplicate Machine name to fail")
	}
	profile, err := Decide(registered.State, Event{Kind: "create_cli_configuration_profile", MachineID: 1, Provider: AgentClaude, ProfileName: "Personal", ProfileDirectory: "~/profiles/claude/1", AppManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	profileID := profile.State.CLIConfigurationProfiles[0].ID
	selectedMachine := int64(1)
	selected, err := Decide(profile.State, Event{Kind: "set_context_execution_machine", ContextID: 2, ExecutionMachineID: &selectedMachine})
	if err != nil {
		t.Fatal(err)
	}
	selected, err = Decide(selected.State, Event{Kind: "set_context_cli_configuration_profile", ContextID: 2, Provider: AgentClaude, CLIProfileID: &profileID})
	if err != nil {
		t.Fatal(err)
	}
	if selected.State.Contexts[1].ClaudeProfileID == nil || *selected.State.Contexts[1].ClaudeProfileID != profileID {
		t.Fatalf("Context profile = %#v", selected.State.Contexts[1])
	}
	if _, err := Decide(selected.State, Event{Kind: "delete_cli_configuration_profile", ProfileID: profileID}); err == nil {
		t.Fatal("expected selected profile deletion to fail")
	}
	otherMachine := int64(42)
	if _, err := Decide(selected.State, Event{Kind: "set_context_execution_machine", ContextID: 2, ExecutionMachineID: &otherMachine}); err == nil {
		t.Fatal("expected profile ownership to block Machine change")
	}
}

func TestDecideRecordsMachineObservationAsAnEffect(t *testing.T) {
	initial := DomainState{Machines: []Machine{{ID: 7, Name: "Runner", LastObserved: "unknown"}}}
	decision, err := Decide(initial, Event{Kind: "observe_machine", MachineID: 7, MachineObservation: "available", ObservedAt: 123})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Machines[0].LastObserved != "unknown" || initial.Machines[0].LastObservedAt != nil {
		t.Fatalf("Decide mutated original Machine: %#v", initial.Machines[0])
	}
	got := decision.State.Machines[0]
	if got.LastObserved != "available" || got.LastObservedAt == nil || *got.LastObservedAt != 123 {
		t.Fatalf("observed Machine = %#v", got)
	}
	if len(decision.Effects) != 1 || decision.Effects[0].Kind != "observe_machine" || decision.Effects[0].Machine == nil {
		t.Fatalf("observation effects = %#v", decision.Effects)
	}
	if _, err := Decide(decision.State, Event{Kind: "observe_machine", MachineID: 7, MachineObservation: "unknown"}); err == nil {
		t.Fatal("expected invalid observation to fail")
	}
}

func TestDecideDoesNotBlockSelectingTheSameMachineDuringActiveRun(t *testing.T) {
	current, next := int64(1), int64(1)
	state := DomainState{Contexts: []Context{{ID: 1, ExecutionMachineID: &current}}, Projects: []Project{{ID: 1, ContextID: 1}}, Items: []Item{{ID: 1, ProjectID: 1}}, Machines: []Machine{{ID: 1}}, Runs: []Run{{ID: 2, ItemID: 1, State: RunWorking}}}
	if _, err := Decide(state, Event{Kind: "set_context_execution_machine", ContextID: 1, ExecutionMachineID: &next}); err != nil {
		t.Fatalf("same Machine selection should be allowed: %v", err)
	}
}

func TestDecideRejectsInvalidProjectDefaults(t *testing.T) {
	state := DomainState{NextProjectID: 2, Contexts: []Context{{ID: 1, Name: "Personal"}}}
	_, err := Decide(state, Event{Kind: "create_project", ContextID: 1, Name: "Research", Defaults: ProjectDefaults{ItemStatus: ItemStatus("Started"), ExecutionMode: ExecutionWorktree}})
	if err == nil || err.Error() != `unknown projects.default_item_status value "Started"` {
		t.Fatalf("error = %v", err)
	}
}

func TestDecideCreatesAndEditsItemsWithLocalMinuteRemindersAndRelations(t *testing.T) {
	state := DomainState{NextItemID: 7, NextItemNumber: 13, NextReminderID: 3,
		Contexts: []Context{{ID: 1}}, Projects: []Project{{ID: 2, ContextID: 1, Name: "Default", Defaults: ProjectDefaults{ItemStatus: StatusWaiting, ExecutionMode: ExecutionDirect}}},
		Items: []Item{{ID: 1, HumanIdentifier: "MC-12", ProjectID: 2, Status: StatusInbox, Reminders: []Reminder{}}}}
	created, err := Decide(state, Event{Kind: "create_item", Name: " New work ", ContextID: 1, ProjectID: 2, Notes: " details "})
	if err != nil {
		t.Fatal(err)
	}
	item := created.State.Items[1]
	if item.ID != 7 || item.HumanIdentifier != "MC-13" || item.Title != "New work" || item.Notes != " details " || item.Status != StatusWaiting || created.State.NextItemNumber != 14 {
		t.Fatalf("created Item = %#v, state=%#v", item, created.State)
	}
	updated, err := Decide(created.State, Event{Kind: "set_item_status", ItemID: 7, Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	updated, err = Decide(updated.State, Event{Kind: "set_item_title", ItemID: 7, Name: "  Revised  "})
	if err != nil {
		t.Fatal(err)
	}
	updated, err = Decide(updated.State, Event{Kind: "set_item_notes", ItemID: 7, Notes: " new notes "})
	if err != nil {
		t.Fatal(err)
	}
	updated, err = Decide(updated.State, Event{Kind: "add_item_reminder", ItemID: 7, RemindAt: "2026-09-30T09:15"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State.Items[1].Reminders[0] != (Reminder{ID: 3, RemindAt: "2026-09-30T09:15"}) {
		t.Fatalf("reminders = %#v", updated.State.Items[1].Reminders)
	}
	relation, err := Decide(updated.State, Event{Kind: "set_item_relation", FromItemID: 1, ToItemID: 7, RelationKind: RelationBlocks})
	if err != nil {
		t.Fatal(err)
	}
	if len(relation.State.Relationships) != 1 || relation.State.Relationships[0] != (ItemRelation{FromItemID: 1, ToItemID: 7, Kind: RelationBlocks}) {
		t.Fatalf("relationships = %#v", relation.State.Relationships)
	}
}

func TestDecideRejectsInvalidItemChangesWithoutChangingSequences(t *testing.T) {
	state := DomainState{NextItemID: 1, NextItemNumber: 1, NextReminderID: 1, Contexts: []Context{{ID: 1}}, Projects: []Project{{ID: 1, ContextID: 1, Name: "Default"}}}
	for _, event := range []Event{{Kind: "create_item", Name: " ", ContextID: 1, ProjectID: 1}, {Kind: "add_item_reminder", ItemID: 44, RemindAt: "2026-09-30T09:15"}, {Kind: "set_item_relation", FromItemID: 1, ToItemID: 1, RelationKind: RelationBlocks}} {
		if _, err := Decide(state, event); err == nil {
			t.Errorf("Decide(%+v) succeeded", event)
		}
	}
	if state.NextItemID != 1 || state.NextItemNumber != 1 || state.NextReminderID != 1 {
		t.Fatalf("input mutated: %#v", state)
	}
}
