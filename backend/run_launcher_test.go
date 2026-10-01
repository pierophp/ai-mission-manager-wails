package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
	"github.com/piero/ai-mission-manager-wails/backend/pstack"
)

type fakeAgentRunExecutor struct {
	runtime    *Runtime
	launched   []string
	released   []string
	killed     []string
	launchErr  error
	releaseErr error
	onLaunch   func() error
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
	if f.releaseErr != nil {
		return f.releaseErr
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
	t.Setenv("HOME", root)
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
	request["strategy"] = map[string]any{"kind": "grill", "machine_id": 1, "primary_repository_id": 1, "configuration": map[string]any{"agent": "claude", "model": "claude-sonnet-5", "effort": "high"}, "language": "english", "prompt": "Ask a focused decision question.", "expected_checkouts": preview.Checkouts, "allow_dirty": false, "allow_shared_checkouts": true}
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
	request["strategy"] = map[string]any{"kind": "worktree", "worktreeId": worktree.ID, "agent": "codex", "configuration": map[string]any{"agent": "codex", "model": "gpt-6-sol", "effort": "high"}, "executionProfile": "autonomous", "workflow": "pstack", "prompt": "Run pstack against this worktree.", "promptSelection": map[string]any{"includeObjective": true, "externalObjectIds": []int64{}}}
	fake.onLaunch = func() error {
		root := filepath.Join(os.Getenv("HOME"), ".local/share/ai-mission-manager/pstack", pstack.TreeHash)
		if _, err := os.Stat(filepath.Join(root, "skills/poteto-mode/SKILL.md")); err != nil {
			return fmt.Errorf("pstack tree missing before pane launch: %w", err)
		}
		roles, err := filepath.Glob(filepath.Join(root, "roles", "context-1-*.md"))
		if err != nil || len(roles) != 1 {
			return fmt.Errorf("pstack roles missing before pane launch: %v", err)
		}
		contents, err := os.ReadFile(roles[0])
		if err != nil {
			return err
		}
		if !strings.Contains(string(contents), "## Code delegate") {
			return errors.New("pstack role file was incomplete before pane launch")
		}
		return nil
	}
	pstackRaw := call("start_run", mustJSON(t, map[string]any{"request": request}))
	var pstackRun domain.Run
	if err := json.Unmarshal(pstackRaw, &pstackRun); err != nil {
		t.Fatal(err)
	}
	if pstackRun.Workflow != domain.WorkflowPstack || pstackRun.SkillSnapshot == nil || !strings.Contains(pstackRun.Prompt, filepath.Join(root, ".local/share/ai-mission-manager/pstack", pstack.TreeHash)) || len(fake.released) != 4 {
		t.Fatalf("pstack Run=%s released=%v", pstackRaw, fake.released)
	}
	roleFiles, err := filepath.Glob(filepath.Join(root, ".local/share/ai-mission-manager/pstack", pstack.TreeHash, "roles", "context-1-*.md"))
	if err != nil || len(roleFiles) != 1 {
		t.Fatalf("role files=%v err=%v", roleFiles, err)
	}
	info, err := os.Stat(roleFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("role file mode = %v, want 0600", info.Mode().Perm())
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

func TestImplementationQueueAdvancesTwoTicketsAfterFinishedRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	remote, seed, checkout := filepath.Join(root, "origin.git"), filepath.Join(root, "seed"), filepath.Join(root, "checkout")
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
	call := func(command string, args any) json.RawMessage {
		t.Helper()
		raw, e := service.Invoke(command, mustJSON(t, args))
		if e != nil {
			t.Fatalf("%s: %v", command, e)
		}
		return raw
	}
	call("register_machine", map[string]any{"contextId": 1, "name": "Local", "socketName": "queue-test", "transport": map[string]any{"kind": "local"}})
	call("set_context_execution_machine", map[string]any{"contextId": 1, "machineId": 1})
	call("register_repository_at_location", map[string]any{"projectId": 1, "name": "app", "remoteUrl": remote, "baseBranch": "main", "machineId": 1, "checkoutPath": checkout, "worktreeRoot": filepath.Join(root, "worktrees"), "cloneIntoDestination": false})
	call("create_item", map[string]any{"title": "Queue fixture", "contextId": 1, "projectId": 1, "notes": ""})
	spec := domain.ExternalObject{Provider: domain.ProviderGitHub, Kind: domain.ObjectIssue, ExternalKey: "issue:acme/app#44", CanonicalURL: "https://github.com/acme/app/issues/44"}
	if _, err := runtime.transition(domain.Event{Kind: "link_external_object", ItemID: 1, ExternalObject: &spec}); err != nil {
		t.Fatal(err)
	}
	linkedState, err := runtime.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var specID int64
	for _, object := range linkedState.ExternalObjects {
		if object.ExternalKey == spec.ExternalKey {
			specID = object.ID
			break
		}
	}
	var linkID int64
	for _, link := range linkedState.Links {
		if link.ItemID == 1 && link.ExternalObjectID == specID {
			linkID = link.ID
			break
		}
	}
	if linkID == 0 || specID == 0 {
		t.Fatal("Spec link was not created")
	}
	if _, err := runtime.runEvent(domain.Event{Kind: "set_link_purpose", LinkID: linkID, Purpose: domain.LinkPurpose("to-spec")}); err != nil {
		t.Fatal(err)
	}
	setTestWorkspaceBranch(t, runtime, "main")
	previewRaw := call("prepare_direct_run", map[string]any{"itemId": 1, "workspaceId": 1, "machineId": 1})
	var preview DirectRunPreview
	if err := json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAgentRunExecutor{runtime: runtime}
	runtime.setRunExecutor(fake)
	ticketState := "OPEN"
	runtime.implementationQueueTicketState = func(_ domain.DomainState, _ domain.Context, url string) (string, error) { return ticketState, nil }
	request := map[string]any{"itemId": 1, "workspaceId": 1, "strategy": map[string]any{
		"kind": "direct", "machineId": 1, "primaryRepositoryId": 1, "agent": "claude",
		"configuration":    map[string]any{"agent": "claude", "model": "claude-sonnet-5", "effort": "high"},
		"executionProfile": "implement", "workflow": "matt-pocock", "prompt": "Implementation Queue",
		"promptSelection":   map[string]any{"includeObjective": true, "externalObjectIds": []int64{}},
		"expectedCheckouts": preview.Checkouts, "allowDirty": false, "allowSharedCheckouts": false,
		"implementation_queue": map[string]any{"specExternalObjectId": specID, "specUrl": "https://github.com/acme/app/issues/44", "entries": []any{
			map[string]any{"position": 0, "ticketNumber": 101, "ticketTitle": "First", "ticketUrl": "https://github.com/acme/app/issues/101", "ticketState": "OPEN", "runId": nil, "done": false, "skipped": false},
			map[string]any{"position": 1, "ticketNumber": 102, "ticketTitle": "Second", "ticketUrl": "https://github.com/acme/app/issues/102", "ticketState": "OPEN", "runId": nil, "done": false, "skipped": false},
		}},
	}}
	var first domain.Run
	if err := json.Unmarshal(call("start_run", map[string]any{"request": request}), &first); err != nil {
		t.Fatal(err)
	}
	queue := runtime.queueCopy(first.ID)
	if queue.ID != first.ID || len(queue.Entries) != 2 || queue.Entries[0].RunID == nil || *queue.Entries[0].RunID != first.ID || !strings.Contains(first.Prompt, "Ticket #101") {
		t.Fatalf("initial queue/run = %#v / %#v", queue, first)
	}
	sequence := int64(1)
	record := AgentStateRecord{Agent: domain.AgentClaude, RunID: fmtRunID(first.ID), State: domain.RunWorking, UpdatedAt: "now", Sequence: &sequence}
	if _, changed, err := runtime.applyAgentStateRecord(first.ID, record); err != nil || !changed {
		t.Fatalf("working queue Run: changed=%v err=%v", changed, err)
	}
	sequence++
	record.State = domain.RunFinished
	if _, changed, err := runtime.applyAgentStateRecord(first.ID, record); err != nil || !changed {
		t.Fatalf("finished queue Run: changed=%v err=%v", changed, err)
	}
	queue = runtime.queueCopy(first.ID)
	if queue.PausedReason == nil || queue.PausedReason.Kind != "ticket_still_open" || queue.Entries[0].Done || len(fake.released) != 1 {
		t.Fatalf("open ticket should pause queue: %#v released=%v", queue, fake.released)
	}
	ticketState = "CLOSED"
	dirtyFile := filepath.Join(checkout, "agent-output.txt")
	if err := os.WriteFile(dirtyFile, []byte("changes need review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	call("check_implementation_queue", map[string]any{"queueId": first.ID})
	queue = runtime.queueCopy(first.ID)
	if queue.PausedReason == nil || queue.PausedReason.Kind != "checkout_dirty" || queue.Entries[0].Done {
		t.Fatalf("dirty checkout should pause queue: %#v", queue)
	}
	if err := os.Remove(dirtyFile); err != nil {
		t.Fatal(err)
	}
	fake.releaseErr = errors.New("gate release failed")
	call("check_implementation_queue", map[string]any{"queueId": first.ID})
	queue = runtime.queueCopy(first.ID)
	if queue.Entries[0].TicketState != "CLOSED" || !queue.Entries[0].Done || queue.Entries[1].RunID == nil || *queue.Entries[1].RunID != 2 || !queue.Active || queue.PausedReason == nil || queue.PausedReason.Kind != "launch_failed" {
		t.Fatalf("failed second Run was not attached and paused: %#v", queue)
	}
	second := runtime.runCopy(2)
	if second.ID != 2 || !strings.Contains(second.Prompt, "Ticket #102") || len(fake.released) != 1 || len(fake.killed) != 1 {
		t.Fatalf("second Run=%#v released=%v killed=%v", second, fake.released, fake.killed)
	}
	fake.releaseErr = nil
	call("check_implementation_queue", map[string]any{"queueId": first.ID})
	queue = runtime.queueCopy(first.ID)
	if len(runtime.domainSnapshot().Runs) != 2 || queue.Entries[1].RunID == nil || *queue.Entries[1].RunID != 2 {
		t.Fatalf("checking a failed release launched a duplicate Run: runs=%#v queue=%#v", runtime.domainSnapshot().Runs, queue)
	}
	queue.PausedReason = &domain.ImplementationQueuePauseReason{Kind: "ticket_still_open"}
	if err := runtime.updateImplementationQueue(queue); err != nil {
		t.Fatal(err)
	}
	call("skip_implementation_queue_entry", map[string]any{"queueId": first.ID})
	queue = runtime.queueCopy(first.ID)
	if queue.Active || !queue.Entries[1].Skipped || len(fake.killed) != 2 {
		t.Fatalf("skip did not close the final Run and finish queue: %#v killed=%v", queue, fake.killed)
	}
}
