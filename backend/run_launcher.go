package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/gitcli"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
	"github.com/piero/ai-mission-manager-wails/backend/pstack"
)

type runLaunchRequest struct {
	ItemID      int64             `json:"itemId"`
	WorkspaceID int64             `json:"workspaceId"`
	Strategy    runLaunchStrategy `json:"strategy"`
}
type runLaunchStrategy struct {
	Kind                 string                     `json:"kind"`
	MachineID            *int64                     `json:"machineId"`
	PrimaryRepositoryID  int64                      `json:"primaryRepositoryId"`
	Agent                domain.AgentKind           `json:"agent"`
	Configuration        *domain.GrillConfiguration `json:"configuration"`
	ExecutionProfile     domain.ExecutionProfile    `json:"executionProfile"`
	Workflow             domain.Workflow            `json:"workflow"`
	Prompt               string                     `json:"prompt"`
	PromptSelection      domain.RunPromptSelection  `json:"promptSelection"`
	ExpectedCheckouts    []domain.RunCheckout       `json:"expectedCheckouts"`
	AllowDirty           bool                       `json:"allowDirty"`
	AllowSharedCheckouts bool                       `json:"allowSharedCheckouts"`
	WorktreeID           int64                      `json:"worktreeId"`
}

// UnmarshalJSON accepts the snake_case strategy nested in the existing
// generated frontend binding as well as the camelCase request used by Go
// callers. The top-level command still validates unknown argument keys.
func (s *runLaunchStrategy) UnmarshalJSON(data []byte) error {
	type camelCaseStrategy runLaunchStrategy
	var strategy camelCaseStrategy
	if err := json.Unmarshal(data, &strategy); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{
		"kind": true, "machineId": true, "machine_id": true,
		"primaryRepositoryId": true, "primary_repository_id": true,
		"agent": true, "configuration": true, "implementation_queue": true,
		"executionProfile": true, "execution_profile": true,
		"workflow": true, "prompt": true, "promptSelection": true,
		"prompt_selection": true, "expectedCheckouts": true,
		"expected_checkouts": true, "allowDirty": true, "allow_dirty": true,
		"allowSharedCheckouts": true, "allow_shared_checkouts": true,
		"worktreeId": true, "worktree_id": true, "language": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown launch strategy field %q", key)
		}
	}
	if err := unmarshalLaunchStrategyAlias(fields, "machine_id", &strategy.MachineID); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "primary_repository_id", &strategy.PrimaryRepositoryID); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "execution_profile", &strategy.ExecutionProfile); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "prompt_selection", &strategy.PromptSelection); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "expected_checkouts", &strategy.ExpectedCheckouts); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "allow_dirty", &strategy.AllowDirty); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "allow_shared_checkouts", &strategy.AllowSharedCheckouts); err != nil {
		return err
	}
	if err := unmarshalLaunchStrategyAlias(fields, "worktree_id", &strategy.WorktreeID); err != nil {
		return err
	}
	if raw := fields["implementation_queue"]; len(raw) > 0 && string(raw) != "null" {
		return errors.New("implementation_queue is not supported by this launcher")
	}
	*s = runLaunchStrategy(strategy)
	return nil
}

func unmarshalLaunchStrategyAlias[T any](fields map[string]json.RawMessage, key string, target *T) error {
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(raw, target)
}

type DirectRunCheckoutPreview struct {
	RepositoryID   int64  `json:"repositoryId"`
	RepositoryName string `json:"repositoryName"`
	Path           string `json:"path"`
	Branch         string `json:"branch"`
	IsDirty        bool   `json:"isDirty"`
}
type DirectRunSharedRun struct {
	RunID  int64  `json:"runId"`
	ItemID int64  `json:"itemId"`
	Path   string `json:"path"`
}
type DirectRunPreview struct {
	WorkspaceID        int64                      `json:"workspaceId"`
	MachineID          int64                      `json:"machineId"`
	MachineName        string                     `json:"machineName"`
	WorkingDirectory   string                     `json:"workingDirectory"`
	Checkouts          []domain.RunCheckout       `json:"checkouts"`
	CheckoutDetails    []DirectRunCheckoutPreview `json:"checkoutDetails"`
	CurrentBranches    []string                   `json:"currentBranches"`
	DirtyRepositoryIDs []int64                    `json:"dirtyRepositoryIds"`
	SharedRuns         []DirectRunSharedRun       `json:"sharedRuns"`
	SharedPaths        []string                   `json:"sharedPaths"`
}

type agentRunExecutor interface {
	Preflight(domain.Machine, domain.AgentKind, int64, *domain.CLIConfigurationProfile) (executable, stateFile, profileDirectory string, readiness MachineReadiness, err error)
	Launch(domain.Machine, string, string, string, string, string, domain.AgentKind, string, string, string, string, string) (string, error)
	Release(domain.Machine, string) error
	Kill(domain.Machine, string) error
}

type tmuxAgentRunExecutor struct{ access MachineAccess }

func (e tmuxAgentRunExecutor) accessOrDefault() MachineAccess {
	if e.access != nil {
		return e.access
	}
	return LocalSSHMachineAccess{}
}

