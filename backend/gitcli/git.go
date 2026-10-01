// Package gitcli adapts Git's command line interface to Machine shell access.
package gitcli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

// Shell is the narrow boundary used by GitCLI, and can be faked without Git or SSH.
type Shell interface {
	RunShell(domain.Machine, string) (string, error)
}

type Checkout struct {
	RemoteURL string
	Branch    string
	Dirty     bool
}

type Worktree struct {
	Path     string
	Branch   string
	Detached bool
	Bare     bool
}

type GitCLI struct{ shell Shell }

func New(shell Shell) GitCLI { return GitCLI{shell: shell} }

func (g GitCLI) Inspect(machine domain.Machine, path string) (Checkout, error) {
	if g.shell == nil {
		return Checkout{}, fmt.Errorf("Git shell is not configured")
	}
	base := "git -C " + quote(path)
	if _, err := g.shell.RunShell(machine, base+" rev-parse --show-toplevel"); err != nil {
		return Checkout{}, fmt.Errorf("path is not a Git repository: %s", path)
	}
	branch, err := g.shell.RunShell(machine, base+" symbolic-ref --short HEAD")
	if err != nil {
		branch = "HEAD (detached)"
	}
	status, err := g.shell.RunShell(machine, base+" status --porcelain --untracked-files=all")
	if err != nil {
		return Checkout{}, fmt.Errorf("could not read Git checkout status: %w", err)
	}
	remotes, err := g.shell.RunShell(machine, base+" remote")
	if err != nil {
		return Checkout{}, fmt.Errorf("Git could not list checkout remotes: %w", err)
	}
	remoteName := ""
	for _, candidate := range strings.Split(remotes, "\n") {
		if strings.TrimSpace(candidate) != "" {
			remoteName = strings.TrimSpace(candidate)
			break
		}
	}
	remoteURL := ""
	if remoteName != "" {
		remoteURL, err = g.shell.RunShell(machine, base+" remote get-url "+quote(remoteName))
		if err != nil {
			return Checkout{}, fmt.Errorf("Git could not read checkout remote %s: %w", remoteName, err)
		}
	}
	return Checkout{RemoteURL: strings.TrimSpace(remoteURL), Branch: strings.TrimSpace(branch), Dirty: strings.TrimSpace(status) != ""}, nil
}

// Adopt inspects an existing checkout and, when supplied, verifies its remote
// against the Repository's configured remote.
func (g GitCLI) Adopt(machine domain.Machine, path string, expectedRemote string) (Checkout, error) {
	checkout, err := g.Inspect(machine, path)
	if err != nil {
		return Checkout{}, err
	}
	expectedRemote = strings.TrimSpace(expectedRemote)
	if expectedRemote != "" {
		if checkout.RemoteURL == "" {
			return Checkout{}, fmt.Errorf("Repository has no configured remote matching %s", expectedRemote)
		}
		if checkout.RemoteURL != expectedRemote {
			return Checkout{}, fmt.Errorf("Repository remote does not match the configured remote: expected %s, found %s", expectedRemote, checkout.RemoteURL)
		}
	}
	return checkout, nil
}

func (g GitCLI) Clone(machine domain.Machine, remote, destination string) error {
	if g.shell == nil {
		return fmt.Errorf("Git shell is not configured")
	}
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return fmt.Errorf("remote URL cannot be blank")
	}
	_, err := g.shell.RunShell(machine, "git clone "+quote(remote)+" "+quote(destination))
	if err != nil {
		return fmt.Errorf("could not clone Git repository: %w", err)
	}
	return nil
}

// PrepareWorktree fetches origin, then creates a branch from baseBranch or
// attaches an existing local/remote branch when reuseExisting is true.
func (g GitCLI) PrepareWorktree(machine domain.Machine, repositoryPath, path, branch, baseBranch, expectedRemote string, reuseExisting bool) error {
	_, err := g.PrepareWorktreeWithResult(machine, repositoryPath, path, branch, baseBranch, expectedRemote, reuseExisting)
	return err
}

