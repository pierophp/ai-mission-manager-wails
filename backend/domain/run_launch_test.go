package domain

import (
	"strings"
	"testing"
)

func TestComposeRunPromptKeepsSelectedSourcesAndRequiresCustomPrompt(t *testing.T) {
	state := DomainState{
		Items:           []Item{{ID: 3, Title: "Build the thing"}},
		ExternalObjects: []ExternalObject{{ID: 8, CanonicalURL: "https://github.com/acme/app/issues/8"}},
		Snapshots:       []ExternalSnapshot{{ExternalObjectID: 8, Title: "Issue title"}},
		Links:           []Link{{ID: 9, ItemID: 3, ExternalObjectID: 8}},
	}
	initial := "  keep this detail  "
	got, err := ComposeRunPrompt(state, 3, ExecutionProfileImplement, RunPromptSelection{IncludeObjective: true, ExternalObjectIDs: []int64{8}}, stringPointer("english"), &initial, WorkflowMattPocock)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Respond to the user in English", "Test-first, at pre-agreed seams", "Item objective:\nBuild the thing", "Issue title", "https://github.com/acme/app/issues/8", "User's initial prompt:\nkeep this detail"} {
		if !strings.Contains(got, part) {
			t.Fatalf("prompt missing %q:\n%s", part, got)
		}
	}
	if strings.Contains(got, "disable-model-invocation") {
		t.Fatal("skill discovery metadata was included in prompt")
	}
	if _, err := ComposeRunPrompt(state, 3, ExecutionProfileCustomPrompt, RunPromptSelection{}, nil, nil, WorkflowMattPocock); err == nil {
		t.Fatal("blank custom prompt was accepted")
	}
}

func TestRunLaunchOptionsOnlyAdvertiseSupportedWorkflowAndWorktreeProfiles(t *testing.T) {
	state := DomainState{Contexts: []Context{{ID: 1, DefaultWorkflow: WorkflowPstack, ImplementDefaults: GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, PstackDefaults: GrillConfiguration{Agent: AgentCodex, Model: "gpt-6-sol", Effort: "high"}}}, Projects: []Project{{ID: 2, ContextID: 1}}, Items: []Item{{ID: 3, ProjectID: 2}}}
	got, err := RunLaunchOptionsFor(state, 3, RunTargetWorktree)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultWorkflow != WorkflowMattPocock || len(got.Workflows) != 1 || got.Workflows[0].Workflow != WorkflowMattPocock {
		t.Fatalf("options = %#v", got)
	}
	checkout, err := RunLaunchOptionsFor(state, 3, RunTargetCheckout)
	if err != nil {
		t.Fatal(err)
	}
	if checkout.DefaultWorkflow != WorkflowMattPocock || checkout.Workflows[0].DefaultProfile != ExecutionProfileGrill || !checkout.Workflows[0].Profiles[0].RequiresInitialPrompt {
		t.Fatalf("checkout defaults = %#v", checkout.Workflows[0])
	}
	for _, workflow := range got.Workflows {
		for _, profile := range workflow.Profiles {
			if profile.ExecutionProfile == ExecutionProfileGrill {
				t.Fatal("worktree options included Grill")
			}
		}
	}
}

func TestStartRunDecisionPersistsRealPaneAndAdvancesRunSequence(t *testing.T) {
	workspaceID := int64(4)
	run := Run{ID: 7, ItemID: 3, WorkspaceID: &workspaceID, MachineID: 2, Agent: AgentClaude, ExecutionProfile: ExecutionProfileImplement, Workflow: WorkflowMattPocock, Prompt: "Implement this.", SessionName: "mission-item-3-run-7", PaneID: "%9", State: RunUnknown, PaneStatus: PaneAvailable}
	decision, err := Decide(DomainState{NextRunID: 7, Runs: []Run{}}, Event{Kind: "start_run", Run: &run})
	if err != nil {
		t.Fatal(err)
	}
	if decision.State.NextRunID != 8 || len(decision.State.Runs) != 1 || decision.State.Runs[0].PaneID != "%9" || len(decision.Effects) != 1 || decision.Effects[0].Kind != "persist_run" || decision.Effects[0].Run == nil {
		t.Fatalf("decision = %#v", decision)
	}
	wrongSequence := run
	wrongSequence.ID++
	if _, err := Decide(DomainState{NextRunID: 7}, Event{Kind: "start_run", Run: &wrongSequence}); err == nil {
		t.Fatal("accepted a Run outside the current sequence")
	}
}