func (e tmuxAgentRunExecutor) Preflight(machine domain.Machine, agent domain.AgentKind, runID int64, profile *domain.CLIConfigurationProfile) (string, string, string, MachineReadiness, error) {
	a := e.accessOrDefault()
	readiness := MachineReadiness{}
	home, err := a.HomeDirectory(machine)
	if err != nil {
		return "", "", "", readiness, fmt.Errorf("Could not reach Machine %s: %w", machine.Name, err)
	}
	setBool(&readiness.Reachable, true)
	if _, err := a.FindExecutable(machine, "bun"); err != nil {
		setBool(&readiness.BunAvailable, false)
		setString(&readiness.BunError, "bun is not installed on Machine "+machine.Name)
	} else {
		setBool(&readiness.BunAvailable, true)
	}
	if _, err := a.RunShell(machine, "tmux -V"); err != nil {
		setBool(&readiness.TmuxAvailable, false)
		return "", "", "", readiness, fmt.Errorf("Machine %s does not have a working tmux runtime: %w", machine.Name, err)
	}
	setBool(&readiness.TmuxAvailable, true)
	executable, err := a.FindExecutable(machine, string(agent))
	if err != nil {
		if agent == domain.AgentClaude {
			setBool(&readiness.ClaudeExecutableResolved, false)
		} else {
			setBool(&readiness.CodexExecutableResolved, false)
		}
		return "", "", "", readiness, fmt.Errorf("%s executable is not installed on Machine %s", agent, machine.Name)
	}
	if agent == domain.AgentClaude {
		setBool(&readiness.ClaudeExecutableResolved, true)
	} else {
		setBool(&readiness.CodexExecutableResolved, true)
	}
	profileDirectory := ""
	if profile != nil {
		if profile.Provider != agent {
			return "", "", "", readiness, errors.New("Selected CLI configuration profile belongs to another provider")
		}
		profileDirectory, err = a.ResolvePath(machine, profile.Directory)
		if err != nil {
			return "", "", "", readiness, err
		}
		if a.IsLocal(machine) {
			info, e := os.Stat(profileDirectory)
			if e != nil || !info.IsDir() {
				return "", "", "", readiness, fmt.Errorf("Selected CLI configuration profile directory is unavailable: %s", profileDirectory)
			}
		}
		command, envName, authArgs, credentials := profileAuthCommand(agent, executable, profileDirectory)
		if _, err := a.RunShell(machine, command); err != nil {
			return "", "", "", readiness, fmt.Errorf("Selected CLI profile is not signed in or usable at %s: %w", profileDirectory, err)
		}
		_ = envName
		_ = authArgs
		_ = credentials
	}
	stateDirectory := filepath.Join(home, ".local", "state", "ai-mission-manager", "runs")
	if a.IsLocal(machine) {
		if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
			return "", "", "", readiness, err
		}
		probe, err := os.CreateTemp(stateDirectory, ".preflight-*")
		if err != nil {
			return "", "", "", readiness, fmt.Errorf("Agent state directory is not writable on Machine %s: %w", machine.Name, err)
		}
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	} else if _, err := a.RunShell(machine, `set -eu; state_dir="$HOME/.local/state/ai-mission-manager/runs"; mkdir -p -- "$state_dir"; probe=$(mktemp "$state_dir/.preflight.XXXXXXXX"); rm -f -- "$probe"`); err != nil {
		return "", "", "", readiness, fmt.Errorf("Agent state directory is not writable on Machine %s: %w", machine.Name, err)
	}
	setBool(&readiness.StateDirectoryWritable, true)
	if err := installAgentHooks(a, machine, home, agent, profileDirectory); err != nil {
		message := err.Error()
		setString(&readiness.LastProvisioningError, message)
		setString(&readiness.Error, message)
		return "", "", "", readiness, fmt.Errorf("Could not provision agent-state hooks: %w", err)
	}
	provisioned, current := true, true
	if agent == domain.AgentClaude {
		readiness.ClaudeHooks = AgentHookReadiness{Provisioned: &provisioned, Current: &current}
	} else {
		readiness.CodexHooks = AgentHookReadiness{Provisioned: &provisioned, Current: &current}
	}
	stateFile := filepath.Join(home, ".local", "state", "ai-mission-manager", "runs", fmt.Sprintf("run-%d.json", runID))
	return executable, stateFile, profileDirectory, readiness, nil
}

func profileAuthCommand(agent domain.AgentKind, executable, directory string) (string, string, []string, []string) {
	name, args, credentials := "CLAUDE_CONFIG_DIR", []string{"auth", "status"}, []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"}
	if agent == domain.AgentCodex {
		name, args, credentials = "CODEX_HOME", []string{"login", "status"}, []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_FEDERATION_RULE_ID", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_WORKLOAD_IDENTITY_CONTEXT"}
	}
	parts := []string{"set -eu"}
	for _, key := range credentials {
		parts = append(parts, "unset "+key)
	}
	parts = append(parts, "export "+name+"="+shellQuote(directory))
	invocation := []string{shellQuote(executable)}
	for _, arg := range args {
		invocation = append(invocation, shellQuote(arg))
	}
	parts = append(parts, "exec "+strings.Join(invocation, " "))
	return strings.Join(parts, "; "), name, args, credentials
}

