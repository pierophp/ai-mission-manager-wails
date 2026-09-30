package backend

import (
	"encoding/json"
	"path/filepath"
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

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
