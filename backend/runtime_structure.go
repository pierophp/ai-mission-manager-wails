package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/gitcli"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func (r *Runtime) transition(event domain.Event) (domain.Decision, error) {
	if r == nil || r.store == nil {
		return domain.Decision{}, errors.New("runtime is not configured")
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	return r.transitionLocked(event)
}

func (r *Runtime) transitionLocked(event domain.Event) (domain.Decision, error) {
	r.mu.Lock()
	snapshot := r.state
	r.mu.Unlock()
	decision, err := domain.Decide(snapshot, event)
	if err != nil {
		return domain.Decision{}, err
	}
	if err := r.persistDecisionLocked(snapshot, &decision); err != nil {
		return domain.Decision{}, err
	}
	return decision, nil
}

func (r *Runtime) persistDecisionLocked(snapshot domain.DomainState, decision *domain.Decision) error {
	effects, audit, err := persistenceEffects(decision.Effects)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if !sameSequences(r.state, snapshot) {
		r.mu.Unlock()
		return errors.New("Mission Manager state changed; retry the command")
	}
	r.mu.Unlock()
	if err := r.store.Apply(effects, audit); err != nil {
		return err
	}
	auditEntries, auditErr := r.store.ListAuditHistory()
	if auditErr == nil {
		decision.State.AuditEntries = auditEntries
	}
	r.mu.Lock()
	r.state = decision.State
	r.mu.Unlock()
	for _, effect := range decision.Effects {
		if effect.Kind == "persist_repository" || effect.Kind == "persist_repository_at_location" || effect.Kind == "update_repository" || effect.Kind == "persist_item" {
			return r.ensureProjectWorkspacesLocked()
		}
	}
	return nil
}

func sameSequences(a, b domain.DomainState) bool {
	return a.NextContextID == b.NextContextID && a.NextProjectID == b.NextProjectID && a.NextRepositoryID == b.NextRepositoryID && a.NextWorkspaceID == b.NextWorkspaceID && a.NextItemID == b.NextItemID && a.NextExternalObjectID == b.NextExternalObjectID && a.NextLinkID == b.NextLinkID && a.NextActivityID == b.NextActivityID && a.NextAuditID == b.NextAuditID
}

