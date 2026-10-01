package backend

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

type fakeAgentRunExecutor struct {
	runtime   *Runtime
	launched  []string
	released  []string
	killed    []string
	launchErr error
	onLaunch  func() error
}

func TestGatedAgentCommandPreservesIdentityProfileAndCodexEffort(t *testing.T) {
	machine := domain.Machine{SocketName: "mission"}
	got, err := buildGatedAgentCommand(machine, "mission-launch-23-mission-item-8-run-23", "/opt/codex", "work prompt", domain.AgentCodex, "23", "/home/runner/.local/state/ai-mission-manager/runs/run-23.json", "gpt-6-sol", "high", "/home/runner/.codex-work")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"wait-for 'mission-launch-23-mission-item-8-run-23'", "export AI_MISSION_MANAGER_RUN_ID='23'", "AI_MISSION_MANAGER_PANE_ID=\"$TMUX_PANE\"", "unset OPENAI_API_KEY CODEX_API_KEY CODEX_ACCESS_TOKEN", "export CODEX_HOME='/home/runner/.codex-work'", "exec '/opt/codex' exec --model 'gpt-6-sol' -c 'model_reasoning_effort=high' 'work prompt'"} {
		if !strings.Contains(got, part) {
			t.Errorf("gated command missing %q:\n%s", part, got)
		}
	}
	if strings.Index(got, "wait-for") > strings.Index(got, "exec '/opt/codex'") {
		t.Fatalf("agent command is not gated before execution: %s", got)
	}
}

func (f *fakeAgentRunExecutor) Preflight(_ domain.Machine, _ domain.AgentKind, id int64, _ *domain.CLIConfigurationProfile) (string, string, string, MachineReadiness, error) {
	return "/usr/bin/claude", filepath.Join(tHomeForRunTest(f.runtime), ".local/state/ai-mission-manager/runs", fmtRunID(id)+".json"), "", MachineReadiness{}, nil
}
func (f *fakeAgentRunExecutor) Launch(_ domain.Machine, session, gate, _, _, _ string, _ domain.AgentKind, _, _, _, _, _ string) (string, error) {
	f.launched = append(f.launched, session+"|"+gate)
	if f.onLaunch != nil {
		if err := f.onLaunch(); err != nil {
			return "", err
		}
	}
	return "%7", f.launchErr
}
func (f *fakeAgentRunExecutor) Release(_ domain.Machine, gate string) error {
	if f.runtime != nil {
		found := false
		for _, run := range f.runtime.domainSnapshot().Runs {
			if strings.Contains(gate, "mission-launch-"+fmtRunID(run.ID)+"-") {
				found = true
			}
		}
		if !found {
			return errors.New("gate was released before Run commit")
		}
	}
	f.released = append(f.released, gate)
	return nil
}
func (f *fakeAgentRunExecutor) Kill(_ domain.Machine, session string) error {
	f.killed = append(f.killed, session)
	return nil
}
func tHomeForRunTest(_ *Runtime) string { return os.TempDir() }
func fmtRunID(id int64) string          { return strconv.FormatInt(id, 10) }

