package backend

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type deletionTestTerminalConnection struct{ closed bool }

func (c *deletionTestTerminalConnection) sendInput([]byte) error      { return nil }
func (c *deletionTestTerminalConnection) resize(uint16, uint16) error { return nil }
func (c *deletionTestTerminalConnection) snapshot() ([]byte, error)   { return nil, nil }
func (c *deletionTestTerminalConnection) close() error                { c.closed = true; return nil }

func TestDeleteProjectRequiresFreshPreviewAndCascadesSQLiteChildren(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "deletion.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	svc := CommandService{Runtime: rt}
	if _, err = svc.Invoke("create_project", `{"name":"Throwaway","contextId":1,"defaultItemStatus":"Inbox"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("prepare_project_deletion", `{"projectId":2}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("create_item", `{"title":"Child task","contextId":1,"projectId":2,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("register_repository", `{"projectId":2,"name":"repo","remoteUrl":"https://example.invalid/repo.git"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("delete_project", `{"projectId":2,"itemIds":[],"repositoryIds":[],"workspaceIds":[],"confirmed":true}`); err == nil {
		t.Fatal("stale preview unexpectedly allowed deletion")
	}
	raw, err := svc.Invoke("prepare_project_deletion", `{"projectId":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var preview ParentDeletionPreview
	if err = json.Unmarshal(raw, &preview); err != nil {
		t.Fatal(err)
	}
	itemIDs := []int64{}
	for _, v := range preview.Plan.Items {
		itemIDs = append(itemIDs, v.ID)
	}
	repoIDs := []int64{}
	for _, v := range preview.Plan.Repositories {
		repoIDs = append(repoIDs, v.ID)
	}
	workspaceIDs := []int64{}
	for _, v := range preview.Plan.Workspaces {
		workspaceIDs = append(workspaceIDs, v.ID)
	}
	args, _ := json.Marshal(map[string]any{"projectId": 2, "itemIds": itemIDs, "repositoryIds": repoIDs, "workspaceIds": workspaceIDs, "confirmed": true})
	if _, err = svc.Invoke("delete_project", string(args)); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	state, err := rt.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Projects) != 1 || len(state.Items) != 0 || len(state.Repositories) != 0 || len(state.Workspaces) != 0 {
		t.Fatalf("project cascade left records: projects=%d items=%d repos=%d workspaces=%d", len(state.Projects), len(state.Items), len(state.Repositories), len(state.Workspaces))
	}
}

func TestMachinePaneDeletionUsesFiveSecondTimeout(t *testing.T) {
	rt := NewRuntime(nil)
	access := NewFakeMachineAccess()
	rt.SetMachineAdapters(access, nil)
	rt.mu.Lock()
	rt.state.Runs = []domain.Run{{ID: 4, MachineID: 2, PaneID: "%9"}}
	rt.mu.Unlock()
	n, failed := rt.killMachinePanes(domain.Machine{ID: 2, SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}, []domain.MachineDeletionRun{{ID: 4, PaneStatus: domain.PaneAvailable}})
	if n != 1 || failed != 0 {
		t.Fatalf("killMachinePanes = (%d,%d)", n, failed)
	}
	want := MachineAccessCall{Operation: "run_shell_timeout", MachineID: 2, Value: "tmux -f /dev/null -L 'mission' kill-pane -t '%9'", Timeout: 5 * time.Second}
	if !reflect.DeepEqual(access.Calls, []MachineAccessCall{want}) {
		t.Fatalf("machine calls = %#v", access.Calls)
	}
}

func TestRepositoryDeletionRemovesOnlyItsRepositoryReferences(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "repository.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	svc := CommandService{Runtime: rt}
	if _, err = svc.Invoke("create_item", `{"title":"Task","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("register_repository", `{"projectId":1,"name":"repo","remoteUrl":"https://example.invalid/repo.git"}`); err != nil {
		t.Fatal(err)
	}
	previewRaw, err := svc.Invoke("prepare_repository_deletion", `{"repositoryId":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var preview RepositoryDeletionPreview
	if err = json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"repositoryId": 1, "workspaceIds": []int64{preview.Plan.Workspaces[0].ID}, "confirmed": true})
	if _, err = svc.Invoke("delete_repository", string(args)); err != nil {
		t.Fatal(err)
	}
	s, err := rt.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Repositories) != 0 || len(s.Workspaces) != 0 {
		t.Fatalf("repository removal state = repos:%d workspaces:%#v", len(s.Repositories), s.Workspaces)
	}
}

func TestContextDeletionAndResetCommands(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "context-reset.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	svc := CommandService{Runtime: rt}
	if _, err = svc.Invoke("create_context", `{"name":"Temporary"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("prepare_context_deletion", `{"contextId":2}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("delete_context", `{"contextId":2,"projectIds":[2],"itemIds":[],"repositoryIds":[],"workspaceIds":[],"machineIds":[],"confirmed":true}`); err != nil {
		t.Fatalf("delete empty Context: %v", err)
	}
	raw, err := svc.Invoke("prepare_reset_local_data", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var preview ResetLocalDataPreview
	if err = json.Unmarshal(raw, &preview); err != nil {
		t.Fatal(err)
	}
	connection := &deletionTestTerminalConnection{}
	rt.terminalMu.Lock()
	rt.terminalConnections["temporary-terminal"] = terminalSession{generation: 1, connection: connection}
	rt.terminalMu.Unlock()
	if _, err = svc.Invoke("reset_all_local_data", `{"confirmation":"reset"}`); err == nil {
		t.Fatal("reset accepted an incorrect phrase")
	}
	if _, err = svc.Invoke("reset_all_local_data", `{"confirmation":"RESET ALL LOCAL DATA"}`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	rt.terminalMu.Lock()
	remainingTerminals := len(rt.terminalConnections)
	rt.terminalMu.Unlock()
	if !connection.closed || remainingTerminals != 0 {
		t.Fatal("reset did not close and clear terminal sessions")
	}
	s, err := rt.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Contexts) != 1 || s.Contexts[0].Name != "Personal" || len(s.Projects) != 1 || s.Projects[0].Name != "Default" || len(s.Items) != 0 {
		t.Fatalf("reset state = contexts:%#v projects:%#v items:%d", s.Contexts, s.Projects, len(s.Items))
	}
}

func TestMachineDeletionRemovesMachineAndSelectionMustMatchPreview(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "machine.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	svc := CommandService{Runtime: rt}
	if _, err = svc.Invoke("register_machine", `{"contextId":1,"name":"Temp","socketName":"temp","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	reachable := true
	rt.mu.Lock()
	rt.machineReadiness[1] = MachineReadiness{Reachable: &reachable}
	rt.mu.Unlock()
	if _, err = svc.Invoke("prepare_machine_deletion", `{"machineId":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Invoke("delete_machine", `{"machineId":1,"runIds":[999],"worktreeIds":[],"repositoryLocationRepositoryIds":[],"confirmed":true}`); err == nil {
		t.Fatal("machine deletion accepted IDs different from preview")
	}
	if _, err = svc.Invoke("delete_machine", `{"machineId":1,"runIds":[],"worktreeIds":[],"repositoryLocationRepositoryIds":[],"confirmed":true}`); err != nil {
		t.Fatalf("delete machine: %v", err)
	}
	s, err := rt.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Machines) != 0 {
		t.Fatalf("Machines after deletion = %d", len(s.Machines))
	}
	rt.mu.Lock()
	readinessExists := rt.machineReadiness[1].Reachable != nil
	rt.mu.Unlock()
	if readinessExists {
		t.Fatal("Machine deletion retained readiness state")
	}
}

func TestDeleteItemRejectsActiveRunAndRequiresConfirmation(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if _, err = rt.runEvent(domain.Event{Kind: "create_item", Name: "Task", ContextID: 1, ProjectID: 1}); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.state.Runs = []domain.Run{{ID: 8, ItemID: 1, State: domain.RunWorking}}
	rt.mu.Unlock()
	preview, err := rt.prepareItemDeletion(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Blockers) != 1 {
		t.Fatalf("blockers = %v", preview.Blockers)
	}
	if _, err = rt.deleteItem(1, false); err == nil {
		t.Fatal("deletion without confirmation unexpectedly succeeded")
	}
	if _, err = rt.deleteItem(1, true); err == nil {
		t.Fatal("active Run did not block deletion")
	}
}