func persistenceEffects(effects []domain.Effect) ([]persistence.Effect, []persistence.AuditAction, error) {
	var out []persistence.Effect
	var audit []persistence.AuditAction
	for _, effect := range effects {
		switch effect.Kind {
		case "persist_context":
			c := effect.Context
			if c == nil {
				return nil, nil, errors.New("context effect has no Context")
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO contexts(id,name,check_dirty_checkouts,grill_agent,grill_model,grill_effort,implement_agent,implement_model,implement_effort,default_workflow,pstack_agent,pstack_model,pstack_effort,pstack_roles_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Args: []any{c.ID, c.Name, 1, c.GrillDefaults.Agent, c.GrillDefaults.Model, c.GrillDefaults.Effort, c.ImplementDefaults.Agent, c.ImplementDefaults.Model, c.ImplementDefaults.Effort, c.DefaultWorkflow, c.PstackDefaults.Agent, c.PstackDefaults.Model, c.PstackDefaults.Effort, pstackRolesJSON(c.PstackRoles)}, InsertedSequence: "next_context_id", InsertedID: c.ID})
			audit = append(audit, auditJSON("contextCreated", "context_id", c.ID))
		case "persist_project":
			p := effect.Project
			if p == nil {
				return nil, nil, errors.New("project effect has no Project")
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO projects(id,context_id,name,default_item_status,default_execution_mode) VALUES(?,?,?,?,?)`, Args: []any{p.ID, p.ContextID, p.Name, p.Defaults.ItemStatus, p.Defaults.ExecutionMode}, InsertedSequence: "next_project_id", InsertedID: p.ID})
			audit = append(audit, auditJSON("projectCreated", "project_id", p.ID))
		case "persist_machine", "update_machine":
			m := effect.Machine
			if m == nil {
				return nil, nil, errors.New("machine effect has no Machine")
			}
			transport, err := json.Marshal(m.Transport)
			if err != nil {
				return nil, nil, err
			}
			if effect.Kind == "persist_machine" {
				out = append(out, persistence.Effect{SQL: `INSERT INTO machines(id,context_id,name,socket_name,transport_json,last_observed,last_observed_at) VALUES(?,?,?,?,?,?,?)`, Args: []any{m.ID, m.ContextID, m.Name, m.SocketName, string(transport), m.LastObserved, m.LastObservedAt}, InsertedSequence: "next_machine_id", InsertedID: m.ID})
				audit = append(audit, auditJSON("machineRegistered", "machine_id", m.ID))
			} else {
				out = append(out, persistence.Effect{SQL: `UPDATE machines SET name=?,socket_name=?,transport_json=? WHERE id=?`, Args: []any{m.Name, m.SocketName, string(transport), m.ID}})
			}
		case "observe_machine":
			m := effect.Machine
			if m == nil || m.LastObservedAt == nil {
				return nil, nil, errors.New("Machine observation effect is incomplete")
			}
			out = append(out, persistence.Effect{SQL: `UPDATE machines SET last_observed=?,last_observed_at=? WHERE id=?`, Args: []any{m.LastObserved, *m.LastObservedAt, m.ID}})
			audit = append(audit, machineObservationAuditJSON(m))
		case "persist_cli_configuration_profile":
			p := effect.CLIProfile
			if p == nil {
				return nil, nil, errors.New("CLI profile effect has no profile")
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO cli_configuration_profiles(id,machine_id,provider,name,directory,app_managed) VALUES(?,?,?,?,?,?)`, Args: []any{p.ID, p.MachineID, p.Provider, p.Name, p.Directory, boolInt64(p.AppManaged)}, InsertedSequence: "next_cli_profile_id", InsertedID: p.ID})
		case "delete_cli_configuration_profile":
			p := effect.CLIProfile
			if p == nil {
				return nil, nil, errors.New("CLI profile effect has no profile")
			}
			out = append(out, persistence.Effect{SQL: `DELETE FROM cli_configuration_profiles WHERE id=?`, Args: []any{p.ID}})
		case "persist_repository", "persist_repository_at_location", "update_repository":
			repository := effect.Repository
			if repository == nil {
				return nil, nil, errors.New("repository effect has no Repository")
			}
			if effect.Kind == "persist_repository" || effect.Kind == "persist_repository_at_location" {
				out = append(out, persistence.Effect{SQL: `INSERT INTO repositories(id,project_id,name,remote_url,base_branch) VALUES(?,?,?,?,?)`, Args: []any{repository.ID, repository.ProjectID, repository.Name, repository.RemoteURL, repository.BaseBranch}, InsertedSequence: "next_repository_id", InsertedID: repository.ID})
				audit = append(audit, auditJSON("repositoryRegistered", "repository_id", repository.ID))
			} else {
				out = append(out, persistence.Effect{SQL: `UPDATE repositories SET name=?,remote_url=?,base_branch=? WHERE id=?`, Args: []any{repository.Name, repository.RemoteURL, repository.BaseBranch, repository.ID}})
			}
			if effect.Kind == "persist_repository_at_location" {
				if effect.RepositoryLocation == nil {
					return nil, nil, errors.New("repository location effect has no location")
				}
				out = append(out, repositoryLocationInsert(*effect.RepositoryLocation))
			}
		case "persist_repository_location":
			if effect.RepositoryLocation == nil {
				return nil, nil, errors.New("repository location effect has no location")
			}
			out = append(out, repositoryLocationInsert(*effect.RepositoryLocation))
		case "update_repository_location":
			if effect.RepositoryLocation == nil {
				return nil, nil, errors.New("repository location effect has no location")
			}
			location := effect.RepositoryLocation
			if effect.PreviousMachineID != nil && *effect.PreviousMachineID == location.MachineID {
				out = append(out, persistence.Effect{SQL: `UPDATE repository_locations SET checkout_path=?,worktree_root=? WHERE repository_id=? AND machine_id=?`, Args: []any{location.CheckoutPath, location.WorktreeRoot, location.RepositoryID, location.MachineID}})
			} else {
				if effect.PreviousMachineID != nil {
					out = append(out, persistence.Effect{SQL: `DELETE FROM repository_locations WHERE repository_id=? AND machine_id=?`, Args: []any{location.RepositoryID, *effect.PreviousMachineID}})
				}
				out = append(out, repositoryLocationInsert(*location))
			}
		case "update_context":
			c := effect.Context
			if c == nil {
				return nil, nil, errors.New("context effect has no Context")
			}
			out = append(out, contextUpdate(c))
		case "persist_context_configuration":
			c := effect.Context
			if c == nil {
				return nil, nil, errors.New("context effect has no Context")
			}
			out = append(out, contextUpdate(c), persistence.Effect{SQL: `DELETE FROM context_attention_defaults WHERE context_id=?`, Args: []any{c.ID}})
			for _, d := range effect.AttentionDefaults {
				out = append(out, attentionEffect(d))
			}
		case "set_context_grill_defaults", "set_context_implement_defaults", "set_context_dirty_checkout_check":
			c := effect.Context
			if c == nil {
				return nil, nil, errors.New("context effect has no Context")
			}
			out = append(out, contextUpdate(c))
		case "update_project":
			p := effect.Project
			if p == nil {
				return nil, nil, errors.New("project effect has no Project")
			}
			out = append(out, persistence.Effect{SQL: `UPDATE projects SET name=?,default_item_status=?,default_execution_mode=? WHERE id=?`, Args: []any{p.Name, p.Defaults.ItemStatus, p.Defaults.ExecutionMode, p.ID}})
		case "persist_attention_default":
			for _, d := range effect.AttentionDefaults {
				out = append(out, attentionEffect(d))
				audit = append(audit, attentionAuditJSON(d))
			}
		case "persist_external_object":
			o := effect.ExternalObject
			if o == nil {
				return nil, nil, errors.New("external object effect has no External Object")
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO external_objects(id,provider,kind,external_key,canonical_url) VALUES(?,?,?,?,?)`, Args: []any{o.ID, o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL}, InsertedSequence: "next_external_object_id", InsertedID: o.ID})
			audit = append(audit, auditJSON("externalObjectCreated", "external_object_id", o.ID))
		case "persist_external_link":
			link := effect.ExternalLink
			if link == nil {
				return nil, nil, errors.New("external link effect has no Link")
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO external_links(id,item_id,external_object_id,purpose,spec_external_object_id) VALUES(?,?,?,?,?)`, Args: []any{link.ID, link.ItemID, link.ExternalObjectID, link.Purpose, link.SpecExternalObjectID}, InsertedSequence: "next_link_id", InsertedID: link.ID})
			audit = append(audit, auditJSON("linkCreated", "link_id", link.ID))
		case "update_external_link":
			link := effect.ExternalLink
			if link == nil {
				return nil, nil, errors.New("external link effect has no Link")
			}
			var title, state, metadata any
			if link.TitleAttention != nil {
				if *link.TitleAttention {
					title = 1
				} else {
					title = 0
				}
			}
			if link.StateAttention != nil {
				if *link.StateAttention {
					state = 1
				} else {
					state = 0
				}
			}
			if link.MetadataAttention != nil {
				if *link.MetadataAttention {
					metadata = 1
				} else {
					metadata = 0
				}
			}
			var provenance any
			if link.Provenance != nil {
				raw, err := json.Marshal(link.Provenance)
				if err != nil {
					return nil, nil, err
				}
				provenance = string(raw)
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO link_attention_state(link_id,reviewed_activity_id,title_attention,state_attention,metadata_attention,watch_until,review_at,provenance_json) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(link_id) DO UPDATE SET reviewed_activity_id=excluded.reviewed_activity_id,title_attention=excluded.title_attention,state_attention=excluded.state_attention,metadata_attention=excluded.metadata_attention,watch_until=excluded.watch_until,review_at=excluded.review_at,provenance_json=excluded.provenance_json`, Args: []any{link.ID, link.ReviewedActivityID, title, state, metadata, link.WatchUntil, link.ReviewAt, provenance}})
			out = append(out, persistence.Effect{SQL: `UPDATE external_links SET purpose=?,spec_external_object_id=? WHERE id=?`, Args: []any{link.Purpose, link.SpecExternalObjectID, link.ID}})
			audit = append(audit, auditJSON("linkUpdated", "link_id", link.ID))
		case "delete_external_link":
			link := effect.ExternalLink
			if link == nil {
				return nil, nil, errors.New("external link effect has no Link")
			}
			out = append(out, persistence.Effect{SQL: `DELETE FROM external_links WHERE id=?`, Args: []any{link.ID}})
			audit = append(audit, auditJSON("linkDeleted", "link_id", link.ID))
		case "delete_external_object":
			out = append(out, persistence.Effect{SQL: `DELETE FROM external_objects WHERE id=?`, Args: []any{effect.ExternalObjectID}})
			audit = append(audit, auditJSON("externalObjectDeleted", "external_object_id", effect.ExternalObjectID))
		case "persist_external_snapshot":
			snapshot := effect.ExternalSnapshot
			if snapshot == nil {
				return nil, nil, errors.New("external snapshot effect has no snapshot")
			}
			metadata, err := json.Marshal(snapshot.Metadata)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO external_snapshots(external_object_id,title,state,metadata_json,fetched_at) VALUES(?,?,?,?,?) ON CONFLICT(external_object_id) DO UPDATE SET title=excluded.title,state=excluded.state,metadata_json=excluded.metadata_json,fetched_at=excluded.fetched_at`, Args: []any{snapshot.ExternalObjectID, snapshot.Title, snapshot.State, string(metadata), snapshot.FetchedAt}})
		case "persist_external_activity":
			activity := effect.ExternalActivity
			if activity == nil {
				return nil, nil, errors.New("external activity effect has no Activity")
			}
			changes, err := json.Marshal(activity.Changes)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, persistence.Effect{SQL: `INSERT INTO activities(id,external_object_id,observed_at,changes_json) VALUES(?,?,?,?)`, Args: []any{activity.ID, activity.ExternalObjectID, activity.ObservedAt, string(changes)}, InsertedSequence: "next_activity_id", InsertedID: activity.ID})
		case "external_object_refreshed":
			audit = append(audit, auditJSON("externalObjectRefreshed", "external_object_id", effect.ExternalObjectID))
		default:
			itemEffects, itemAudit, handled, itemErr := itemPersistenceEffects(effect)
			if itemErr != nil {
				return nil, nil, itemErr
			}
			if !handled {
				return nil, nil, fmt.Errorf("unsupported persistence effect %q", effect.Kind)
			}
			out = append(out, itemEffects...)
			audit = append(audit, itemAudit...)
		}
	}
	return out, audit, nil
}

