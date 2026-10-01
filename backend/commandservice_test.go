package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	_ "modernc.org/sqlite"
)

func TestInvokeListsContextsFromExistingDatabaseWithoutChangingIt(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	createContextsFixture(t, databasePath)
	before, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	service := CommandService{Runtime: NewRuntime(SQLiteContextReader{DatabasePath: databasePath})}
	got, err := service.Invoke("list_contexts", `{}`)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	var contexts []map[string]any
	if err := json.Unmarshal(got, &contexts); err != nil {
		t.Fatalf("Invoke() returned invalid JSON: %s: %v", got, err)
	}
	if len(contexts) != 1 {
		t.Fatalf("expected one context, got %s", got)
	}
	want := map[string]any{
		"id": float64(7), "name": "Personal", "execution_machine_id": nil,
		"claude_profile_id": nil, "codex_profile_id": nil, "check_dirty_checkouts": true,
		"grill_defaults":     map[string]any{"agent": "claude", "model": "claude-sonnet-5", "effort": "high"},
		"implement_defaults": map[string]any{"agent": "codex", "model": "gpt-6-sol", "effort": "medium"},
		"default_workflow":   "matt-pocock",
		"pstack_defaults":    map[string]any{"agent": "claude", "model": "claude-opus-5", "effort": "high"},
		"pstack_roles": []any{
			map[string]any{"role": "code-delegate", "configuration": map[string]any{"agent": "claude", "model": "claude-opus-5", "effort": "high"}},
			map[string]any{"role": "judge-and-prose", "configuration": map[string]any{"agent": "codex", "model": "gpt-6-sol", "effort": "high"}},
			map[string]any{"role": "review-panel", "configuration": map[string]any{"agent": "codex", "model": "gpt-6-sol", "effort": "high"}},
			map[string]any{"role": "explorers", "configuration": map[string]any{"agent": "claude", "model": "claude-sonnet-5", "effort": "medium"}},
		}, "gh_executable_path": nil, "twg_executable_path": nil,
		"az_executable_path": nil, "atlassian_site": nil,
		"azure_devops_organization": nil, "bitbucket_workspace": nil,
	}
	if !jsonEqual(contexts[0], want) {
		t.Fatalf("context JSON mismatch\n got: %#v\nwant: %#v", contexts[0], want)
	}
	after, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("list_contexts changed the database file")
	}
}

