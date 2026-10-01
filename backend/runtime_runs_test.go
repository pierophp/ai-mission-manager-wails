package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func TestParseAgentStateRecordValidatesIdentityStateAndSequence(t *testing.T) {
	sequence := int64(3)
	got, err := parseAgentStateRecord(`{"agent":"claude","runId":"9","state":"blocked","updatedAt":"123","sequence":3}`)
	if err != nil || got.Agent != domain.AgentClaude || got.State != domain.RunBlocked || got.RunID != "9" || got.Sequence == nil || *got.Sequence != sequence {
		t.Fatalf("record=%#v err=%v", got, err)
	}
	for _, raw := range []string{`{"agent":"other","runId":"9","state":"blocked","updatedAt":"1"}`, `{"agent":"claude","runId":"0","state":"blocked","updatedAt":"1"}`, `{"agent":"claude","runId":"9","state":"unknown","updatedAt":"1"}`, `{"agent":"claude","runId":"9","state":"working","updatedAt":"1","sequence":-1}`} {
		if _, err := parseAgentStateRecord(raw); err == nil {
			t.Errorf("accepted invalid agent record %s", raw)
		}
	}
}

func TestAgentStateSequenceRejectsStaleRecordsAndEmitsChangedState(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	sequence := int64(4)
	runtime.mu.Lock()
	runtime.state.Runs = []domain.Run{{ID: 9, Agent: domain.AgentClaude, State: domain.RunWorking, LastAppliedAgentStateSequence: &sequence}}
	runtime.mu.Unlock()
	var events []RunStateChangedEvent
	runtime.SetEventEmitter(EventEmitterFunc(func(name string, payload any) {
		if name == "run-state-changed" {
			var event RunStateChangedEvent
			data, _ := json.Marshal(payload)
			_ = json.Unmarshal(data, &event)
			events = append(events, event)
		}
	}))
	stale := AgentStateRecord{Agent: domain.AgentClaude, RunID: "9", State: domain.RunBlocked, UpdatedAt: "123", Sequence: ptrInt64(4)}
	if accepted, _, err := runtime.applyAgentStateRecord(9, stale); err != nil || accepted {
		t.Fatalf("stale accepted=%v err=%v", accepted, err)
	}
	newer := stale
	newer.Sequence = ptrInt64(5)
	if accepted, changed, err := runtime.applyAgentStateRecord(9, newer); err != nil || !accepted || !changed {
		t.Fatalf("newer accepted=%v changed=%v err=%v", accepted, changed, err)
	}
	if got := runtime.runCopy(9); got.State != domain.RunBlocked || got.LastAppliedAgentStateSequence == nil || *got.LastAppliedAgentStateSequence != 5 {
		t.Fatalf("Run after apply=%#v", got)
	}
	if len(events) != 1 || events[0].RunID != 9 || events[0].State != domain.RunBlocked {
		t.Fatalf("events=%#v", events)
	}
}

func TestReconciliationDiscardsObservationFromOlderGeneration(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "generation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.mu.Lock()
	runtime.state.Runs = []domain.Run{{ID: 9, Agent: domain.AgentClaude, State: domain.RunWorking}}
	runtime.runObservationGeneration[9] = 3
	runtime.mu.Unlock()
	stale := uint64(2)
	record := AgentStateRecord{Agent: domain.AgentClaude, RunID: "9", State: domain.RunFinished, UpdatedAt: "123", Sequence: ptrInt64(1)}
	accepted, changed, err := runtime.applyAgentStateObservation(9, record, &stale)
	if err != nil || accepted || changed {
		t.Fatalf("stale observation accepted=%v changed=%v err=%v", accepted, changed, err)
	}
	if got := runtime.runCopy(9); got.State != domain.RunWorking {
		t.Fatalf("stale observation changed Run state: %#v", got)
	}
}

func TestStartupRecoveryReadsLegacyRunStateFileWithoutChangingItsLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyDirectory := filepath.Join(home, ".ai-mission-manager", "agent-state")
	if err := os.MkdirAll(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDirectory, "run-9.json")
	contents := `{"agent":"codex","runId":"9","state":"finished","updatedAt":"1780000000","sequence":7}`
	if err := os.WriteFile(legacyPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.mu.Lock()
	runtime.state.Runs = []domain.Run{{ID: 9, Agent: domain.AgentCodex, State: domain.RunWorking}}
	runtime.mu.Unlock()
	if err := runtime.recoverLegacyRunStates(); err != nil {
		t.Fatal(err)
	}
	run := runtime.runCopy(9)
	if run.State != domain.RunFinished || run.LastAppliedAgentStateSequence == nil || *run.LastAppliedAgentStateSequence != 7 {
		t.Fatalf("recovered Run=%#v", run)
	}
	if got, err := os.ReadFile(legacyPath); err != nil || string(got) != contents {
		t.Fatalf("legacy state file changed during recovery: %q err=%v", got, err)
	}
}