func repositoryLocationInsert(location domain.RepositoryLocation) persistence.Effect {
	return persistence.Effect{SQL: `INSERT INTO repository_locations(repository_id,machine_id,checkout_path,worktree_root) VALUES(?,?,?,?)`, Args: []any{location.RepositoryID, location.MachineID, location.CheckoutPath, location.WorktreeRoot}}
}

func pstackRolesJSON(roles domain.PstackRoleTable) string {
	if len(roles) == 0 {
		return ""
	}
	raw, err := json.Marshal(roles)
	if err != nil {
		return ""
	}
	return string(raw)
}
func contextUpdate(c *domain.Context) persistence.Effect {
	return persistence.Effect{SQL: `UPDATE contexts SET name=?,execution_machine_id=?,check_dirty_checkouts=?,grill_agent=?,grill_model=?,grill_effort=?,implement_agent=?,implement_model=?,implement_effort=?,default_workflow=?,pstack_agent=?,pstack_model=?,pstack_effort=?,pstack_roles_json=?,claude_profile_id=?,codex_profile_id=?,gh_executable_path=?,twg_executable_path=?,az_executable_path=?,atlassian_site=?,azure_devops_organization=?,bitbucket_workspace=? WHERE id=?`, Args: []any{c.Name, c.ExecutionMachineID, boolInt64(c.CheckDirtyCheckouts), c.GrillDefaults.Agent, c.GrillDefaults.Model, c.GrillDefaults.Effort, c.ImplementDefaults.Agent, c.ImplementDefaults.Model, c.ImplementDefaults.Effort, c.DefaultWorkflow, c.PstackDefaults.Agent, c.PstackDefaults.Model, c.PstackDefaults.Effort, pstackRolesJSON(c.PstackRoles), c.ClaudeProfileID, c.CodexProfileID, c.GHExecutablePath, c.TWGExecutablePath, c.AZExecutablePath, c.AtlassianSite, c.AzureDevOpsOrganization, c.BitbucketWorkspace, c.ID}}
}
func attentionEffect(d domain.ContextAttentionDefault) persistence.Effect {
	return persistence.Effect{SQL: `INSERT OR REPLACE INTO context_attention_defaults(context_id,object_kind,title_attention,state_attention,metadata_attention) VALUES(?,?,?,?,?)`, Args: []any{d.ContextID, d.ObjectKind, boolInt64(d.Policy.Title), boolInt64(d.Policy.State), boolInt64(d.Policy.Metadata)}}
}
func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
func auditJSON(action, key string, id int64) persistence.AuditAction {
	raw, _ := json.Marshal(map[string]any{"action": action, key: id})
	return persistence.AuditAction(raw)
}

