package backend

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/gitcli"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

type WorktreeRemovalReport struct {
	WorktreeID                      int64  `json:"worktreeId"`
	WorkspaceID                     int64  `json:"workspaceId"`
	RepositoryID                    int64  `json:"repositoryId"`
	RepositoryName                  string `json:"repositoryName"`
	MachineID                       int64  `json:"machineId"`
	Path                            string `json:"path"`
	Branch                          string `json:"branch"`
	IsDirty                         bool   `json:"isDirty"`
	RequiresDestructiveConfirmation bool   `json:"requiresDestructiveConfirmation"`
}

type WorktreeRemovalResult struct {
	WorktreeID      int64 `json:"worktreeId"`
	BranchPreserved bool  `json:"branchPreserved"`
}

type pendingWorktreeRemoval struct {
	Report     WorktreeRemovalReport
	Worktree   domain.Worktree
	Repository domain.Repository
	Machine    domain.Machine
	Location   domain.RepositoryLocation
}

func (r *Runtime) recoverWorktreeRemovalIntents() error {
	if r == nil || r.store == nil {
		return nil
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	intents, err := r.store.ListWorktreeRemovalIntents()
	if err != nil {
		return err
	}
	for _, intent := range intents {
		state := r.domainSnapshot()
		worktree, ok := findWorktree(state, intent.WorktreeID)
		if !ok {
			if err := r.store.DeleteWorktreeRemovalIntent(intent.WorktreeID); err != nil {
				return err
			}
			continue
		}
		if worktree.Path != intent.Path {
			continue
		}
		machine, ok := findMachine(state, worktree.MachineID)
		if !ok {
			continue
		}
		exists, err := gitcli.New(r.machineAccess).PathExists(machine, worktree.Path)
		if err != nil {
			continue // Keep the durable intent for the next startup if the Machine is offline.
		}
		if exists {
			// Git removal did not complete; keep the durable Worktree record and
			// clear the now-resolved intent so the user can review and retry.
			if err := r.store.DeleteWorktreeRemovalIntent(intent.WorktreeID); err != nil {
				return err
			}
			continue
		}
		if _, err := r.transitionLocked(domain.Event{Kind: "remove_worktree", WorktreeID: worktree.ID, Confirmed: true}); err != nil {
			return fmt.Errorf("finish interrupted Worktree removal %d: %w", worktree.ID, err)
		}
		if err := r.store.DeleteWorktreeRemovalIntent(intent.WorktreeID); err != nil {
			return fmt.Errorf("clear completed Worktree removal intent %d: %w", intent.WorktreeID, err)
		}
	}
	return nil
}

func (r *Runtime) createWorktree(workspaceID, repositoryID, machineID int64, path, branch, baseBranch string) (domain.Worktree, error) {
	state := r.domainSnapshot()
	workspace, item, repository, machine, location, err := worktreeContext(state, workspaceID, repositoryID, machineID)
	if err != nil {
		return domain.Worktree{}, err
	}
	path, err = r.machineAccess.ResolvePath(machine, path)
	if err != nil {
		return domain.Worktree{}, err
	}
	repositoryPath, err := r.machineAccess.ResolvePath(machine, location.CheckoutPath)
	if err != nil {
		return domain.Worktree{}, err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = workspaceBranch(workspace, item, repositoryID)
	}
	if baseBranch == "" {
		baseBranch = repository.BaseBranch
	}
	git := gitcli.New(r.machineAccess)
	newBranch, err := git.PrepareWorktreeWithResult(machine, repositoryPath, path, branch, baseBranch, repository.RemoteURL, false)
	if err != nil {
		return domain.Worktree{}, err
	}
	return r.registerCreatedWorktree(domain.Worktree{WorkspaceID: workspace.ID, RepositoryID: repository.ID, MachineID: machine.ID, Path: path, Branch: branch, BaseBranch: baseBranch}, machine, repositoryPath, newBranch)
}

func (r *Runtime) prepareWorktree(workspaceID, repositoryID, machineID int64, reuseExistingBranch, confirmDirtyAttachment bool) (domain.Worktree, error) {
	state := r.domainSnapshot()
	workspace, item, repository, machine, location, err := worktreeContext(state, workspaceID, repositoryID, machineID)
	if err != nil {
		return domain.Worktree{}, err
	}
	root, err := r.machineAccess.ResolvePath(machine, location.WorktreeRoot)
	if err != nil {
		return domain.Worktree{}, err
	}
	repositoryPath, err := r.machineAccess.ResolvePath(machine, location.CheckoutPath)
	if err != nil {
		return domain.Worktree{}, err
	}
	path := filepath.Join(root, worktreeDirectory(item, repository))
	branch := workspaceBranch(workspace, item, repositoryID)
	git := gitcli.New(r.machineAccess)
	newBranch, err := git.PrepareWorktreeWithResult(machine, repositoryPath, path, branch, repository.BaseBranch, repository.RemoteURL, reuseExistingBranch)
	if err != nil {
		return domain.Worktree{}, err
	}
	checkout, err := git.Adopt(machine, path, repository.RemoteURL)
	if err != nil {
		return domain.Worktree{}, cleanupPreparedWorktree(git, machine, repositoryPath, path, branch, newBranch, err)
	}
	if checkout.Dirty && !confirmDirtyAttachment {
		return domain.Worktree{}, cleanupPreparedWorktree(git, machine, repositoryPath, path, branch, newBranch, fmt.Errorf("dirty Worktree attachment must be confirmed"))
	}
	return r.registerCreatedWorktree(domain.Worktree{WorkspaceID: workspace.ID, RepositoryID: repository.ID, MachineID: machine.ID, Path: path, Branch: branch, BaseBranch: repository.BaseBranch, IsDirty: checkout.Dirty}, machine, repositoryPath, newBranch)
}

func cleanupPreparedWorktree(git gitcli.GitCLI, machine domain.Machine, repositoryPath, path, branch string, deleteBranch bool, cause error) error {
	if err := git.RollbackPreparedWorktree(machine, repositoryPath, path, branch, deleteBranch); err != nil {
		return fmt.Errorf("Worktree preparation failed: %v; the checkout at %s could not be removed: %w", cause, path, err)
	}
	return cause
}

func (r *Runtime) attachWorktree(workspaceID, repositoryID, machineID int64, path string, confirmDirtyAttachment bool) (domain.Worktree, error) {
	state := r.domainSnapshot()
	workspace, item, repository, machine, location, err := worktreeContext(state, workspaceID, repositoryID, machineID)
	if err != nil {
		return domain.Worktree{}, err
	}
	path, err = r.machineAccess.ResolvePath(machine, path)
	if err != nil {
		return domain.Worktree{}, err
	}
	repositoryPath, err := r.machineAccess.ResolvePath(machine, location.CheckoutPath)
	if err != nil {
		return domain.Worktree{}, err
	}
	git := gitcli.New(r.machineAccess)
	checkouts, err := git.ListWorktrees(machine, repositoryPath)
	if err != nil {
		return domain.Worktree{}, err
	}
	listed := false
	var branch string
	for _, candidate := range checkouts {
		if filepath.Clean(candidate.Path) == filepath.Clean(path) {
			listed = true
			branch = candidate.Branch
			break
		}
	}
	if !listed {
		return domain.Worktree{}, fmt.Errorf("path is not a Git Worktree of the configured Repository")
	}
	if branch == "" {
		return domain.Worktree{}, fmt.Errorf("detached Git Worktrees cannot be attached")
	}
	if expected := workspaceBranch(workspace, item, repositoryID); branch != expected {
		return domain.Worktree{}, fmt.Errorf("Worktree branch does not match the branch configured for this Workspace: expected %s, found %s", expected, branch)
	}
	checkout, err := git.Adopt(machine, path, repository.RemoteURL)
	if err != nil {
		return domain.Worktree{}, err
	}
	if checkout.Dirty && !confirmDirtyAttachment {
		return domain.Worktree{}, fmt.Errorf("dirty Worktree attachment must be confirmed")
	}
	baseBranch := repository.BaseBranch
	wt := domain.Worktree{WorkspaceID: workspace.ID, RepositoryID: repository.ID, MachineID: machine.ID, Path: path, Branch: branch, BaseBranch: baseBranch, IsDirty: checkout.Dirty}
	return r.applyWorktreeEvent(domain.Event{Kind: "register_worktree", Worktree: &wt})
}

func (r *Runtime) prepareWorktreeRemoval(worktreeID int64) (WorktreeRemovalReport, error) {
	state := r.domainSnapshot()
	worktree, ok := findWorktree(state, worktreeID)
	if !ok {
		return WorktreeRemovalReport{}, fmt.Errorf("Worktree %d does not exist", worktreeID)
	}
	machine, ok := findMachine(state, worktree.MachineID)
	if !ok {
		return WorktreeRemovalReport{}, fmt.Errorf("Machine %d does not exist", worktree.MachineID)
	}
	repository, ok := findRepository(state, worktree.RepositoryID)
	if !ok {
		return WorktreeRemovalReport{}, fmt.Errorf("Repository %d does not exist", worktree.RepositoryID)
	}
	location, ok := findRepositoryLocation(state, worktree.RepositoryID, worktree.MachineID)
	if !ok {
		return WorktreeRemovalReport{}, fmt.Errorf("Repository has no location configured on Machine %s", machine.Name)
	}
	git := gitcli.New(r.machineAccess)
	checkoutPath, err := r.machineAccess.ResolvePath(machine, location.CheckoutPath)
	if err != nil {
		return WorktreeRemovalReport{}, err
	}
	if err := ensureListedWorktree(git, machine, checkoutPath, worktree.Path); err != nil {
		return WorktreeRemovalReport{}, err
	}
	checkout, err := git.Adopt(machine, worktree.Path, repository.RemoteURL)
	if err != nil {
		return WorktreeRemovalReport{}, err
	}
	if checkout.Branch != worktree.Branch {
		return WorktreeRemovalReport{}, fmt.Errorf("Worktree branch changed; expected %s, found %s", worktree.Branch, checkout.Branch)
	}
	report := WorktreeRemovalReport{WorktreeID: worktree.ID, WorkspaceID: worktree.WorkspaceID, RepositoryID: repository.ID, RepositoryName: repository.Name, MachineID: machine.ID, Path: worktree.Path, Branch: worktree.Branch, IsDirty: checkout.Dirty, RequiresDestructiveConfirmation: checkout.Dirty}
	r.mu.Lock()
	defer r.mu.Unlock()
	currentWorktree, currentWorktreeExists := findWorktree(r.state, worktree.ID)
	currentRepository, currentRepositoryExists := findRepository(r.state, repository.ID)
	currentMachine, currentMachineExists := findMachine(r.state, machine.ID)
	currentLocation, currentLocationExists := findRepositoryLocation(r.state, repository.ID, machine.ID)
	if !currentWorktreeExists || !currentRepositoryExists || !currentMachineExists || !currentLocationExists || !reflect.DeepEqual(worktree, currentWorktree) || !reflect.DeepEqual(repository, currentRepository) || !reflect.DeepEqual(machine, currentMachine) || !reflect.DeepEqual(location, currentLocation) {
		return WorktreeRemovalReport{}, fmt.Errorf("The Worktree or its Repository location changed while its state was inspected; review it again")
	}
	r.pendingWorktreeRemovals[worktreeID] = pendingWorktreeRemoval{Report: report, Worktree: worktree, Repository: repository, Machine: machine, Location: location}
	return report, nil
}

func (r *Runtime) removeWorktree(worktreeID int64, confirmed, destructiveConfirmed bool) (WorktreeRemovalResult, error) {
	if !confirmed {
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree removal requires explicit confirmation after reviewing its safety report")
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	state := r.domainSnapshot()
	r.mu.Lock()
	pending, hasPending := r.pendingWorktreeRemovals[worktreeID]
	r.mu.Unlock()
	if !hasPending {
		return WorktreeRemovalResult{}, fmt.Errorf("Review the Worktree removal safety report before removing it")
	}
	worktree, ok := findWorktree(state, worktreeID)
	if !ok {
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree %d does not exist", worktreeID)
	}
	machine, ok := findMachine(state, worktree.MachineID)
	if !ok {
		return WorktreeRemovalResult{}, fmt.Errorf("Machine %d does not exist", worktree.MachineID)
	}
	repository, ok := findRepository(state, worktree.RepositoryID)
	if !ok {
		return WorktreeRemovalResult{}, fmt.Errorf("Repository %d does not exist", worktree.RepositoryID)
	}
	location, ok := findRepositoryLocation(state, worktree.RepositoryID, worktree.MachineID)
	if !ok {
		return WorktreeRemovalResult{}, fmt.Errorf("Repository has no location configured on Machine %s", machine.Name)
	}
	if !reflect.DeepEqual(worktree, pending.Worktree) || !reflect.DeepEqual(repository, pending.Repository) || !reflect.DeepEqual(machine, pending.Machine) || !reflect.DeepEqual(location, pending.Location) {
		return WorktreeRemovalResult{}, fmt.Errorf("The Worktree or its Repository location changed after the safety report; review the updated report before removing it")
	}
	repositoryPath, err := r.machineAccess.ResolvePath(machine, location.CheckoutPath)
	if err != nil {
		return WorktreeRemovalResult{}, err
	}
	git := gitcli.New(r.machineAccess)
	if err := ensureListedWorktree(git, machine, repositoryPath, worktree.Path); err != nil {
		return WorktreeRemovalResult{}, err
	}
	checkout, err := git.Adopt(machine, worktree.Path, repository.RemoteURL)
	if err != nil {
		return WorktreeRemovalResult{}, err
	}
	if checkout.Branch != worktree.Branch {
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree branch changed; expected %s, found %s", worktree.Branch, checkout.Branch)
	}
	currentReport := WorktreeRemovalReport{WorktreeID: worktree.ID, WorkspaceID: worktree.WorkspaceID, RepositoryID: repository.ID, RepositoryName: repository.Name, MachineID: machine.ID, Path: worktree.Path, Branch: worktree.Branch, IsDirty: checkout.Dirty, RequiresDestructiveConfirmation: checkout.Dirty}
	if currentReport != pending.Report {
		return WorktreeRemovalResult{}, fmt.Errorf("The Worktree changed after the safety report; review the updated report before removing it")
	}
	if currentReport.RequiresDestructiveConfirmation && !destructiveConfirmed {
		return WorktreeRemovalResult{}, fmt.Errorf("Removing a dirty Worktree requires destructive confirmation")
	}
	if err := r.store.SaveWorktreeRemovalIntent(persistence.WorktreeRemovalIntent{WorktreeID: worktree.ID, Path: worktree.Path}); err != nil {
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree removal intent could not be persisted; its checkout remains at %s: %w", worktree.Path, err)
	}
	if err := git.RemoveWorktree(machine, repositoryPath, worktree.Path, currentReport.RequiresDestructiveConfirmation); err != nil {
		if clearErr := r.store.DeleteWorktreeRemovalIntent(worktreeID); clearErr != nil {
			return WorktreeRemovalResult{}, fmt.Errorf("Worktree checkout could not be removed (%v), and its durable removal intent could not be cleared: %w", err, clearErr)
		}
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree checkout could not be removed; its metadata remains available for review: %w", err)
	}
	// The durable intent bridges the filesystem/database boundary. If the process
	// exits after Git removes the checkout, startup sees the missing path and
	// completes this metadata transition.
	if _, err := r.transitionLocked(domain.Event{Kind: "remove_worktree", WorktreeID: worktree.ID, Confirmed: true, DestructiveConfirmed: destructiveConfirmed, DestructiveRequired: currentReport.RequiresDestructiveConfirmation}); err != nil {
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree checkout was removed, but its metadata cleanup is pending startup reconciliation: %w", err)
	}
	if err := r.store.DeleteWorktreeRemovalIntent(worktreeID); err != nil {
		return WorktreeRemovalResult{}, fmt.Errorf("Worktree was removed, but its completed removal intent could not be cleared: %w", err)
	}
	r.mu.Lock()
	delete(r.pendingWorktreeRemovals, worktreeID)
	r.mu.Unlock()
	return WorktreeRemovalResult{WorktreeID: worktree.ID, BranchPreserved: true}, nil
}

func (r *Runtime) registerCreatedWorktree(worktree domain.Worktree, machine domain.Machine, repositoryPath string, deleteBranch bool) (domain.Worktree, error) {
	registered, err := r.applyWorktreeEvent(domain.Event{Kind: "register_worktree", Worktree: &worktree})
	if err != nil {
		cleanupErr := gitcli.New(r.machineAccess).RollbackPreparedWorktree(machine, repositoryPath, worktree.Path, worktree.Branch, deleteBranch)
		if cleanupErr != nil {
			return domain.Worktree{}, fmt.Errorf("Git Worktree was created at %s but could not be registered: %v; cleanup failed: %w", worktree.Path, err, cleanupErr)
		}
		return domain.Worktree{}, fmt.Errorf("Git Worktree could not be registered; the newly created checkout was removed: %w", err)
	}
	return registered, nil
}

func (r *Runtime) applyWorktreeEvent(event domain.Event) (domain.Worktree, error) {
	decision, err := r.transition(event)
	if err != nil {
		return domain.Worktree{}, err
	}
	for _, worktree := range decision.State.Worktrees {
		if worktree.WorkspaceID == event.Worktree.WorkspaceID && worktree.RepositoryID == event.Worktree.RepositoryID {
			return worktree, nil
		}
	}
	return domain.Worktree{}, fmt.Errorf("Worktree registration did not produce a Worktree")
}

func (r *Runtime) domainSnapshot() domain.DomainState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func worktreeContext(state domain.DomainState, workspaceID, repositoryID, machineID int64) (domain.Workspace, domain.Item, domain.Repository, domain.Machine, domain.RepositoryLocation, error) {
	var workspace domain.Workspace
	for _, value := range state.Workspaces {
		if value.ID == workspaceID {
			workspace = value
			break
		}
	}
	if workspace.ID == 0 {
		return workspace, domain.Item{}, domain.Repository{}, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Workspace %d does not exist", workspaceID)
	}
	if !workspaceHasRepository(workspace, repositoryID) {
		return workspace, domain.Item{}, domain.Repository{}, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Repository %d is not configured for Workspace %d", repositoryID, workspaceID)
	}
	var item domain.Item
	for _, value := range state.Items {
		if value.ID == workspace.ItemID {
			item = value
			break
		}
	}
	if item.ID == 0 {
		return workspace, item, domain.Repository{}, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Item %d does not exist", workspace.ItemID)
	}
	var project *domain.Project
	for i := range state.Projects {
		if state.Projects[i].ID == item.ProjectID {
			p := state.Projects[i]
			project = &p
			break
		}
	}
	if project == nil {
		return workspace, item, domain.Repository{}, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Project %d does not exist", item.ProjectID)
	}
	var context *domain.Context
	for i := range state.Contexts {
		if state.Contexts[i].ID == project.ContextID {
			c := state.Contexts[i]
			context = &c
			break
		}
	}
	if context == nil || context.ExecutionMachineID == nil {
		return workspace, item, domain.Repository{}, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Context %d has no execution Machine configured", project.ContextID)
	}
	if *context.ExecutionMachineID != machineID {
		return workspace, item, domain.Repository{}, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Machine %d is not the execution Machine configured for Context %d", machineID, project.ContextID)
	}
	repository, ok := findRepository(state, repositoryID)
	if !ok {
		return workspace, item, repository, domain.Machine{}, domain.RepositoryLocation{}, fmt.Errorf("Repository %d does not exist", repositoryID)
	}
	machine, ok := findMachine(state, machineID)
	if !ok {
		return workspace, item, repository, machine, domain.RepositoryLocation{}, fmt.Errorf("Machine %d does not exist", machineID)
	}
	location, ok := findRepositoryLocation(state, repositoryID, machineID)
	if !ok {
		return workspace, item, repository, machine, location, fmt.Errorf("Repository has no location configured on Machine %s", machine.Name)
	}
	return workspace, item, repository, machine, location, nil
}

func workspaceHasRepository(workspace domain.Workspace, repositoryID int64) bool {
	for _, item := range workspace.Repositories {
		if item.RepositoryID == repositoryID {
			return true
		}
	}
	return false
}
func workspaceBranch(workspace domain.Workspace, item domain.Item, repositoryID int64) string {
	for _, repository := range workspace.Repositories {
		if repository.RepositoryID == repositoryID {
			return repository.Branch
		}
	}
	return "mission-" + item.HumanIdentifier
}
func worktreeDirectory(item domain.Item, repository domain.Repository) string {
	return item.HumanIdentifier + "-" + slug(repository.Name)
}
func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
func findWorktree(state domain.DomainState, id int64) (domain.Worktree, bool) {
	for _, value := range state.Worktrees {
		if value.ID == id {
			return value, true
		}
	}
	return domain.Worktree{}, false
}
func findMachine(state domain.DomainState, id int64) (domain.Machine, bool) {
	for _, value := range state.Machines {
		if value.ID == id {
			return value, true
		}
	}
	return domain.Machine{}, false
}
func findRepository(state domain.DomainState, id int64) (domain.Repository, bool) {
	for _, value := range state.Repositories {
		if value.ID == id {
			return value, true
		}
	}
	return domain.Repository{}, false
}
func findRepositoryLocation(state domain.DomainState, repositoryID, machineID int64) (domain.RepositoryLocation, bool) {
	for _, value := range state.RepositoryLocations {
		if value.RepositoryID == repositoryID && value.MachineID == machineID {
			return value, true
		}
	}
	return domain.RepositoryLocation{}, false
}

func ensureListedWorktree(git gitcli.GitCLI, machine domain.Machine, repositoryPath, worktreePath string) error {
	listed, err := git.ListWorktrees(machine, repositoryPath)
	if err != nil {
		return err
	}
	canonicalWorktree, err := git.CanonicalPath(machine, worktreePath)
	if err != nil {
		return err
	}
	canonicalRepository, err := git.CanonicalPath(machine, repositoryPath)
	if err != nil {
		return err
	}
	for _, candidate := range listed {
		if filepath.Clean(candidate.Path) == filepath.Clean(canonicalWorktree) && filepath.Clean(candidate.Path) != filepath.Clean(canonicalRepository) {
			return nil
		}
	}
	return fmt.Errorf("path is not a Git Worktree of the configured Repository")
}