func TestReconcileRunsAppliesNewestPaneStateAndReportsPaneStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	access := NewFakeMachineAccess()
	list := "tmux -f /dev/null -L 'mission' list-panes -a -F '#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}\t#{@ai_mission_manager_run_state}'"
	access.ShellResults[list] = MachineAccessResult{Value: "run-9\t%4\tclaude\tterminal\t/home/fake/src/app\t{\"agent\":\"claude\",\"runId\":\"9\",\"state\":\"blocked\",\"updatedAt\":\"123\",\"sequence\":2}\n"}
	access.ShellResults["cat -- '/home/fake/.local/state/ai-mission-manager/runs/run-9.json' 2>/dev/null || true"] = MachineAccessResult{Value: `{"agent":"claude","runId":"9","state":"working","updatedAt":"123","sequence":1}`}
	capture := "tmux -f /dev/null -L 'mission' capture-pane -p -J -S - -t '%4'"
	access.ShellResults[capture] = MachineAccessResult{Value: "AI_MISSION_MANAGER_EVENT {\"event\":\"pull_request.opened\",\"url\":\"https://github.com/acme/app/pull/7\"}\n"}
	databasePath := filepath.Join(t.TempDir(), "runs.sqlite")
	seedPersistedRun(t, databasePath)
	runtime, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.mu.Lock()
	runtime.machineAccess = access
	runtime.mu.Unlock()
	result, err := runtime.reconcileRuns()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("expected reconciliation to report a change")
	}
	run := runtime.runCopy(9)
	if run.State != domain.RunBlocked || run.PaneStatus != domain.PaneAvailable || run.LastAppliedAgentStateSequence == nil || *run.LastAppliedAgentStateSequence != 2 {
		t.Fatalf("Run=%#v", run)
	}
	if len(run.ReportedPullRequests) != 1 || run.ReportedPullRequests[0] != "https://github.com/acme/app/pull/7" {
		t.Fatalf("reported PRs=%#v", run.ReportedPullRequests)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted := reopened.runCopy(9)
	if persisted.State != domain.RunBlocked || persisted.PaneStatus != domain.PaneAvailable || persisted.LastAppliedAgentStateSequence == nil || *persisted.LastAppliedAgentStateSequence != 2 || len(persisted.ReportedPullRequests) != 1 {
		t.Fatalf("persisted Run after reopen=%#v", persisted)
	}
}