func TestInvokeRejectsUnknownCommandsAndInvalidArguments(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	createContextsFixture(t, databasePath)
	service := CommandService{Runtime: NewRuntime(SQLiteContextReader{DatabasePath: databasePath})}

	if _, err := service.Invoke("not_a_command", `{}`); err == nil {
		t.Fatal("expected unknown command error")
	}
	if _, err := service.Invoke("list_contexts", `{"unexpected":true}`); err == nil {
		t.Fatal("expected invalid arguments error")
	}
	if _, err := service.Invoke("list_contexts", `not-json`); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestInvokeListsNoContextsAsEmptyArray(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	createContextsFixture(t, databasePath)
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM contexts`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	service := CommandService{Runtime: NewRuntime(SQLiteContextReader{DatabasePath: databasePath})}
	got, err := service.Invoke("list_contexts", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[]" {
		t.Fatalf("empty list JSON = %s, want []", got)
	}
}

func TestItemsCommandsPersistAndHomeGoldenJSON(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	runtime, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	created, err := service.Invoke("create_item", `{"title":"Inbox task","contextId":1,"projectId":1,"notes":null}`)
	if err != nil {
		t.Fatal(err)
	}
	const itemGolden = `{"id":1,"human_identifier":"MC-1","title":"Inbox task","project_id":1,"status":"Inbox","notes":"","reminders":[]}`
	if string(created) != itemGolden {
		t.Fatalf("create_item JSON = %s, want %s", created, itemGolden)
	}
	const itemViewGolden = `{"item":{"id":1,"human_identifier":"MC-1","title":"Inbox task","project_id":1,"status":"Inbox","notes":"","reminders":[]},"context_id":1,"context_name":"Personal","project_name":"Default","relationships":[],"workspaces":[],"worktrees":[],"runs":[],"run_projections":[],"run_signals":{"grillWaiting":false,"runActive":false},"implementation_queues":[],"links":[]}`
	got, err := service.Invoke("get_home", `{"contextId":null,"now":"2026-09-30T09:15"}`)
	if err != nil {
		t.Fatal(err)
	}
	const homeGolden = `{"needs_attention":[` + itemViewGolden + `],"attention_entries":[],"running":[],"waiting":[],"due":[],"completed":[]}`
	if string(got) != homeGolden {
		t.Fatalf("get_home JSON mismatch\n got: %s\nwant: %s", got, homeGolden)
	}
	search, err := service.Invoke("search_items_command", `{"query":" inbox ","contextId":1}`)
	if err != nil || string(search) != `[`+itemViewGolden+`]` {
		t.Fatalf("search_items_command = %s, %v", search, err)
	}
	if _, err := service.Invoke("set_item_status", `{"itemId":1,"status":"Active"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("set_item_title", `{"itemId":1,"title":"Updated title"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("set_item_notes", `{"itemId":1,"notes":"updated notes"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("add_item_reminder", `{"itemId":1,"remindAt":"2026-09-30T09:15"}`); err != nil {
		t.Fatal(err)
	}
	inbox, err := service.Invoke("list_inbox_items", `{}`)
	if err != nil || string(inbox) != `[]` {
		t.Fatalf("list_inbox_items = %s, %v", inbox, err)
	}
	removal, err := service.Invoke("remove_item_reminder", `{"itemId":1,"reminderId":1}`)
	if err != nil || !strings.Contains(string(removal), `"reminders":[]`) {
		t.Fatalf("remove reminder = %s, %v", removal, err)
	}
	second, err := service.Invoke("create_item", `{"title":"Second","contextId":1,"projectId":1,"notes":null}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = second
	if _, err := service.Invoke("set_item_relation", `{"fromItemId":2,"toItemId":1,"kind":"Blocks"}`); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var state = reopened.home(nil, "2026-09-30T09:15")
	if len(state.Running) != 1 || state.Running[0].Item.Title != "Updated title" || state.Running[0].Item.Notes != "updated notes" || len(state.Running[0].Relationships) != 1 {
		t.Fatalf("reopened home state = %#v", state)
	}
}

func TestExternalObjectCommandsUseGHAdapterPersistSnapshotsAndActivities(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	rt, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ghPath := filepath.Join(t.TempDir(), "gh")
	script := `#!/bin/sh
case "$*" in
*"--json number,title,state,author,labels,milestone,createdAt,updatedAt") printf '{"number":7,"title":"%s","state":"OPEN","author":{"login":"piero"},"labels":[],"milestone":null,"createdAt":"2026-01-01","updatedAt":"2026-01-02"}' "$GH_SNAPSHOT_TITLE" ;;
*"--json body") printf '%s' '{"body":"Issue body"}' ;;
*"/sub_issues "*) printf '%s\n' '{"number":8,"title":"Child","state":"OPEN","html_url":"https://github.com/acme/app/issues/8"}' ;;
*"/comments --paginate --slurp") printf '%s' '[[{"id":4,"user":{"login":"piero"},"body":"Comment","created_at":"2026-01-03"}]]' ;;
"issue comment "*) exit 0 ;;
*) echo "unexpected gh args: $*" >&2; exit 2;;
esac
`
	if err := os.WriteFile(ghPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_SNAPSHOT_TITLE", "Before")
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track upstream","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	cfg := defaultContextConfiguration()
	cfg.Name = "Personal"
	cfg.GHExecutablePath = &ghPath
	for i := range cfg.AttentionDefaults {
		cfg.AttentionDefaults[i].ContextID = 1
	}
	if _, err = service.Invoke("update_context_configuration", mustJSON(t, map[string]any{"contextId": 1, "configuration": cfg})); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://github.com/Acme/App/issues/7?view=full"}`)
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		t.Fatal(err)
	}
	if action.Link.Object.CanonicalURL != "https://github.com/acme/app/issues/7" || action.Link.Snapshot == nil || action.Link.Snapshot.Title != "Before" {
		t.Fatalf("link result = %s", linked)
	}
	duplicate, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://github.com/acme/app/issues/7"}`)
	if err != nil {
		t.Fatal(err)
	}
	var second ExternalLinkAction
	if err = json.Unmarshal(duplicate, &second); err != nil {
		t.Fatal(err)
	}
	if second.Link.Link.ID != action.Link.Link.ID {
		t.Fatalf("link was not deduplicated: %s", duplicate)
	}
	doc, err := service.Invoke("fetch_issue_document", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var issueDoc IssueDocument
	if err = json.Unmarshal(doc, &issueDoc); err != nil || issueDoc.Body != "Issue body" || len(issueDoc.SubIssues) != 1 {
		t.Fatalf("issue document %s (%v)", doc, err)
	}
	comments, err := service.Invoke("fetch_external_comments", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(comments), `"author":"piero"`) {
		t.Fatalf("comments %s", comments)
	}
	if _, err = service.Invoke("add_external_comment", fmt.Sprintf(`{"linkId":%d,"body":"Please review"}`, action.Link.Link.ID)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_SNAPSHOT_TITLE", "After")
	refreshed, err := service.Invoke("refresh_external_object", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot domain.ExternalSnapshot
	if err = json.Unmarshal(refreshed, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Title != "After" {
		t.Fatalf("refresh = %s", refreshed)
	}
	activities := rt.activityTab().Activities
	if len(activities) != 1 || len(activities[0].Activity.Changes) == 0 {
		t.Fatalf("Activity projection = %#v", activities)
	}
	var raw []byte
	for _, entry := range activities[0].Activity.Changes {
		if entry.Kind == "title" {
			raw, _ = json.Marshal(entry)
			break
		}
	}
	if string(raw) != `{"kind":"title","key":null,"previous":"Before","current":"After"}` {
		t.Fatalf("title change JSON = %s", raw)
	}
	if _, err = service.Invoke("poll_external_objects", `{}`); err != nil {
		t.Fatal(err)
	}
}

func TestAtlassianLinkFetchesDocumentsCommentsAndRefreshesThroughRuntime(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	twg := fakeCommand(t, "twg", filepath.Join(t.TempDir(), "twg-args"), `case "$1 $2 $3" in
"jira workitem get") printf '{"key":"APP-42","summary":"%s","status":"Open","description":"Description"}' "$TWG_TITLE" ;;
"jira workitem comment") printf '%s' '{"comments":[{"id":8,"author":{"displayName":"Ada"},"body":"Note","created":"2026-09-01"}]}' ;;
*) echo "unexpected TWG command: $*" >&2; exit 2 ;;
esac`)
	t.Setenv("TWG_TITLE", "Before")
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track Jira","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	cfg := defaultContextConfiguration()
	cfg.Name = "Personal"
	cfg.TWGExecutablePath = &twg
	cfg.AtlassianSite = stringPtr("acme.atlassian.net")
	for i := range cfg.AttentionDefaults {
		cfg.AttentionDefaults[i].ContextID = 1
	}
	if _, err = service.Invoke("update_context_configuration", mustJSON(t, map[string]any{"contextId": 1, "configuration": cfg})); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://acme.atlassian.net/browse/APP-42"}`)
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		t.Fatal(err)
	}
	if action.Link.Snapshot == nil || action.Link.Snapshot.Title != "Before" {
		t.Fatalf("link=%s", linked)
	}
	doc, err := service.Invoke("fetch_issue_document", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var issueDoc IssueDocument
	if err = json.Unmarshal(doc, &issueDoc); err != nil || issueDoc.Body != "Description" {
		t.Fatalf("document=%s err=%v", doc, err)
	}
	comments, err := service.Invoke("fetch_external_comments", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil || !strings.Contains(string(comments), `"author":"Ada"`) {
		t.Fatalf("comments=%s err=%v", comments, err)
	}
	t.Setenv("TWG_TITLE", "After")
	refreshed, err := service.Invoke("refresh_external_object", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot domain.ExternalSnapshot
	if err = json.Unmarshal(refreshed, &snapshot); err != nil || snapshot.Title != "After" {
		t.Fatalf("snapshot=%s err=%v", refreshed, err)
	}
	poll, err := service.Invoke("poll_external_objects", `{}`)
	if err != nil || !strings.Contains(string(poll), `"refreshed":1`) {
		t.Fatalf("poll=%s err=%v", poll, err)
	}
}

func TestMismatchedProviderContextKeepsLinkWarnsAndRefreshes(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	argsPath := filepath.Join(t.TempDir(), "args")
	twg := fakeCommand(t, "twg-mismatch", argsPath, `printf '%s' '{"key":"APP-9","summary":"Jira from configured CLI","status":"Open"}'`)
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track mismatch","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	cfg := defaultContextConfiguration()
	cfg.Name = "Personal"
	cfg.TWGExecutablePath = &twg
	cfg.AtlassianSite = stringPtr("acme.atlassian.net")
	for i := range cfg.AttentionDefaults {
		cfg.AttentionDefaults[i].ContextID = 1
	}
	if _, err = service.Invoke("update_context_configuration", mustJSON(t, map[string]any{"contextId": 1, "configuration": cfg})); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://other.atlassian.net/browse/APP-9"}`)
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		t.Fatal(err)
	}
	if action.Warning == nil || !strings.Contains(*action.Warning, "The Link was created") || action.Link.Snapshot != nil {
		t.Fatalf("mismatched link=%s", linked)
	}
	if _, err = service.Invoke("refresh_external_object", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID)); err == nil || !strings.Contains(err.Error(), "different site or organization") {
		t.Fatalf("refresh with mismatched Context target = %v", err)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatalf("provider CLI must not run for a mismatched target; args file stat error = %v", err)
	}
}

func TestAzureDevOpsLinkRefreshesThroughRuntime(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	az := fakeCommand(t, "az", filepath.Join(t.TempDir(), "az-args"), `printf '{"id":42,"fields":{"System.Title":"%s","System.State":"Active","System.WorkItemType":"Bug","System.Description":"<p>Details</p>"}}' "$AZ_TITLE"`)
	t.Setenv("AZ_TITLE", "Before")
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track Azure","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	cfg := defaultContextConfiguration()
	cfg.Name = "Personal"
	cfg.AZExecutablePath = &az
	cfg.AzureDevOpsOrganization = stringPtr("acme")
	for i := range cfg.AttentionDefaults {
		cfg.AttentionDefaults[i].ContextID = 1
	}
	if _, err = service.Invoke("update_context_configuration", mustJSON(t, map[string]any{"contextId": 1, "configuration": cfg})); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://dev.azure.com/acme/apps/_workitems/edit/42"}`)
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		t.Fatal(err)
	}
	if action.Link.Snapshot == nil || action.Link.Snapshot.Title != "Before" {
		t.Fatalf("link=%s", linked)
	}
	doc, err := service.Invoke("fetch_issue_document", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var azureDocument IssueDocument
	if err = json.Unmarshal(doc, &azureDocument); err != nil || azureDocument.Body != "<p>Details</p>" {
		t.Fatalf("document=%s", doc)
	}
	t.Setenv("AZ_TITLE", "After")
	refreshed, err := service.Invoke("refresh_external_object", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot domain.ExternalSnapshot
	if err = json.Unmarshal(refreshed, &snapshot); err != nil || snapshot.Title != "After" {
		t.Fatalf("snapshot=%s err=%v", refreshed, err)
	}
}

func TestLocalMarkdownSpecAndTicketsLinkReadAndPoll(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track local feature","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	feature := filepath.Join(root, "feature")
	issues := filepath.Join(feature, "issues")
	if err = os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(feature, "spec.md")
	ticket := filepath.Join(issues, "01-first.md")
	if err = os.WriteFile(spec, []byte("# Local feature\nStatus: Open\nDescription\n## Comments\nPlease review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(ticket, []byte("# First ticket\nStatus: Done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, _ := rt.stateSnapshot()
	machineID := int64(1)
	state.Contexts[0].ExecutionMachineID = &machineID
	state.Machines = append(state.Machines, domain.Machine{ID: machineID, ContextID: 1, Name: "Local", Transport: domain.MachineTransport{Kind: domain.TransportLocal}})
	state.Repositories = append(state.Repositories, domain.Repository{ID: 1, ProjectID: 1, Name: "repo"})
	state.RepositoryLocations = append(state.RepositoryLocations, domain.RepositoryLocation{RepositoryID: 1, MachineID: machineID, CheckoutPath: root})
	rt.mu.Lock()
	rt.state = state
	rt.mu.Unlock()
	rt.SetMachineAdapters(NewFakeMachineAccess(), nil)
	linked, err := service.Invoke("link_external_object", mustJSON(t, map[string]any{"itemId": 1, "url": spec}))
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		t.Fatal(err)
	}
	if action.Link.Snapshot == nil || action.Link.Snapshot.Title != "Local feature" || action.Link.Object.ExternalKey != "local:1#feature/spec.md" {
		t.Fatalf("link=%s", linked)
	}
	doc, err := service.Invoke("fetch_issue_document", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var parsed IssueDocument
	if err = json.Unmarshal(doc, &parsed); err != nil || len(parsed.SubIssues) != 1 || parsed.SubIssues[0].Title != "First ticket" {
		t.Fatalf("document=%s err=%v", doc, err)
	}
	comments, err := service.Invoke("fetch_external_comments", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil || !strings.Contains(string(comments), "Please review") {
		t.Fatalf("comments=%s err=%v", comments, err)
	}
	if _, err = service.Invoke("poll_external_objects", `{}`); err != nil {
		t.Fatal(err)
	}
}

func TestAzureDevOpsIdentityCollisionReturnsDomainErrorBeforeSQLite(t *testing.T) {
	rt, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track Azure work","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	first, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://dev.azure.com/acme/app/_workitems/edit/42"}`)
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(first, &action); err != nil {
		t.Fatal(err)
	}
	_, err = service.Invoke("link_external_object", `{"itemId":1,"url":"https://dev.azure.com/acme/app/_git/frontend/pullrequest/42"}`)
	if err == nil || !strings.Contains(err.Error(), "identity collision") {
		t.Fatalf("expected explicit Rust-compatible Azure identity collision, got %v", err)
	}
}

func TestCreateGitHubIssueCommandUsesRepositoryAndLinksCreatedIssue(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	rt, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ghPath := filepath.Join(t.TempDir(), "gh")
	script := `#!/bin/sh
case "$*" in
"api repos/acme/app/issues --method POST"*) printf '%s' '{"html_url":"https://github.com/Acme/App/issues/44"}' ;;
*"--json number,title,state,author,labels,milestone,createdAt,updatedAt") printf '%s' '{"number":44,"title":"Created","state":"OPEN","author":null,"labels":[],"milestone":null,"createdAt":"2026-01-01","updatedAt":"2026-01-02"}' ;;
*) echo "unexpected gh args: $*" >&2; exit 2;;
esac
`
	if err := os.WriteFile(ghPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Create upstream issue","contextId":1,"projectId":1,"notes":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Invoke("register_repository", `{"projectId":1,"name":"app","remoteUrl":"ssh://git@github.com/acme/app.git"}`); err != nil {
		t.Fatal(err)
	}
	cfg := defaultContextConfiguration()
	cfg.Name = "Personal"
	cfg.GHExecutablePath = &ghPath
	for i := range cfg.AttentionDefaults {
		cfg.AttentionDefaults[i].ContextID = 1
	}
	if _, err = service.Invoke("update_context_configuration", mustJSON(t, map[string]any{"contextId": 1, "configuration": cfg})); err != nil {
		t.Fatal(err)
	}
	created, err := service.Invoke("create_github_issue", `{"itemId":1,"repositoryId":1,"title":"Created","body":"Body"}`)
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(created, &action); err != nil {
		t.Fatal(err)
	}
	if action.Link.Object.CanonicalURL != "https://github.com/acme/app/issues/44" || action.Link.Snapshot == nil {
		t.Fatalf("created Issue was not linked with snapshot: %s", created)
	}
}

func TestSetupCommandsPersistSettingsInNewDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	runtime, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service := CommandService{Runtime: runtime}

	got, err := service.Invoke("get_setup_state", `{}`)
	if err != nil || string(got) != `{"completed":false,"provider":"github"}` {
		t.Fatalf("initial get_setup_state = %s, %v", got, err)
	}
	got, err = service.Invoke("complete_setup", `{"contextName":"  Personal  ","provider":"none"}`)
	if err != nil || string(got) != `{"completed":true,"provider":"none"}` {
		t.Fatalf("complete_setup = %s, %v", got, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedService := CommandService{Runtime: reopened}
	got, err = reopenedService.Invoke("get_setup_state", `{}`)
	if err != nil || string(got) != `{"completed":true,"provider":"none"}` {
		t.Fatalf("persisted get_setup_state = %s, %v", got, err)
	}
	var setupCompleted, providerChoice string
	if setupCompleted, err = reopened.store.Setting("setup_completed"); err != nil {
		t.Fatal(err)
	}
	if providerChoice, err = reopened.store.Setting("provider_choice"); err != nil {
		t.Fatal(err)
	}
	if setupCompleted != "true" || providerChoice != "none" {
		t.Fatalf("settings = (%q, %q), want (true, none)", setupCompleted, providerChoice)
	}
}

func TestHealthStatusDispatcherResolvesAndPersistsExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based executable fixtures require Unix")
	}
	directory := t.TempDir()
	writeExecutable(t, filepath.Join(directory, "tmux"), "#!/bin/sh\nprintf 'tmux 3.4\\n'\n")
	writeExecutable(t, filepath.Join(directory, "gh"), "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nprintf 'not logged in to github.com\\n' >&2\nexit 1\n")
	writeExecutable(t, filepath.Join(directory, "claude"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(directory, "codex"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", directory)

	databasePath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	runtime, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("complete_setup", `{"contextName":"Personal","provider":"github"}`); err != nil {
		t.Fatal(err)
	}
	got, err := service.Invoke("get_health_status", `{"provider":null}`)
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Runtime struct {
			State          string  `json:"state"`
			ExecutablePath *string `json:"executablePath"`
		} `json:"runtime"`
		Provider struct {
			State   string `json:"state"`
			Message string `json:"message"`
		} `json:"provider"`
		Agents []struct {
			Key            string  `json:"key"`
			State          string  `json:"state"`
			ExecutablePath *string `json:"executablePath"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(got, &health); err != nil {
		t.Fatal(err)
	}
	if health.Runtime.State != "available" || health.Provider.State != "unauthenticated" || !strings.Contains(health.Provider.Message, "not logged in") {
		t.Fatalf("health status = %s", got)
	}
	if len(health.Agents) != 2 || health.Agents[0].State != "available" || health.Agents[1].State != "available" {
		t.Fatalf("agent health = %s", got)
	}
	for _, path := range []*string{health.Runtime.ExecutablePath, health.Agents[0].ExecutablePath, health.Agents[1].ExecutablePath} {
		if path == nil || !filepath.IsAbs(*path) {
			t.Fatalf("resolved path is not absolute: %#v", path)
		}
	}
	for _, key := range []string{"tmux_executable_path", "gh_executable_path", "claude_executable_path", "codex_executable_path"} {
		value, err := runtime.store.Setting(key)
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if !filepath.IsAbs(value) {
			t.Fatalf("persisted %s is not absolute: %q", key, value)
		}
	}

	// Once persisted, configured absolute paths work even without a useful PATH.
	t.Setenv("PATH", "")
	got, err = service.Invoke("get_health_status", `{"provider":"github"}`)
	if err != nil || !strings.Contains(string(got), `"state":"unauthenticated"`) {
		t.Fatalf("health status from saved paths = %s, %v", got, err)
	}

	if err := os.WriteFile(filepath.Join(directory, "tmux"), []byte("#!/bin/sh\nprintf 'tmux is broken\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = service.Invoke("get_health_status", `{"provider":"none"}`)
	if err != nil || !strings.Contains(string(got), `"state":"unavailable"`) || !strings.Contains(string(got), "tmux is broken") {
		t.Fatalf("failed tmux check did not preserve stderr: %s, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nprintf 'network unavailable\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = service.Invoke("get_health_status", `{"provider":"github"}`)
	if err != nil || !strings.Contains(string(got), `"state":"unavailable"`) || !strings.Contains(string(got), "network unavailable") {
		t.Fatalf("non-authentication gh failure was not classified as unavailable: %s, %v", got, err)
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBindingsCommandsAreRegisteredOrPendingWithExactArgumentKeys(t *testing.T) {
	bindings, err := os.ReadFile(filepath.Join("..", "frontend", "src", "runtime", "bindings.ts"))
	if err != nil {
		t.Fatal(err)
	}
	pendingJSON, err := os.ReadFile("pending_commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var pending []string
	if err := json.Unmarshal(pendingJSON, &pending); err != nil {
		t.Fatal(err)
	}
	pendingSet := make(map[string]bool, len(pending))
	for _, command := range pending {
		if pendingSet[command] {
			t.Fatalf("duplicate pending command %q", command)
		}
		pendingSet[command] = true
	}

	commandPattern := regexp.MustCompile(`__TAURI_INVOKE<.*?>\("([a-z_]+)"(?:,\s*\{([^}]*)\})?\)`)
	declared := make(map[string][]string)
	for _, line := range strings.Split(string(bindings), "\n") {
		match := commandPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name := match[1]
		if _, exists := declared[name]; exists {
			t.Fatalf("duplicate binding command %q", name)
		}
		var args []string
		for _, argument := range strings.Split(match[2], ",") {
			key := strings.TrimSpace(strings.SplitN(argument, ":", 2)[0])
			if key != "" {
				args = append(args, key)
			}
		}
		sort.Strings(args)
		declared[name] = args
		registeredArgs, registered := registeredCommandArguments[name]
		if registered {
			if pendingSet[name] {
				t.Errorf("command %q is both registered and pending", name)
			}
			want := append([]string(nil), registeredArgs...)
			sort.Strings(want)
			if !equalStrings(args, want) {
				t.Errorf("command %q argument keys = %v, registered keys = %v", name, args, want)
			}
			continue
		}
		if !pendingSet[name] {
			t.Errorf("command %q is neither registered nor pending", name)
		}
	}
	for command := range pendingSet {
		if _, exists := declared[command]; !exists {
			t.Errorf("pending command %q is not declared in bindings.ts", command)
		}
	}
	if len(declared) != 107 {
		t.Errorf("found %d commands in bindings.ts, want 107", len(declared))
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func createContextsFixture(t *testing.T, databasePath string) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ddl, err := os.ReadFile(filepath.Join("persistence", "testdata", "tauri_schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(context.Background(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"next_context_id", "next_project_id", "next_item_id", "next_item_number", "next_repository_id", "next_workspace_id", "next_worktree_id", "next_machine_id", "next_cli_profile_id", "next_run_id", "next_external_object_id", "next_link_id", "next_activity_id", "next_reminder_id", "next_audit_id"} {
		if _, err = db.ExecContext(context.Background(), `INSERT INTO metadata(key,value) VALUES (?,1)`, key); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO contexts (
		id,name,check_dirty_checkouts,grill_agent,grill_model,grill_effort,implement_agent,implement_model,implement_effort,
		default_workflow,pstack_agent,pstack_model,pstack_effort,pstack_roles_json
	) VALUES (7,'Personal',1,'claude','claude-sonnet-5','high','codex','gpt-6-sol','medium','matt-pocock','claude','claude-opus-5','high','')`)
	if err != nil {
		t.Fatal(err)
	}
}

func jsonEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