func TestStartRunPersistsBeforeGateAndCoversDirectAndWorktree(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	checkout := filepath.Join(root, "checkout")
	worktrees := filepath.Join(root, "worktrees")
	runGitTest(t, "init", "--bare", "--initial-branch=main", remote)
	runGitTest(t, "init", "--initial-branch=main", seed)
	runGitTestAt(t, seed, "config", "user.name", "Test")
	runGitTestAt(t, seed, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTestAt(t, seed, "add", "README.md")
	runGitTestAt(t, seed, "commit", "-m", "initial")
	runGitTestAt(t, seed, "remote", "add", "origin", remote)
	runGitTestAt(t, seed, "push", "-u", "origin", "main")
	runGitTest(t, "clone", remote, checkout)
	runtime, err := OpenRuntime(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	call := func(command, args string) json.RawMessage {
		t.Helper()
		v, e := service.Invoke(command, args)
		if e != nil {
			t.Fatalf("%s: %v", command, e)
		}
		return v
	}
	call("register_machine", `{"contextId":1,"name":"Local","socketName":"launch-test","transport":{"kind":"local"}}`)
	call("set_context_execution_machine", `{"contextId":1,"machineId":1}`)
	call("register_repository_at_location", mustJSON(t, map[string]any{"projectId": 1, "name": "app", "remoteUrl": remote, "baseBranch": "main", "machineId": 1, "checkoutPath": checkout, "worktreeRoot": worktrees, "cloneIntoDestination": false}))
	call("create_item", `{"title":"Run launch fixture","contextId":1,"projectId":1,"notes":null}`)
	setTestWorkspaceBranch(t, runtime, "main")
	previewRaw := call("prepare_direct_run", `{"itemId":1,"workspaceId":1,"machineId":1}`)
	var preview DirectRunPreview
	if err := json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAgentRunExecutor{runtime: runtime}
	runtime.setRunExecutor(fake)
	request := map[string]any{"itemId": 1, "workspaceId": 1, "strategy": map[string]any{"kind": "direct", "machineId": 1, "primaryRepositoryId": 1, "agent": "claude", "configuration": map[string]any{"agent": "claude", "model": "claude-sonnet-5", "effort": "high"}, "executionProfile": "implement", "workflow": "matt-pocock", "prompt": "Implement the fixture.", "promptSelection": map[string]any{"includeObjective": true, "externalObjectIds": []int64{}}, "expectedCheckouts": preview.Checkouts, "allowDirty": false, "allowSharedCheckouts": false}}
	changedFile := filepath.Join(checkout, "changed-after-preview.txt")
	if err := os.WriteFile(changedFile, []byte("new untracked change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("start_run", mustJSON(t, map[string]any{"request": request})); err == nil || !strings.Contains(err.Error(), "changed after the preview") {
		t.Fatalf("dirty checkout changed after preview was accepted: %v", err)
	}
	if err := os.Remove(changedFile); err != nil {
		t.Fatal(err)
	}
	runRaw := call("start_run", mustJSON(t, map[string]any{"request": request}))
	var direct domain.Run
	if err := json.Unmarshal(runRaw, &direct); err != nil {
		t.Fatal(err)
	}
	if direct.SessionName != "mission-item-1-run-1" || direct.PaneID != "%7" || direct.State != domain.RunUnknown || len(fake.released) != 1 {
		t.Fatalf("direct Run=%s released=%v", runRaw, fake.released)
	}
	request["strategy"] = map[string]any{"kind": "grill", "machineId": 1, "primaryRepositoryId": 1, "configuration": map[string]any{"agent": "claude", "model": "claude-sonnet-5", "effort": "high"}, "prompt": "Ask a focused decision question.", "expectedCheckouts": preview.Checkouts, "allowDirty": false, "allowSharedCheckouts": true}
	grillRaw := call("start_run", mustJSON(t, map[string]any{"request": request}))
	var grill domain.Run
	if err := json.Unmarshal(grillRaw, &grill); err != nil {
		t.Fatal(err)
	}
	if grill.ExecutionProfile != domain.ExecutionProfileGrill || grill.SessionName != "mission-item-1-grill-2" || len(fake.released) != 2 {
		t.Fatalf("Grill Run=%s released=%v", grillRaw, fake.released)
	}
	seq := int64(1)
	record := AgentStateRecord{Agent: domain.AgentClaude, RunID: "1", State: domain.RunWorking, UpdatedAt: "now", Sequence: &seq}
	if _, changed, err := runtime.applyAgentStateRecord(1, record); err != nil || !changed {
		t.Fatalf("working transition: changed=%v err=%v", changed, err)
	}
	record.State = domain.RunFinished
	seq = 2
	if _, changed, err := runtime.applyAgentStateRecord(1, record); err != nil || !changed {
		t.Fatalf("finished transition: changed=%v err=%v", changed, err)
	}
	runGitTestAt(t, checkout, "switch", "-c", "mission-MC-1")
	runGitTestAt(t, checkout, "switch", "main")
	setTestWorkspaceBranch(t, runtime, "mission-MC-1")
	worktreeRaw := call("prepare_worktree", `{"workspaceId":1,"repositoryId":1,"machineId":1,"reuseExistingBranch":true,"confirmDirtyAttachment":false}`)
	var worktree domain.Worktree
	if err := json.Unmarshal(worktreeRaw, &worktree); err != nil {
		t.Fatal(err)
	}
	request["strategy"] = map[string]any{"kind": "worktree", "worktreeId": worktree.ID, "agent": "codex", "configuration": map[string]any{"agent": "codex", "model": "gpt-6-sol", "effort": "high"}, "executionProfile": "implement", "workflow": "matt-pocock", "prompt": "Implement in the worktree.", "promptSelection": map[string]any{"includeObjective": true, "externalObjectIds": []int64{}}}
	wtRaw := call("start_run", mustJSON(t, map[string]any{"request": request}))
	var wtRun domain.Run
	if err := json.Unmarshal(wtRaw, &wtRun); err != nil {
		t.Fatal(err)
	}
	if wtRun.WorktreeID == nil || *wtRun.WorktreeID != worktree.ID || wtRun.SessionName != "mission-item-1-run-3" || len(fake.released) != 3 {
		t.Fatalf("worktree Run=%s released=%v", wtRaw, fake.released)
	}
	seq = 1
	worktreeRecord := AgentStateRecord{Agent: domain.AgentCodex, RunID: "3", State: domain.RunWorking, UpdatedAt: "now", Sequence: &seq}
	if _, changed, err := runtime.applyAgentStateRecord(3, worktreeRecord); err != nil || !changed {
		t.Fatalf("worktree working transition: changed=%v err=%v", changed, err)
	}
	seq = 2
	worktreeRecord.State = domain.RunFinished
	if _, changed, err := runtime.applyAgentStateRecord(3, worktreeRecord); err != nil || !changed {
		t.Fatalf("worktree finished transition: changed=%v err=%v", changed, err)
	}
}

func TestFailedRunCommitKillsGatedSessionWithoutReleasing(t *testing.T) {
	root := t.TempDir()
	runtime, err := OpenRuntime(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	checkout := filepath.Join(root, "checkout")
	runGitTest(t, "init", "--bare", "--initial-branch=main", remote)
	runGitTest(t, "init", "--initial-branch=main", seed)
	runGitTestAt(t, seed, "config", "user.name", "Test")
	runGitTestAt(t, seed, "config", "user.email", "test@example.invalid")
	os.WriteFile(filepath.Join(seed, "README.md"), []byte("x\n"), 0o600)
	runGitTestAt(t, seed, "add", "README.md")
	runGitTestAt(t, seed, "commit", "-m", "initial")
	runGitTestAt(t, seed, "remote", "add", "origin", remote)
	runGitTestAt(t, seed, "push", "-u", "origin", "main")
	runGitTest(t, "clone", remote, checkout)
	for _, call := range []struct{ cmd, args string }{{"register_machine", `{"contextId":1,"name":"Local","socketName":"fail-test","transport":{"kind":"local"}}`}, {"set_context_execution_machine", `{"contextId":1,"machineId":1}`}, {"register_repository_at_location", mustJSON(t, map[string]any{"projectId": 1, "name": "app", "remoteUrl": remote, "baseBranch": "main", "machineId": 1, "checkoutPath": checkout, "worktreeRoot": filepath.Join(root, "wt"), "cloneIntoDestination": false})}, {"create_item", `{"title":"Failure","contextId":1,"projectId":1,"notes":null}`}} {
		if _, err := service.Invoke(call.cmd, call.args); err != nil {
			t.Fatal(err)
		}
	}
	setTestWorkspaceBranch(t, runtime, "main")
	preview, err := runtime.prepareDirectRun(1, 1, int64Ptr(1))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeAgentRunExecutor{runtime: runtime, onLaunch: func() error {
		_ = runtime.store.Apply([]persistence.Effect{{SQL: `INSERT INTO runs(id,item_id,machine_id,agent,execution_profile,prompt,working_directory,session_name,pane_id,started_at,state,pane_status) VALUES(1,1,1,'claude','implement','conflict','/tmp','conflict','%9',1,'unknown','unknown')`}}, nil)
		return nil
	}}
	runtime.setRunExecutor(fake)
	strategy := runLaunchStrategy{Kind: "direct", MachineID: int64Ptr(1), PrimaryRepositoryID: 1, Agent: domain.AgentClaude, Configuration: &domain.GrillConfiguration{Agent: domain.AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, ExecutionProfile: domain.ExecutionProfileImplement, Workflow: domain.WorkflowMattPocock, Prompt: "prompt", ExpectedCheckouts: preview.Checkouts}
	if _, err := runtime.launchRun(runLaunchRequest{ItemID: 1, WorkspaceID: 1, Strategy: strategy}); err == nil || !strings.Contains(err.Error(), "record Run") {
		t.Fatalf("launch error=%v", err)
	}
	if len(fake.killed) != 1 || len(fake.released) != 0 || len(runtime.domainSnapshot().Runs) != 0 {
		t.Fatalf("cleanup=%v release=%v runs=%#v", fake.killed, fake.released, runtime.domainSnapshot().Runs)
	}
}

func runGitTest(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
func setTestWorkspaceBranch(t *testing.T, runtime *Runtime, branch string) {
	t.Helper()
	if err := runtime.store.Apply([]persistence.Effect{{SQL: `UPDATE workspace_repositories SET branch=? WHERE workspace_id=1 AND repository_id=1`, Args: []any{branch}}}, nil); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	for i := range runtime.state.Workspaces {
		if runtime.state.Workspaces[i].ID == 1 {
			runtime.state.Workspaces[i].Repositories[0].Branch = branch
		}
	}
	runtime.mu.Unlock()
}
func runGitTestAt(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