func machineObservationAuditJSON(machine *domain.Machine) persistence.AuditAction {
	raw, _ := json.Marshal(map[string]any{"action": "machineObserved", "machine_id": machine.ID, "observation": machine.LastObserved})
	return persistence.AuditAction(raw)
}

func attentionAuditJSON(defaultValue domain.ContextAttentionDefault) persistence.AuditAction {
	raw, _ := json.Marshal(map[string]any{"action": "contextAttentionDefaultChanged", "context_id": defaultValue.ContextID, "object_kind": defaultValue.ObjectKind})
	return persistence.AuditAction(raw)
}

func (r *Runtime) runEvent(event domain.Event) (any, error) {
	decision, err := r.transition(event)
	if err != nil {
		return nil, err
	}
	switch event.Kind {
	case "set_link_attention_policy", "set_link_purpose", "set_link_watch_until", "set_link_review_at", "clear_link_review_at", "mark_link_reviewed":
		return r.externalLinkView(event.LinkID)
	case "create_context", "create_context_configuration":
		return projectContext(decision.State.Contexts[len(decision.State.Contexts)-1]), nil
	case "update_context", "update_context_configuration", "set_context_grill_defaults", "set_context_implement_defaults", "set_context_dirty_checkout_check":
		for _, c := range decision.State.Contexts {
			if c.ID == event.ContextID {
				return projectContext(c), nil
			}
		}
	case "create_project":
		return decision.State.Projects[len(decision.State.Projects)-1], nil
	case "update_project":
		for _, p := range decision.State.Projects {
			if p.ID == event.ProjectID {
				return p, nil
			}
		}
	case "set_context_attention_default":
		for _, d := range decision.State.AttentionDefaults {
			if d.ContextID == event.ContextID && d.ObjectKind == event.ObjectKind {
				return d, nil
			}
		}
	case "set_context_execution_machine", "set_context_cli_configuration_profile":
		for _, c := range decision.State.Contexts {
			if c.ID == event.ContextID {
				return projectContext(c), nil
			}
		}
	case "register_machine", "update_machine":
		for _, m := range decision.State.Machines {
			if m.ID == decision.State.NextMachineID-1 || m.ID == event.MachineID {
				return m, nil
			}
		}
	case "create_cli_configuration_profile":
		if len(decision.State.CLIConfigurationProfiles) > 0 {
			return cliProfileSettingsView(decision.State.CLIConfigurationProfiles[len(decision.State.CLIConfigurationProfiles)-1]), nil
		}
	case "delete_cli_configuration_profile":
		return nil, nil
	}
	return itemEventResult(decision, event)
}