// PrepareWorktreeWithResult also reports whether this operation created a new
// local branch, allowing callers to roll that branch back if registration fails.
func (g GitCLI) PrepareWorktreeWithResult(machine domain.Machine, repositoryPath, path, branch, baseBranch, expectedRemote string, reuseExisting bool) (bool, error) {
	if g.shell == nil {
		return false, fmt.Errorf("Git shell is not configured")
	}
	repositoryPath, path, branch, baseBranch = strings.TrimSpace(repositoryPath), strings.TrimSpace(path), strings.TrimSpace(branch), strings.TrimSpace(baseBranch)
	if repositoryPath == "" || path == "" || branch == "" || baseBranch == "" {
		return false, fmt.Errorf("Repository, Worktree path, branch, and base branch are required")
	}
	base := "git -C " + quote(repositoryPath)
	if out, err := g.shell.RunShell(machine, "if [ -e "+quote(path)+" ]; then printf exists; else printf missing; fi"); err != nil {
		return false, fmt.Errorf("could not inspect Worktree destination: %w", err)
	} else if strings.TrimSpace(out) == "exists" {
		return false, fmt.Errorf("Worktree destination already exists: %s", path)
	}
	remoteName, err := g.ConfiguredRemote(machine, repositoryPath, expectedRemote)
	if err != nil {
		return false, err
	}
	if _, err := g.shell.RunShell(machine, base+" fetch "+quote(remoteName)); err != nil {
		return false, fmt.Errorf("could not fetch configured remote %s: %w", remoteName, err)
	}
	baseRef := "refs/remotes/" + remoteName + "/" + baseBranch
	baseExists, err := g.refExists(machine, base, baseRef)
	if err != nil {
		return false, err
	}
	if !baseExists {
		return false, fmt.Errorf("base branch %s does not exist on remote %s", baseBranch, remoteName)
	}
	worktrees, err := g.ListWorktrees(machine, repositoryPath)
	if err != nil {
		return false, err
	}
	for _, existing := range worktrees {
		if existing.Branch == branch {
			return false, fmt.Errorf("branch %s is already attached at %s", branch, existing.Path)
		}
	}
	local, err := g.refExists(machine, base, "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	remoteBranchExists, err := g.refExists(machine, base, "refs/remotes/"+remoteName+"/"+branch)
	if err != nil {
		return false, err
	}
	if (local || remoteBranchExists) && !reuseExisting {
		return false, fmt.Errorf("branch %s already exists; confirm reuse to attach it", branch)
	}
	if _, err := g.shell.RunShell(machine, "mkdir -p "+quote(filepath.Dir(path))); err != nil {
		return false, fmt.Errorf("could not prepare Git Worktree parent directory: %w", err)
	}
	var add string
	switch {
	case reuseExisting && !local && !remoteBranchExists:
		return false, fmt.Errorf("branch %s does not exist locally or on remote %s", branch, remoteName)
	case local:
		add = base + " worktree add " + quote(path) + " " + quote(branch)
	case remoteBranchExists:
		add = base + " worktree add --track -b " + quote(branch) + " " + quote(path) + " " + quote(remoteName+"/"+branch)
	default:
		add = base + " worktree add -b " + quote(branch) + " " + quote(path) + " " + quote(baseRef)
	}
	if _, err := g.shell.RunShell(machine, add); err != nil {
		return false, fmt.Errorf("could not create Git Worktree: %w", err)
	}
	newLocalBranch := !local
	if !local && !remoteBranchExists {
		if _, err := g.shell.RunShell(machine, "git -C "+quote(path)+" config "+quote("branch."+branch+".remote")+" "+quote(remoteName)); err != nil {
			return false, g.cleanupCreatedWorktree(machine, repositoryPath, path, branch, true, fmt.Errorf("could not configure Worktree branch upstream: %w", err))
		}
		if _, err := g.shell.RunShell(machine, "git -C "+quote(path)+" config "+quote("branch."+branch+".merge")+" "+quote("refs/heads/"+branch)); err != nil {
			return false, g.cleanupCreatedWorktree(machine, repositoryPath, path, branch, true, fmt.Errorf("could not configure Worktree branch upstream: %w", err))
		}
	}
	return newLocalBranch, nil
}

func (g GitCLI) cleanupCreatedWorktree(machine domain.Machine, repositoryPath, path, branch string, deleteBranch bool, cause error) error {
	if err := g.RollbackPreparedWorktree(machine, repositoryPath, path, branch, deleteBranch); err != nil {
		return fmt.Errorf("%v; newly created Git Worktree cleanup failed: %w", cause, err)
	}
	return cause
}

func (g GitCLI) ConfiguredRemote(machine domain.Machine, repositoryPath, expectedURL string) (string, error) {
	if g.shell == nil {
		return "", fmt.Errorf("Git shell is not configured")
	}
	base := "git -C " + quote(repositoryPath)
	out, err := g.shell.RunShell(machine, base+" remote")
	if err != nil {
		return "", fmt.Errorf("could not list Repository remotes: %w", err)
	}
	for _, candidate := range strings.Fields(out) {
		url, err := g.shell.RunShell(machine, base+" remote get-url "+quote(candidate))
		if err != nil {
			continue
		}
		if actual := strings.TrimSpace(url); actual != "" && (expectedURL == "" || actual == strings.TrimSpace(expectedURL)) {
			return candidate, nil
		}
	}
	if strings.TrimSpace(expectedURL) != "" {
		return "", fmt.Errorf("Repository has no configured remote matching %s", expectedURL)
	}
	return "", fmt.Errorf("Repository has no configured Git remote")
}

func (g GitCLI) refExists(machine domain.Machine, base, ref string) (bool, error) {
	// Emit the exit status as data so an absent ref (status 1) is distinct from
	// transport or repository errors reported by MachineAccess.
	out, err := g.shell.RunShell(machine, base+" show-ref --verify --quiet "+quote(ref)+"; printf 'status:%s' \"$?\"")
	if err != nil {
		return false, fmt.Errorf("could not inspect Git ref %s: %w", ref, err)
	}
	status := strings.TrimSpace(strings.TrimPrefix(out, "status:"))
	code, err := strconv.Atoi(status)
	if err != nil || (code != 0 && code != 1) {
		return false, fmt.Errorf("could not inspect Git ref %s: unexpected status %q", ref, out)
	}
	return code == 0, nil
}

func (g GitCLI) ListWorktrees(machine domain.Machine, repositoryPath string) ([]Worktree, error) {
	if g.shell == nil {
		return nil, fmt.Errorf("Git shell is not configured")
	}
	out, err := g.shell.RunShell(machine, "git -C "+quote(repositoryPath)+" worktree list --porcelain")
	if err != nil {
		return nil, fmt.Errorf("could not list Git Worktrees: %w", err)
	}
	var result []Worktree
	var current *Worktree
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			current = nil
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			result = append(result, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			current = &result[len(result)-1]
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		}
	}
	if result == nil {
		result = []Worktree{}
	}
	return result, nil
}

