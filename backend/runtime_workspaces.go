package backend

import (
	"errors"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

// ensureProjectWorkspaces runs the pure domain reconciliation when opening a
// Runtime, matching the same event path used after item/Repository commits.
func (r *Runtime) ensureProjectWorkspaces() error {
	if r == nil || r.store == nil {
		return nil
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	return r.ensureProjectWorkspacesLocked()
}

// The caller holds transitionMu so the in-memory snapshot cannot change between
// Decide and applying its persistence effects.
func (r *Runtime) ensureProjectWorkspacesLocked() error {
	r.mu.Lock()
	snapshot := r.state
	r.mu.Unlock()
	decision, err := domain.Decide(snapshot, domain.Event{Kind: "ensure_project_workspaces"})
	if err != nil {
		return err
	}
	effects := make([]persistence.Effect, 0)
	for _, effect := range decision.Effects {
		workspace := effect.Workspace
		if workspace == nil {
			return errors.New("workspace effect has no Workspace")
		}
		switch effect.Kind {
		case "persist_workspace":
			effects = append(effects, persistence.Effect{SQL: `INSERT INTO workspaces(id,item_id,preparation_state) VALUES(?,?,?)`, Args: []any{workspace.ID, workspace.ItemID, workspace.PreparationState}, InsertedSequence: "next_workspace_id", InsertedID: workspace.ID})
		case "update_workspace_repositories":
			effects = append(effects, persistence.Effect{SQL: `DELETE FROM workspace_repositories WHERE workspace_id=?`, Args: []any{workspace.ID}})
		default:
			return errors.New("unsupported Workspace effect: " + string(effect.Kind))
		}
		for _, repository := range workspace.Repositories {
			effects = append(effects, workspaceRepositoryInsert(workspace.ID, repository))
		}
	}
	if len(effects) > 0 {
		if err := r.store.Apply(effects, nil); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.state = decision.State
	r.mu.Unlock()
	return nil
}

func workspaceRepositoryInsert(workspaceID int64, repository domain.WorkspaceRepository) persistence.Effect {
	return persistence.Effect{SQL: `INSERT INTO workspace_repositories(workspace_id,repository_id,branch,base_branch) VALUES(?,?,?,?)`, Args: []any{workspaceID, repository.RepositoryID, repository.Branch, repository.BaseBranch}}
}