func (r *Runtime) listProjects() []domain.Project {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.Project{}, r.state.Projects...)
}
func (r *Runtime) listAttentionDefaults() []domain.ContextAttentionDefault {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.ContextAttentionDefault{}, r.state.AttentionDefaults...)
}

func (r *Runtime) listRepositories() []domain.Repository {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.Repository{}, r.state.Repositories...)
}

func (r *Runtime) listRepositoryLocations() []domain.RepositoryLocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.RepositoryLocation{}, r.state.RepositoryLocations...)
}

func (r *Runtime) registerRepository(projectID int64, name, remoteURL string) (domain.Repository, error) {
	decision, err := r.transition(domain.Event{Kind: "register_repository", ProjectID: projectID, Name: name, RemoteURL: remoteURL})
	if err != nil {
		return domain.Repository{}, err
	}
	repository := decision.State.Repositories[len(decision.State.Repositories)-1]
	return repository, nil
}

func (r *Runtime) updateRepository(repositoryID int64, name, remoteURL, baseBranch string) (domain.Repository, error) {
	decision, err := r.transition(domain.Event{Kind: "update_repository", RepositoryID: repositoryID, Name: name, RemoteURL: remoteURL, BaseBranch: baseBranch})
	if err != nil {
		return domain.Repository{}, err
	}
	var repository domain.Repository
	for _, candidate := range decision.State.Repositories {
		if candidate.ID == repositoryID {
			repository = candidate
			break
		}
	}
	return repository, nil
}

