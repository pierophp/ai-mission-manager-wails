package backend

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/gitcli"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func TestWorktreeCommandsPrepareReviewAndRemoveLocalWorktree(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	checkout := filepath.Join(root, "checkout")
	worktreeRoot := filepath.Join(root, "worktrees")
	gitTest(t, "init", "--bare", "--initial-branch=main", remote)
	gitTest(t, "init", "--initial-branch=main", seed)
	gitTestAt(t, seed, "config", "user.name", "Test")
	gitTestAt(t, seed, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("safe fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestAt(t, seed, "add", "README.md")
	gitTestAt(t, seed, "commit", "-m", "initial")
	gitTestAt(t, seed, "remote", "add", "origin", remote)
	gitTestAt(t, seed, "push", "-u", "origin", "main")
	gitTest(t, "clone", remote, checkout)

	runtime, err := OpenRuntime(filepath.Join(root, "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	invoke := func(command, args string) json.RawMessage {
		t.Helper()
		result, err := service.Invoke(command, args)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		return result
	}
	invoke("register_machine", `{"contextId":1,"name":"Local","socketName":"test-worktrees","transport":{"kind":"local"}}`)
	invoke("set_context_execution_machine", `{"contextId":1,"machineId":1}`)
	invoke("register_repository_at_location", mustJSON(t, map[string]any{"projectId": 1, "name": "app", "remoteUrl": remote, "baseBranch": "main", "machineId": 1, "checkoutPath": checkout, "worktreeRoot": worktreeRoot, "cloneIntoDestination": false}))
	invoke("create_item", `{"title":"Worktree smoke","contextId":1,"projectId":1,"notes":null}`)
	state := runtime.domainSnapshot()
	if len(state.Workspaces) != 1 || len(state.Workspaces[0].Repositories) != 1 {
		t.Fatalf("Workspace setup = %#v", state.Workspaces)
	}

	createdJSON := invoke("prepare_worktree", `{"workspaceId":1,"repositoryId":1,"machineId":1,"reuseExistingBranch":false,"confirmDirtyAttachment":false}`)
	var created domain.Worktree
	if err := json.Unmarshal(createdJSON, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != 1 || created.Branch != "mission-MC-1" || created.BaseBranch != "main" || created.Path != filepath.Join(worktreeRoot, "MC-1-app") {
		t.Fatalf("created Worktree = %s", createdJSON)
	}
	for _, camelKey := range []string{`"workspaceId":1`, `"repositoryId":1`, `"machineId":1`, `"baseBranch":"main"`, `"isDirty":false`} {
		if !strings.Contains(string(createdJSON), camelKey) {
			t.Fatalf("Worktree JSON missing camelCase key %s: %s", camelKey, createdJSON)
		}
	}
	if got := gitOutputAt(t, checkout, "config", "branch.mission-MC-1.remote"); got != "origin" {
		t.Fatalf("upstream remote = %q", got)
	}
	if got := gitOutputAt(t, checkout, "config", "branch.mission-MC-1.merge"); got != "refs/heads/mission-MC-1" {
		t.Fatalf("upstream merge = %q", got)
	}
	if len(runtime.domainSnapshot().Worktrees) != 1 || runtime.domainSnapshot().Workspaces[0].PreparationState != domain.WorkspaceReady {
		t.Fatalf("Worktree was not reflected in Runtime: %#v", runtime.domainSnapshot())
	}

	if err := os.WriteFile(filepath.Join(created.Path, "dirty.txt"), []byte("keep only after explicit confirmation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previewJSON := invoke("prepare_worktree_removal", `{"worktreeId":1}`)
	var preview WorktreeRemovalReport
	if err := json.Unmarshal(previewJSON, &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.IsDirty || !preview.RequiresDestructiveConfirmation {
		t.Fatalf("dirty removal preview = %s", previewJSON)
	}
	if err := runtime.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":true}`); err == nil || !strings.Contains(err.Error(), "checkout remains") {
		t.Fatalf("remove with failing persistence = %v", err)
	}
	if _, err := os.Stat(created.Path); err != nil {
		t.Fatalf("checkout was removed before persistence succeeded: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(created.Path, "dirty.txt")); err != nil || string(contents) != "keep only after explicit confirmation\n" {
		t.Fatalf("dirty contents changed after persistence failure: contents=%q err=%v", contents, err)
	}
	reopenedStore, err := persistence.Open(filepath.Join(root, "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	runtime.store = reopenedStore
	previewJSON = invoke("prepare_worktree_removal", `{"worktreeId":1}`)
	if err := json.Unmarshal(previewJSON, &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.IsDirty {
		t.Fatalf("Worktree should remain dirty after the failed metadata transaction: %s", previewJSON)
	}
	removedJSON := invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":true}`)
	if string(removedJSON) != `{"worktreeId":1,"branchPreserved":true}` {
		t.Fatalf("removal result = %s", removedJSON)
	}
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("removed Worktree path still exists: %v", err)
	}
	if got := gitOutputAt(t, checkout, "show-ref", "--verify", "--quiet", "refs/heads/mission-MC-1"); got != "" {
		t.Fatalf("branch verification output = %q", got)
	}
	if len(runtime.domainSnapshot().Worktrees) != 0 {
		t.Fatalf("removed Worktree remains in Runtime: %#v", runtime.domainSnapshot().Worktrees)
	}

	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRuntime(filepath.Join(root, "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.domainSnapshot().Worktrees) != 0 {
		t.Fatalf("removed Worktree was reloaded: %#v", reopened.domainSnapshot().Worktrees)
	}
}

func TestWorktreeRemovalIntentReconcilesInterruptedRemoval(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "mission-manager.sqlite")
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	checkout := filepath.Join(root, "checkout")
	worktreeRoot := filepath.Join(root, "worktrees")
	gitTest(t, "init", "--bare", "--initial-branch=main", remote)
	gitTest(t, "init", "--initial-branch=main", seed)
	gitTestAt(t, seed, "config", "user.name", "Test")
	gitTestAt(t, seed, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("recovery fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestAt(t, seed, "add", "README.md")
	gitTestAt(t, seed, "commit", "-m", "initial")
	gitTestAt(t, seed, "remote", "add", "origin", remote)
	gitTestAt(t, seed, "push", "-u", "origin", "main")
	gitTest(t, "clone", remote, checkout)

	runtime, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	service := CommandService{Runtime: runtime}
	invoke := func(command, args string) json.RawMessage {
		t.Helper()
		result, err := service.Invoke(command, args)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		return result
	}
	invoke("register_machine", `{"contextId":1,"name":"Local","socketName":"worktree-recovery","transport":{"kind":"local"}}`)
	invoke("set_context_execution_machine", `{"contextId":1,"machineId":1}`)
	invoke("register_repository_at_location", mustJSON(t, map[string]any{"projectId": 1, "name": "app", "remoteUrl": remote, "baseBranch": "main", "machineId": 1, "checkoutPath": checkout, "worktreeRoot": worktreeRoot, "cloneIntoDestination": false}))
	invoke("create_item", `{"title":"Recovery smoke","contextId":1,"projectId":1,"notes":null}`)
	createdJSON := invoke("prepare_worktree", `{"workspaceId":1,"repositoryId":1,"machineId":1,"reuseExistingBranch":false,"confirmDirtyAttachment":false}`)
	var created domain.Worktree
	if err := json.Unmarshal(createdJSON, &created); err != nil {
		t.Fatal(err)
	}

	// A crash before Git removes the path must retain the Worktree record.
	if err := runtime.store.SaveWorktreeRemovalIntent(persistence.WorktreeRemovalIntent{WorktreeID: created.ID, Path: created.Path}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	service.Runtime = runtime
	if _, ok := findWorktree(runtime.domainSnapshot(), created.ID); !ok {
		t.Fatal("Worktree record was removed even though its checkout path still exists")
	}
	intents, err := runtime.store.ListWorktreeRemovalIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 0 {
		t.Fatalf("resolved pre-removal intent remains: %#v", intents)
	}

	// A crash after Git removes the checkout must finish deleting its DB record.
	if err := runtime.store.SaveWorktreeRemovalIntent(persistence.WorktreeRemovalIntent{WorktreeID: created.ID, Path: created.Path}); err != nil {
		t.Fatal(err)
	}
	if err := gitcli.New(runtime.machineAccess).RemoveWorktree(domain.Machine{ID: 1, Transport: domain.MachineTransport{Kind: "local"}}, checkout, created.Path, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, ok := findWorktree(runtime.domainSnapshot(), created.ID); ok {
		t.Fatal("startup did not finish deleting metadata for the missing checkout")
	}
	intents, err = runtime.store.ListWorktreeRemovalIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 0 {
		t.Fatalf("completed removal intent remains: %#v", intents)
	}
}

func TestWorktreeDispatcherAttachesExistingWorktreeThroughFakeMachineAccess(t *testing.T) {
	root := t.TempDir()
	runtime, err := OpenRuntime(filepath.Join(root, "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	access := NewFakeMachineAccess()
	base := "git -C '/home/fake/src/app'"
	worktree := "git -C '/home/fake/worktrees/existing'"
	remote := "https://example.invalid/team/app.git"
	for _, prefix := range []string{base, worktree} {
		path := strings.TrimSuffix(strings.TrimPrefix(prefix, "git -C '"), "'")
		access.ShellResults[prefix+" rev-parse --show-toplevel"] = MachineAccessResult{Value: path + "\n"}
		branch := "main\n"
		if prefix == worktree {
			branch = "mission-MC-1\n"
		}
		access.ShellResults[prefix+" symbolic-ref --short HEAD"] = MachineAccessResult{Value: branch}
		access.ShellResults[prefix+" status --porcelain --untracked-files=all"] = MachineAccessResult{Value: " M file.txt\n"}
		access.ShellResults[prefix+" remote"] = MachineAccessResult{Value: "origin\n"}
		access.ShellResults[prefix+" remote get-url 'origin'"] = MachineAccessResult{Value: remote + "\n"}
	}
	access.ShellResults[base+" worktree list --porcelain"] = MachineAccessResult{Value: "worktree /home/fake/src/app\nHEAD abc\nbranch refs/heads/main\n\nworktree /home/fake/worktrees/existing\nHEAD def\nbranch refs/heads/mission-MC-1\n"}
	runtime.SetMachineAdapters(access, nil)
	service := CommandService{Runtime: runtime}
	invoke := func(command, args string) json.RawMessage {
		t.Helper()
		result, err := service.Invoke(command, args)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		return result
	}
	invoke("register_machine", `{"contextId":1,"name":"Fake","socketName":"fake","transport":{"kind":"local"}}`)
	invoke("set_context_execution_machine", `{"contextId":1,"machineId":1}`)
	invoke("register_repository_at_location", mustJSON(t, map[string]any{"projectId": 1, "name": "app", "remoteUrl": remote, "baseBranch": "main", "machineId": 1, "checkoutPath": "/home/fake/src/app", "worktreeRoot": "/home/fake/worktrees", "cloneIntoDestination": false}))
	invoke("create_item", `{"title":"Attach","contextId":1,"projectId":1,"notes":null}`)
	invoke("register_machine", `{"contextId":1,"name":"Other","socketName":"other","transport":{"kind":"local"}}`)
	if _, err := service.Invoke("attach_worktree", `{"workspaceId":1,"repositoryId":1,"machineId":2,"path":"/home/fake/worktrees/existing","confirmDirtyAttachment":true}`); err == nil || !strings.Contains(err.Error(), "not the execution Machine configured") {
		t.Fatalf("attach on non-execution Machine = %v", err)
	}
	if _, err := service.Invoke("attach_worktree", `{"workspaceId":1,"repositoryId":1,"machineId":1,"path":"/home/fake/worktrees/existing","confirmDirtyAttachment":false}`); err == nil || !strings.Contains(err.Error(), "dirty Worktree attachment must be confirmed") {
		t.Fatalf("attach without confirmation = %v; calls=%#v", err, access.Calls)
	}
	attached := invoke("attach_worktree", `{"workspaceId":1,"repositoryId":1,"machineId":1,"path":"/home/fake/worktrees/existing","confirmDirtyAttachment":true}`)
	var result domain.Worktree
	if err := json.Unmarshal(attached, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != 1 || result.Branch != "mission-MC-1" || !result.IsDirty {
		t.Fatalf("attached Worktree = %s", attached)
	}
	foundList := false
	for _, call := range access.Calls {
		if call.Value == base+" worktree list --porcelain" {
			foundList = true
		}
	}
	if !foundList {
		t.Fatal("dispatcher did not use fake MachineAccess for Git Worktree listing")
	}
	if _, err := service.Invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":true}`); err == nil || !strings.Contains(err.Error(), "Review the Worktree removal safety report") {
		t.Fatalf("remove without reviewed report = %v", err)
	}
	preview := invoke("prepare_worktree_removal", `{"worktreeId":1}`)
	if !strings.Contains(string(preview), `"requiresDestructiveConfirmation":true`) {
		t.Fatalf("dirty removal preview = %s", preview)
	}
	access.ShellResults[worktree+" status --porcelain --untracked-files=all"] = MachineAccessResult{}
	if _, err := service.Invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":true}`); err == nil || !strings.Contains(err.Error(), "changed after the safety report") {
		t.Fatalf("remove after stale report = %v", err)
	}
	access.ShellResults[worktree+" status --porcelain --untracked-files=all"] = MachineAccessResult{Value: " M file.txt\n"}
	invoke("prepare_worktree_removal", `{"worktreeId":1}`)
	if _, err := service.Invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":false}`); err == nil || !strings.Contains(err.Error(), "requires destructive confirmation") {
		t.Fatalf("dirty remove without destructive confirmation = %v", err)
	}
	removeCommand := base + " worktree remove --force '/home/fake/worktrees/existing'"
	access.ShellResults[removeCommand] = MachineAccessResult{Err: errors.New("simulated Git failure")}
	if _, err := service.Invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":true}`); err == nil || !strings.Contains(err.Error(), "metadata remains available for review") {
		t.Fatalf("remove after Git failure = %v", err)
	}
	if _, ok := findWorktree(runtime.domainSnapshot(), 1); !ok {
		t.Fatal("failed Git removal deleted the Worktree record")
	}
	intents, err := runtime.store.ListWorktreeRemovalIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 0 {
		t.Fatalf("failed Git removal left a stale durable intent: %#v", intents)
	}
	access.ShellResults[removeCommand] = MachineAccessResult{}
	invoke("remove_worktree", `{"worktreeId":1,"confirmed":true,"destructiveConfirmed":true}`)

	baseList := "worktree /home/fake/src/app\nHEAD abc\nbranch refs/heads/main\n"
	access.ShellResults[base+" worktree list --porcelain"] = MachineAccessResult{Value: baseList}
	access.ShellResults[base+" show-ref --verify --quiet 'refs/remotes/origin/main'; printf 'status:%s' \"$?\""] = MachineAccessResult{Value: "status:0"}
	second := invoke("create_item", `{"title":"Prepare fake","contextId":1,"projectId":1,"notes":null}`)
	if !strings.Contains(string(second), `"id":2`) {
		t.Fatalf("second item = %s", second)
	}
	preparePath := "git -C '/home/fake/worktrees/MC-2-app'"
	access.ShellResults["if [ -e '/home/fake/worktrees/MC-2-app' ]; then printf exists; else printf missing; fi"] = MachineAccessResult{Value: "missing"}
	access.ShellResults[base+" show-ref --verify --quiet 'refs/heads/mission-MC-2'; printf 'status:%s' \"$?\""] = MachineAccessResult{Value: "status:1"}
	access.ShellResults[base+" show-ref --verify --quiet 'refs/remotes/origin/mission-MC-2'; printf 'status:%s' \"$?\""] = MachineAccessResult{Value: "status:1"}
	access.ShellResults[preparePath+" rev-parse --show-toplevel"] = MachineAccessResult{Value: "/home/fake/worktrees/MC-2-app\n"}
	access.ShellResults[preparePath+" symbolic-ref --short HEAD"] = MachineAccessResult{Value: "mission-MC-2\n"}
	access.ShellResults[preparePath+" status --porcelain --untracked-files=all"] = MachineAccessResult{Value: " M new-file.txt\n"}
	access.ShellResults[preparePath+" remote"] = MachineAccessResult{Value: "origin\n"}
	access.ShellResults[preparePath+" remote get-url 'origin'"] = MachineAccessResult{Value: remote + "\n"}
	if _, err := service.Invoke("prepare_worktree", `{"workspaceId":2,"repositoryId":1,"machineId":1,"reuseExistingBranch":false,"confirmDirtyAttachment":false}`); err == nil || !strings.Contains(err.Error(), "dirty Worktree attachment must be confirmed") {
		t.Fatalf("dirty prepared Worktree without confirmation = %v", err)
	}
	if len(runtime.domainSnapshot().Worktrees) != 0 {
		t.Fatalf("unconfirmed prepared Worktree was registered: %#v", runtime.domainSnapshot().Worktrees)
	}
	cleanedPreparation := false
	for _, call := range access.Calls {
		if call.Value == base+" worktree remove '/home/fake/worktrees/MC-2-app'" {
			cleanedPreparation = true
		}
	}
	if !cleanedPreparation {
		t.Fatal("unconfirmed dirty preparation did not remove its newly created checkout")
	}
	third := invoke("create_item", `{"title":"Create fake","contextId":1,"projectId":1,"notes":null}`)
	if !strings.Contains(string(third), `"id":3`) {
		t.Fatalf("third item = %s", third)
	}
	createPath := "/home/fake/worktrees/manual"
	access.ShellResults["if [ -e '"+createPath+"' ]; then printf exists; else printf missing; fi"] = MachineAccessResult{Value: "missing"}
	access.ShellResults[base+" show-ref --verify --quiet 'refs/heads/feature/manual'; printf 'status:%s' \"$?\""] = MachineAccessResult{Value: "status:1"}
	access.ShellResults[base+" show-ref --verify --quiet 'refs/remotes/origin/feature/manual'; printf 'status:%s' \"$?\""] = MachineAccessResult{Value: "status:1"}
	created := invoke("create_worktree", `{"workspaceId":3,"repositoryId":1,"machineId":1,"path":"/home/fake/worktrees/manual","branch":"feature/manual","baseBranch":"main"}`)
	if !strings.Contains(string(created), `"branch":"feature/manual"`) {
		t.Fatalf("created Worktree = %s", created)
	}
}

func gitTest(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
}
func gitTestAt(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %v: %s: %v", directory, args, output, err)
	}
}
func gitOutputAt(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %v: %s: %v", directory, args, output, err)
	}
	return strings.TrimSpace(string(output))
}
