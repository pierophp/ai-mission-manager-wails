package backend

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func TestStructureCommandsPersistAcrossRuntimeReopen(t *testing.T) {
	database := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	runtime, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	service := CommandService{Runtime: runtime}
	cfg := defaultContextConfiguration()
	cfg.Name = "Research"
	cfg.DefaultWorkflow = domain.WorkflowPstack
	cfg.AttentionDefaults = cfg.AttentionDefaults[:3]
	created, err := service.Invoke("create_context_configuration", mustJSON(t, map[string]any{"configuration": cfg}))
	if err != nil {
		t.Fatal(err)
	}
	var context Context
	if err := json.Unmarshal(created, &context); err != nil {
		t.Fatal(err)
	}
	if context.ID != 2 || context.Name != "Research" || context.DefaultWorkflow != "pstack" {
		t.Fatalf("created Context = %s", created)
	}
	projectJSON, err := service.Invoke("create_project", `{"name":"Operations","contextId":2,"defaultItemStatus":"Active","executionMode":"direct"}`)
	if err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(projectJSON, &project); err != nil {
		t.Fatal(err)
	}
	if project.ID != 3 || project.Defaults.ItemStatus != domain.StatusActive || project.Defaults.ExecutionMode != domain.ExecutionDirect {
		t.Fatalf("Project = %s", projectJSON)
	}
	_, err = service.Invoke("set_context_attention_default", `{"contextId":2,"objectKind":"document","policy":{"title":true,"state":false,"metadata":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	contexts, err := reopened.ListContexts()
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 2 || contexts[1].Name != "Research" {
		t.Fatalf("Contexts after reopen = %#v", contexts)
	}
	projects := reopened.listProjects()
	if len(projects) != 3 || projects[2].Name != "Operations" {
		t.Fatalf("Projects after reopen = %#v", projects)
	}
	history := reopened.listAuditHistory()
	if len(history) != 4 {
		t.Fatalf("Audit history = %#v", history)
	}
	if got := string(history[0].Action); got != `{"action":"contextAttentionDefaultChanged","context_id":2,"object_kind":"document"}` {
		t.Fatalf("latest audit action = %s", got)
	}
	activity := reopened.activityTab()
	if len(activity.AuditEntries) != 4 || activity.Activities == nil || len(activity.Activities) != 0 {
		t.Fatalf("Activity tab = %#v", activity)
	}
}

func TestStructureAndActivityGoldenJSON(t *testing.T) {
	contextJSON, err := json.Marshal(projectContext(domain.Context{ID: 7, Name: "Personal", CheckDirtyCheckouts: true, GrillDefaults: domain.GrillConfiguration{Agent: domain.AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, ImplementDefaults: domain.GrillConfiguration{Agent: domain.AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, DefaultWorkflow: domain.WorkflowMattPocock, PstackDefaults: domain.GrillConfiguration{Agent: domain.AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, PstackRoles: domain.DefaultPstackRoles()}))
	if err != nil {
		t.Fatal(err)
	}
	wantContext := `{"id":7,"name":"Personal","execution_machine_id":null,"claude_profile_id":null,"codex_profile_id":null,"check_dirty_checkouts":true,"grill_defaults":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"implement_defaults":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"default_workflow":"matt-pocock","pstack_defaults":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"pstack_roles":[{"role":"code-delegate","configuration":{"agent":"claude","model":"claude-opus-5","effort":"high"}},{"role":"judge-and-prose","configuration":{"agent":"codex","model":"gpt-6-sol","effort":"high"}},{"role":"review-panel","configuration":{"agent":"codex","model":"gpt-6-sol","effort":"high"}},{"role":"explorers","configuration":{"agent":"claude","model":"claude-sonnet-5","effort":"medium"}}],"gh_executable_path":null,"twg_executable_path":null,"az_executable_path":null,"atlassian_site":null,"azure_devops_organization":null,"bitbucket_workspace":null}`
	if string(contextJSON) != wantContext {
		t.Fatalf("Context golden mismatch\n got: %s\nwant: %s", contextJSON, wantContext)
	}
	cfgJSON, err := json.Marshal(defaultContextConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := `{"name":"","executionMachineId":null,"claudeProfileId":null,"codexProfileId":null,"checkDirtyCheckouts":true,"grillDefaults":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"implementDefaults":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"defaultWorkflow":"matt-pocock","pstackDefaults":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"pstackRoles":[{"role":"code-delegate","configuration":{"agent":"claude","model":"claude-opus-5","effort":"high"}},{"role":"judge-and-prose","configuration":{"agent":"codex","model":"gpt-6-sol","effort":"high"}},{"role":"review-panel","configuration":{"agent":"codex","model":"gpt-6-sol","effort":"high"}},{"role":"explorers","configuration":{"agent":"claude","model":"claude-sonnet-5","effort":"medium"}}],"ghExecutablePath":null,"twgExecutablePath":null,"azExecutablePath":null,"atlassianSite":null,"azureDevopsOrganization":null,"bitbucketWorkspace":null,"attentionDefaults":[{"context_id":0,"object_kind":"issue","policy":{"title":true,"state":true,"metadata":true}},{"context_id":0,"object_kind":"pull_request","policy":{"title":true,"state":true,"metadata":true}},{"context_id":0,"object_kind":"generic","policy":{"title":true,"state":true,"metadata":true}}]}`
	if string(cfgJSON) != wantConfig {
		t.Fatalf("ContextConfiguration golden mismatch\n got: %s\nwant: %s", cfgJSON, wantConfig)
	}
	entry := domain.AuditEntry{ID: 9, RecordedAt: 123, Action: json.RawMessage(`{"action":"contextCreated","context_id":7}`)}
	auditJSON, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(auditJSON), `{"id":9,"recorded_at":123,"action":{"action":"contextCreated","context_id":7}}`; got != want {
		t.Fatalf("AuditEntry golden = %s, want %s", got, want)
	}
}

func TestRuntimeEmitsEventsThroughInjectedAdapter(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	var gotName string
	var gotPayload any
	runtime.SetEventEmitter(EventEmitterFunc(func(name string, payload any) { gotName = name; gotPayload = payload }))
	runtime.EmitEvent("run-state-changed", map[string]any{"runId": 9, "state": "working"})
	if gotName != "run-state-changed" {
		t.Fatalf("event name = %q", gotName)
	}
	if gotPayload.(map[string]any)["runId"] != 9 {
		t.Fatalf("event payload = %#v", gotPayload)
	}
}

func TestRepositoryDispatcherUsesMachineShellAndPersistsWorkspaceMapping(t *testing.T) {
	database := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	runtime, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	access := NewFakeMachineAccess()
	base := "git -C '/home/fake/src/app'"
	access.ShellResults[base+" rev-parse --show-toplevel"] = MachineAccessResult{Value: "/home/fake/src/app\n"}
	access.ShellResults[base+" symbolic-ref --short HEAD"] = MachineAccessResult{Value: "main\n"}
	access.ShellResults[base+" status --porcelain --untracked-files=all"] = MachineAccessResult{}
	access.ShellResults[base+" remote"] = MachineAccessResult{Value: "origin\n"}
	access.ShellResults[base+" remote get-url 'origin'"] = MachineAccessResult{Value: "git@example.com:team/app.git\n"}
	runtime.SetMachineAdapters(access, nil)
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("register_machine", `{"contextId":1,"name":"Local","socketName":"local","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("create_item", `{"title":"Build","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	created, err := service.Invoke("register_repository_at_location", `{"projectId":1,"name":"app","remoteUrl":null,"baseBranch":"main","machineId":1,"checkoutPath":"~/src/app","worktreeRoot":null,"cloneIntoDestination":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(created), `"remote_url":"git@example.com:team/app.git"`) {
		t.Fatalf("registered Repository = %s", created)
	}
	locations, err := service.Invoke("list_repository_locations", `{}`)
	if err != nil || string(locations) != `[{"repository_id":1,"machine_id":1,"checkout_path":"~/src/app","worktree_root":"~/worktrees"}]` {
		t.Fatalf("locations = %s, %v", locations, err)
	}
	if len(runtime.state.Workspaces) != 1 || len(runtime.state.Workspaces[0].Repositories) != 1 || runtime.state.Workspaces[0].Repositories[0].Branch != "mission-MC-1" {
		t.Fatalf("Workspace repositories = %#v", runtime.state.Workspaces)
	}
	repositories, err := service.Invoke("list_repositories", `{}`)
	if err != nil || !strings.Contains(string(repositories), `"base_branch":"main"`) {
		t.Fatalf("repositories = %s, %v", repositories, err)
	}
	if _, err := service.Invoke("update_repository", `{"repositoryId":1,"name":"app","remoteUrl":"git@example.com:team/app.git","baseBranch":"trunk"}`); err != nil {
		t.Fatal(err)
	}
	if got := runtime.state.Workspaces[0].Repositories[0]; got.Branch != "mission-MC-1" || got.BaseBranch != "trunk" {
		t.Fatalf("updated Workspace repository = %#v", got)
	}
	updatedLocation, err := service.Invoke("update_repository_location", `{"repositoryId":1,"previousMachineId":1,"machineId":1,"checkoutPath":"~/src/renamed-app","worktreeRoot":"~/worktrees-v2"}`)
	if err != nil || !strings.Contains(string(updatedLocation), `"checkout_path":"~/src/renamed-app"`) {
		t.Fatalf("updated location = %s, %v", updatedLocation, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.state.Workspaces) != 1 || reopened.state.Workspaces[0].Repositories[0].BaseBranch != "trunk" || len(reopened.state.RepositoryLocations) != 1 || reopened.state.RepositoryLocations[0].CheckoutPath != "~/src/renamed-app" {
		t.Fatalf("reopened Workspace = %#v", reopened.state.Workspaces)
	}
}

func TestRepositoryDispatcherClonesOnMachineAfterPrevalidatingRegistration(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	access := NewFakeMachineAccess()
	cloneCommand := "git clone 'git@example.com:team/clone.git' '/home/fake/src/clone'"
	base := "git -C '/home/fake/src/clone'"
	access.ShellResults[cloneCommand] = MachineAccessResult{}
	access.ShellResults[base+" rev-parse --show-toplevel"] = MachineAccessResult{Value: "/home/fake/src/clone\n"}
	access.ShellResults[base+" symbolic-ref --short HEAD"] = MachineAccessResult{Value: "main\n"}
	access.ShellResults[base+" status --porcelain --untracked-files=all"] = MachineAccessResult{}
	access.ShellResults[base+" remote"] = MachineAccessResult{Value: "origin\n"}
	access.ShellResults[base+" remote get-url 'origin'"] = MachineAccessResult{Value: "git@example.com:team/clone.git\n"}
	runtime.SetMachineAdapters(access, nil)
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("register_machine", `{"contextId":1,"name":"Local","socketName":"local","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("create_item", `{"title":"Build","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("register_repository", `{"projectId":1,"name":"clone","remoteUrl":"git@example.com:team/clone.git"}`); err != nil {
		t.Fatal(err)
	}
	_, err = service.Invoke("register_repository_at_location", `{"projectId":1,"name":"clone","remoteUrl":"https://other.example/repo.git","baseBranch":"main","machineId":1,"checkoutPath":"~/src/duplicate","worktreeRoot":null,"cloneIntoDestination":true}`)
	if err == nil || !strings.Contains(err.Error(), "different remote URL") {
		t.Fatalf("duplicate clone error = %v", err)
	}
	for _, call := range access.Calls {
		if call.Operation == "run_shell" && strings.HasPrefix(call.Value, "git clone ") {
			t.Fatalf("invalid registration cloned before validation: %s", call.Value)
		}
	}
	created, err := service.Invoke("register_repository_at_location", `{"projectId":1,"name":"app-clone","remoteUrl":"git@example.com:team/clone.git","baseBranch":"main","machineId":1,"checkoutPath":"~/src/clone","worktreeRoot":null,"cloneIntoDestination":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(created), `"name":"app-clone"`) {
		t.Fatalf("cloned Repository = %s", created)
	}
	if len(runtime.state.Workspaces) != 1 || len(runtime.state.Workspaces[0].Repositories) != 2 {
		t.Fatalf("Workspace = %#v", runtime.state.Workspaces)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