func (r *Runtime) registerRepositoryAtLocation(projectID int64, name string, remoteURL *string, baseBranch string, machineID int64, checkoutPath string, worktreeRoot *string, clone bool) (domain.Repository, error) {
	r.mu.Lock()
	stateSnapshot := r.state
	var project *domain.Project
	var machine *domain.Machine
	for i := range r.state.Projects {
		if r.state.Projects[i].ID == projectID {
			p := r.state.Projects[i]
			project = &p
			break
		}
	}
	for i := range r.state.Machines {
		if r.state.Machines[i].ID == machineID {
			m := r.state.Machines[i]
			machine = &m
			break
		}
	}
	access := r.machineAccess
	r.mu.Unlock()
	if project == nil {
		return domain.Repository{}, fmt.Errorf("Project %d does not exist", projectID)
	}
	if machine == nil {
		return domain.Repository{}, fmt.Errorf("Machine %d does not exist", machineID)
	}
	if machine.ContextID != project.ContextID {
		return domain.Repository{}, fmt.Errorf("Machine %d belongs to another Context", machineID)
	}
	home, err := access.HomeDirectory(*machine)
	if err != nil {
		return domain.Repository{}, err
	}
	checkoutPath = normalizeRepositoryPath(checkoutPath, home)
	root := "~/worktrees"
	if worktreeRoot != nil && strings.TrimSpace(*worktreeRoot) != "" {
		root = *worktreeRoot
	}
	root = normalizeRepositoryPath(root, home)
	resolved, err := access.ResolvePath(*machine, checkoutPath)
	if err != nil {
		return domain.Repository{}, err
	}
	git := gitcli.New(access)
	effectiveRemote := ""
	if clone {
		if remoteURL == nil || strings.TrimSpace(*remoteURL) == "" {
			return domain.Repository{}, errors.New("A remote URL is required when cloning a Repository")
		}
		if _, err := domain.Decide(stateSnapshot, domain.Event{Kind: "register_repository_at_location", ProjectID: projectID, Name: name, RemoteURL: *remoteURL, BaseBranch: baseBranch, MachineID: machineID, CheckoutPath: checkoutPath, WorktreeRoot: root}); err != nil {
			return domain.Repository{}, err
		}
		if err := git.Clone(*machine, *remoteURL, resolved); err != nil {
			return domain.Repository{}, err
		}
		effectiveRemote = strings.TrimSpace(*remoteURL)
		_, err := git.Adopt(*machine, resolved, effectiveRemote)
		if err != nil {
			return domain.Repository{}, fmt.Errorf("Repository was cloned, but its checkout could not be inspected: %w; the cloned checkout remains at %s", err, checkoutPath)
		}
	} else {
		expectedRemote := ""
		if remoteURL != nil {
			expectedRemote = *remoteURL
		}
		inspection, err := git.Adopt(*machine, resolved, expectedRemote)
		if err != nil {
			return domain.Repository{}, err
		}
		effectiveRemote = inspection.RemoteURL
		if effectiveRemote == "" {
			return domain.Repository{}, errors.New("The existing checkout has no Git remote")
		}
	}
	r.mu.Lock()
	currentState := r.state
	r.mu.Unlock()
	if !repositoryRegistrationSnapshotIsCurrent(stateSnapshot, currentState, projectID, machineID) {
		err := errors.New("The Project, Machine, or Repository registration changed while Git was inspecting the checkout; review it again")
		if clone {
			return domain.Repository{}, fmt.Errorf("%w; the cloned checkout remains at %s", err, checkoutPath)
		}
		return domain.Repository{}, err
	}
	decision, err := r.transition(domain.Event{Kind: "register_repository_at_location", ProjectID: projectID, Name: name, RemoteURL: effectiveRemote, BaseBranch: baseBranch, MachineID: machineID, CheckoutPath: checkoutPath, WorktreeRoot: root})
	if err != nil {
		if clone {
			return domain.Repository{}, fmt.Errorf("%w; the cloned checkout remains at %s", err, checkoutPath)
		}
		return domain.Repository{}, err
	}
	var repository domain.Repository
	for i := range decision.State.Repositories {
		if decision.State.Repositories[i].ProjectID == projectID && decision.State.Repositories[i].Name == strings.TrimSpace(name) {
			repository = decision.State.Repositories[i]
			break
		}
	}
	if repository.ID == 0 {
		return domain.Repository{}, errors.New("Repository registration produced no Repository")
	}
	return repository, nil
}