func (e tmuxAgentRunExecutor) Launch(machine domain.Machine, session, gate, cwd, executable, prompt string, agent domain.AgentKind, runIDString, stateFile, model, effort, profileDirectory string) (string, error) {
	if err := validateTmuxTarget(session); err != nil {
		return "", err
	}
	if err := validateTmuxTarget(gate); err != nil {
		return "", err
	}
	access := e.accessOrDefault()
	command, err := buildGatedAgentCommand(machine, gate, executable, prompt, agent, runIDString, stateFile, model, effort, profileDirectory)
	if err != nil {
		return "", err
	}
	_, err = access.RunShell(machine, "tmux -f /dev/null -L "+shellQuote(machine.SocketName)+" new-session -d -s "+shellQuote(session)+" -c "+shellQuote(cwd)+" sh -lc "+shellQuote(command))
	if err != nil {
		return "", fmt.Errorf("Could not start agent Pane: %w", err)
	}
	pane, err := access.RunShell(machine, "tmux -f /dev/null -L "+shellQuote(machine.SocketName)+" display-message -p -t "+shellQuote(session+":0.0")+" #{pane_id}")
	pane = strings.TrimSpace(pane)
	if err != nil || !strings.HasPrefix(pane, "%") {
		_ = e.Kill(machine, session)
		if err != nil {
			return "", fmt.Errorf("Could not identify the agent Pane: %w", err)
		}
		return "", fmt.Errorf("tmux returned an invalid Pane identity: %s", pane)
	}
	status, err := access.RunShell(machine, "tmux -f /dev/null -L "+shellQuote(machine.SocketName)+" list-panes -t "+shellQuote(session+":0.0")+" -F '#{pane_id}|#{pane_dead}'")
	if err != nil || strings.TrimSpace(status) != pane+"|0" {
		_ = e.Kill(machine, session)
		return "", errors.New("the agent Pane exited before it could be recorded")
	}
	return pane, nil
}

func buildGatedAgentCommand(machine domain.Machine, gate, executable, prompt string, agent domain.AgentKind, runID, stateFile, model, effort, profileDirectory string) (string, error) {
	if err := validateTmuxTarget(machine.SocketName); err != nil {
		return "", err
	}
	if err := validateTmuxTarget(gate); err != nil {
		return "", err
	}
	title := string(agent)
	command := `tmux -f /dev/null -L ` + shellQuote(machine.SocketName) + ` select-pane -T ` + shellQuote(title) + ` -t "$TMUX_PANE" && tmux -f /dev/null -L ` + shellQuote(machine.SocketName) + ` wait-for ` + shellQuote(gate) + ` && export AI_MISSION_MANAGER_RUN_ID=` + shellQuote(runID) + ` AI_MISSION_MANAGER_STATE_FILE=` + shellQuote(stateFile) + ` AI_MISSION_MANAGER_TMUX_PATH=tmux AI_MISSION_MANAGER_TMUX_SOCKET=` + shellQuote(machine.SocketName) + ` AI_MISSION_MANAGER_PANE_ID="$TMUX_PANE"`
	credentials := credentialVariables(agent)
	command += ` && unset ` + strings.Join(credentials, " ")
	homeEnv := "CLAUDE_CONFIG_DIR"
	if agent == domain.AgentCodex {
		homeEnv = "CODEX_HOME"
	}
	if profileDirectory != "" {
		command += ` && export ` + homeEnv + `=` + shellQuote(profileDirectory)
	} else {
		command += ` && unset ` + homeEnv
	}
	command += ` && exec ` + shellQuote(executable)
	if agent == domain.AgentCodex {
		command += ` exec`
	}
	if model != "" {
		command += ` --model ` + shellQuote(model)
	}
	if effort != "" {
		if agent == domain.AgentClaude {
			command += ` --effort ` + shellQuote(effort)
		} else {
			command += ` -c ` + shellQuote("model_reasoning_effort="+effort)
		}
	}
	command += ` ` + shellQuote(prompt)
	return command, nil
}
func credentialVariables(agent domain.AgentKind) []string {
	if agent == domain.AgentCodex {
		return []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_FEDERATION_RULE_ID", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_WORKLOAD_IDENTITY_CONTEXT"}
	}
	return []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"}
}
func (e tmuxAgentRunExecutor) Release(machine domain.Machine, gate string) error {
	_, err := e.accessOrDefault().RunShell(machine, "tmux -f /dev/null -L "+shellQuote(machine.SocketName)+" wait-for -S "+shellQuote(gate))
	return err
}
func (e tmuxAgentRunExecutor) Kill(machine domain.Machine, session string) error {
	_, err := e.accessOrDefault().RunShell(machine, "tmux -f /dev/null -L "+shellQuote(machine.SocketName)+" kill-session -t "+shellQuote(session))
	return err
}

var _ agentRunExecutor = tmuxAgentRunExecutor{}

