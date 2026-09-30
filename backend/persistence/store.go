package persistence

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql testdata/tauri_schema.sql
var schemaFiles embed.FS

var ErrIncompatibleSchema = errors.New("database schema is incompatible; open the Tauri app once to update it, then try again")

// Store owns the single SQLite connection used by persistence.
type Store struct {
	db *sql.DB
}

// Open opens a database at path and creates the current schema for a new file.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	store := &Store{db: db}
	if err := store.initialize(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// OpenDefault opens ~/.ai-mission-manager/mission-manager.sqlite.
func OpenDefault() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}
	return Open(filepath.Join(home, ".ai-mission-manager", "mission-manager.sqlite"))
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) initialize(ctx context.Context) error {
	var tableCount int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		ddl, err := schemaFiles.ReadFile("schema.sql")
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, string(ddl)); err != nil {
			return fmt.Errorf("create SQLite schema: %w", err)
		}
		return s.seedNewDatabase(ctx)
	}
	if err := s.rejectIncompatibleSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureCompatibleColumns(ctx); err != nil {
		return err
	}
	if err := s.validateCurrentSchema(ctx); err != nil {
		return err
	}
	if err := s.insertMissingSequences(ctx); err != nil {
		return err
	}
	if err := s.normalizeModelIDs(ctx); err != nil {
		return err
	}
	return s.ensureSequences(ctx)
}

func (s *Store) seedNewDatabase(ctx context.Context) error {
	for key, value := range map[string]int64{
		"next_context_id": 1, "next_project_id": 1, "next_item_id": 1,
		"next_item_number": 1, "next_repository_id": 1, "next_workspace_id": 1,
		"next_worktree_id": 1, "next_machine_id": 1, "next_cli_profile_id": 1,
		"next_run_id": 1, "next_external_object_id": 1, "next_link_id": 1,
		"next_activity_id": 1, "next_reminder_id": 1, "next_audit_id": 1,
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO metadata (key, value) VALUES (?, ?)`, key, value); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO contexts (id, name) VALUES (1, 'Personal')`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO projects (id, context_id, name, default_item_status) VALUES (1, 1, 'Default', 'Inbox')`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE metadata SET value = 2 WHERE key IN ('next_context_id', 'next_project_id')`)
	return err
}

func (s *Store) rejectIncompatibleSchema(ctx context.Context) error {
	var worksetCount int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('worksets', 'workset_repositories')`).Scan(&worksetCount); err != nil {
		return err
	}
	if worksetCount > 0 {
		return ErrIncompatibleSchema
	}
	for table, required := range map[string][]string{
		"runs":                 {"model"},
		"items":                {"project_id", "notes"},
		"link_attention_state": {"watch_until", "review_at"},
	} {
		columns, err := s.columns(ctx, table)
		if err != nil {
			return ErrIncompatibleSchema
		}
		for _, column := range required {
			if !columns[column] {
				return ErrIncompatibleSchema
			}
		}
	}
	for _, check := range []struct {
		table  string
		values []string
	}{
		{"projects", []string{"Inbox", "Active", "Waiting", "Done"}},
		{"items", []string{"Inbox", "Active", "Waiting", "Done"}},
		{"runs", []string{"claude", "codex", "investigate", "implement", "review", "custom", "grill", "autonomous", "plan", "pstack-review"}},
		{"item_relationships", []string{"blocks", "blocked_by", "related_to"}},
		{"cli_configuration_profiles", []string{"claude", "codex"}},
		{"external_objects", []string{"github", "atlassian", "azure_dev_ops", "generic", "issue", "pull_request", "document"}},
		{"context_attention_defaults", []string{"issue", "pull_request", "document", "generic"}},
	} {
		var ddl string
		if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, check.table).Scan(&ddl); err != nil {
			return ErrIncompatibleSchema
		}
		for _, value := range check.values {
			if !strings.Contains(ddl, "'"+value+"'") {
				return ErrIncompatibleSchema
			}
		}
	}
	var projectsDDL string
	if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'projects'`).Scan(&projectsDDL); err != nil {
		return ErrIncompatibleSchema
	}
	if field := strings.Index(projectsDDL, "default_execution_mode"); field >= 0 {
		// Older Tauri databases got this column through ALTER TABLE, which did not
		// add a CHECK. Preserve those schemas, but reject an incomplete CHECK when
		// one is present, as required for schemas created from the current DDL.
		definition := projectsDDL[field:]
		if strings.Contains(definition, "CHECK") &&
			(!strings.Contains(definition, "'direct'") || !strings.Contains(definition, "'worktree'")) {
			return ErrIncompatibleSchema
		}
	}
	return nil
}

