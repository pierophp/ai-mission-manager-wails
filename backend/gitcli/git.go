// Package gitcli adapts Git's command line interface to Machine shell access.
package gitcli

import (
	"fmt"
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

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
