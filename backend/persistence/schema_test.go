package persistence

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenCreatesCurrentSchemaAndSeeds(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "new.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	var contextCount, projectCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM contexts WHERE name = 'Personal'`).Scan(&contextCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM projects WHERE name = 'Default'`).Scan(&projectCount); err != nil {
		t.Fatal(err)
	}
	if contextCount != 1 || projectCount != 1 {
		t.Fatalf("seeds = Personal:%d Default:%d, want one each", contextCount, projectCount)
	}
	var foreignKeys int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var journalMode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "delete" {
		t.Fatalf("journal_mode = %q, want SQLite default delete", journalMode)
	}
}

func TestOpenRejectsLegacyWorksets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE worksets (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(path)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatalf("Open() error = %v, want ErrIncompatibleSchema", err)
	}
}

func TestRustFixtureAndNewDatabaseKeepIdenticalSchema(t *testing.T) {
	fixtureDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSQL, err := schemaFiles.ReadFile("testdata/tauri_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureDB.Exec(string(fixtureSQL)); err != nil {
		t.Fatalf("create Rust fixture: %v", err)
	}
	want := schemaSnapshot(t, fixtureDB)
	fixtureDB.Close()

	fixturePath := filepath.Join(t.TempDir(), "rust.sqlite")
	fixtureDB, err = sql.Open("sqlite", fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureDB.Exec(string(fixtureSQL)); err != nil {
		t.Fatal(err)
	}
	fixtureDB.Close()
	openedFixture, err := Open(fixturePath)
	if err != nil {
		t.Fatalf("open Rust fixture: %v", err)
	}
	if got := schemaSnapshot(t, openedFixture.db); !equalSchema(got, want) {
		t.Fatalf("opening Rust fixture changed sqlite_master\nwant: %v\ngot: %v", want, got)
	}
	openedFixture.Close()

	newStore, err := Open(filepath.Join(t.TempDir(), "go.sqlite"))
	if err != nil {
		t.Fatalf("open new Go database: %v", err)
	}
	defer newStore.Close()
	if got := schemaSnapshot(t, newStore.db); !equalSchema(got, want) {
		t.Fatalf("new Go database schema differs from Rust fixture\nwant: %v\ngot: %v", want, got)
	}
}

func schemaSnapshot(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ':' || name, sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, ddl string
		if err := rows.Scan(&key, &ddl); err != nil {
			t.Fatal(err)
		}
		result[key] = ddl
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func equalSchema(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, ddl := range left {
		if right[key] != ddl {
			return false
		}
	}
	return true
}

func TestOpenRealDatabaseCopyWithoutSchemaChanges(t *testing.T) {
	source := os.Getenv("AI_MISSION_MANAGER_TEST_DB")
	if source == "" {
		t.Skip("set AI_MISSION_MANAGER_TEST_DB to check a copy of a real Tauri database")
	}
	copyPath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeDB, err := sql.Open("sqlite", copyPath)
	if err != nil {
		t.Fatal(err)
	}
	before := schemaSnapshot(t, beforeDB)
	beforeDB.Close()

	store, err := Open(copyPath)
	if err != nil {
		t.Fatalf("open real database copy: %v", err)
	}
	defer store.Close()
	if after := schemaSnapshot(t, store.db); !equalSchema(after, before) {
		t.Fatalf("opening real database copy changed sqlite_master\nbefore: %v\nafter: %v", before, after)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("load real database copy: %v", err)
	}
}

func TestOpenRejectsSchemaBeforeGrillAndOldChecks(t *testing.T) {
	fixture, err := schemaFiles.ReadFile("testdata/tauri_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		ddl  string
	}{
		{name: "runs before grill", ddl: strings.ReplaceAll(string(fixture), "model TEXT", "legacy_model TEXT")},
		{name: "old execution profile check", ddl: strings.ReplaceAll(string(fixture), "'pstack-review'", "'review-old'")},
		{name: "old external object check", ddl: strings.ReplaceAll(string(fixture), "'azure_dev_ops'", "'azure' ")},
		{name: "old default execution mode check", ddl: strings.ReplaceAll(string(fixture), "'direct', 'worktree'", "'worktree'")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(test.ddl); err != nil {
				t.Fatalf("create old schema: %v", err)
			}
			db.Close()
			_, err = Open(path)
			if !errors.Is(err, ErrIncompatibleSchema) {
				t.Fatalf("Open() error = %v, want ErrIncompatibleSchema", err)
			}
			if !strings.Contains(err.Error(), "Tauri app once") {
				t.Fatalf("error = %q, want a Tauri migration instruction", err)
			}
		})
	}
}

func TestOpenDefaultCreatesFixedDatabasePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := os.Stat(filepath.Join(home, ".ai-mission-manager", "mission-manager.sqlite")); err != nil {
		t.Fatalf("fixed database file was not created: %v", err)
	}
}

func TestOpenAddsSupportedColumnsOnlyWhenMissing(t *testing.T) {
	fixture, err := schemaFiles.ReadFile("testdata/tauri_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "add-column.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE contexts DROP COLUMN gh_executable_path`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	columns, err := store.columns(t.Context(), "contexts")
	if err != nil {
		t.Fatal(err)
	}
	if !columns["gh_executable_path"] {
		t.Fatal("conditional column addition did not restore gh_executable_path")
	}
}

func TestOpenNormalizesModelIDsAndRaisesSequencesIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sequences.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO contexts (id, name, grill_model) VALUES (7, 'Legacy', 'codex-luna')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO projects (id, context_id, name, default_item_status) VALUES (12, 1, 'Extra', 'Inbox')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO items (id, human_identifier, title, project_id, status) VALUES (19, 'MC-19', 'Existing', 12, 'Inbox')`); err != nil {
		t.Fatal(err)
	}
	store.Close()

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var model string
	if err := store.db.QueryRow(`SELECT grill_model FROM contexts WHERE id = 7`).Scan(&model); err != nil {
		t.Fatal(err)
	}
	if model != "gpt-6-luna" {
		t.Fatalf("normalized grill model = %q, want gpt-6-luna", model)
	}
	for key, want := range map[string]int64{"next_context_id": 8, "next_project_id": 13, "next_item_id": 20} {
		var got int64
		if err := store.db.QueryRow(`SELECT value FROM metadata WHERE key = ?`, key).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
}
