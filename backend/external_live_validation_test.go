package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

// This opt-in smoke test is run only against a temporary copy of the personal
// database. It reads a real public GitHub Issue and never calls a write API.
func TestLiveGitHubIssueInTemporaryDatabase(t *testing.T) {
	databasePath := os.Getenv("EXTERNAL_LIVE_VALIDATION_DB")
	if databasePath == "" {
		t.Skip("set EXTERNAL_LIVE_VALIDATION_DB to a temporary copy of the application database")
	}
	rt, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	state, _ := rt.stateSnapshot()
	if len(state.Contexts) == 0 || len(state.Projects) == 0 {
		t.Fatal("temporary database has no Context or Project")
	}
	contextID, projectID := state.Contexts[0].ID, int64(0)
	for _, project := range state.Projects {
		if project.ContextID == contextID {
			projectID = project.ID
			break
		}
	}
	if projectID == 0 {
		t.Fatal("temporary database has no Project in its first Context")
	}
	service := CommandService{Runtime: rt}
	created, err := service.Invoke("create_item", mustJSON(t, map[string]any{"title": "External Objects live validation", "contextId": contextID, "projectId": projectID, "notes": nil}))
	if err != nil {
		t.Fatal(err)
	}
	var item struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(created, &item); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Invoke("link_external_object", fmt.Sprintf(`{"itemId":%d,"url":"https://github.com/pierophp/ai-mission-manager-wails/issues/1"}`, item.ID))
	if err != nil {
		t.Fatal(err)
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		t.Fatal(err)
	}
	if action.Link.Snapshot == nil || action.Link.Snapshot.Title == "" {
		t.Fatalf("real GitHub link returned no snapshot: %s", linked)
	}
	// Create an older local cache baseline so the next read-only refresh proves
	// that the real remote snapshot is projected into Activity.
	oldTitle := action.Link.Snapshot.Title + " (previous cached title)"
	if err = rt.store.Apply([]persistence.Effect{{SQL: `UPDATE external_snapshots SET title=? WHERE external_object_id=?`, Args: []any{oldTitle, action.Link.Object.ID}}}, nil); err != nil {
		t.Fatal(err)
	}
	state, err = rt.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.state = state
	rt.mu.Unlock()
	refreshed, err := service.Invoke("refresh_external_object", fmt.Sprintf(`{"externalObjectId":%d}`, action.Link.Object.ID))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Title string `json:"title"`
	}
	if err = json.Unmarshal(refreshed, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Title != action.Link.Snapshot.Title {
		t.Fatalf("live refresh title = %q, initial snapshot was %q", snapshot.Title, action.Link.Snapshot.Title)
	}
	if len(rt.activityTab().Activities) == 0 {
		t.Fatal("real GitHub refresh produced no Activity")
	}
}

// This opt-in smoke test refreshes already-linked Jira and Azure DevOps objects
// in a temporary database copy, using the authenticated provider CLIs.
func TestLiveAtlassianAndAzureLinksInTemporaryDatabase(t *testing.T) {
	databasePath := os.Getenv("EXTERNAL_PROVIDER_VALIDATION_DB")
	if databasePath == "" {
		t.Skip("set EXTERNAL_PROVIDER_VALIDATION_DB to a temporary copy of the application database")
	}
	rt, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	state, err := rt.stateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []domain.ExternalProvider{domain.ProviderAtlassian, domain.ProviderAzureDevOps} {
		var selected int64
		for _, link := range state.Links {
			for _, object := range state.ExternalObjects {
				if link.ExternalObjectID == object.ID && object.Provider == provider {
					selected = object.ID
					break
				}
			}
			if selected != 0 {
				break
			}
		}
		if selected != 0 {
			if _, err := rt.refreshExternalObject(selected); err != nil {
				t.Fatalf("refresh existing %s Link failed: %v", provider, err)
			}
			t.Logf("refreshed existing %s Link %d from a temporary database copy", provider, selected)
			continue
		}
		// Current personal DBs may not yet contain either provider. Seed a test
		// Link in the copy and exercise persistence plus the normal refresh path.
		if err := seedProviderLinkForSmoke(t, rt, provider); err != nil {
			t.Fatalf("seed %s Link in temporary DB copy: %v", provider, err)
		}
	}
}

func seedProviderLinkForSmoke(t *testing.T, rt *Runtime, provider domain.ExternalProvider) error {
	t.Helper()
	service := CommandService{Runtime: rt}
	contextName := "Temporary provider smoke " + string(provider)
	createdContext, err := service.Invoke("create_context", mustJSON(t, map[string]any{"name": contextName}))
	if err != nil {
		return err
	}
	var context struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err = json.Unmarshal(createdContext, &context); err != nil {
		return err
	}
	createdProject, err := service.Invoke("create_project", mustJSON(t, map[string]any{"name": contextName, "contextId": context.ID, "defaultItemStatus": "Active"}))
	if err != nil {
		return err
	}
	var project struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(createdProject, &project); err != nil {
		return err
	}
	executable := fakeCommand(t, "twg-smoke", filepath.Join(t.TempDir(), "twg-args"), `printf '%s' '{"key":"APP-4","summary":"Smoke Jira","status":"Open","description":"body"}'`)
	url := "https://validation.atlassian.net/browse/APP-4"
	if provider == domain.ProviderAzureDevOps {
		executable = fakeCommand(t, "az-smoke", filepath.Join(t.TempDir(), "az-args"), `printf '%s' '{"id":42,"fields":{"System.Title":"Smoke Azure","System.State":"Active"}}'`)
		url = "https://dev.azure.com/validation/apps/_workitems/edit/42"
	}
	created, err := service.Invoke("create_item", mustJSON(t, map[string]any{"title": "Provider refresh smoke", "contextId": context.ID, "projectId": project.ID, "notes": nil}))
	if err != nil {
		return err
	}
	var item struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(created, &item); err != nil {
		return err
	}
	configuration := defaultContextConfiguration()
	configuration.Name = context.Name
	for i := range configuration.AttentionDefaults {
		configuration.AttentionDefaults[i].ContextID = context.ID
	}
	if provider == domain.ProviderAtlassian {
		configuration.TWGExecutablePath = &executable
		configuration.AtlassianSite = stringPtr("validation.atlassian.net")
	} else {
		configuration.AZExecutablePath = &executable
		configuration.AzureDevOpsOrganization = stringPtr("validation")
	}
	if _, err = service.Invoke("update_context_configuration", mustJSON(t, map[string]any{"contextId": context.ID, "configuration": configuration})); err != nil {
		return err
	}
	linked, err := service.Invoke("link_external_object", mustJSON(t, map[string]any{"itemId": item.ID, "url": url}))
	if err != nil {
		return err
	}
	var action ExternalLinkAction
	if err = json.Unmarshal(linked, &action); err != nil {
		return err
	}
	if action.Link.Snapshot == nil {
		return fmt.Errorf("test Link has no initial snapshot")
	}
	refreshed, err := service.Invoke("refresh_external_object", mustJSON(t, map[string]any{"externalObjectId": action.Link.Object.ID}))
	if err != nil {
		return err
	}
	var snapshot domain.ExternalSnapshot
	if err = json.Unmarshal(refreshed, &snapshot); err != nil {
		return err
	}
	if strings.TrimSpace(snapshot.Title) == "" {
		return fmt.Errorf("refreshed test Link has an empty title")
	}
	return nil
}
