package backend

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// ContextReader is the read seam used by the command dispatcher.
type ContextReader interface {
	ListContexts() ([]Context, error)
}

// Runtime holds application state and coordinates commands across backend
// adapters. The first tracer operation reads Contexts from its persistence
// seam; later operations extend this shared runtime.
type Runtime struct {
	contexts ContextReader
}

func NewRuntime(contexts ContextReader) *Runtime {
	return &Runtime{contexts: contexts}
}

func (r *Runtime) ListContexts() ([]Context, error) {
	if r == nil || r.contexts == nil {
		return nil, fmt.Errorf("context reader is not configured")
	}
	return r.contexts.ListContexts()
}

// SQLiteContextReader reads the Tauri-compatible Context projection from an
// existing Mission Manager database without initializing or mutating it.
type SQLiteContextReader struct {
	DatabasePath string
}

func (r SQLiteContextReader) ListContexts() ([]Context, error) {
	databasePath := r.DatabasePath
	if databasePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home: %w", err)
		}
		databasePath = filepath.Join(home, ".ai-mission-manager", "mission-manager.sqlite")
	}
	if _, err := os.Stat(databasePath); err != nil {
		return nil, fmt.Errorf("open Mission Manager database %q: %w", databasePath, err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(databasePath)}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open Mission Manager database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows, err := db.Query(`
		SELECT id, name, execution_machine_id, check_dirty_checkouts,
		       grill_agent, grill_model, grill_effort,
		       implement_agent, implement_model, implement_effort,
		       default_workflow, pstack_agent, pstack_model, pstack_effort,
		       claude_profile_id, codex_profile_id,
		       gh_executable_path, twg_executable_path, az_executable_path,
		       atlassian_site, azure_devops_organization, bitbucket_workspace,
		       pstack_roles_json
		FROM contexts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list contexts: %w", err)
	}
	defer rows.Close()
	contexts := make([]Context, 0)
	for rows.Next() {
		var context Context
		var checkDirty int64
		var grillAgent, implementAgent, workflow, pstackAgent string
		var pstackRolesJSON string
		if err := rows.Scan(
			&context.ID, &context.Name, &context.ExecutionMachineID, &checkDirty,
			&grillAgent, &context.GrillDefaults.Model, &context.GrillDefaults.Effort,
			&implementAgent, &context.ImplementDefaults.Model, &context.ImplementDefaults.Effort,
			&workflow, &pstackAgent, &context.PstackDefaults.Model, &context.PstackDefaults.Effort,
			&context.ClaudeProfileID, &context.CodexProfileID,
			&context.GHExecutablePath, &context.TWGExecutablePath, &context.AZExecutablePath,
			&context.AtlassianSite, &context.AzureDevOpsOrganization, &context.BitbucketWorkspace,
			&pstackRolesJSON,
		); err != nil {
			return nil, fmt.Errorf("read context row: %w", err)
		}
		context.CheckDirtyCheckouts = checkDirty != 0
		context.GrillDefaults.Agent = grillAgent
		context.ImplementDefaults.Agent = implementAgent
		context.DefaultWorkflow = workflow
		context.PstackDefaults.Agent = pstackAgent
		if strings.TrimSpace(pstackRolesJSON) == "" {
			context.PstackRoles = defaultPstackRoles()
		} else if err := json.Unmarshal([]byte(pstackRolesJSON), &context.PstackRoles); err != nil {
			return nil, fmt.Errorf("decode context pstack_roles_json: %w", err)
		}
		contexts = append(contexts, context)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read contexts: %w", err)
	}
	return contexts, nil
}

type Context struct {
	ID                      int64               `json:"id"`
	Name                    string              `json:"name"`
	ExecutionMachineID      *int64              `json:"execution_machine_id"`
	ClaudeProfileID         *int64              `json:"claude_profile_id"`
	CodexProfileID          *int64              `json:"codex_profile_id"`
	CheckDirtyCheckouts     bool                `json:"check_dirty_checkouts"`
	GrillDefaults           GrillConfiguration  `json:"grill_defaults"`
	ImplementDefaults       GrillConfiguration  `json:"implement_defaults"`
	DefaultWorkflow         string              `json:"default_workflow"`
	PstackDefaults          GrillConfiguration  `json:"pstack_defaults"`
	PstackRoles             []PstackRoleSetting `json:"pstack_roles"`
	GHExecutablePath        *string             `json:"gh_executable_path"`
	TWGExecutablePath       *string             `json:"twg_executable_path"`
	AZExecutablePath        *string             `json:"az_executable_path"`
	AtlassianSite           *string             `json:"atlassian_site"`
	AzureDevOpsOrganization *string             `json:"azure_devops_organization"`
	BitbucketWorkspace      *string             `json:"bitbucket_workspace"`
}

type GrillConfiguration struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

type PstackRoleSetting struct {
	Role          string             `json:"role"`
	Configuration GrillConfiguration `json:"configuration"`
}

func defaultPstackRoles() []PstackRoleSetting {
	return []PstackRoleSetting{
		{Role: "code-delegate", Configuration: GrillConfiguration{Agent: "claude", Model: "claude-opus-5", Effort: "high"}},
		{Role: "judge-and-prose", Configuration: GrillConfiguration{Agent: "codex", Model: "gpt-6-sol", Effort: "high"}},
		{Role: "review-panel", Configuration: GrillConfiguration{Agent: "codex", Model: "gpt-6-sol", Effort: "high"}},
		{Role: "explorers", Configuration: GrillConfiguration{Agent: "claude", Model: "claude-sonnet-5", Effort: "medium"}},
	}
}
