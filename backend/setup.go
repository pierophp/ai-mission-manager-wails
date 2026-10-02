package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type ProviderChoice string

const (
	ProviderGitHub ProviderChoice = "github"
	ProviderNone   ProviderChoice = "none"
)

type SetupState struct {
	Completed bool           `json:"completed"`
	Provider  ProviderChoice `json:"provider"`
}

type DependencyState string

const (
	DependencyAvailable       DependencyState = "available"
	DependencyMissing         DependencyState = "missing"
	DependencyUnauthenticated DependencyState = "unauthenticated"
	DependencyNotConfigured   DependencyState = "notConfigured"
	DependencyUnavailable     DependencyState = "unavailable"
)

type DependencyStatus struct {
	Key            string          `json:"key"`
	Label          string          `json:"label"`
	State          DependencyState `json:"state"`
	ExecutablePath *string         `json:"executablePath"`
	Message        string          `json:"message"`
	Action         *string         `json:"action"`
}

type HealthStatus struct {
	Runtime   DependencyStatus   `json:"runtime"`
	Provider  DependencyStatus   `json:"provider"`
	Agents    []DependencyStatus `json:"agents"`
	CheckedAt int64              `json:"checkedAt"`
}

type dependencyDefinition struct {
	key           string
	label         string
	settingKey    string
	missingAction string
}

func (r *Runtime) setupState() (SetupState, error) {
	completedValue, err := r.store.Setting("setup_completed")
	if err != nil {
		return SetupState{}, fmt.Errorf("read setup state: %w", err)
	}
	providerValue, err := r.store.Setting("provider_choice")
	if err != nil {
		return SetupState{}, fmt.Errorf("read provider choice: %w", err)
	}
	provider := ProviderGitHub
	switch providerValue {
	case "", "github":
	case "none":
		provider = ProviderNone
	default:
		return SetupState{}, fmt.Errorf("unknown provider choice: %s", providerValue)
	}
	return SetupState{Completed: completedValue == "true", Provider: provider}, nil
}

func (r *Runtime) completeSetup(contextName string, provider ProviderChoice) (SetupState, error) {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return SetupState{}, errors.New("a Context name is required to finish setup")
	}
	if provider != ProviderGitHub && provider != ProviderNone {
		return SetupState{}, fmt.Errorf("unknown provider choice: %s", provider)
	}
	r.mu.Lock()
	contextExists := false
	for _, context := range r.state.Contexts {
		if context.Name == contextName {
			contextExists = true
			break
		}
	}
	r.mu.Unlock()
	if !contextExists {
		if _, err := r.runEvent(domain.Event{Kind: "create_context", Name: contextName}); err != nil {
			return SetupState{}, err
		}
	}
	if err := r.store.SetSettings(map[string]string{
		"setup_completed": "true",
		"provider_choice": string(provider),
	}); err != nil {
		return SetupState{}, fmt.Errorf("persist setup state: %w", err)
	}
	return SetupState{Completed: true, Provider: provider}, nil
}

func (r *Runtime) healthStatus(providerOverride *ProviderChoice) (HealthStatus, error) {
	setup, err := r.setupState()
	if err != nil {
		return HealthStatus{}, err
	}
	providerChoice := setup.Provider
	if providerOverride != nil {
		providerChoice = *providerOverride
	}
	if providerChoice != ProviderGitHub && providerChoice != ProviderNone {
		return HealthStatus{}, fmt.Errorf("unknown provider choice: %s", providerChoice)
	}
	runtimeStatus := r.checkRuntimeDependency()
	var providerStatus DependencyStatus
	if providerChoice == ProviderGitHub {
		providerStatus = r.checkGitHubDependency()
	} else {
		providerStatus = DependencyStatus{
			Key: "github", Label: "GitHub provider", State: DependencyNotConfigured,
			Message: "No provider selected; local Items remain available.",
			Action:  stringPtr("Choose GitHub in setup when you are ready to link external work."),
		}
	}
	agents := []DependencyStatus{
		r.checkLocalDependency(dependencyDefinition{
			key: "claude", label: "Claude Code", settingKey: "claude_executable_path",
			missingAction: "Install Claude Code before starting a Run with it.",
		}),
		r.checkLocalDependency(dependencyDefinition{
			key: "codex", label: "Codex", settingKey: "codex_executable_path",
			missingAction: "Install Codex before starting a Run with it.",
		}),
	}
	return HealthStatus{
		Runtime: runtimeStatus, Provider: providerStatus, Agents: agents, CheckedAt: time.Now().Unix(),
	}, nil
}

