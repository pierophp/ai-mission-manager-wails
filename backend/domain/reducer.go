package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func cloneState(state DomainState) DomainState {
	raw, _ := json.Marshal(state)
	var clone DomainState
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func DefaultGrillConfiguration() GrillConfiguration {
	return GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "high"}
}

func NewContextConfiguration() ContextConfiguration {
	policy := ExternalChangePolicy{Title: true, State: true, Metadata: true}
	return ContextConfiguration{
		CheckDirtyCheckouts: true,
		GrillDefaults:       DefaultGrillConfiguration(),
		ImplementDefaults:   DefaultGrillConfiguration(),
		DefaultWorkflow:     WorkflowMattPocock,
		PstackDefaults:      DefaultGrillConfiguration(),
		PstackRoles:         DefaultPstackRoles(),
		AttentionDefaults: []ContextAttentionDefault{
			{ObjectKind: ObjectIssue, Policy: policy},
			{ObjectKind: ObjectPullRequest, Policy: policy},
			{ObjectKind: ObjectGeneric, Policy: policy},
		},
	}
}

// Decide applies a deterministic transition. It performs no I/O and reads no
// clock; all timestamps used by effects are supplied by adapters.
func Decide(input DomainState, event Event) (Decision, error) {
	state := cloneState(input)
	clean := func(value string) string { return strings.TrimSpace(value) }
	switch event.Kind {
	case "start_run":
		if event.Run == nil {
			return Decision{}, DomainError("a Run is required")
		}
		run := *event.Run
		if state.NextRunID < 1 || state.NextRunID == math.MaxInt64 || run.ID != state.NextRunID {
			return Decision{}, DomainError("Run identifier does not match the next Run sequence")
		}
		if run.ItemID < 1 || run.MachineID < 1 || run.WorkspaceID == nil || *run.WorkspaceID < 1 || strings.TrimSpace(run.Prompt) == "" || strings.TrimSpace(run.SessionName) == "" || strings.TrimSpace(run.PaneID) == "" {
			return Decision{}, DomainError("Run identity, prompt, and Pane are required")
		}
		for _, existing := range state.Runs {
			if existing.ID == run.ID || existing.SessionName == run.SessionName {
				return Decision{}, DomainError("Run identifier or session already exists")
			}
		}
		state.Runs = append(state.Runs, run)
		state.NextRunID++
		return Decision{State: state, Effects: []Effect{{Kind: "persist_run", Run: &run}}}, nil
	case "create_context":
		name := clean(event.Name)
		if name == "" {
			return Decision{}, DomainError("a Context name cannot be blank")
		}
		for _, c := range state.Contexts {
			if c.Name == name {
				return Decision{}, DomainError("Context name already exists: " + name)
			}
		}
		if state.NextContextID < 1 || state.NextProjectID < 1 || state.NextContextID == 1<<63-1 || state.NextProjectID == 1<<63-1 {
			return Decision{}, DomainError("the Item identifier sequence is exhausted")
		}
		configuration := NewContextConfiguration()
		c := Context{ID: state.NextContextID, Name: name, CheckDirtyCheckouts: configuration.CheckDirtyCheckouts, GrillDefaults: configuration.GrillDefaults, ImplementDefaults: configuration.ImplementDefaults, DefaultWorkflow: configuration.DefaultWorkflow, PstackDefaults: configuration.PstackDefaults, PstackRoles: configuration.PstackRoles}
		p := Project{ID: state.NextProjectID, ContextID: c.ID, Name: "Default", Defaults: ProjectDefaults{ItemStatus: StatusInbox, ExecutionMode: ExecutionWorktree}}
		state.NextContextID++
		state.NextProjectID++
		state.Contexts = append(state.Contexts, c)
		state.Projects = append(state.Projects, p)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_context", Context: &c}, {Kind: "persist_project", Project: &p}}}, nil
	case "create_context_configuration":
		cfg := event.Configuration
		cfg.Name = clean(cfg.Name)
		created, err := Decide(state, Event{Kind: "create_context", Name: cfg.Name})
		if err != nil {
			return Decision{}, err
		}
		id := created.State.Contexts[len(created.State.Contexts)-1].ID
		for i := range cfg.AttentionDefaults {
			cfg.AttentionDefaults[i].ContextID = id
		}
		updated, err := Decide(created.State, Event{Kind: "update_context_configuration", ContextID: id, Configuration: cfg})
		if err != nil {
			return Decision{}, err
		}
		return Decision{State: updated.State, Effects: append(created.Effects, updated.Effects...)}, nil
	case "update_context", "update_context_configuration":
		idx := -1
		for i, c := range state.Contexts {
			if c.ID == event.ContextID {
				idx = i
			}
			name := event.Name
			if event.Kind == "update_context_configuration" {
				name = event.Configuration.Name
			}
			if c.ID != event.ContextID && c.Name == clean(name) {
				return Decision{}, DomainError("Context name already exists: " + clean(name))
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		c := state.Contexts[idx]
		if event.Kind == "update_context" {
			c.Name = clean(event.Name)
			if c.Name == "" {
				return Decision{}, DomainError("a Context name cannot be blank")
			}
		} else {
			cfg := event.Configuration
			cfg.Name = clean(cfg.Name)
			if cfg.Name == "" {
				return Decision{}, DomainError("a Context name cannot be blank")
			}
			if len(cfg.AttentionDefaults) != 3 {
				return Decision{}, DomainError("Context attention defaults must contain one policy for Issue, Pull Request, and generic External Objects")
			}
			if cfg.ExecutionMachineID != nil && !hasMachine(state, *cfg.ExecutionMachineID) {
				return Decision{}, DomainError(fmt.Sprintf("Machine %d does not exist", *cfg.ExecutionMachineID))
			}
			if c.ExecutionMachineID != cfg.ExecutionMachineID {
				active := activeContextRunIDs(state, event.ContextID)
				if len(active) > 0 {
					return Decision{}, DomainError(fmt.Sprintf("Context %d has active Runs: %v", event.ContextID, active))
				}
			}
			if cfg.ClaudeProfileID != nil {
				if err := validateContextProfile(state, cfg.ExecutionMachineID, AgentClaude, *cfg.ClaudeProfileID); err != nil {
					return Decision{}, err
				}
			}
			if cfg.CodexProfileID != nil {
				if err := validateContextProfile(state, cfg.ExecutionMachineID, AgentCodex, *cfg.CodexProfileID); err != nil {
					return Decision{}, err
				}
			}
			if err := validateGrill(cfg.GrillDefaults); err != nil {
				return Decision{}, err
			}
			if err := validateGrill(cfg.ImplementDefaults); err != nil {
				return Decision{}, err
			}
			if err := validateGrill(cfg.PstackDefaults); err != nil {
				return Decision{}, err
			}
			if cfg.DefaultWorkflow != WorkflowMattPocock && cfg.DefaultWorkflow != WorkflowPstack {
				return Decision{}, DomainError(fmt.Sprintf("unknown contexts.default_workflow value %q", cfg.DefaultWorkflow))
			}
			if err := validatePstackRoles(cfg.PstackRoles); err != nil {
				return Decision{}, err
			}
			seen := map[ExternalObjectKind]bool{}
			for _, d := range cfg.AttentionDefaults {
				if d.ContextID != event.ContextID || seen[d.ObjectKind] {
					return Decision{}, DomainError("Context attention defaults must contain one policy for Issue, Pull Request, and generic External Objects")
				}
				seen[d.ObjectKind] = true
			}
			for _, kind := range []ExternalObjectKind{ObjectIssue, ObjectPullRequest, ObjectGeneric} {
				if !seen[kind] {
					return Decision{}, DomainError("Context attention defaults must contain one policy for Issue, Pull Request, and generic External Objects")
				}
			}
			c.Name = cfg.Name
			c.ExecutionMachineID = cfg.ExecutionMachineID
			c.ClaudeProfileID = cfg.ClaudeProfileID
			c.CodexProfileID = cfg.CodexProfileID
			c.CheckDirtyCheckouts = cfg.CheckDirtyCheckouts
			c.GrillDefaults = cfg.GrillDefaults
			c.ImplementDefaults = cfg.ImplementDefaults
			c.DefaultWorkflow = cfg.DefaultWorkflow
			c.PstackDefaults = cfg.PstackDefaults
			c.PstackRoles = cfg.PstackRoles
			c.GHExecutablePath = cleanOptional(cfg.GHExecutablePath)
			c.TWGExecutablePath = cleanOptional(cfg.TWGExecutablePath)
			c.AZExecutablePath = cleanOptional(cfg.AZExecutablePath)
			c.AtlassianSite = cleanOptional(cfg.AtlassianSite)
			c.AzureDevOpsOrganization = cleanOptional(cfg.AzureDevOpsOrganization)
			c.BitbucketWorkspace = cleanOptional(cfg.BitbucketWorkspace)
			state.AttentionDefaults = filterDefaults(state.AttentionDefaults, event.ContextID)
			state.AttentionDefaults = append(state.AttentionDefaults, cfg.AttentionDefaults...)
		}
		state.Contexts[idx] = c
		if event.Kind == "update_context_configuration" {
			return Decision{State: state, Effects: []Effect{{Kind: "persist_context_configuration", Context: &c, AttentionDefaults: event.Configuration.AttentionDefaults}}}, nil
		}
		return Decision{State: state, Effects: []Effect{{Kind: "update_context", Context: &c}}}, nil
	case "set_context_grill_defaults", "set_context_implement_defaults", "set_context_dirty_checkout_check":
		idx := -1
		for i, c := range state.Contexts {
			if c.ID == event.ContextID {
				idx = i
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		c := state.Contexts[idx]
		switch event.Kind {
		case "set_context_grill_defaults":
			if err := validateGrill(event.GrillConfiguration); err != nil {
				return Decision{}, err
			}
			c.GrillDefaults = event.GrillConfiguration
		case "set_context_implement_defaults":
			if err := validateGrill(event.GrillConfiguration); err != nil {
				return Decision{}, err
			}
			c.ImplementDefaults = event.GrillConfiguration
		case "set_context_dirty_checkout_check":
			c.CheckDirtyCheckouts = event.Enabled
		}
		state.Contexts[idx] = c
		return Decision{State: state, Effects: []Effect{{Kind: EffectKind(event.Kind), Context: &c}}}, nil
	case "set_context_execution_machine":
		idx := contextIndex(state, event.ContextID)
		if idx < 0 {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		if event.ExecutionMachineID != nil && !hasMachine(state, *event.ExecutionMachineID) {
			return Decision{}, DomainError(fmt.Sprintf("Machine %d does not exist", *event.ExecutionMachineID))
		}
		if !sameOptionalInt64(state.Contexts[idx].ExecutionMachineID, event.ExecutionMachineID) && len(activeContextRunIDs(state, event.ContextID)) > 0 {
			return Decision{}, DomainError(fmt.Sprintf("Context %d has active Runs: %v", event.ContextID, activeContextRunIDs(state, event.ContextID)))
		}
		c := state.Contexts[idx]
		c.ExecutionMachineID = event.ExecutionMachineID
		if c.ClaudeProfileID != nil {
			if err := validateContextProfile(state, c.ExecutionMachineID, AgentClaude, *c.ClaudeProfileID); err != nil {
				return Decision{}, err
			}
		}
		if c.CodexProfileID != nil {
			if err := validateContextProfile(state, c.ExecutionMachineID, AgentCodex, *c.CodexProfileID); err != nil {
				return Decision{}, err
			}
		}
		state.Contexts[idx] = c
		return Decision{State: state, Effects: []Effect{{Kind: "update_context", Context: &c}}}, nil
	case "set_context_cli_configuration_profile":
		idx := contextIndex(state, event.ContextID)
		if idx < 0 {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		if event.CLIProfileID != nil {
			if err := validateContextProfile(state, state.Contexts[idx].ExecutionMachineID, event.Provider, *event.CLIProfileID); err != nil {
				return Decision{}, err
			}
		}
		c := state.Contexts[idx]
		if event.Provider == AgentClaude {
			c.ClaudeProfileID = event.CLIProfileID
		} else if event.Provider == AgentCodex {
			c.CodexProfileID = event.CLIProfileID
		} else {
			return Decision{}, DomainError(fmt.Sprintf("unknown AgentKind variant %q", event.Provider))
		}
		state.Contexts[idx] = c
		return Decision{State: state, Effects: []Effect{{Kind: "update_context", Context: &c}}}, nil
	case "register_machine", "update_machine":
		name, socket := clean(event.Name), clean(event.SocketName)
		if name == "" {
			return Decision{}, DomainError("a Machine name cannot be blank")
		}
		if socket == "" {
			return Decision{}, DomainError("a Machine socket name cannot be blank")
		}
		if err := validateMachineTransport(event.Transport); err != nil {
			return Decision{}, err
		}
		if event.Kind == "register_machine" {
			if !hasContext(state, event.ContextID) {
				return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
			}
			for _, m := range state.Machines {
				if m.ContextID == event.ContextID && m.Name == name {
					return Decision{}, DomainError("Machine name already exists in Context " + itoa(event.ContextID) + ": " + name)
				}
			}
			if state.NextMachineID < 1 || state.NextMachineID == 1<<63-1 {
				return Decision{}, DomainError("Machine identifier sequence is exhausted")
			}
			m := Machine{ID: state.NextMachineID, ContextID: event.ContextID, Name: name, SocketName: socket, Transport: event.Transport, LastObserved: MachineObservation("unknown")}
			state.NextMachineID++
			state.Machines = append(state.Machines, m)
			return Decision{State: state, Effects: []Effect{{Kind: "persist_machine", Machine: &m}}}, nil
		}
		idx := -1
		for i, m := range state.Machines {
			if m.ID == event.MachineID {
				idx = i
			}
		}
		if idx < 0 {
			return Decision{}, DomainError(fmt.Sprintf("Machine %d does not exist", event.MachineID))
		}
		m := state.Machines[idx]
		for _, other := range state.Machines {
			if other.ID != m.ID && other.ContextID == m.ContextID && other.Name == name {
				return Decision{}, DomainError("Machine name already exists in Context " + itoa(m.ContextID) + ": " + name)
			}
		}
		m.Name = name
		m.SocketName = socket
		m.Transport = event.Transport
		state.Machines[idx] = m
		return Decision{State: state, Effects: []Effect{{Kind: "update_machine", Machine: &m}}}, nil
	case "observe_machine":
		if event.MachineObservation != "available" && event.MachineObservation != "offline" {
			return Decision{}, DomainError(fmt.Sprintf("unknown Machine observation %q", event.MachineObservation))
		}
		for i, machine := range state.Machines {
			if machine.ID == event.MachineID {
				machine.LastObserved = event.MachineObservation
				observedAt := event.ObservedAt
				machine.LastObservedAt = &observedAt
				state.Machines[i] = machine
				return Decision{State: state, Effects: []Effect{{Kind: "observe_machine", Machine: &machine}}}, nil
			}
		}
		return Decision{}, DomainError(fmt.Sprintf("Machine %d does not exist", event.MachineID))
	case "create_cli_configuration_profile":
		name, directory := clean(event.ProfileName), clean(event.ProfileDirectory)
		if name == "" {
			return Decision{}, DomainError("a CLI configuration profile name cannot be blank")
		}
		if directory == "" {
			return Decision{}, DomainError("a CLI configuration profile directory cannot be blank")
		}
		if event.Provider != AgentClaude && event.Provider != AgentCodex {
			return Decision{}, DomainError(fmt.Sprintf("unknown AgentKind variant %q", event.Provider))
		}
		if !hasMachine(state, event.MachineID) {
			return Decision{}, DomainError(fmt.Sprintf("Machine %d does not exist", event.MachineID))
		}
		for _, p := range state.CLIConfigurationProfiles {
			if p.MachineID == event.MachineID && p.Provider == event.Provider && p.Name == name {
				return Decision{}, DomainError("CLI configuration profile already exists: " + name)
			}
		}
		if state.NextCLIProfileID < 1 || state.NextCLIProfileID == 1<<63-1 {
			return Decision{}, DomainError("CLI profile identifier sequence is exhausted")
		}
		p := CLIConfigurationProfile{ID: state.NextCLIProfileID, MachineID: event.MachineID, Provider: event.Provider, Name: name, Directory: directory, AppManaged: event.AppManaged}
		state.NextCLIProfileID++
		state.CLIConfigurationProfiles = append(state.CLIConfigurationProfiles, p)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_cli_configuration_profile", CLIProfile: &p}}}, nil
	case "delete_cli_configuration_profile":
		idx := -1
		for i, p := range state.CLIConfigurationProfiles {
			if p.ID == event.ProfileID {
				idx = i
			}
		}
		if idx < 0 {
			return Decision{}, DomainError(fmt.Sprintf("CLI configuration profile %d does not exist", event.ProfileID))
		}
		for _, c := range state.Contexts {
			if c.ClaudeProfileID != nil && *c.ClaudeProfileID == event.ProfileID || c.CodexProfileID != nil && *c.CodexProfileID == event.ProfileID {
				return Decision{}, DomainError("CLI configuration profile is in use by a Context")
			}
		}
		p := state.CLIConfigurationProfiles[idx]
		state.CLIConfigurationProfiles = append(state.CLIConfigurationProfiles[:idx], state.CLIConfigurationProfiles[idx+1:]...)
		return Decision{State: state, Effects: []Effect{{Kind: "delete_cli_configuration_profile", CLIProfile: &p}}}, nil
	case "create_project":
		name := clean(event.Name)
		if name == "" {
			return Decision{}, DomainError("a Project name cannot be blank")
		}
		if !hasContext(state, event.ContextID) {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		for _, p := range state.Projects {
			if p.ContextID == event.ContextID && p.Name == name {
				return Decision{}, DomainError("Project name already exists in Context " + itoa(event.ContextID) + ": " + name)
			}
		}
		if state.NextProjectID < 1 || state.NextProjectID == 1<<63-1 {
			return Decision{}, DomainError("the Item identifier sequence is exhausted")
		}
		if err := validateProjectDefaults(event.Defaults); err != nil {
			return Decision{}, err
		}
		p := Project{ID: state.NextProjectID, ContextID: event.ContextID, Name: name, Defaults: event.Defaults}
		if p.Defaults.ItemStatus == "" {
			p.Defaults.ItemStatus = StatusInbox
		}
		if p.Defaults.ExecutionMode == "" {
			p.Defaults.ExecutionMode = ExecutionWorktree
		}
		state.NextProjectID++
		state.Projects = append(state.Projects, p)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_project", Project: &p}}}, nil
	case "update_project":
		idx := -1
		for i, p := range state.Projects {
			if p.ID == event.ProjectID {
				idx = i
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Project " + itoa(event.ProjectID) + " does not exist")
		}
		p := state.Projects[idx]
		name := clean(event.Name)
		if name == "" {
			return Decision{}, DomainError("a Project name cannot be blank")
		}
		for _, other := range state.Projects {
			if other.ID != p.ID && other.ContextID == p.ContextID && other.Name == name {
				return Decision{}, DomainError("Project name already exists in Context " + itoa(p.ContextID) + ": " + name)
			}
		}
		if err := validateProjectDefaults(event.Defaults); err != nil {
			return Decision{}, err
		}
		p.Name = name
		p.Defaults = event.Defaults
		if p.Defaults.ItemStatus == "" {
			p.Defaults.ItemStatus = StatusInbox
		}
		if p.Defaults.ExecutionMode == "" {
			p.Defaults.ExecutionMode = ExecutionWorktree
		}
		state.Projects[idx] = p
		return Decision{State: state, Effects: []Effect{{Kind: "update_project", Project: &p}}}, nil
	case "register_repository", "update_repository", "register_repository_at_location", "update_repository_location":
		cleanRepositoryName := func(name string) (string, error) {
			name = clean(name)
			if name == "" {
				return "", DomainError("a Repository name cannot be blank")
			}
			if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
				return "", DomainError("a Repository name must be a single directory name")
			}
			return name, nil
		}
		if event.Kind == "register_repository" || event.Kind == "register_repository_at_location" {
			name, err := cleanRepositoryName(event.Name)
			if err != nil {
				return Decision{}, err
			}
			remote := clean(event.RemoteURL)
			if remote == "" {
				return Decision{}, DomainError("a Repository remote URL cannot be blank")
			}
			projectIndex := -1
			for i := range state.Projects {
				if state.Projects[i].ID == event.ProjectID {
					projectIndex = i
					break
				}
			}
			if projectIndex < 0 {
				return Decision{}, DomainError("Project " + itoa(event.ProjectID) + " does not exist")
			}
			if event.Kind == "register_repository" {
				for _, repository := range state.Repositories {
					if repository.ProjectID == event.ProjectID && repository.Name == name {
						return Decision{}, DomainError("Repository name already exists in Project " + itoa(event.ProjectID) + ": " + name)
					}
				}
				if state.NextRepositoryID < 1 || state.NextRepositoryID == 1<<63-1 {
					return Decision{}, DomainError("the Item identifier sequence is exhausted")
				}
				repository := Repository{ID: state.NextRepositoryID, ProjectID: event.ProjectID, Name: name, RemoteURL: remote, BaseBranch: "main"}
				state.NextRepositoryID++
				state.Repositories = append(state.Repositories, repository)
				return Decision{State: state, Effects: []Effect{{Kind: "persist_repository", Repository: &repository}}}, nil
			}
			baseBranch := clean(event.BaseBranch)
			if baseBranch == "" {
				return Decision{}, DomainError("a Repository base branch cannot be blank")
			}
			locationPath, worktreeRoot := clean(event.CheckoutPath), clean(event.WorktreeRoot)
			if locationPath == "" {
				return Decision{}, DomainError("a Repository checkout path cannot be blank")
			}
			if worktreeRoot == "" {
				return Decision{}, DomainError("a Repository Worktree root cannot be blank")
			}
			machineIndex := -1
			for i := range state.Machines {
				if state.Machines[i].ID == event.MachineID {
					machineIndex = i
					break
				}
			}
			if machineIndex < 0 {
				return Decision{}, DomainError("Machine " + itoa(event.MachineID) + " does not exist")
			}
			if state.Machines[machineIndex].ContextID != state.Projects[projectIndex].ContextID {
				return Decision{}, DomainError(fmt.Sprintf("Machine %d belongs to another Context", event.MachineID))
			}
			var existing *Repository
			for i := range state.Repositories {
				if state.Repositories[i].ProjectID == event.ProjectID && state.Repositories[i].Name == name {
					existing = &state.Repositories[i]
					break
				}
			}
			if existing != nil {
				if existing.RemoteURL != remote {
					return Decision{}, DomainError(fmt.Sprintf("Repository %s in Project %d has a different remote URL", name, event.ProjectID))
				}
				for _, location := range state.RepositoryLocations {
					if location.RepositoryID == existing.ID && location.MachineID == event.MachineID {
						return Decision{}, DomainError(fmt.Sprintf("Repository %d has already been configured on Machine %d", existing.ID, event.MachineID))
					}
				}
				existing.BaseBranch = baseBranch
				location := RepositoryLocation{RepositoryID: existing.ID, MachineID: event.MachineID, CheckoutPath: locationPath, WorktreeRoot: worktreeRoot}
				state.RepositoryLocations = append(state.RepositoryLocations, location)
				return Decision{State: state, Effects: []Effect{{Kind: "update_repository", Repository: existing}, {Kind: "persist_repository_location", RepositoryLocation: &location}}}, nil
			}
			if state.NextRepositoryID < 1 || state.NextRepositoryID == 1<<63-1 {
				return Decision{}, DomainError("the Item identifier sequence is exhausted")
			}
			repository := Repository{ID: state.NextRepositoryID, ProjectID: event.ProjectID, Name: name, RemoteURL: remote, BaseBranch: baseBranch}
			location := RepositoryLocation{RepositoryID: repository.ID, MachineID: event.MachineID, CheckoutPath: locationPath, WorktreeRoot: worktreeRoot}
			state.NextRepositoryID++
			state.Repositories = append(state.Repositories, repository)
			state.RepositoryLocations = append(state.RepositoryLocations, location)
			return Decision{State: state, Effects: []Effect{{Kind: "persist_repository_at_location", Repository: &repository, RepositoryLocation: &location}}}, nil
		}
		if event.Kind == "update_repository" {
			name, err := cleanRepositoryName(event.Name)
			if err != nil {
				return Decision{}, err
			}
			remote, baseBranch := clean(event.RemoteURL), clean(event.BaseBranch)
			if remote == "" {
				return Decision{}, DomainError("a Repository remote URL cannot be blank")
			}
			if baseBranch == "" {
				return Decision{}, DomainError("a Repository base branch cannot be blank")
			}
			index := -1
			for i := range state.Repositories {
				if state.Repositories[i].ID == event.RepositoryID {
					index = i
					break
				}
			}
			if index < 0 {
				return Decision{}, DomainError("Repository " + itoa(event.RepositoryID) + " does not exist")
			}
			projectID := state.Repositories[index].ProjectID
			for _, other := range state.Repositories {
				if other.ID != event.RepositoryID && other.ProjectID == projectID && other.Name == name {
					return Decision{}, DomainError("Repository name already exists in Project " + itoa(projectID) + ": " + name)
				}
			}
			state.Repositories[index].Name, state.Repositories[index].RemoteURL, state.Repositories[index].BaseBranch = name, remote, baseBranch
			repository := state.Repositories[index]
			return Decision{State: state, Effects: []Effect{{Kind: "update_repository", Repository: &repository}}}, nil
		}
		index := -1
		for i := range state.Repositories {
			if state.Repositories[i].ID == event.RepositoryID {
				index = i
				break
			}
		}
		if index < 0 {
			return Decision{}, DomainError("Repository " + itoa(event.RepositoryID) + " does not exist")
		}
		projectIndex := -1
		for i := range state.Projects {
			if state.Projects[i].ID == state.Repositories[index].ProjectID {
				projectIndex = i
				break
			}
		}
		machineIndex := -1
		for i := range state.Machines {
			if state.Machines[i].ID == event.MachineID {
				machineIndex = i
				break
			}
		}
		if projectIndex < 0 {
			return Decision{}, DomainError("Project does not exist")
		}
		if machineIndex < 0 {
			return Decision{}, DomainError("Machine " + itoa(event.MachineID) + " does not exist")
		}
		if state.Machines[machineIndex].ContextID != state.Projects[projectIndex].ContextID {
			return Decision{}, DomainError(fmt.Sprintf("Machine %d belongs to another Context", event.MachineID))
		}
		if event.PreviousMachineID != nil {
			found := false
			for _, location := range state.RepositoryLocations {
				if location.RepositoryID == event.RepositoryID && location.MachineID == *event.PreviousMachineID {
					found = true
				}
			}
			if !found {
				return Decision{}, DomainError(fmt.Sprintf("Repository %d has no location on Machine %d", event.RepositoryID, *event.PreviousMachineID))
			}
		}
		if event.PreviousMachineID == nil || *event.PreviousMachineID != event.MachineID {
			for _, location := range state.RepositoryLocations {
				if location.RepositoryID == event.RepositoryID && location.MachineID == event.MachineID {
					return Decision{}, DomainError(fmt.Sprintf("Repository %d has already been configured on Machine %d", event.RepositoryID, event.MachineID))
				}
			}
		}
		checkoutPath, root := clean(event.CheckoutPath), clean(event.WorktreeRoot)
		if checkoutPath == "" {
			return Decision{}, DomainError("a Repository checkout path cannot be blank")
		}
		if root == "" {
			return Decision{}, DomainError("a Repository Worktree root cannot be blank")
		}
		locations := make([]RepositoryLocation, 0, len(state.RepositoryLocations)+1)
		for _, location := range state.RepositoryLocations {
			if event.PreviousMachineID == nil || location.RepositoryID != event.RepositoryID || location.MachineID != *event.PreviousMachineID {
				locations = append(locations, location)
			}
		}
		location := RepositoryLocation{RepositoryID: event.RepositoryID, MachineID: event.MachineID, CheckoutPath: checkoutPath, WorktreeRoot: root}
		locations = append(locations, location)
		state.RepositoryLocations = locations
		return Decision{State: state, Effects: []Effect{{Kind: "update_repository_location", RepositoryLocation: &location, PreviousMachineID: event.PreviousMachineID}}}, nil
	case "ensure_project_workspaces":
		var effects []Effect
		for _, item := range state.Items {
			desired := make([]WorkspaceRepository, 0)
			for _, repository := range state.Repositories {
				if repository.ProjectID != item.ProjectID {
					continue
				}
				branch := "mission-" + item.HumanIdentifier
				for _, workspace := range state.Workspaces {
					if workspace.ItemID != item.ID {
						continue
					}
					for _, selected := range workspace.Repositories {
						if selected.RepositoryID == repository.ID {
							branch = selected.Branch
						}
					}
				}
				desired = append(desired, WorkspaceRepository{RepositoryID: repository.ID, Branch: branch, BaseBranch: repository.BaseBranch})
			}
			var itemWorkspaces []Workspace
			for _, workspace := range state.Workspaces {
				if workspace.ItemID == item.ID {
					itemWorkspaces = append(itemWorkspaces, workspace)
				}
			}
			if len(itemWorkspaces) == 0 {
				if len(desired) == 0 {
					continue
				}
				if state.NextWorkspaceID < 1 || state.NextWorkspaceID == math.MaxInt64 {
					return Decision{}, DomainError("the Item identifier sequence is exhausted")
				}
				workspace := Workspace{ID: state.NextWorkspaceID, ItemID: item.ID, Repositories: desired, PreparationState: WorkspacePending}
				state.NextWorkspaceID++
				state.Workspaces = append(state.Workspaces, workspace)
				effects = append(effects, Effect{Kind: "persist_workspace", Workspace: &workspace})
				continue
			}
			for _, current := range itemWorkspaces {
				if equalWorkspaceRepositorySet(current.Repositories, desired) {
					continue
				}
				for i := range state.Workspaces {
					if state.Workspaces[i].ID == current.ID {
						state.Workspaces[i].Repositories = append([]WorkspaceRepository{}, desired...)
					}
				}
				updated := current
				updated.Repositories = append([]WorkspaceRepository{}, desired...)
				effects = append(effects, Effect{Kind: "update_workspace_repositories", Workspace: &updated})
			}
		}
		return Decision{State: state, Effects: effects}, nil
	case "register_worktree":
		if event.Worktree == nil {
			return Decision{}, DomainError("Worktree details are required")
		}
		worktree := *event.Worktree
		if worktree.ID != 0 {
			return Decision{}, DomainError("Worktree ID is assigned by the application")
		}
		worktree.Path = strings.TrimSpace(worktree.Path)
		worktree.Branch = strings.TrimSpace(worktree.Branch)
		worktree.BaseBranch = strings.TrimSpace(worktree.BaseBranch)
		if worktree.Path == "" || worktree.Branch == "" || worktree.BaseBranch == "" {
			return Decision{}, DomainError("Worktree path, branch, and base branch are required")
		}
		var workspace *Workspace
		for i := range state.Workspaces {
			if state.Workspaces[i].ID == worktree.WorkspaceID {
				workspace = &state.Workspaces[i]
				break
			}
		}
		if workspace == nil {
			return Decision{}, DomainError("Workspace " + itoa(worktree.WorkspaceID) + " does not exist")
		}
		if !containsWorkspaceRepository(workspace.Repositories, worktree.RepositoryID) {
			return Decision{}, DomainError("Repository " + itoa(worktree.RepositoryID) + " is not configured for Workspace " + itoa(worktree.WorkspaceID))
		}
		var repository *Repository
		for i := range state.Repositories {
			if state.Repositories[i].ID == worktree.RepositoryID {
				repository = &state.Repositories[i]
				break
			}
		}
		if repository == nil {
			return Decision{}, DomainError("Repository " + itoa(worktree.RepositoryID) + " does not exist")
		}
		var item *Item
		for i := range state.Items {
			if state.Items[i].ID == workspace.ItemID {
				item = &state.Items[i]
				break
			}
		}
		if item == nil {
			return Decision{}, DomainError("Item " + itoa(workspace.ItemID) + " does not exist")
		}
		var project *Project
		for i := range state.Projects {
			if state.Projects[i].ID == item.ProjectID {
				project = &state.Projects[i]
				break
			}
		}
		if project == nil || repository.ProjectID != project.ID {
			return Decision{}, DomainError("Repository " + itoa(worktree.RepositoryID) + " does not belong to the Workspace Item's Project")
		}
		var machine *Machine
		for i := range state.Machines {
			if state.Machines[i].ID == worktree.MachineID {
				machine = &state.Machines[i]
				break
			}
		}
		if machine == nil {
			return Decision{}, DomainError("Machine " + itoa(worktree.MachineID) + " does not exist")
		}
		if machine.ContextID != project.ContextID {
			return Decision{}, DomainError("Machine " + itoa(worktree.MachineID) + " does not belong to Context " + itoa(project.ContextID))
		}
		context := contextIndex(state, project.ContextID)
		if context < 0 || state.Contexts[context].ExecutionMachineID == nil || *state.Contexts[context].ExecutionMachineID != worktree.MachineID {
			return Decision{}, DomainError("Machine " + itoa(worktree.MachineID) + " is not the Context's execution Machine")
		}
		for _, existing := range state.Worktrees {
			if existing.WorkspaceID == worktree.WorkspaceID && existing.RepositoryID == worktree.RepositoryID {
				return Decision{}, DomainError("Workspace already has a Worktree for this Repository")
			}
			if existing.MachineID == worktree.MachineID && existing.Path == worktree.Path {
				return Decision{}, DomainError("Worktree path is already registered")
			}
		}
		if state.NextWorktreeID < 1 || state.NextWorktreeID == math.MaxInt64 {
			return Decision{}, DomainError("the Worktree identifier sequence is exhausted")
		}
		worktree.ID = state.NextWorktreeID
		state.NextWorktreeID++
		state.Worktrees = append(state.Worktrees, worktree)
		previousPreparationState := workspace.PreparationState
		workspace.PreparationState = workspacePreparationState(state, workspace.ID, previousPreparationState)
		updatedWorkspace := *workspace
		return Decision{State: state, Effects: []Effect{{Kind: "persist_worktree", Worktree: &worktree, Workspace: &updatedWorkspace}}}, nil
	case "remove_worktree":
		if !event.Confirmed {
			return Decision{}, DomainError("Worktree removal must be confirmed")
		}
		if event.DestructiveRequired && !event.DestructiveConfirmed {
			return Decision{}, DomainError("Removing a dirty Worktree requires destructive confirmation")
		}
		for i, worktree := range state.Worktrees {
			if worktree.ID != event.WorktreeID {
				continue
			}
			state.Worktrees = append(state.Worktrees[:i], state.Worktrees[i+1:]...)
			workspaceIndex := -1
			for j := range state.Workspaces {
				if state.Workspaces[j].ID == worktree.WorkspaceID {
					workspaceIndex = j
					break
				}
			}
			if workspaceIndex < 0 {
				return Decision{}, DomainError("Workspace " + itoa(worktree.WorkspaceID) + " does not exist")
			}
			state.Workspaces[workspaceIndex].PreparationState = workspacePreparationState(state, worktree.WorkspaceID, state.Workspaces[workspaceIndex].PreparationState)
			updatedWorkspace := state.Workspaces[workspaceIndex]
			return Decision{State: state, Effects: []Effect{{Kind: "delete_worktree", Worktree: &worktree, Workspace: &updatedWorkspace}}}, nil
		}
		return Decision{}, DomainError("Worktree " + itoa(event.WorktreeID) + " does not exist")
	case "link_external_object":
		if event.ExternalObject == nil {
			return Decision{}, DomainError("External Object identity is required")
		}
		return decideLinkExternalObject(state, event.ItemID, *event.ExternalObject, event.ExternalSnapshot)
	case "refresh_external_object":
		return decideRefreshExternalObject(state, event.ExternalObjectID, event.ExternalSnapshot)
	case "set_link_attention_policy", "set_link_purpose", "set_link_watch_until", "set_link_review_at", "clear_link_review_at", "mark_link_reviewed":
		return decideLinkMutation(state, event)
	case "unlink_external_link":
		return decideUnlinkExternalLink(state, event)
	case "delete_external_object":
		return decideDeleteExternalObject(state, event)
	case "create_item":
		title := strings.TrimSpace(event.Name)
		if title == "" {
			return Decision{}, DomainError("an Item title cannot be blank")
		}
		if !hasContext(state, event.ContextID) {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		var project *Project
		for i := range state.Projects {
			if state.Projects[i].ID == event.ProjectID {
				project = &state.Projects[i]
				break
			}
		}
		if project == nil {
			return Decision{}, DomainError("Project " + itoa(event.ProjectID) + " does not exist")
		}
		if project.ContextID != event.ContextID {
			return Decision{}, DomainError(fmt.Sprintf("Project %d belongs to another Context", event.ProjectID))
		}
		if state.NextItemID < 1 || state.NextItemID == math.MaxInt64 || state.NextItemNumber < 1 || state.NextItemNumber == math.MaxInt64 {
			return Decision{}, DomainError("the Item identifier sequence is exhausted")
		}
		item := Item{ID: state.NextItemID, HumanIdentifier: fmt.Sprintf("MC-%d", state.NextItemNumber), Title: title, ProjectID: event.ProjectID, Status: project.Defaults.ItemStatus, Notes: event.Notes, Reminders: []Reminder{}}
		state.NextItemID++
		state.NextItemNumber++
		state.Items = append(append([]Item(nil), state.Items...), item)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_item", Item: &item}}}, nil
	case "set_item_status", "set_item_title", "set_item_notes", "add_item_reminder", "remove_item_reminder":
		idx := -1
		for i := range state.Items {
			if state.Items[i].ID == event.ItemID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Item " + itoa(event.ItemID) + " does not exist")
		}
		items := append([]Item(nil), state.Items...)
		item := items[idx]
		switch event.Kind {
		case "set_item_status":
			if event.Status != StatusInbox && event.Status != StatusActive && event.Status != StatusWaiting && event.Status != StatusDone {
				return Decision{}, DomainError(fmt.Sprintf("unknown ItemStatus variant %q; expected Inbox, Active, Waiting, or Done", event.Status))
			}
			item.Status = event.Status
		case "set_item_title":
			item.Title = strings.TrimSpace(event.Name)
			if item.Title == "" {
				return Decision{}, DomainError("an Item title cannot be blank")
			}
		case "set_item_notes":
			item.Notes = event.Notes
		case "add_item_reminder":
			remindAt := strings.TrimSpace(event.RemindAt)
			if remindAt == "" {
				return Decision{}, DomainError("a Reminder date cannot be blank")
			}
			if state.NextReminderID < 1 || state.NextReminderID == math.MaxInt64 {
				return Decision{}, DomainError("the Item identifier sequence is exhausted")
			}
			item.Reminders = append(append([]Reminder(nil), item.Reminders...), Reminder{ID: state.NextReminderID, RemindAt: remindAt})
			state.NextReminderID++
		case "remove_item_reminder":
			reminders := make([]Reminder, 0, len(item.Reminders))
			found := false
			for _, reminder := range item.Reminders {
				if reminder.ID == event.ReminderID {
					found = true
				} else {
					reminders = append(reminders, reminder)
				}
			}
			if !found {
				return Decision{}, DomainError(fmt.Sprintf("Reminder %d does not exist on Item %d", event.ReminderID, event.ItemID))
			}
			item.Reminders = reminders
		}
		items[idx] = item
		state.Items = items
		return Decision{State: state, Effects: []Effect{{Kind: EffectKind(event.Kind), Item: &item}}}, nil
	case "set_item_relation":
		if event.FromItemID == event.ToItemID {
			return Decision{}, DomainError(fmt.Sprintf("an Item cannot relate to itself: %d", event.FromItemID))
		}
		fromContext, fromOK := contextForItem(state, event.FromItemID)
		toContext, toOK := contextForItem(state, event.ToItemID)
		if !fromOK {
			return Decision{}, DomainError("Item " + itoa(event.FromItemID) + " does not exist")
		}
		if !toOK {
			return Decision{}, DomainError("Item " + itoa(event.ToItemID) + " does not exist")
		}
		if fromContext != toContext {
			return Decision{}, DomainError(fmt.Sprintf("Items %d and %d belong to different Contexts", event.FromItemID, event.ToItemID))
		}
		if event.RelationKind != RelationBlocks && event.RelationKind != RelationBlockedBy && event.RelationKind != RelationRelatedTo {
			return Decision{}, DomainError(fmt.Sprintf("unknown ItemRelationKind variant %q", event.RelationKind))
		}
		relation := ItemRelation{FromItemID: event.FromItemID, ToItemID: event.ToItemID, Kind: event.RelationKind}
		for _, existing := range state.Relationships {
			if existing == relation {
				return Decision{}, DomainError("the relationship already exists")
			}
		}
		state.Relationships = append(append([]ItemRelation(nil), state.Relationships...), relation)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_item_relation", Relation: &relation}}}, nil
	case "set_context_attention_default":
		if !hasContext(state, event.ContextID) {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		if err := validateObjectKind(event.ObjectKind); err != nil {
			return Decision{}, err
		}
		d := ContextAttentionDefault{ContextID: event.ContextID, ObjectKind: event.ObjectKind, Policy: event.Policy}
		found := false
		for i, v := range state.AttentionDefaults {
			if v.ContextID == event.ContextID && v.ObjectKind == event.ObjectKind {
				state.AttentionDefaults[i] = d
				found = true
			}
		}
		if !found {
			state.AttentionDefaults = append(state.AttentionDefaults, d)
		}
		return Decision{State: state, Effects: []Effect{{Kind: "persist_attention_default", AttentionDefaults: []ContextAttentionDefault{d}}}}, nil
	default:
		return Decision{}, DomainError("unsupported domain event: " + string(event.Kind))
	}
}

func contextForItem(s DomainState, itemID int64) (int64, bool) {
	for _, item := range s.Items {
		if item.ID != itemID {
			continue
		}
		for _, project := range s.Projects {
			if project.ID == item.ProjectID {
				return project.ContextID, true
			}
		}
		return 0, false
	}
	return 0, false
}

func filterDefaults(values []ContextAttentionDefault, id int64) []ContextAttentionDefault {
	out := make([]ContextAttentionDefault, 0, len(values))
	for _, v := range values {
		if v.ContextID != id {
			out = append(out, v)
		}
	}
	return out
}
func hasContext(s DomainState, id int64) bool {
	for _, v := range s.Contexts {
		if v.ID == id {
			return true
		}
	}
	return false
}
func itoa(value int64) string { return strconv.FormatInt(value, 10) }

func equalWorkspaceRepositorySet(left, right []WorkspaceRepository) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func hasMachine(s DomainState, id int64) bool {
	for _, m := range s.Machines {
		if m.ID == id {
			return true
		}
	}
	return false
}

func containsWorkspaceRepository(repositories []WorkspaceRepository, id int64) bool {
	for _, repository := range repositories {
		if repository.RepositoryID == id {
			return true
		}
	}
	return false
}

func workspacePreparationState(state DomainState, workspaceID int64, previous WorkspacePreparationState) WorkspacePreparationState {
	var workspace *Workspace
	for i := range state.Workspaces {
		if state.Workspaces[i].ID == workspaceID {
			workspace = &state.Workspaces[i]
			break
		}
	}
	if workspace == nil {
		return previous
	}
	var projectID int64
	for _, item := range state.Items {
		if item.ID == workspace.ItemID {
			projectID = item.ProjectID
			break
		}
	}
	if projectID == 0 {
		return previous
	}
	count := 0
	for _, repository := range state.Repositories {
		if repository.ProjectID != projectID {
			continue
		}
		count++
		found := false
		for _, worktree := range state.Worktrees {
			if worktree.WorkspaceID == workspaceID && worktree.RepositoryID == repository.ID {
				found = true
				break
			}
		}
		if !found {
			if previous == WorkspaceResumable {
				return WorkspaceResumable
			}
			return WorkspacePending
		}
	}
	if count > 0 {
		return WorkspaceReady
	}
	if previous == WorkspaceResumable {
		return WorkspaceResumable
	}
	return WorkspacePending
}

func contextIndex(state DomainState, id int64) int {
	for i, context := range state.Contexts {
		if context.ID == id {
			return i
		}
	}
	return -1
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateMachineTransport(transport MachineTransport) error {
	switch transport.Kind {
	case TransportLocal:
		if transport.Host != nil || transport.User != nil || transport.Port != nil || transport.IdentityFile != nil || transport.KnownHostsFile != nil || transport.StrictHostKeyChecking != nil {
			return DomainError("local Machine transport cannot include SSH settings")
		}
	case TransportSSH:
		if transport.Host == nil || strings.TrimSpace(*transport.Host) == "" {
			return DomainError("SSH host cannot be blank")
		}
		if transport.Port != nil && *transport.Port == 0 {
			return DomainError("SSH port must be positive")
		}
	default:
		return DomainError(fmt.Sprintf("unknown MachineTransport kind %q", transport.Kind))
	}
	return nil
}
func activeContextRunIDs(s DomainState, contextID int64) []int64 {
	var ids []int64
	for _, run := range s.Runs {
		active := run.State != RunFinished || (run.ExecutionProfile == ExecutionProfile("grill") && (run.GrillPhase == nil || *run.GrillPhase != GrillPhase("Finished"))) || (run.ExecutionProfile == ExecutionProfile("plan") && run.PlanPhase != nil && *run.PlanPhase == PlanPhase("awaitingGo"))
		if !active {
			continue
		}
		for _, item := range s.Items {
			if item.ID == run.ItemID {
				for _, project := range s.Projects {
					if project.ID == item.ProjectID && hasContext(s, contextID) && contextForProject(s, project.ID) == contextID {
						ids = append(ids, run.ID)
					}
				}
			}
		}
	}
	return ids
}
func contextForProject(s DomainState, projectID int64) int64 {
	for _, p := range s.Projects {
		if p.ID == projectID {
			return p.ContextID
		}
	}
	return 0
}
func validateContextProfile(s DomainState, machineID *int64, provider AgentKind, profileID int64) error {
	var profile *CLIConfigurationProfile
	for i := range s.CLIConfigurationProfiles {
		if s.CLIConfigurationProfiles[i].ID == profileID {
			profile = &s.CLIConfigurationProfiles[i]
			break
		}
	}
	if profile == nil {
		return DomainError(fmt.Sprintf("CLI configuration profile %d does not exist", profileID))
	}
	if profile.Provider != provider {
		return DomainError(fmt.Sprintf("CLI configuration profile %d is for %s, not %s", profileID, profile.Provider, provider))
	}
	if machineID == nil || *machineID != profile.MachineID {
		return DomainError(fmt.Sprintf("CLI configuration profile %d belongs to another Machine", profileID))
	}
	if !hasMachine(s, *machineID) {
		return DomainError(fmt.Sprintf("Machine %d does not exist", *machineID))
	}
	return nil
}
func validateGrill(c GrillConfiguration) error {
	valid := strings.TrimSpace(c.Model) != "" && strings.TrimSpace(c.Effort) != "" && (c.Agent == AgentClaude || c.Agent == AgentCodex)
	if c.Agent == AgentClaude {
		valid = valid && isKnownClaudeModel(c.Model)
	}
	if !valid {
		return DomainError(fmt.Sprintf("Grill configuration is invalid for %s: model %s, effort %s", agentDebug(c.Agent), c.Model, c.Effort))
	}
	return nil
}

func isKnownClaudeModel(model string) bool {
	for _, catalog := range GrillModelCatalog() {
		if catalog.Agent != AgentClaude {
			continue
		}
		for _, candidate := range catalog.Models {
			if candidate.ID == model {
				return true
			}
		}
	}
	return false
}

func agentDebug(agent AgentKind) string {
	switch agent {
	case AgentClaude:
		return "Claude"
	case AgentCodex:
		return "Codex"
	default:
		return string(agent)
	}
}
func validatePstackRoles(roles PstackRoleTable) error {
	if len(roles) != 4 {
		return DomainError("pstack role table must contain each supported role exactly once")
	}
	expected := map[string]bool{"code-delegate": true, "judge-and-prose": true, "review-panel": true, "explorers": true}
	for _, role := range roles {
		if !expected[string(role.Role)] {
			return DomainError("pstack role table must contain each supported role exactly once")
		}
		delete(expected, string(role.Role))
		if err := validateGrill(role.Configuration); err != nil {
			return err
		}
	}
	if len(expected) > 0 {
		return DomainError("pstack role table must contain each supported role exactly once")
	}
	return nil
}
func validateProjectDefaults(d ProjectDefaults) error {
	if d.ItemStatus != StatusInbox && d.ItemStatus != StatusActive && d.ItemStatus != StatusWaiting && d.ItemStatus != StatusDone {
		return DomainError(fmt.Sprintf("unknown projects.default_item_status value %q", d.ItemStatus))
	}
	if d.ExecutionMode != ExecutionDirect && d.ExecutionMode != ExecutionWorktree {
		return DomainError(fmt.Sprintf("unknown projects.default_execution_mode value %q", d.ExecutionMode))
	}
	return nil
}
func validateObjectKind(kind ExternalObjectKind) error {
	if kind != ObjectIssue && kind != ObjectPullRequest && kind != ObjectDocument && kind != ObjectGeneric {
		return DomainError(fmt.Sprintf("unknown ExternalObjectKind variant %q; expected issue, pull_request, document, or generic", kind))
	}
	return nil
}
func cleanOptional(value *string) *string {
	if value == nil {
		return nil
	}
	clean := strings.TrimSpace(*value)
	if clean == "" {
		return nil
	}
	return &clean
}