func (g GitCLI) CanonicalPath(machine domain.Machine, path string) (string, error) {
	if g.shell == nil {
		return "", fmt.Errorf("Git shell is not configured")
	}
	out, err := g.shell.RunShell(machine, "git -C "+quote(path)+" rev-parse --show-toplevel")
	if err != nil {
		return "", fmt.Errorf("path is not a Git repository: %s", path)
	}
	return strings.TrimSpace(out), nil
}

func (g GitCLI) PathExists(machine domain.Machine, path string) (bool, error) {
	if g.shell == nil {
		return false, fmt.Errorf("Git shell is not configured")
	}
	out, err := g.shell.RunShell(machine, "if [ -e "+quote(path)+" ]; then printf exists; else printf missing; fi")
	if err != nil {
		return false, fmt.Errorf("could not inspect Worktree path: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "exists":
		return true, nil
	case "missing":
		return false, nil
	default:
		return false, fmt.Errorf("could not inspect Worktree path: unexpected response %q", out)
	}
}

func (g GitCLI) RemoveWorktree(machine domain.Machine, repositoryPath, path string, force bool) error {
	if g.shell == nil {
		return fmt.Errorf("Git shell is not configured")
	}
	flag := ""
	if force {
		flag = " --force"
	}
	if _, err := g.shell.RunShell(machine, "git -C "+quote(repositoryPath)+" worktree remove"+flag+" "+quote(path)); err != nil {
		return fmt.Errorf("could not remove Git Worktree: %w", err)
	}
	return nil
}

// RollbackPreparedWorktree removes a checkout created by a failed preparation
// and deletes its branch only when this operation created that branch.
func (g GitCLI) RollbackPreparedWorktree(machine domain.Machine, repositoryPath, path, branch string, deleteBranch bool) error {
	if err := g.RemoveWorktree(machine, repositoryPath, path, false); err != nil {
		return err
	}
	if deleteBranch {
		if g.shell == nil {
			return fmt.Errorf("Git shell is not configured")
		}
		if _, err := g.shell.RunShell(machine, "git -C "+quote(repositoryPath)+" branch -D "+quote(branch)); err != nil {
			return fmt.Errorf("could not remove newly created branch: %w", err)
		}
	}
	return nil
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