func (s *Store) ensureCompatibleColumns(ctx context.Context) error {
	for _, column := range []struct{ table, name, definition string }{
		{"contexts", "execution_machine_id", "INTEGER"},
		{"contexts", "check_dirty_checkouts", "INTEGER NOT NULL DEFAULT 1"},
		{"contexts", "grill_agent", "TEXT NOT NULL DEFAULT 'claude'"},
		{"contexts", "grill_model", "TEXT NOT NULL DEFAULT 'claude-sonnet-5'"},
		{"contexts", "grill_effort", "TEXT NOT NULL DEFAULT 'high'"},
		{"contexts", "implement_agent", "TEXT NOT NULL DEFAULT 'claude'"},
		{"contexts", "implement_model", "TEXT NOT NULL DEFAULT 'claude-sonnet-5'"},
		{"contexts", "implement_effort", "TEXT NOT NULL DEFAULT 'high'"},
		{"contexts", "default_workflow", "TEXT NOT NULL DEFAULT 'matt-pocock'"},
		{"contexts", "pstack_agent", "TEXT NOT NULL DEFAULT 'claude'"},
		{"contexts", "pstack_model", "TEXT NOT NULL DEFAULT 'claude-sonnet-5'"},
		{"contexts", "pstack_effort", "TEXT NOT NULL DEFAULT 'high'"},
		{"contexts", "pstack_roles_json", "TEXT NOT NULL DEFAULT ''"},
		{"contexts", "claude_profile_id", "INTEGER"}, {"contexts", "codex_profile_id", "INTEGER"},
		{"contexts", "gh_executable_path", "TEXT"}, {"contexts", "twg_executable_path", "TEXT"},
		{"contexts", "az_executable_path", "TEXT"}, {"contexts", "atlassian_site", "TEXT"},
		{"contexts", "azure_devops_organization", "TEXT"}, {"contexts", "bitbucket_workspace", "TEXT"},
		{"projects", "default_execution_mode", "TEXT NOT NULL DEFAULT 'worktree'"},
		{"repositories", "base_branch", "TEXT NOT NULL DEFAULT 'main'"},
		{"machines", "transport_json", `TEXT NOT NULL DEFAULT '{"kind":"local"}'`},
		{"machines", "last_observed", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"machines", "last_observed_at", "INTEGER"},
		{"workspaces", "preparation_state", "TEXT NOT NULL DEFAULT 'pending'"},
		{"link_attention_state", "provenance_json", "TEXT"},
		{"external_links", "purpose", "TEXT NOT NULL DEFAULT 'others'"},
		{"external_links", "spec_external_object_id", "INTEGER REFERENCES external_objects(id) ON DELETE SET NULL"},
		{"runs", "workflow", "TEXT NOT NULL DEFAULT 'matt-pocock'"},
		{"runs", "state", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"runs", "last_applied_agent_state_sequence", "INTEGER"},
		{"runs", "cli_configuration_profile_json", "TEXT"},
		{"runs", "pane_status", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"runs", "direct_checkouts_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"runs", "transcript", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "reported_pull_requests_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"runs", "attention_summary", "TEXT"},
		{"runs", "grill_question_group_json", "TEXT"},
		{"runs", "grill_answers_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"runs", "grill_decisions_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"runs", "grill_response", "TEXT"}, {"runs", "grill_phase", "TEXT"},
		{"runs", "grill_action", "TEXT"}, {"runs", "grill_action_started_at", "INTEGER"},
		{"runs", "repository_id", "INTEGER"}, {"runs", "worktree_id", "INTEGER"},
		{"runs", "plan_phase", "TEXT"}, {"runs", "plan_path", "TEXT"},
	} {
		columns, err := s.columns(ctx, column.table)
		if err != nil {
			return ErrIncompatibleSchema
		}
		if !columns[column.name] {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("add SQLite column %s.%s: %w", column.table, column.name, err)
			}
		}
	}
	return nil
}

func (s *Store) validateCurrentSchema(ctx context.Context) error {
	for _, table := range []string{
		"metadata", "settings", "contexts", "projects", "items", "repositories", "machines",
		"cli_configuration_profiles", "repository_locations", "workspaces", "workspace_repositories",
		"worktrees", "runs", "implementation_queues", "reminders", "item_relationships",
		"external_objects", "external_links", "external_snapshots", "link_attention_state",
		"activities", "context_attention_defaults", "audit_entries",
	} {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrIncompatibleSchema
		}
	}
	for table, required := range map[string][]string{
		"runs":     {"model", "grill_phase", "plan_phase"},
		"contexts": {"grill_model", "implement_model", "pstack_model"},
	} {
		columns, err := s.columns(ctx, table)
		if err != nil {
			return err
		}
		for _, column := range required {
			if !columns[column] {
				return ErrIncompatibleSchema
			}
		}
	}
	return nil
}

func (s *Store) columns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (s *Store) insertMissingSequences(ctx context.Context) error {
	for _, key := range []string{"next_context_id", "next_project_id", "next_item_id", "next_item_number", "next_repository_id", "next_workspace_id", "next_worktree_id", "next_machine_id", "next_cli_profile_id", "next_run_id", "next_external_object_id", "next_link_id", "next_activity_id", "next_reminder_id", "next_audit_id"} {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO metadata (key, value) VALUES (?, 1)`, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) normalizeModelIDs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE contexts SET grill_model = CASE grill_model
        WHEN 'claude-opus-4-1' THEN 'claude-opus-5'
        WHEN 'codex-sol' THEN 'gpt-6-sol'
        WHEN 'codex-terra' THEN 'gpt-6-sol'
        WHEN 'codex-luna' THEN 'gpt-6-luna'
        ELSE grill_model END
        WHERE grill_model IN ('claude-opus-4-1', 'codex-sol', 'codex-terra', 'codex-luna')`)
	return err
}

func (s *Store) ensureSequences(ctx context.Context) error {
	for _, pair := range [][2]string{{"next_context_id", "contexts"}, {"next_project_id", "projects"}, {"next_item_id", "items"}, {"next_repository_id", "repositories"}, {"next_workspace_id", "workspaces"}, {"next_worktree_id", "worktrees"}, {"next_machine_id", "machines"}, {"next_run_id", "runs"}, {"next_external_object_id", "external_objects"}, {"next_link_id", "external_links"}, {"next_activity_id", "activities"}, {"next_reminder_id", "reminders"}, {"next_audit_id", "audit_entries"}} {
		query := `UPDATE metadata SET value = MAX(value, (SELECT COALESCE(MAX(id), 0) + 1 FROM ` + pair[1] + `)) WHERE key = ?`
		if _, err := s.db.ExecContext(ctx, query, pair[0]); err != nil {
			return err
		}
	}
	return nil
}
