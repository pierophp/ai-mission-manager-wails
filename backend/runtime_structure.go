package backend

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func (r *Runtime) transition(event domain.Event) (domain.Decision, error) {
	if r == nil || r.store == nil {
		return domain.Decision{}, errors.New("runtime is not configured")
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	snapshot := r.state
	r.mu.Unlock()
	decision, err := domain.Decide(snapshot, event)
	if err != nil {
		return domain.Decision{}, err
	}
	effects, audit, err := persistenceEffects(decision.Effects)
	if err != nil {
		return domain.Decision{}, err
	}
	r.mu.Lock()
	if !sameSequences(r.state, snapshot) {
		r.mu.Unlock()
		return domain.Decision{}, errors.New("Mission Manager state changed; retry the command")
	}
	r.mu.Unlock()
	if err := r.store.Apply(effects, audit); err != nil {
		return domain.Decision{}, err
	}
	auditEntries, auditErr := r.store.ListAuditHistory()
	if auditErr == nil {
		decision.State.AuditEntries = auditEntries
	}
	r.mu.Lock()
	r.state = decision.State
	r.mu.Unlock()
	return decision, nil
}

func sameSequences(a, b domain.DomainState) bool {
	return a.NextContextID == b.NextContextID && a.NextProjectID == b.NextProjectID && a.NextAuditID == b.NextAuditID
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
