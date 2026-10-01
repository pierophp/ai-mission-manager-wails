package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

const resetConfirmationPhrase = "RESET ALL LOCAL DATA"

type ItemDeletionPreview struct {
	Plan     domain.ItemDeletionPlan `json:"plan"`
	Blockers []string                `json:"blockers"`
}
type ItemDeletionResult struct {
	Summary domain.ItemDeletionSummary `json:"summary"`
}
type ParentDeletionPreview struct {
	Plan     domain.ParentDeletionPlan `json:"plan"`
	Blockers []string                  `json:"blockers"`
}
type ParentDeletionResult struct {
	Summary domain.ParentDeletionSummary `json:"summary"`
}
type RepositoryDeletionPreview struct {
	Plan     domain.RepositoryDeletionPlan `json:"plan"`
	Blockers []string                      `json:"blockers"`
}
type RepositoryDeletionResult struct {
	RepositoryID   int64 `json:"repositoryId"`
	WorkspaceCount int   `json:"workspaceCount"`
}
type MachineDeletionPreview struct {
	Plan     domain.MachineDeletionPlan `json:"plan"`
	Blockers []string                   `json:"blockers"`
}
type MachineDeletionResult struct {
	MachineID               int64 `json:"machineId"`
	RunCount                int   `json:"runCount"`
	WorktreeCount           int   `json:"worktreeCount"`
	RepositoryLocationCount int   `json:"repositoryLocationCount"`
	StopAttemptCount        int   `json:"stopAttemptCount"`
	StopFailureCount        int   `json:"stopFailureCount"`
}
type ResetLocalDataPreview struct {
	Plan               domain.ResetLocalDataPlan `json:"plan"`
	AuditEntryCount    int                       `json:"auditEntryCount"`
	Blockers           []string                  `json:"blockers"`
	ConfirmationPhrase string                    `json:"confirmationPhrase"`
}
type ResetLocalDataResult struct {
	Summary         domain.ResetLocalDataSummary `json:"summary"`
	AuditEntryCount int                          `json:"auditEntryCount"`
}