func (r *Runtime) launchRun(request runLaunchRequest) (domain.Run, error) {
	if r == nil || r.store == nil {
		return domain.Run{}, errors.New("runtime is not configured")
	}
	// The separate launch lock serializes IDs while the shared state mutex is held only for snapshots and commit.
	r.runLaunchMu.Lock()
	defer r.runLaunchMu.Unlock()
	state, err := r.stateSnapshot()
	if err != nil {
		return domain.Run{}, err
	}
	strategy := request.Strategy
	item, ok := launchItemByID(state, request.ItemID)
	if !ok {
		return domain.Run{}, fmt.Errorf("Item %d does not exist", request.ItemID)
	}
	project, ok := launchProjectByID(state, item.ProjectID)
	if !ok {
		return domain.Run{}, fmt.Errorf("Project %d does not exist", item.ProjectID)
	}
	ctx, ok := launchContextByID(state, project.ContextID)
	if !ok {
		return domain.Run{}, fmt.Errorf("Context %d does not exist", project.ContextID)
	}
	workspace, ok := workspaceByID(state, request.WorkspaceID)
	if !ok || workspace.ItemID != item.ID {
		return domain.Run{}, fmt.Errorf("Workspace %d does not belong to Item %d", request.WorkspaceID, item.ID)
	}
	var machine domain.Machine
	var worktree *domain.Worktree
	checkDirty := ctx.CheckDirtyCheckouts
	if strategy.Kind == "grill" {
		if strategy.Configuration == nil {
			return domain.Run{}, errors.New("Grill launch requires a model configuration")
		}
		strategy.Agent = strategy.Configuration.Agent
		strategy.ExecutionProfile = domain.ExecutionProfileGrill
		strategy.Workflow = domain.WorkflowMattPocock
	}
	if strategy.Kind == "direct" || strategy.Kind == "grill" {
		machine, ok = machineByID(state, ctx.ExecutionMachineID)
		if !ok {
			return domain.Run{}, fmt.Errorf("Context %s has no execution Machine", ctx.Name)
		}
		if strategy.MachineID != nil && *strategy.MachineID != machine.ID {
			return domain.Run{}, errors.New("selected Machine is not the Context execution Machine")
		}
	} else if strategy.Kind == "worktree" {
		for _, candidate := range state.Worktrees {
			if candidate.ID == strategy.WorktreeID && candidate.WorkspaceID == workspace.ID {
				copy := candidate
				worktree = &copy
				machine, ok = machineByID(state, &candidate.MachineID)
				break
			}
		}
		if !ok || worktree == nil {
			return domain.Run{}, fmt.Errorf("Worktree %d is not registered for this Item", strategy.WorktreeID)
		}
	} else {
		return domain.Run{}, fmt.Errorf("unsupported Run launch strategy %q", strategy.Kind)
	}
	if strategy.Agent != domain.AgentClaude && strategy.Agent != domain.AgentCodex {
		return domain.Run{}, errors.New("Run agent must be Claude or Codex")
	}
	if strategy.Workflow != domain.WorkflowMattPocock && strategy.Workflow != domain.WorkflowPstack {
		return domain.Run{}, fmt.Errorf("unknown Run workflow %q", strategy.Workflow)
	}
	if strategy.ExecutionProfile == domain.ExecutionProfileGrill && strategy.Kind != "grill" {
		return domain.Run{}, errors.New("Grill must use the Grill launch strategy")
	}
	if strategy.Kind != "grill" && !domain.WorkflowOffers(strategy.Workflow, strategy.ExecutionProfile) {
		return domain.Run{}, fmt.Errorf("execution profile %q is not offered by workflow %q", strategy.ExecutionProfile, strategy.Workflow)
	}
	if strings.TrimSpace(strategy.Prompt) == "" {
		return domain.Run{}, errors.New("a Run prompt cannot be blank")
	}
	model, effort := "", ""
	if strategy.Configuration != nil {
		if strategy.Configuration.Agent != strategy.Agent {
			return domain.Run{}, errors.New("Run model configuration must match the selected agent")
		}
		model, effort = strategy.Configuration.Model, strategy.Configuration.Effort
	}
	var profile *domain.CLIConfigurationProfile
	profileID := ctx.ClaudeProfileID
	if strategy.Agent == domain.AgentCodex {
		profileID = ctx.CodexProfileID
	}
	if profileID != nil {
		for _, p := range state.CLIConfigurationProfiles {
			if p.ID == *profileID && p.MachineID == machine.ID && p.Provider == strategy.Agent {
				copy := p
				profile = &copy
				break
			}
		}
		if profile == nil {
			return domain.Run{}, fmt.Errorf("Selected %s configuration profile %d is unavailable on Machine %s", strategy.Agent, *profileID, machine.Name)
		}
	}
	access := r.machineAccess
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	checkouts := []domain.RunCheckout{}
	cwd := ""
	if worktree != nil {
		repo, found := repositoryByID(state, worktree.RepositoryID)
		if !found {
			return domain.Run{}, errors.New("registered Worktree Repository does not exist")
		}
		resolved, err := access.ResolvePath(machine, worktree.Path)
		if err != nil {
			return domain.Run{}, err
		}
		observed, err := gitcli.New(access).Inspect(machine, resolved)
		if err != nil {
			return domain.Run{}, fmt.Errorf("Could not inspect Worktree %s on Machine %s: %w", worktree.Path, machine.Name, err)
		}
		if observed.Branch != worktree.Branch {
			return domain.Run{}, errors.New("The Worktree branch changed after it was approved; review the Worktree before starting a Run")
		}
		if observed.RemoteURL != repo.RemoteURL {
			return domain.Run{}, errors.New("The Worktree remote does not match its registered Repository")
		}
		cwd = worktree.Path
	} else {
		for _, repo := range state.Repositories {
			if repo.ProjectID != project.ID {
				continue
			}
			var location *domain.RepositoryLocation
			for i := range state.RepositoryLocations {
				if state.RepositoryLocations[i].RepositoryID == repo.ID && state.RepositoryLocations[i].MachineID == machine.ID {
					location = &state.RepositoryLocations[i]
					break
				}
			}
			if location == nil {
				return domain.Run{}, fmt.Errorf("Repository %s has no checkout registered on Machine %s", repo.Name, machine.Name)
			}
			path, err := access.ResolvePath(machine, location.CheckoutPath)
			if err != nil {
				return domain.Run{}, err
			}
			observed, err := gitcli.New(access).Inspect(machine, path)
			if err != nil {
				return domain.Run{}, err
			}
			branch := ""
			for _, entry := range workspace.Repositories {
				if entry.RepositoryID == repo.ID {
					branch = entry.Branch
					break
				}
			}
			if observed.Branch != branch {
				return domain.Run{}, fmt.Errorf("Repository %s branch changed after preview", repo.Name)
			}
			if observed.RemoteURL != "" && observed.RemoteURL != repo.RemoteURL {
				return domain.Run{}, fmt.Errorf("Repository %s checkout remote does not match its registered Repository", repo.Name)
			}
			checkouts = append(checkouts, domain.RunCheckout{RepositoryID: repo.ID, Path: path, Branch: observed.Branch, IsDirty: observed.Dirty})
			if repo.ID == strategy.PrimaryRepositoryID {
				cwd = path
			}
		}
		if cwd == "" {
			return domain.Run{}, errors.New("Choose a configured Repository checkout before starting")
		}
		if !checkoutsMatch(strategy.ExpectedCheckouts, checkouts, checkDirty) {
			return domain.Run{}, errors.New("A Direct checkout changed after the preview (branch or dirty state); review the Direct Run preview again before starting")
		}
		if checkDirty {
			for _, c := range checkouts {
				if c.IsDirty && !strategy.AllowDirty {
					return domain.Run{}, errors.New("Dirty checkout requires explicit approval")
				}
			}
		}
		if !strategy.AllowSharedCheckouts {
			for _, run := range state.Runs {
				if run.MachineID == machine.ID && launchRunActive(run) {
					for _, old := range run.DirectCheckouts {
						for _, c := range checkouts {
							if old.Path == c.Path {
								return domain.Run{}, fmt.Errorf("checkout %s is already used by active Run %d", c.Path, run.ID)
							}
						}
					}
				}
			}
		}
	}
	if r.runExecutor == nil {
		r.runExecutor = tmuxAgentRunExecutor{access: access}
	}
	r.mu.Lock()
	executor := r.runExecutor
	r.mu.Unlock()
	runID := state.NextRunID
	if runID < 1 {
		runID = 1
	}
	sessionKind := "run"
	if strategy.Kind == "grill" {
		sessionKind = "grill"
	}
	session := fmt.Sprintf("mission-item-%d-%s-%d", item.ID, sessionKind, runID)
	gate := fmt.Sprintf("mission-launch-%d-%s", runID, session)
	executable, stateFile, profileDirectory, readiness, err := executor.Preflight(machine, strategy.Agent, runID, profile)
	if err != nil && readiness.Error == nil {
		setString(&readiness.Error, err.Error())
	}
	r.mu.Lock()
	r.machineReadiness[machine.ID] = readiness
	r.mu.Unlock()
	if err != nil {
		return domain.Run{}, fmt.Errorf("Run preflight failed on Machine %s. The Run was not started locally: %w", machine.Name, err)
	}
	if strategy.Configuration != nil {
		model = strategy.Configuration.Model
		effort = strategy.Configuration.Effort
	}
	var pstackRoot string
	if strategy.Workflow == domain.WorkflowPstack {
		pstackRoot, err = access.ProvisionPstackTree(machine)
		if err != nil {
			return domain.Run{}, fmt.Errorf("Could not provision pstack on Machine %s: %w", machine.Name, err)
		}
		rolePath := pstack.RoleFilePath(pstackRoot, ctx)
		roleContents := pstack.ComposeRoleFile(strategy.Agent, ctx.PstackRoles, profileByID(state, ctx.ClaudeProfileID, machine.ID), profileByID(state, ctx.CodexProfileID, machine.ID))
		if err := access.WriteFile(machine, rolePath, roleContents); err != nil {
			return domain.Run{}, fmt.Errorf("Could not write pstack role instructions: %w", err)
		}
		strategy.Prompt = pstackRunPrompt(strategy.Prompt, pstackRoot, rolePath, strategy.ExecutionProfile)
	}
	pane, err := executor.Launch(machine, session, gate, cwd, executable, strategy.Prompt, strategy.Agent, fmt.Sprint(runID), stateFile, model, effort, profileDirectory)
	if err != nil {
		return domain.Run{}, err
	}
	cleanup := func(cause error) (domain.Run, error) {
		if cleanupErr := executor.Kill(machine, session); cleanupErr != nil {
			return domain.Run{}, fmt.Errorf("%v; launch cleanup failed: %w", cause, cleanupErr)
		}
		return domain.Run{}, cause
	}
	r.transitionMu.Lock()
	latest, snapshotErr := r.stateSnapshot()
	if snapshotErr != nil {
		r.transitionMu.Unlock()
		return cleanup(snapshotErr)
	}
	if latest.NextRunID != state.NextRunID || !stillSameLaunchEntities(state, latest, item, project, ctx, workspace, machine, worktree) {
		r.transitionMu.Unlock()
		return cleanup(errors.New("the Item, Workspace, Repository, Worktree, or Machine changed before the Run could be recorded; review it again"))
	}
	run := domain.Run{ID: runID, ItemID: item.ID, WorkspaceID: int64Ptr(workspace.ID), MachineID: machine.ID, Agent: strategy.Agent, ExecutionProfile: strategy.ExecutionProfile, Workflow: strategy.Workflow, Prompt: strategy.Prompt, WorkingDirectory: cwd, SessionName: session, PaneID: pane, StartedAt: time.Now().Unix(), State: domain.RunUnknown, PaneStatus: domain.PaneAvailable, DirectCheckouts: checkouts, ReportedPullRequests: []string{}, GrillAnswers: []domain.GrillAnswer{}, GrillDecisions: []domain.GrillAnswer{}}
	if strategy.Workflow == domain.WorkflowPstack {
		snapshot := pstack.SkillSnapshot()
		run.SkillSnapshot = &snapshot
	}
	if strategy.Kind == "direct" || strategy.Kind == "grill" {
		id := strategy.PrimaryRepositoryID
		run.RepositoryID = &id
	} else {
		id := worktree.ID
		run.WorktreeID = &id
		repo := worktree.RepositoryID
		run.RepositoryID = &repo
	}
	if strategy.Configuration != nil {
		m, e := strategy.Configuration.Model, strategy.Configuration.Effort
		run.Model = &m
		run.Effort = &e
	}
	if profile != nil {
		run.CLIConfigurationProfile = &domain.CLIConfigurationProfileIdentity{ProfileID: profile.ID, Provider: profile.Provider, Name: profile.Name}
	}
	if strategy.Workflow == domain.WorkflowPstack {
		run.Workflow = domain.WorkflowPstack
	}
	decision, decideErr := domain.Decide(latest, domain.Event{Kind: "start_run", Run: &run})
	if decideErr != nil {
		r.transitionMu.Unlock()
		return cleanup(fmt.Errorf("could not record Run: %w", decideErr))
	}
	if err := r.persistDecisionLocked(latest, &decision); err != nil {
		r.transitionMu.Unlock()
		return cleanup(fmt.Errorf("could not record Run: %w", err))
	}
	r.transitionMu.Unlock()
	if err := executor.Release(machine, gate); err != nil {
		return domain.Run{}, fmt.Errorf("Run %d is recorded but the agent was not released: %w", run.ID, err)
	}
	return run, nil
}