func repositoryRegistrationSnapshotIsCurrent(before, after domain.DomainState, projectID, machineID int64) bool {
	var beforeProject, afterProject *domain.Project
	var beforeMachine, afterMachine *domain.Machine
	for i := range before.Projects {
		if before.Projects[i].ID == projectID {
			value := before.Projects[i]
			beforeProject = &value
		}
	}
	for i := range after.Projects {
		if after.Projects[i].ID == projectID {
			value := after.Projects[i]
			afterProject = &value
		}
	}
	for i := range before.Machines {
		if before.Machines[i].ID == machineID {
			value := before.Machines[i]
			beforeMachine = &value
		}
	}
	for i := range after.Machines {
		if after.Machines[i].ID == machineID {
			value := after.Machines[i]
			afterMachine = &value
		}
	}
	if !reflect.DeepEqual(beforeProject, afterProject) || !reflect.DeepEqual(beforeMachine, afterMachine) {
		return false
	}
	repositoriesForProject := func(state domain.DomainState) []domain.Repository {
		var repositories []domain.Repository
		for _, repository := range state.Repositories {
			if repository.ProjectID == projectID {
				repositories = append(repositories, repository)
			}
		}
		return repositories
	}
	if !reflect.DeepEqual(repositoriesForProject(before), repositoriesForProject(after)) {
		return false
	}
	repositoryIDs := make(map[int64]bool)
	for _, repository := range repositoriesForProject(before) {
		repositoryIDs[repository.ID] = true
	}
	locationsForProject := func(state domain.DomainState) []domain.RepositoryLocation {
		var locations []domain.RepositoryLocation
		for _, location := range state.RepositoryLocations {
			if repositoryIDs[location.RepositoryID] {
				locations = append(locations, location)
			}
		}
		return locations
	}
	return reflect.DeepEqual(locationsForProject(before), locationsForProject(after))
}

func (r *Runtime) updateRepositoryLocation(repositoryID int64, previousMachineID *int64, machineID int64, checkoutPath, worktreeRoot string) (domain.RepositoryLocation, error) {
	decision, err := r.transition(domain.Event{Kind: "update_repository_location", RepositoryID: repositoryID, PreviousMachineID: previousMachineID, MachineID: machineID, CheckoutPath: checkoutPath, WorktreeRoot: worktreeRoot})
	if err != nil {
		return domain.RepositoryLocation{}, err
	}
	for _, location := range decision.State.RepositoryLocations {
		if location.RepositoryID == repositoryID && location.MachineID == machineID {
			return location, nil
		}
	}
	return domain.RepositoryLocation{}, errors.New("Repository location update produced no location")
}

func normalizeRepositoryPath(path, home string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		return path
	}
	home = strings.TrimRight(strings.TrimSpace(home), "/")
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		relative := strings.TrimLeft(strings.TrimPrefix(path, home), "/")
		if relative == "" {
			return "~"
		}
		return "~/" + relative
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "~/" + path
}