func (r *Runtime) checkRuntimeDependency() DependencyStatus {
	path := r.resolveAndStoreExecutable("tmux", "tmux_executable_path")
	if path == "" {
		return missingExecutable("tmux", "tmux runtime", "tmux was not found.", "Install tmux (for example, with `brew install tmux`) and check again.")
	}
	if detail := checkCommand(path, "-V"); detail != "" {
		return DependencyStatus{
			Key: "tmux", Label: "tmux runtime", State: DependencyUnavailable, ExecutablePath: stringPtr(path),
			Message: "tmux could not be checked: " + detail,
			Action:  stringPtr("Repair or reinstall tmux, then check again."),
		}
	}
	return DependencyStatus{
		Key: "tmux", Label: "tmux runtime", State: DependencyAvailable, ExecutablePath: stringPtr(path),
		Message: "tmux is ready.",
	}
}

func (r *Runtime) checkGitHubDependency() DependencyStatus {
	path := r.resolveAndStoreExecutable("gh", "gh_executable_path")
	if path == "" {
		return missingExecutable("github", "GitHub provider", "GitHub CLI (`gh`) was not found.", "Install GitHub CLI, then authenticate it with `gh auth login`.")
	}
	if detail := checkCommand(path, "--version"); detail != "" {
		return DependencyStatus{
			Key: "github", Label: "GitHub provider", State: DependencyUnavailable, ExecutablePath: stringPtr(path),
			Message: "GitHub CLI could not run: " + detail,
			Action:  stringPtr("Repair or reinstall GitHub CLI, then check again."),
		}
	}
	if detail := checkCommand(path, "auth", "status", "--hostname", "github.com"); detail != "" {
		if looksLikeAuthenticationFailure(detail) {
			return DependencyStatus{
				Key: "github", Label: "GitHub provider", State: DependencyUnauthenticated, ExecutablePath: stringPtr(path),
				Message: "GitHub CLI is not authenticated: " + detail,
				Action:  stringPtr("Run `gh auth login` in your terminal; Mission Manager will not log in for you."),
			}
		}
		return DependencyStatus{
			Key: "github", Label: "GitHub provider", State: DependencyUnavailable, ExecutablePath: stringPtr(path),
			Message: "GitHub authentication status could not be checked: " + detail,
			Action:  stringPtr("Check network access to github.com, then check again."),
		}
	}
	return DependencyStatus{
		Key: "github", Label: "GitHub provider", State: DependencyAvailable, ExecutablePath: stringPtr(path),
		Message: "GitHub CLI is installed and authenticated.",
	}
}

func (r *Runtime) checkLocalDependency(dependency dependencyDefinition) DependencyStatus {
	path := r.resolveAndStoreExecutable(dependency.key, dependency.settingKey)
	if path == "" {
		return missingExecutable(dependency.key, dependency.label, dependency.label+" was not found.", dependency.missingAction)
	}
	return DependencyStatus{
		Key: dependency.key, Label: dependency.label, State: DependencyAvailable, ExecutablePath: stringPtr(path),
		Message: dependency.label + " is installed.",
	}
}

func missingExecutable(key, label, message, action string) DependencyStatus {
	return DependencyStatus{
		Key: key, Label: label, State: DependencyMissing, Message: message, Action: stringPtr(action),
	}
}

func (r *Runtime) resolveAndStoreExecutable(name, settingKey string) string {
	stored, err := r.store.Setting(settingKey)
	if err != nil {
		return ""
	}
	path := resolveExecutable(name, stored)
	if path == "" {
		return ""
	}
	_ = r.store.SetSettings(map[string]string{settingKey: path})
	return path
}

func resolveExecutable(name, stored string) string {
	if filepath.IsAbs(stored) && isExecutable(stored) && !isMiseProxy(stored, name) {
		return filepath.Clean(stored)
	}
	path, err := exec.LookPath(name)
	if err != nil || !isExecutable(path) || isMiseProxy(path, name) {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	// Preserve symlinks such as mise shims: mise uses the invoked filename
	// (for example, "gh") to select the tool to run.
	return filepath.Clean(absolute)
}

func isMiseProxy(path, name string) bool {
	if filepath.Base(path) == name {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && filepath.Base(resolved) == "mise"
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func checkCommand(path string, args ...string) string {
	command := exec.Command(path, args...)
	_, err := command.Output()
	if err == nil {
		return ""
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		detail := strings.TrimSpace(string(exitError.Stderr))
		if detail != "" {
			return detail
		}
		return fmt.Sprintf("command exited with %s", exitError.ProcessState)
	}
	return err.Error()
}

func looksLikeAuthenticationFailure(detail string) bool {
	detail = strings.ToLower(detail)
	for _, marker := range []string{"not logged in", "not authenticated", "no accounts", "authentication token", "token is invalid"} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func stringPtr(value string) *string { return &value }