func seedPersistedRun(t *testing.T, path string) {
	t.Helper()
	store, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	effects := []persistence.Effect{
		{SQL: `INSERT INTO machines(id,context_id,name,socket_name,transport_json,last_observed) VALUES(2,1,'Local','mission','{"kind":"local"}','unknown')`},
		{SQL: `INSERT INTO items(id,human_identifier,title,project_id,status,notes) VALUES(9,'MC-9','Live Run',1,'Active','')`},
		{SQL: `INSERT INTO runs(id,item_id,machine_id,agent,execution_profile,workflow,prompt,working_directory,session_name,pane_id,started_at,state,pane_status) VALUES(9,9,2,'claude','implement','pstack','prompt','/home/fake/src/app','run-9','%4',1780000000,'unknown','unknown')`},
		{SQL: `UPDATE metadata SET value=10 WHERE key='next_run_id'`},
	}
	if err := store.Apply(effects, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRunLifecycleDispatcherStopsFinishesAndDeletesWithConfirmation(t *testing.T) {
	access := NewFakeMachineAccess()
	machine := domain.Machine{ID: 2, Name: "Local", SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	list := "tmux -f /dev/null -L 'mission' list-panes -a -F '#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}\t#{@ai_mission_manager_run_state}'"
	access.ShellResults[list] = MachineAccessResult{Value: "run-9\t%4\tclaude\tterminal\t/home/fake/src/app\t\n"}
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "lifecycle.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.mu.Lock()
	runtime.machineAccess = access
	runtime.state.Machines = []domain.Machine{machine}
	runtime.state.Runs = []domain.Run{{ID: 9, MachineID: 2, Agent: domain.AgentClaude, SessionName: "run-9", PaneID: "%4", State: domain.RunWorking, PaneStatus: domain.PaneAvailable}}
	runtime.mu.Unlock()
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("stop_run", `{"runId":9}`); err != nil {
		t.Fatal(err)
	}
	killCommand := "tmux -f /dev/null -L 'mission' kill-pane -t '%4'"
	if !containsCall(access.Calls, killCommand) {
		t.Fatalf("stop_run did not kill the observed pane; calls=%#v", access.Calls)
	}
	if run := runtime.runCopy(9); run.PaneStatus != domain.PaneMissing {
		t.Fatalf("stopped Run=%#v", run)
	}
	if _, err := service.Invoke("finish_run", `{"runId":9}`); err != nil {
		t.Fatal(err)
	}
	if run := runtime.runCopy(9); run.State != domain.RunFinished {
		t.Fatalf("finished Run=%#v", run)
	}
	if _, err := service.Invoke("delete_run", `{"runId":9,"confirmed":false}`); err == nil {
		t.Fatal("unconfirmed deletion succeeded")
	}
	encoded, err := service.Invoke("delete_run", `{"runId":9,"confirmed":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var deleted RunDeletionResult
	if err := json.Unmarshal(encoded, &deleted); err != nil || deleted.RunID != 9 {
		t.Fatalf("deletion=%s err=%v", encoded, err)
	}
	if run := runtime.runCopy(9); run.ID != 0 {
		t.Fatalf("Run remains after deletion: %#v", run)
	}
}

func TestListRunSuggestionsMatchesRegisteredWorktreeAndAgentCommand(t *testing.T) {
	access := NewFakeMachineAccess()
	machine := domain.Machine{ID: 2, ContextID: 1, Name: "Local", SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	command := "tmux -f /dev/null -L 'mission' list-panes -a -F '#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}\t#{@ai_mission_manager_run_state}'"
	access.ShellResults[command] = MachineAccessResult{Value: "manual\t%4\tsh\tclaude\t/home/fake/worktrees/MC-1-app\t\n"}
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "suggestions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	selected := int64(2)
	runtime.mu.Lock()
	runtime.machineAccess = access
	runtime.state.Contexts = []domain.Context{{ID: 1, ExecutionMachineID: &selected, Name: "Personal"}}
	runtime.state.Machines = []domain.Machine{machine}
	runtime.state.Projects = []domain.Project{{ID: 1, ContextID: 1, Name: "Project"}}
	runtime.state.Items = []domain.Item{{ID: 3, HumanIdentifier: "MC-1", Title: "Ticket", ProjectID: 1}}
	runtime.state.Workspaces = []domain.Workspace{{ID: 4, ItemID: 3}}
	runtime.state.Repositories = []domain.Repository{{ID: 5, ProjectID: 1, Name: "app"}}
	runtime.state.Worktrees = []domain.Worktree{{ID: 6, WorkspaceID: 4, RepositoryID: 5, MachineID: 2, Path: "~/worktrees/MC-1-app"}}
	runtime.mu.Unlock()
	service := CommandService{Runtime: runtime}
	encoded, err := service.Invoke("list_run_suggestions", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var suggestions []RunSuggestion
	if err := json.Unmarshal(encoded, &suggestions); err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 1 || suggestions[0].Agent != domain.AgentClaude || suggestions[0].WorktreeID == nil || *suggestions[0].WorktreeID != 6 || suggestions[0].ItemIdentifier != "MC-1" {
		t.Fatalf("suggestions=%s", encoded)
	}
}

func TestAttachRunAdoptsSuggestionAndPersistsAllNullableFields(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "attach.sqlite")
	seedPersistedRun(t, databasePath)
	store, err := persistence.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	effects := []persistence.Effect{
		{SQL: `UPDATE contexts SET execution_machine_id=2 WHERE id=1`},
		{SQL: `INSERT INTO repositories(id,project_id,name,remote_url) VALUES(7,1,'app','https://github.com/acme/app.git')`},
		{SQL: `INSERT INTO repository_locations(repository_id,machine_id,checkout_path,worktree_root) VALUES(7,2,'/home/fake/src/app','/home/fake/worktrees')`},
		{SQL: `INSERT INTO workspaces(id,item_id,preparation_state) VALUES(8,9,'ready')`},
		{SQL: `INSERT INTO workspace_repositories(workspace_id,repository_id,branch,base_branch) VALUES(8,7,'feature','main')`},
	}
	if err := store.Apply(effects, nil); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	access := NewFakeMachineAccess()
	list := "tmux -f /dev/null -L 'mission' list-panes -a -F '#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}\t#{@ai_mission_manager_run_state}'"
	access.ShellResults[list] = MachineAccessResult{Value: "manual\t%7\tclaude\tterminal\t/home/fake/src/app\t\n"}
	runtime, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.mu.Lock()
	runtime.machineAccess = access
	runtime.mu.Unlock()
	service := CommandService{Runtime: runtime}
	encoded, err := service.Invoke("list_run_suggestions", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var suggestions []RunSuggestion
	if err := json.Unmarshal(encoded, &suggestions); err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 1 || suggestions[0].PaneID != "%7" {
		t.Fatalf("suggestions=%s", encoded)
	}
	request, err := json.Marshal(map[string]any{"suggestion": suggestions[0]})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = service.Invoke("attach_run", string(request))
	if err != nil {
		t.Fatalf("attach_run: %v", err)
	}
	var attached domain.Run
	if err := json.Unmarshal(encoded, &attached); err != nil {
		t.Fatal(err)
	}
	if attached.ID != 10 || attached.ItemID != 9 || attached.WorkspaceID == nil || *attached.WorkspaceID != 8 || attached.RepositoryID == nil || *attached.RepositoryID != 7 || attached.PaneID != "%7" || attached.State != domain.RunUnknown {
		t.Fatalf("attached Run=%#v", attached)
	}
}

func ptrInt64(value int64) *int64 { return &value }
func containsCall(calls []MachineAccessCall, value string) bool {
	for _, call := range calls {
		if call.Value == value {
			return true
		}
	}
	return false
}
