package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

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
	argumentPattern := regexp.MustCompile(`([A-Za-z][A-Za-z0-9]*):`)
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
		for _, argument := range argumentPattern.FindAllStringSubmatch(match[2], -1) {
			args = append(args, argument[1])
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
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE contexts (
			id INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL,
			execution_machine_id INTEGER, check_dirty_checkouts INTEGER NOT NULL,
			grill_agent TEXT NOT NULL, grill_model TEXT NOT NULL, grill_effort TEXT NOT NULL,
			implement_agent TEXT NOT NULL, implement_model TEXT NOT NULL, implement_effort TEXT NOT NULL,
			default_workflow TEXT NOT NULL, pstack_agent TEXT NOT NULL, pstack_model TEXT NOT NULL,
			pstack_effort TEXT NOT NULL, pstack_roles_json TEXT NOT NULL,
			claude_profile_id INTEGER, codex_profile_id INTEGER,
			gh_executable_path TEXT, twg_executable_path TEXT, az_executable_path TEXT,
			atlassian_site TEXT, azure_devops_organization TEXT, bitbucket_workspace TEXT
		);
		INSERT INTO contexts VALUES (
			7, 'Personal', NULL, 1, 'claude', 'claude-sonnet-5', 'high',
			'codex', 'gpt-6-sol', 'medium', 'matt-pocock', 'claude',
			'claude-opus-5', 'high', '', NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL
		);`)
	if err != nil {
		t.Fatal(err)
	}
}

func jsonEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
