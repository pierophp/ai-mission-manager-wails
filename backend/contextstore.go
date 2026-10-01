package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

// ContextReader is the read seam used by the command dispatcher.
type ContextReader interface {
	ListContexts() ([]Context, error)
}

// Runtime holds application state and coordinates commands across backend
// adapters. The first tracer operation reads Contexts from its persistence
// seam; later operations extend this shared runtime.
type Runtime struct {
	contexts                ContextReader
	mu                      sync.Mutex
	transitionMu            sync.Mutex
	state                   domain.DomainState
	store                   *persistence.Store
	events                  EventEmitter
	machineAccess           MachineAccess
	machineChecker          MachineCheckFunc
	machineReadiness        map[int64]MachineReadiness
	machineCheckGenerations map[int64]uint64
	pendingWorktreeRemovals map[int64]pendingWorktreeRemoval
	backgroundMu            sync.Mutex
	planUsageRefreshing     bool
	planUsageLastAttempt    int64
	catalogRefreshing       bool
	catalogLastAttempt      int64
}

func NewRuntime(contexts ContextReader) *Runtime {
	access := LocalSSHMachineAccess{}
	return newRuntime(contexts, nil, domain.DomainState{}, access)
}

// OpenRuntime opens a writable application Runtime backed by the shared store.
func OpenRuntime(path string) (*Runtime, error) {
	store, err := persistence.Open(path)
	if err != nil {
		return nil, err
	}
	state, err := store.Load()
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	access := LocalSSHMachineAccess{}
	runtime := newRuntime(nil, store, state, access)
	if err := runtime.recoverWorktreeRemovalIntents(); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	if err := runtime.ensureProjectWorkspaces(); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	return runtime, nil
}

func OpenDefaultRuntime() (*Runtime, error) {
	store, err := persistence.OpenDefault()
	if err != nil {
		return nil, err
	}
	state, err := store.Load()
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	access := LocalSSHMachineAccess{}
	runtime := newRuntime(nil, store, state, access)
	if err := runtime.recoverWorktreeRemovalIntents(); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	if err := runtime.ensureProjectWorkspaces(); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	return runtime, nil
}

func newRuntime(contexts ContextReader, store *persistence.Store, state domain.DomainState, access MachineAccess) *Runtime {
	tmux := TmuxTerminalRuntime{Access: access}
	return &Runtime{contexts: contexts, store: store, state: state, events: discardEventEmitter{}, machineAccess: access, machineChecker: tmux.CheckMachine, machineReadiness: map[int64]MachineReadiness{}, machineCheckGenerations: map[int64]uint64{}, pendingWorktreeRemovals: map[int64]pendingWorktreeRemoval{}}
}

// SetMachineAdapters replaces the local adapters, primarily for dispatcher
// tests that must not depend on SSH, tmux, or the host's installed tools.
func (r *Runtime) SetMachineAdapters(access MachineAccess, check MachineCheckFunc) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if access != nil {
		r.machineAccess = access
		tmux := TmuxTerminalRuntime{Access: access}
		r.machineChecker = tmux.CheckMachine
	}
	if check != nil {
		r.machineChecker = check
	}
}

func (r *Runtime) SetEventEmitter(emitter EventEmitter) {
	if r == nil {
		return
	}
	if emitter == nil {
		emitter = discardEventEmitter{}
	}
	r.mu.Lock()
	r.events = emitter
	r.mu.Unlock()
}

func (r *Runtime) EmitEvent(name string, payload any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	emitter := r.events
	r.mu.Unlock()
	if emitter != nil {
		emitter.Emit(name, payload)
	}
}

func (r *Runtime) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

func (r *Runtime) ListContexts() ([]Context, error) {
	if r == nil {
		return nil, fmt.Errorf("context reader is not configured")
	}
	if r.store != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		out := make([]Context, 0, len(r.state.Contexts))
		for _, value := range r.state.Contexts {
			out = append(out, projectContext(value))
		}
		return out, nil
	}
	if r.contexts == nil {
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
	store, err := persistence.OpenReadOnly(databasePath)
	if err != nil {
		return nil, fmt.Errorf("open Mission Manager database: %w", err)
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load Mission Manager state: %w", err)
	}
	contexts := make([]Context, 0, len(state.Contexts))
	for _, stored := range state.Contexts {
		contexts = append(contexts, projectContext(stored))
	}
	return contexts, nil
}

func projectContext(stored domain.Context) Context {
	context := Context{
		ID: stored.ID, Name: stored.Name, ExecutionMachineID: stored.ExecutionMachineID,
		ClaudeProfileID: stored.ClaudeProfileID, CodexProfileID: stored.CodexProfileID,
		CheckDirtyCheckouts: stored.CheckDirtyCheckouts,
		GrillDefaults:       GrillConfiguration{Agent: string(stored.GrillDefaults.Agent), Model: stored.GrillDefaults.Model, Effort: stored.GrillDefaults.Effort},
		ImplementDefaults:   GrillConfiguration{Agent: string(stored.ImplementDefaults.Agent), Model: stored.ImplementDefaults.Model, Effort: stored.ImplementDefaults.Effort},
		DefaultWorkflow:     string(stored.DefaultWorkflow),
		PstackDefaults:      GrillConfiguration{Agent: string(stored.PstackDefaults.Agent), Model: stored.PstackDefaults.Model, Effort: stored.PstackDefaults.Effort},
		GHExecutablePath:    stored.GHExecutablePath, TWGExecutablePath: stored.TWGExecutablePath, AZExecutablePath: stored.AZExecutablePath,
		AtlassianSite: stored.AtlassianSite, AzureDevOpsOrganization: stored.AzureDevOpsOrganization, BitbucketWorkspace: stored.BitbucketWorkspace,
	}
	for _, role := range stored.PstackRoles {
		context.PstackRoles = append(context.PstackRoles, PstackRoleSetting{Role: string(role.Role), Configuration: GrillConfiguration{Agent: string(role.Configuration.Agent), Model: role.Configuration.Model, Effort: role.Configuration.Effort}})
	}
	if context.PstackRoles == nil {
		context.PstackRoles = []PstackRoleSetting{}
	}
	return context
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