func checkoutsMatch(expected, observed []domain.RunCheckout, checkDirty bool) bool {
	if len(expected) != len(observed) {
		return false
	}
	for i := range expected {
		if expected[i].RepositoryID != observed[i].RepositoryID || expected[i].Path != observed[i].Path || expected[i].Branch != observed[i].Branch || checkDirty && expected[i].IsDirty != observed[i].IsDirty {
			return false
		}
	}
	return true
}
func stillSameLaunchEntities(before, after domain.DomainState, item domain.Item, project domain.Project, ctx domain.Context, workspace domain.Workspace, machine domain.Machine, worktree *domain.Worktree) bool {
	if !reflect.DeepEqual(itemByIDValue(before, item.ID), itemByIDValue(after, item.ID)) || !reflect.DeepEqual(projectByIDValue(before, project.ID), projectByIDValue(after, project.ID)) || !reflect.DeepEqual(contextByIDValue(before, ctx.ID), contextByIDValue(after, ctx.ID)) || !reflect.DeepEqual(workspaceByIDValue(before, workspace.ID), workspaceByIDValue(after, workspace.ID)) || !reflect.DeepEqual(machineByIDValue(before, machine.ID), machineByIDValue(after, machine.ID)) {
		return false
	}
	if worktree != nil {
		if !reflect.DeepEqual(worktreeByIDValue(before, worktree.ID), worktreeByIDValue(after, worktree.ID)) {
			return false
		}
	}
	return reflect.DeepEqual(projectRepositories(before, project.ID), projectRepositories(after, project.ID)) && reflect.DeepEqual(machineLocations(before, machine.ID), machineLocations(after, machine.ID))
}
func projectRepositories(s domain.DomainState, projectID int64) []domain.Repository {
	out := []domain.Repository{}
	for _, v := range s.Repositories {
		if v.ProjectID == projectID {
			out = append(out, v)
		}
	}
	return out
}
func machineLocations(s domain.DomainState, machineID int64) []domain.RepositoryLocation {
	out := []domain.RepositoryLocation{}
	for _, v := range s.RepositoryLocations {
		if v.MachineID == machineID {
			out = append(out, v)
		}
	}
	return out
}
func itemByIDValue(s domain.DomainState, id int64) any {
	v, ok := launchItemByID(s, id)
	if !ok {
		return nil
	}
	return v
}
func projectByIDValue(s domain.DomainState, id int64) any {
	v, ok := launchProjectByID(s, id)
	if !ok {
		return nil
	}
	return v
}
func contextByIDValue(s domain.DomainState, id int64) any {
	v, ok := launchContextByID(s, id)
	if !ok {
		return nil
	}
	return v
}
func workspaceByIDValue(s domain.DomainState, id int64) any {
	v, ok := workspaceByID(s, id)
	if !ok {
		return nil
	}
	return v
}
func machineByIDValue(s domain.DomainState, id int64) any {
	v, ok := machineByID(s, &id)
	if !ok {
		return nil
	}
	return v
}
func worktreeByIDValue(s domain.DomainState, id int64) any {
	for _, v := range s.Worktrees {
		if v.ID == id {
			return v
		}
	}
	return nil
}
func workspaceByID(s domain.DomainState, id int64) (domain.Workspace, bool) {
	for _, v := range s.Workspaces {
		if v.ID == id {
			return v, true
		}
	}
	return domain.Workspace{}, false
}
func machineByID(s domain.DomainState, id *int64) (domain.Machine, bool) {
	if id == nil {
		return domain.Machine{}, false
	}
	for _, v := range s.Machines {
		if v.ID == *id {
			return v, true
		}
	}
	return domain.Machine{}, false
}
func repositoryByID(s domain.DomainState, id int64) (domain.Repository, bool) {
	for _, v := range s.Repositories {
		if v.ID == id {
			return v, true
		}
	}
	return domain.Repository{}, false
}