func (r *Runtime) deletionState() (domain.DomainState, error) {
	if r == nil || r.store == nil {
		return domain.DomainState{}, errors.New("runtime is not configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, nil
}
func (r *Runtime) previewKey(kind string, id int64) string { return fmt.Sprintf("%s:%d", kind, id) }
func (r *Runtime) prepareItemDeletion(id int64) (ItemDeletionPreview, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	s, e := r.deletionState()
	if e != nil {
		return ItemDeletionPreview{}, e
	}
	p, e := domain.PlanItemDeletion(s, id)
	if e != nil {
		return ItemDeletionPreview{}, e
	}
	b := []string{}
	for _, rid := range p.ActiveRunIDs {
		b = append(b, fmt.Sprintf("Run #%d is active; stop it before deleting this Item.", rid))
	}
	v := ItemDeletionPreview{p, b}
	r.pendingDeletions[r.previewKey("item", id)] = v
	return v, nil
}
func (r *Runtime) prepareProjectDeletion(id int64) (ParentDeletionPreview, error) {
	return r.prepareParentDeletion("project", id)
}
func (r *Runtime) prepareContextDeletion(id int64) (ParentDeletionPreview, error) {
	return r.prepareParentDeletion("context", id)
}
func (r *Runtime) prepareParentDeletion(kind string, id int64) (ParentDeletionPreview, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	s, e := r.deletionState()
	if e != nil {
		return ParentDeletionPreview{}, e
	}
	var p domain.ParentDeletionPlan
	if kind == "project" {
		p, e = domain.PlanProjectDeletion(s, id)
	} else {
		p, e = domain.PlanContextDeletion(s, id)
	}
	if e != nil {
		return ParentDeletionPreview{}, e
	}
	b := []string{}
	for _, rid := range p.ActiveRunIDs {
		b = append(b, fmt.Sprintf("Run #%d is active; stop it before deleting this %s.", rid, strings.Title(kind)))
	}
	if kind == "context" && len(s.Contexts) == 1 {
		b = append(b, "This is the last Context; create another Context before deleting it.")
	}
	v := ParentDeletionPreview{p, b}
	r.pendingDeletions[r.previewKey(kind, id)] = v
	return v, nil
}
func (r *Runtime) prepareRepositoryDeletion(id int64) (RepositoryDeletionPreview, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	s, e := r.deletionState()
	if e != nil {
		return RepositoryDeletionPreview{}, e
	}
	p, e := domain.PlanRepositoryDeletion(s, id)
	if e != nil {
		return RepositoryDeletionPreview{}, e
	}
	b := []string{}
	for _, run := range s.Runs {
		uses := run.RepositoryID != nil && *run.RepositoryID == id
		if run.WorkspaceID != nil {
			for _, w := range s.Workspaces {
				if w.ID == *run.WorkspaceID {
					for _, wr := range w.Repositories {
						if wr.RepositoryID == id {
							uses = true
						}
					}
				}
			}
		}
		if uses {
			b = append(b, fmt.Sprintf("Run #%d on Item #%d uses this Repository and must be deleted first.", run.ID, run.ItemID))
		}
	}
	v := RepositoryDeletionPreview{p, b}
	r.pendingDeletions[r.previewKey("repository", id)] = v
	return v, nil
}
func (r *Runtime) prepareMachineDeletion(id int64) (MachineDeletionPreview, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	s, e := r.deletionState()
	if e != nil {
		return MachineDeletionPreview{}, e
	}
	p, e := domain.PlanMachineDeletion(s, id)
	if e != nil {
		return MachineDeletionPreview{}, e
	}
	b := []string{}
	for _, rid := range p.ActiveRunIDs {
		b = append(b, fmt.Sprintf("Run #%d is active; stop it before deleting this Machine.", rid))
	}
	v := MachineDeletionPreview{p, b}
	r.pendingDeletions[r.previewKey("machine", id)] = v
	return v, nil
}
func (r *Runtime) prepareResetLocalData() (ResetLocalDataPreview, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	s, e := r.deletionState()
	if e != nil {
		return ResetLocalDataPreview{}, e
	}
	p := domain.PlanResetLocalData(s)
	b := []string{}
	for _, run := range s.Runs {
		if launchRunActive(run) {
			b = append(b, fmt.Sprintf("Run #%d is active; stop it before resetting local data.", run.ID))
		}
	}
	n := len(s.AuditEntries)
	v := ResetLocalDataPreview{p, n, b, resetConfirmationPhrase}
	r.pendingDeletions["reset"] = v
	return v, nil
}

func sortIDs(xs []int64) { sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] }) }
func equalIDs(a, b []int64) bool {
	aa := append([]int64(nil), a...)
	bb := append([]int64(nil), b...)
	sortIDs(aa)
	sortIDs(bb)
	if len(aa) != len(bb) {
		return false
	}
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}
func (r *Runtime) applyDeletion(effects []persistence.Effect, audit persistence.AuditAction) error {
	if err := r.store.Apply(effects, []persistence.AuditAction{audit}); err != nil {
		return err
	}
	s, e := r.store.Load()
	if e != nil {
		return e
	}
	r.mu.Lock()
	r.state = s
	r.mu.Unlock()
	return nil
}
func summaryAudit(action string, summary any) persistence.AuditAction {
	raw, _ := json.Marshal(map[string]any{"action": action, "summary": summary})
	return persistence.AuditAction(raw)
}
func sqlDeleteIDs(table, column string, ids []int64) persistence.Effect {
	if len(ids) == 0 {
		return persistence.Effect{SQL: "SELECT 1"}
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	return persistence.Effect{SQL: fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", table, column, marks), Args: args}
}
func itemCascadeEffects(p domain.ItemDeletionPlan) []persistence.Effect {
	ids := []int64{p.ItemID}
	out := []persistence.Effect{{SQL: "DELETE FROM runs WHERE item_id=?", Args: []any{p.ItemID}}, {SQL: "DELETE FROM worktrees WHERE workspace_id IN (SELECT id FROM workspaces WHERE item_id=?)", Args: []any{p.ItemID}}, {SQL: "DELETE FROM workspace_repositories WHERE workspace_id IN (SELECT id FROM workspaces WHERE item_id=?)", Args: []any{p.ItemID}}, {SQL: "DELETE FROM workspaces WHERE item_id=?", Args: []any{p.ItemID}}, {SQL: "DELETE FROM item_relationships WHERE from_item_id=? OR to_item_id=?", Args: []any{p.ItemID, p.ItemID}}, {SQL: "DELETE FROM reminders WHERE item_id=?", Args: []any{p.ItemID}}, {SQL: "DELETE FROM implementation_queues WHERE item_id=?", Args: []any{p.ItemID}}, {SQL: "DELETE FROM external_links WHERE item_id=?", Args: []any{p.ItemID}}}
	out = append(out, sqlDeleteIDs("external_objects", "id", p.OrphanedExternalObjectIDs))
	out = append(out, sqlDeleteIDs("items", "id", ids))
	return out
}
func (r *Runtime) deleteItem(id int64, confirmed bool) (ItemDeletionResult, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if !confirmed {
		return ItemDeletionResult{}, errors.New("Item deletion requires explicit confirmation after reviewing its deletion preview")
	}
	s, e := r.deletionState()
	if e != nil {
		return ItemDeletionResult{}, e
	}
	current, e := domain.PlanItemDeletion(s, id)
	if e != nil {
		return ItemDeletionResult{}, e
	}
	pending, ok := r.pendingDeletions[r.previewKey("item", id)].(ItemDeletionPreview)
	if !ok {
		return ItemDeletionResult{}, errors.New("Review the Item deletion preview before deleting it")
	}
	if pending.Plan.StateFingerprint != current.StateFingerprint {
		return ItemDeletionResult{}, errors.New("The Item changed after the preview; review the updated deletion preview")
	}
	if len(current.ActiveRunIDs) > 0 {
		return ItemDeletionResult{}, errors.New("Item deletion is blocked by active Runs")
	}
	if e = r.applyDeletion(itemCascadeEffects(current), summaryAudit("itemDeleted", current.Summary())); e != nil {
		return ItemDeletionResult{}, e
	}
	delete(r.pendingDeletions, r.previewKey("item", id))
	return ItemDeletionResult{current.Summary()}, nil
}
func parentEffects(p domain.ParentDeletionPlan) []persistence.Effect {
	out := []persistence.Effect{}
	itemIDs := []int64{}
	repoIDs := []int64{}
	projectIDs := []int64{}
	machineIDs := []int64{}
	for _, v := range p.Items {
		itemIDs = append(itemIDs, v.ID)
	}
	for _, v := range p.Repositories {
		repoIDs = append(repoIDs, v.ID)
	}
	for _, v := range p.Projects {
		projectIDs = append(projectIDs, v.ID)
	}
	for _, v := range p.Machines {
		machineIDs = append(machineIDs, v.ID)
	}
	// Child tables with restrictive FKs are removed explicitly in dependency order.
	out = append(out, sqlDeleteIDs("runs", "item_id", itemIDs))
	out = append(out, persistence.Effect{SQL: "DELETE FROM runs WHERE machine_id IN (SELECT id FROM machines WHERE id IN (" + sqlMarks(machineIDs) + "))", Args: intArgs(machineIDs)})
	out = append(out, sqlDeleteIDs("worktrees", "workspace_id", workspaceIDsForItems(p.Workspaces)))
	out = append(out, sqlDeleteIDs("worktrees", "repository_id", repoIDs))
	out = append(out, sqlDeleteIDs("worktrees", "machine_id", machineIDs))
	out = append(out, sqlDeleteIDs("workspace_repositories", "repository_id", repoIDs))
	out = append(out, sqlDeleteIDs("workspace_repositories", "workspace_id", workspaceIDsForItems(p.Workspaces)))
	out = append(out, sqlDeleteIDs("workspaces", "item_id", itemIDs))
	out = append(out, sqlDeleteIDs("repository_locations", "repository_id", repoIDs))
	out = append(out, sqlDeleteIDs("repository_locations", "machine_id", machineIDs))
	out = append(out, sqlDeleteIDs("cli_configuration_profiles", "machine_id", machineIDs))
	out = append(out, sqlDeleteIDs("item_relationships", "from_item_id", itemIDs))
	out = append(out, persistence.Effect{SQL: "DELETE FROM item_relationships WHERE to_item_id IN (" + sqlMarks(itemIDs) + ")", Args: intArgs(itemIDs)})
	out = append(out, sqlDeleteIDs("reminders", "item_id", itemIDs))
	out = append(out, sqlDeleteIDs("implementation_queues", "item_id", itemIDs))
	out = append(out, sqlDeleteIDs("external_links", "item_id", itemIDs))
	out = append(out, sqlDeleteIDs("external_objects", "id", p.OrphanedExternalObjectIDs))
	out = append(out, sqlDeleteIDs("items", "id", itemIDs))
	out = append(out, sqlDeleteIDs("repositories", "id", repoIDs))
	out = append(out, sqlDeleteIDs("machines", "id", machineIDs))
	out = append(out, sqlDeleteIDs("projects", "id", projectIDs))
	if p.ContextID != nil {
		out = append(out, persistence.Effect{SQL: "DELETE FROM context_attention_defaults WHERE context_id=?", Args: []any{*p.ContextID}}, persistence.Effect{SQL: "DELETE FROM contexts WHERE id=?", Args: []any{*p.ContextID}})
	}
	return out
}
func workspaceIDsForItems(ws []domain.ItemDeletionWorkspace) []int64 {
	x := make([]int64, 0, len(ws))
	for _, w := range ws {
		x = append(x, w.ID)
	}
	return x
}
func intArgs(xs []int64) []any {
	x := make([]any, len(xs))
	for i, v := range xs {
		x[i] = v
	}
	return x
}
func sqlMarks(xs []int64) string {
	if len(xs) == 0 {
		return "NULL"
	}
	return strings.TrimRight(strings.Repeat("?,", len(xs)), ",")
}
func (r *Runtime) deleteParent(kind string, id int64, projectIDs, itemIDs, repositoryIDs, workspaceIDs, machineIDs []int64, confirmed bool) (ParentDeletionResult, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if !confirmed {
		return ParentDeletionResult{}, fmt.Errorf("%s deletion requires explicit confirmation after reviewing its deletion preview", strings.Title(kind))
	}
	s, e := r.deletionState()
	if e != nil {
		return ParentDeletionResult{}, e
	}
	var p domain.ParentDeletionPlan
	if kind == "project" {
		p, e = domain.PlanProjectDeletion(s, id)
	} else {
		p, e = domain.PlanContextDeletion(s, id)
	}
	if e != nil {
		return ParentDeletionResult{}, e
	}
	pending, ok := r.pendingDeletions[r.previewKey(kind, id)].(ParentDeletionPreview)
	if !ok {
		return ParentDeletionResult{}, fmt.Errorf("Review the %s deletion preview before deleting it", strings.Title(kind))
	}
	if pending.Plan.StateFingerprint != p.StateFingerprint {
		return ParentDeletionResult{}, fmt.Errorf("The %s changed after the preview; review the updated deletion preview", strings.Title(kind))
	}
	if len(p.ActiveRunIDs) > 0 {
		return ParentDeletionResult{}, fmt.Errorf("%s deletion is blocked by active Runs", strings.Title(kind))
	}
	if kind == "context" && len(s.Contexts) == 1 {
		return ParentDeletionResult{}, errors.New("Cannot delete the last Context")
	}
	expectedProjectIDs := []int64{}
	for _, v := range p.Projects {
		expectedProjectIDs = append(expectedProjectIDs, v.ID)
	}
	expectedItemIDs := []int64{}
	for _, v := range p.Items {
		expectedItemIDs = append(expectedItemIDs, v.ID)
	}
	expectedRepoIDs := []int64{}
	for _, v := range p.Repositories {
		expectedRepoIDs = append(expectedRepoIDs, v.ID)
	}
	if !equalIDs(itemIDs, expectedItemIDs) || !equalIDs(repositoryIDs, expectedRepoIDs) || !equalIDs(workspaceIDs, workspaceIDsForItems(p.Workspaces)) || (kind == "context" && !equalIDs(projectIDs, expectedProjectIDs)) {
		return ParentDeletionResult{}, errors.New("The reviewed deletion contents do not match the confirmation; review the preview again")
	}
	if kind == "context" {
		wantMachines := []int64{}
		for _, m := range p.Machines {
			wantMachines = append(wantMachines, m.ID)
		}
		if !equalIDs(machineIDs, wantMachines) {
			return ParentDeletionResult{}, errors.New("The reviewed Context Machines do not match the confirmation")
		}
	}
	if e = r.applyDeletion(parentEffects(p), summaryAudit(kind+"Deleted", p.Summary())); e != nil {
		return ParentDeletionResult{}, e
	}
	delete(r.pendingDeletions, r.previewKey(kind, id))
	return ParentDeletionResult{p.Summary()}, nil
}
func (r *Runtime) deleteProject(id int64, itemIDs, repositoryIDs, workspaceIDs []int64, confirmed bool) (ParentDeletionResult, error) {
	return r.deleteParent("project", id, nil, itemIDs, repositoryIDs, workspaceIDs, nil, confirmed)
}
func (r *Runtime) deleteContext(id int64, projectIDs, itemIDs, repositoryIDs, workspaceIDs, machineIDs []int64, confirmed bool) (ParentDeletionResult, error) {
	return r.deleteParent("context", id, projectIDs, itemIDs, repositoryIDs, workspaceIDs, machineIDs, confirmed)
}

func (r *Runtime) deleteRepository(id int64, workspaceIDs []int64, confirmed bool) (RepositoryDeletionResult, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if !confirmed {
		return RepositoryDeletionResult{}, errors.New("Repository deletion requires explicit confirmation after reviewing its deletion preview")
	}
	s, e := r.deletionState()
	if e != nil {
		return RepositoryDeletionResult{}, e
	}
	p, e := domain.PlanRepositoryDeletion(s, id)
	if e != nil {
		return RepositoryDeletionResult{}, e
	}
	pending, ok := r.pendingDeletions[r.previewKey("repository", id)].(RepositoryDeletionPreview)
	if !ok || pending.Plan.StateFingerprint != p.StateFingerprint {
		return RepositoryDeletionResult{}, errors.New("Review the Repository deletion preview again; its state changed")
	}
	wids := make([]int64, 0, len(p.Workspaces))
	for _, w := range p.Workspaces {
		wids = append(wids, w.ID)
	}
	if !equalIDs(workspaceIDs, wids) {
		return RepositoryDeletionResult{}, errors.New("The reviewed Repository workspaces do not match the confirmation")
	}
	for _, run := range s.Runs {
		uses := run.RepositoryID != nil && *run.RepositoryID == id
		if run.WorkspaceID != nil {
			for _, w := range s.Workspaces {
				if w.ID == *run.WorkspaceID {
					for _, wr := range w.Repositories {
						if wr.RepositoryID == id {
							uses = true
						}
					}
				}
			}
		}
		if uses {
			return RepositoryDeletionResult{}, fmt.Errorf("Repository deletion is blocked by Run #%d", run.ID)
		}
	}
	effects := []persistence.Effect{{SQL: "DELETE FROM repository_locations WHERE repository_id=?", Args: []any{id}}, {SQL: "DELETE FROM worktrees WHERE repository_id=? OR workspace_id IN (SELECT workspace_id FROM workspace_repositories WHERE repository_id=?)", Args: []any{id, id}}, {SQL: "DELETE FROM workspaces WHERE id IN (SELECT workspace_id FROM workspace_repositories WHERE repository_id=?)", Args: []any{id}}, {SQL: "DELETE FROM workspace_repositories WHERE repository_id=? OR workspace_id IN (SELECT workspace_id FROM workspace_repositories WHERE repository_id=?)", Args: []any{id, id}}, {SQL: "DELETE FROM repositories WHERE id=?", Args: []any{id}}}
	rawAudit, _ := json.Marshal(map[string]any{"action": "repositoryDeleted", "repository_id": id, "workspace_count": len(p.Workspaces)})
	if e = r.applyDeletion(effects, persistence.AuditAction(rawAudit)); e != nil {
		return RepositoryDeletionResult{}, e
	}
	delete(r.pendingDeletions, r.previewKey("repository", id))
	return RepositoryDeletionResult{id, len(p.Workspaces)}, nil
}
func (r *Runtime) deleteMachine(id int64, runIDs, worktreeIDs, locationRepositoryIDs []int64, confirmed bool) (MachineDeletionResult, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if !confirmed {
		return MachineDeletionResult{}, errors.New("Machine deletion requires explicit confirmation after reviewing its deletion preview")
	}
	s, e := r.deletionState()
	if e != nil {
		return MachineDeletionResult{}, e
	}
	p, e := domain.PlanMachineDeletion(s, id)
	if e != nil {
		return MachineDeletionResult{}, e
	}
	pending, ok := r.pendingDeletions[r.previewKey("machine", id)].(MachineDeletionPreview)
	if !ok || pending.Plan.StateFingerprint != p.StateFingerprint {
		return MachineDeletionResult{}, errors.New("Review the Machine deletion preview again; its state changed")
	}
	if len(p.ActiveRunIDs) > 0 {
		return MachineDeletionResult{}, errors.New("Machine deletion is blocked by active Runs")
	}
	if !equalIDs(runIDs, idsMachineRuns(p.Runs)) || !equalIDs(worktreeIDs, p.WorktreeIDs) || !equalIDs(locationRepositoryIDs, p.RepositoryLocationRepositoryIDs) {
		return MachineDeletionResult{}, errors.New("The reviewed Machine deletion contents do not match the confirmation")
	}
	machine := domain.Machine{}
	for _, m := range s.Machines {
		if m.ID == id {
			machine = m
		}
	}
	stopped, failed := r.killMachinePanes(machine, p.Runs)
	effects := []persistence.Effect{{SQL: "DELETE FROM runs WHERE machine_id=?", Args: []any{id}}, {SQL: "DELETE FROM worktrees WHERE machine_id=?", Args: []any{id}}, {SQL: "DELETE FROM repository_locations WHERE machine_id=?", Args: []any{id}}, {SQL: "DELETE FROM cli_configuration_profiles WHERE machine_id=?", Args: []any{id}}, {SQL: "UPDATE contexts SET execution_machine_id=NULL,claude_profile_id=NULL,codex_profile_id=NULL WHERE execution_machine_id=?", Args: []any{id}}, {SQL: "DELETE FROM machines WHERE id=?", Args: []any{id}}}
	removedWorktrees := map[int64]bool{}
	nextState := s
	for _, w := range s.Worktrees {
		if w.MachineID == id {
			removedWorktrees[w.WorkspaceID] = true
		}
	}
	keptWorktrees := make([]domain.Worktree, 0, len(s.Worktrees))
	for _, w := range s.Worktrees {
		if w.MachineID != id {
			keptWorktrees = append(keptWorktrees, w)
		}
	}
	nextState.Worktrees = keptWorktrees
	for workspaceID := range removedWorktrees {
		for _, workspace := range s.Workspaces {
			if workspace.ID != workspaceID {
				continue
			}
			preparation := domain.RecomputeWorkspacePreparationState(nextState, workspaceID, workspace.PreparationState)
			effects = append(effects, persistence.Effect{SQL: "UPDATE workspaces SET preparation_state=? WHERE id=?", Args: []any{preparation, workspaceID}})
		}
	}
	rawAudit, _ := json.Marshal(map[string]any{"action": "machineDeleted", "machine_id": id, "run_count": len(p.Runs), "worktree_count": len(p.WorktreeIDs)})
	if e = r.applyDeletion(effects, persistence.AuditAction(rawAudit)); e != nil {
		return MachineDeletionResult{}, e
	}
	r.mu.Lock()
	r.machineCheckGenerations[id]++
	delete(r.machineReadiness, id)
	r.mu.Unlock()
	delete(r.pendingDeletions, r.previewKey("machine", id))
	return MachineDeletionResult{id, len(p.Runs), len(p.WorktreeIDs), len(p.RepositoryLocationRepositoryIDs), stopped, failed}, nil
}
func idsMachineRuns(rs []domain.MachineDeletionRun) []int64 {
	x := make([]int64, len(rs))
	for i, v := range rs {
		x[i] = v.ID
	}
	return x
}
func (r *Runtime) killMachinePanes(m domain.Machine, runs []domain.MachineDeletionRun) (int, int) {
	ids := []string{}
	r.mu.Lock()
	stateRuns := append([]domain.Run(nil), r.state.Runs...)
	r.mu.Unlock()
	for _, run := range runs {
		if run.PaneStatus != domain.PaneMissing {
			for _, s := range stateRuns {
				if s.ID == run.ID {
					ids = append(ids, s.PaneID)
				}
			}
		}
	}
	fail := 0
	for _, pane := range ids {
		command := "tmux -f /dev/null -L " + shellQuote(m.SocketName) + " kill-pane -t " + shellQuote(pane)
		if timed, ok := r.machineAccess.(TimedMachineAccess); ok {
			if _, err := timed.RunShellWithTimeout(m, command, 5*time.Second); err != nil {
				fail++
			}
			continue
		}
		done := make(chan error, 1)
		go func() {
			_, err := r.machineAccess.RunShell(m, command)
			done <- err
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		select {
		case err := <-done:
			if err != nil {
				fail++
			}
		case <-ctx.Done():
			fail++
		}
		cancel()
	}
	return len(ids), fail
}
func (r *Runtime) resetAllLocalData(confirmation string) (ResetLocalDataResult, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if confirmation != resetConfirmationPhrase {
		return ResetLocalDataResult{}, fmt.Errorf("Reset requires the exact confirmation phrase: %s", resetConfirmationPhrase)
	}
	s, e := r.deletionState()
	if e != nil {
		return ResetLocalDataResult{}, e
	}
	p := domain.PlanResetLocalData(s)
	pending, ok := r.pendingDeletions["reset"].(ResetLocalDataPreview)
	if !ok {
		return ResetLocalDataResult{}, errors.New("Review the reset preview before resetting local data")
	}
	if pending.Plan.StateFingerprint != p.StateFingerprint {
		return ResetLocalDataResult{}, errors.New("The local model changed after the reset preview; review it again")
	}
	for _, run := range s.Runs {
		if launchRunActive(run) {
			return ResetLocalDataResult{}, fmt.Errorf("Reset is blocked by active Run #%d", run.ID)
		}
	}
	n := len(s.AuditEntries)
	tables := []string{"link_attention_state", "external_links", "activities", "external_snapshots", "external_objects", "item_relationships", "reminders", "implementation_queues", "runs", "worktrees", "workspace_repositories", "workspaces", "repository_locations", "repositories", "cli_configuration_profiles", "machines", "items", "projects", "context_attention_defaults", "contexts"}
	effects := []persistence.Effect{}
	for _, t := range tables {
		effects = append(effects, persistence.Effect{SQL: "DELETE FROM " + t})
	}
	ctxID := s.NextContextID
	if ctxID < 1 {
		ctxID = 1
	}
	projectID := s.NextProjectID
	if projectID < 1 {
		projectID = 1
	}
	effects = append(effects, persistence.Effect{SQL: "INSERT INTO contexts(id,name) VALUES(?, 'Personal')", Args: []any{ctxID}}, persistence.Effect{SQL: "INSERT INTO projects(id,context_id,name,default_item_status,default_execution_mode) VALUES(?,?, 'Default','Inbox','worktree')", Args: []any{projectID, ctxID}}, persistence.Effect{SQL: "UPDATE metadata SET value=? WHERE key='next_context_id'", Args: []any{ctxID + 1}}, persistence.Effect{SQL: "UPDATE metadata SET value=? WHERE key='next_project_id'", Args: []any{projectID + 1}})
	rawAudit, _ := json.Marshal(map[string]any{"action": "resetBoundary", "context_id": ctxID, "project_id": projectID})
	if e = r.applyDeletion(effects, persistence.AuditAction(rawAudit)); e != nil {
		return ResetLocalDataResult{}, e
	}
	r.closeAllTerminalConnections()
	r.mu.Lock()
	for _, run := range s.Runs {
		r.runObservationGeneration[run.ID]++
	}
	for _, machine := range s.Machines {
		r.machineCheckGenerations[machine.ID]++
		delete(r.machineReadiness, machine.ID)
	}
	r.mu.Unlock()
	r.pendingDeletions = map[string]any{}
	return ResetLocalDataResult{p.Summary, n}, nil
}
