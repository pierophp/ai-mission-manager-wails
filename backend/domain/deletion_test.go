package domain

import "testing"

func TestDeletionPlansReportActiveRunsAndChangeFingerprint(t *testing.T) {
	state := DomainState{
		Contexts: []Context{{ID: 1, Name: "Personal"}},
		Projects: []Project{{ID: 2, ContextID: 1, Name: "Demo"}},
		Items:    []Item{{ID: 3, HumanIdentifier: "MM-3", Title: "Task", ProjectID: 2}},
		Machines: []Machine{{ID: 4, ContextID: 1, Name: "Local"}},
		Runs:     []Run{{ID: 5, ItemID: 3, MachineID: 4, State: RunWorking}},
	}
	item, err := PlanItemDeletion(state, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.ActiveRunIDs) != 1 || item.ActiveRunIDs[0] != 5 {
		t.Fatalf("active item Runs = %v", item.ActiveRunIDs)
	}
	project, err := PlanProjectDeletion(state, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.ActiveRunIDs) != 1 || project.ActiveRunIDs[0] != 5 {
		t.Fatalf("active project Runs = %v", project.ActiveRunIDs)
	}
	context, err := PlanContextDeletion(state, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(context.ActiveRunIDs) != 1 || context.ActiveRunIDs[0] != 5 {
		t.Fatalf("active context Runs = %v", context.ActiveRunIDs)
	}
	changed := state
	changed.Items = append([]Item(nil), state.Items...)
	changed.Items[0].Title = "Changed after preview"
	updated, err := PlanItemDeletion(changed, 3)
	if err != nil {
		t.Fatal(err)
	}
	if item.StateFingerprint == updated.StateFingerprint {
		t.Fatal("state fingerprint did not change with the Item")
	}
}

func TestResetPlanCountsRecordsWithoutChangingState(t *testing.T) {
	state := DomainState{Contexts: []Context{{ID: 1, Name: "Personal"}}, Projects: []Project{{ID: 2, ContextID: 1, Name: "Demo"}}}
	before := stateFingerprint(state)
	plan := PlanResetLocalData(state)
	if plan.Summary.ContextCount != 1 || plan.Summary.ProjectCount != 1 || len(plan.AffectedRecords) != 2 {
		t.Fatalf("reset plan = %#v", plan)
	}
	if stateFingerprint(state) != before {
		t.Fatal("planning reset mutated DomainState")
	}
}