func startedRunPersistence(run domain.Run) (persistence.Effect, persistence.AuditAction) {
	values := []any{run.ID, run.ItemID, run.WorkspaceID, run.RepositoryID, run.WorktreeID, run.MachineID, run.Agent, run.ExecutionProfile, run.Model, run.Effort, run.SkillSnapshot, run.Prompt, run.WorkingDirectory, run.SessionName, run.PaneID, run.StartedAt, run.State, run.PaneStatus, marshalStartedRunJSON(run.DirectCheckouts), "", nil, marshalStartedRunJSON(run.GrillAnswers), marshalStartedRunJSON(run.GrillDecisions), nil, nil, nil, nil, nil, nullableProfile(run.CLIConfigurationProfile), run.Workflow, marshalStartedRunJSON(run.ReportedPullRequests), run.AttentionSummary, run.PlanPhase, run.PlanPath}
	sqlText := `INSERT INTO runs(id,item_id,workspace_id,repository_id,worktree_id,machine_id,agent,execution_profile,model,effort,skill_snapshot,prompt,working_directory,session_name,pane_id,started_at,state,pane_status,direct_checkouts_json,transcript,grill_question_group_json,grill_answers_json,grill_decisions_json,grill_response,grill_phase,grill_action,last_applied_agent_state_sequence,grill_action_started_at,cli_configuration_profile_json,workflow,reported_pull_requests_json,attention_summary,plan_phase,plan_path) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	return persistence.Effect{SQL: sqlText, Args: values, InsertedSequence: "next_run_id", InsertedID: run.ID}, auditJSON("runCreated", "run_id", run.ID)
}
func marshalStartedRunJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func nullableProfile(p *domain.CLIConfigurationProfileIdentity) any {
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func profileByID(state domain.DomainState, id *int64, machineID int64) *domain.CLIConfigurationProfile {
	if id == nil {
		return nil
	}
	for index := range state.CLIConfigurationProfiles {
		profile := &state.CLIConfigurationProfiles[index]
		if profile.ID == *id && profile.MachineID == machineID {
			return profile
		}
	}
	return nil
}

func pstackRunPrompt(prompt, root, rolePath string, profile domain.ExecutionProfile) string {
	instruction := fmt.Sprintf("Read `%s/skills/poteto-mode/SKILL.md` in full before acting. The Skill tool is unavailable for these embedded skills; read any other pstack skill by its absolute path under `%s/skills/`. Read the generated role instructions at `%s` and follow them when delegating.", root, root, rolePath)
	if profile == domain.ExecutionProfilePlan {
		instruction = fmt.Sprintf("Read `%s/skills/poteto-mode/SKILL.md` and follow `%s/skills/poteto-mode/playbooks/multi-phase-plan.md` in full. Read both by their absolute paths. Read the generated role instructions at `%s` and follow them when delegating. Complete the required planning phases, write the plan in the repository, then stop without implementing it. Report its repository-relative path. Do not delegate implementation.", root, root, rolePath)
	}
	if profile == domain.ExecutionProfilePstackReview {
		instruction = fmt.Sprintf("Read `%s/skills/interrogate/SKILL.md` and follow it to review the Pull Request or branch named in the Initial Prompt. Read the generated role instructions at `%s` and use its Review panel entry to configure the read-only reviewers. Review only; do not edit files, commit, push, or apply suggestions. Synthesize the reviewers' findings into a verdict.", root, rolePath)
	}
	return strings.TrimSpace(instruction + "\n\n" + prompt)
}

func (r *Runtime) prepareDirectRun(itemID, workspaceID int64, machineID *int64) (DirectRunPreview, error) {
	state, err := r.stateSnapshot()
	if err != nil {
		return DirectRunPreview{}, err
	}
	item, ok := launchItemByID(state, itemID)
	if !ok {
		return DirectRunPreview{}, fmt.Errorf("Item %d does not exist", itemID)
	}
	workspace, ok := workspaceByID(state, workspaceID)
	if !ok || workspace.ItemID != itemID {
		return DirectRunPreview{}, fmt.Errorf("Project Repository execution setup does not belong to Item %d", itemID)
	}
	project, ok := launchProjectByID(state, item.ProjectID)
	if !ok {
		return DirectRunPreview{}, fmt.Errorf("Project %d does not exist", item.ProjectID)
	}
	ctx, ok := launchContextByID(state, project.ContextID)
	if !ok {
		return DirectRunPreview{}, fmt.Errorf("Context %d does not exist", project.ContextID)
	}
	if machineID != nil && (ctx.ExecutionMachineID == nil || *machineID != *ctx.ExecutionMachineID) {
		return DirectRunPreview{}, errors.New("selected Machine is not the Context execution Machine")
	}
	machine, ok := machineByID(state, ctx.ExecutionMachineID)
	if !ok {
		return DirectRunPreview{}, fmt.Errorf("Context %s has no execution Machine", ctx.Name)
	}
	access := r.machineAccess
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	details := []DirectRunCheckoutPreview{}
	checkouts := []domain.RunCheckout{}
	for _, repo := range state.Repositories {
		if repo.ProjectID != project.ID {
			continue
		}
		var loc *domain.RepositoryLocation
		for i := range state.RepositoryLocations {
			if state.RepositoryLocations[i].RepositoryID == repo.ID && state.RepositoryLocations[i].MachineID == machine.ID {
				loc = &state.RepositoryLocations[i]
				break
			}
		}
		if loc == nil {
			return DirectRunPreview{}, fmt.Errorf("Repository %s has no checkout registered on Machine %s", repo.Name, machine.Name)
		}
		path, err := access.ResolvePath(machine, loc.CheckoutPath)
		if err != nil {
			return DirectRunPreview{}, err
		}
		observed, err := gitcli.New(access).Inspect(machine, path)
		if err != nil {
			return DirectRunPreview{}, err
		}
		branch := ""
		for _, wr := range workspace.Repositories {
			if wr.RepositoryID == repo.ID {
				branch = wr.Branch
				break
			}
		}
		if observed.Branch != branch {
			return DirectRunPreview{}, fmt.Errorf("Repository %s is on branch %s; Workspace expects %s", repo.Name, observed.Branch, branch)
		}
		if observed.RemoteURL != "" && observed.RemoteURL != repo.RemoteURL {
			return DirectRunPreview{}, fmt.Errorf("Repository %s checkout remote does not match its registered Repository", repo.Name)
		}
		checkouts = append(checkouts, domain.RunCheckout{RepositoryID: repo.ID, Path: path, Branch: observed.Branch, IsDirty: observed.Dirty})
		details = append(details, DirectRunCheckoutPreview{RepositoryID: repo.ID, RepositoryName: repo.Name, Path: path, Branch: observed.Branch, IsDirty: observed.Dirty})
	}
	if len(checkouts) == 0 {
		return DirectRunPreview{}, errors.New("Project has no configured Repositories")
	}
	preview := DirectRunPreview{WorkspaceID: workspace.ID, MachineID: machine.ID, MachineName: machine.Name, WorkingDirectory: checkouts[0].Path, Checkouts: checkouts, CheckoutDetails: details, DirtyRepositoryIDs: []int64{}, SharedRuns: []DirectRunSharedRun{}, SharedPaths: []string{}}
	for _, c := range checkouts {
		preview.CurrentBranches = append(preview.CurrentBranches, c.Branch)
		if ctx.CheckDirtyCheckouts && c.IsDirty {
			preview.DirtyRepositoryIDs = append(preview.DirtyRepositoryIDs, c.RepositoryID)
		}
	}
	for _, run := range state.Runs {
		if run.MachineID != machine.ID || !launchRunActive(run) || run.PaneStatus == domain.PaneMissing {
			continue
		}
		for _, active := range run.DirectCheckouts {
			for _, checkout := range checkouts {
				if active.Path == checkout.Path {
					preview.SharedRuns = append(preview.SharedRuns, DirectRunSharedRun{RunID: run.ID, ItemID: run.ItemID, Path: checkout.Path})
					preview.SharedPaths = append(preview.SharedPaths, checkout.Path)
				}
			}
		}
	}
	sort.Slice(preview.SharedRuns, func(i, j int) bool {
		a, b := preview.SharedRuns[i], preview.SharedRuns[j]
		if a.RunID != b.RunID {
			return a.RunID < b.RunID
		}
		return a.Path < b.Path
	})
	sort.Strings(preview.SharedPaths)
	if len(preview.SharedPaths) > 1 {
		unique := preview.SharedPaths[:1]
		for _, path := range preview.SharedPaths[1:] {
			if path != unique[len(unique)-1] {
				unique = append(unique, path)
			}
		}
		preview.SharedPaths = unique
	}
	return preview, nil
}

func (r *Runtime) composeRunPrompt(itemID int64, profile domain.ExecutionProfile, selection domain.RunPromptSelection, language, initial *string, workflow domain.Workflow) (string, error) {
	state, err := r.stateSnapshot()
	if err != nil {
		return "", err
	}
	return domain.ComposeRunPrompt(state, itemID, profile, selection, language, initial, workflow)
}
func (r *Runtime) getRunLaunchOptions(itemID int64, target domain.RunLaunchTargetKind) (domain.RunLaunchOptions, error) {
	state, err := r.stateSnapshot()
	if err != nil {
		return domain.RunLaunchOptions{}, err
	}
	return domain.RunLaunchOptionsFor(state, itemID, target)
}

func launchItemByID(s domain.DomainState, id int64) (domain.Item, bool) {
	for _, v := range s.Items {
		if v.ID == id {
			return v, true
		}
	}
	return domain.Item{}, false
}
func launchProjectByID(s domain.DomainState, id int64) (domain.Project, bool) {
	for _, v := range s.Projects {
		if v.ID == id {
			return v, true
		}
	}
	return domain.Project{}, false
}
func launchContextByID(s domain.DomainState, id int64) (domain.Context, bool) {
	for _, v := range s.Contexts {
		if v.ID == id {
			return v, true
		}
	}
	return domain.Context{}, false
}
func launchRunActive(v domain.Run) bool {
	return v.State != domain.RunFinished || v.ExecutionProfile == domain.ExecutionProfileGrill && (v.GrillPhase == nil || *v.GrillPhase != "finished") || v.ExecutionProfile == domain.ExecutionProfilePlan && v.PlanPhase != nil && *v.PlanPhase == "awaiting_go"
}
