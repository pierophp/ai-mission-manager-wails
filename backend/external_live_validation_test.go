package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

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
