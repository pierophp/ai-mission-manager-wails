package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/pstack"
)

// MachineAccess is the shell and file boundary for local and SSH Machines.
// Application features use this seam so tests never need a real SSH server.
type MachineAccess interface {
	RunShell(domain.Machine, string) (string, error)
	RunShellWithInput(domain.Machine, string, []byte) (string, error)
	FindExecutable(domain.Machine, string) (string, error)
	HomeDirectory(domain.Machine) (string, error)
	HomePath(domain.Machine) string
	IsLocal(domain.Machine) bool
	ResolvePath(domain.Machine, string) (string, error)
	WriteFile(domain.Machine, string, []byte) error
	ProvisionPstackTree(domain.Machine) (string, error)
}

// TimedMachineAccess lets destructive workflows bound pane cleanup without
// leaving an SSH process running after its caller proceeds.
type TimedMachineAccess interface {
	RunShellWithTimeout(domain.Machine, string, time.Duration) (string, error)
}

type LocalSSHMachineAccess struct{}

type machineHomeError struct {
	reachable bool
	message   string
}

func (e machineHomeError) Error() string { return e.message }

func (LocalSSHMachineAccess) RunShell(machine domain.Machine, command string) (string, error) {
	return LocalSSHMachineAccess{}.RunShellWithInput(machine, command, nil)
}

func (LocalSSHMachineAccess) RunShellWithInput(machine domain.Machine, command string, input []byte) (string, error) {
	program, args, err := buildMachineShellCommand(machine, command)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(program, args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return "", fmt.Errorf("command exited with %w", err)
		}
		return "", errors.New(detail)
	}
	return string(output), nil
}

func (LocalSSHMachineAccess) RunShellWithTimeout(machine domain.Machine, command string, timeout time.Duration) (string, error) {
	program, args, err := buildMachineShellCommand(machine, command)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("command timed out after %s: %s", timeout, strings.TrimSpace(string(output)))
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return "", errors.New(detail)
		}
		return "", fmt.Errorf("command exited with %w", err)
	}
	return string(output), nil
}

func (a LocalSSHMachineAccess) FindExecutable(machine domain.Machine, name string) (string, error) {
	if strings.TrimSpace(name) == "" || !allASCII(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") {
		return "", fmt.Errorf("unsupported executable name: %s", name)
	}
	if a.IsLocal(machine) {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%s is not installed on Machine %s", name, machine.Name)
		}
		return filepath.Abs(path)
	}
	path, err := a.RunShell(machine, "command -v "+shellQuote(name)+" || true")
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s is not installed on Machine %s", name, machine.Name)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("Machine %s returned a non-absolute %s executable path", machine.Name, name)
	}
	return path, nil
}

func (a LocalSSHMachineAccess) HomeDirectory(machine domain.Machine) (string, error) {
	if a.IsLocal(machine) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", machineHomeError{reachable: true, message: "HOME is not set on the local Machine"}
		}
		return home, nil
	}
	home, err := a.RunShell(machine, `printf '%s' "$HOME"`)
	if err != nil {
		return "", machineHomeError{reachable: false, message: fmt.Sprintf("Could not reach Machine %s: %v", machine.Name, err)}
	}
	home = strings.TrimSpace(home)
	if !filepath.IsAbs(home) {
		return "", machineHomeError{reachable: true, message: fmt.Sprintf("Machine %s did not report an absolute home directory", machine.Name)}
	}
	return home, nil
}

func (LocalSSHMachineAccess) HomePath(machine domain.Machine) string {
	if machine.Transport.Kind == domain.TransportLocal {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return "/"
	}
	return "~"
}

func (a LocalSSHMachineAccess) IsLocal(machine domain.Machine) bool {
	return machine.Transport.Kind == domain.TransportLocal
}

func (a LocalSSHMachineAccess) ResolvePath(machine domain.Machine, path string) (string, error) {
	if !filepath.IsAbs(path) || path == "~" || strings.HasPrefix(path, "~/") {
		home, err := a.HomeDirectory(machine)
		if err != nil {
			return "", err
		}
		return resolveMachinePath(machine, path, home)
	}
	return resolveMachinePath(machine, path, a.HomePath(machine))
}

func resolveMachinePath(machine domain.Machine, path, home string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	if !machineIsLocal(machine) && path != "~" && !strings.HasPrefix(path, "~/") {
		return "", fmt.Errorf("relative Machine paths must start with ~/ or be absolute")
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return filepath.Join(home, path), nil
}

func machineIsLocal(machine domain.Machine) bool {
	return machine.Transport.Kind == domain.TransportLocal
}

func (a LocalSSHMachineAccess) WriteFile(machine domain.Machine, path string, contents []byte) error {
	resolved, err := a.ResolvePath(machine, path)
	if err != nil {
		return err
	}
	if a.IsLocal(machine) {
		return atomicWriteFile(resolved, contents)
	}
	command := "set -eu; umask 077; target=" + shellQuote(resolved) + "; parent=${target%/*}; mkdir -p \"$parent\"; temporary=\"$target.tmp.$$\"; trap 'rm -f \"$temporary\"' EXIT HUP INT TERM; cat > \"$temporary\"; chmod 600 \"$temporary\"; mv -f \"$temporary\" \"$target\"; trap - EXIT HUP INT TERM"
	_, err = a.RunShellWithInput(machine, command, contents)
	return err
}

// ProvisionPstackTree prepares the stable target used by the pstack installer.
// The installer can replace this narrow adapter behavior without exposing
// transport details to application features.
func (a LocalSSHMachineAccess) ProvisionPstackTree(machine domain.Machine) (string, error) {
	home, err := a.HomeDirectory(machine)
	if err != nil {
		return "", err
	}
	target := filepath.Join(home, ".local", "share", "ai-mission-manager", "pstack", pstack.TreeHash)
	if a.IsLocal(machine) {
		if err := pstack.InstallLocal(target); err != nil {
			return "", err
		}
		return target, nil
	}
	payload, err := pstack.Payload()
	if err != nil {
		return "", err
	}
	_, err = a.RunShellWithInput(machine, pstack.RemoteInstallCommand(target), payload)
	return target, err
}

func atomicWriteFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ai-mission-manager-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func buildMachineShellCommand(machine domain.Machine, command string) (string, []string, error) {
	if machine.Transport.Kind == domain.TransportLocal {
		return "sh", []string{"-lc", command}, nil
	}
	transport := machine.Transport
	if err := validateSSHTransport(transport); err != nil {
		return "", nil, err
	}
	target := *transport.Host
	if transport.User != nil {
		target = *transport.User + "@" + *transport.Host
	}
	args := []string{"-T", "-o", "BatchMode=yes"}
	if transport.Port != nil {
		args = append(args, "-p", fmt.Sprint(*transport.Port))
	}
	if transport.IdentityFile != nil {
		args = append(args, "-i", *transport.IdentityFile)
	}
	if transport.KnownHostsFile != nil {
		args = append(args, "-o", "UserKnownHostsFile="+*transport.KnownHostsFile)
	}
	if transport.StrictHostKeyChecking != nil {
		args = append(args, "-o", "StrictHostKeyChecking="+*transport.StrictHostKeyChecking)
	}
	return "ssh", append(args, target, command), nil
}

func buildTmuxProcessCommand(machine domain.Machine, allocateTTY bool, tmuxArgs []string, tmuxPath string) (string, []string, error) {
	if strings.TrimSpace(tmuxPath) == "" {
		return "", nil, errors.New("tmux executable path cannot be blank")
	}
	if machine.Transport.Kind == domain.TransportLocal {
		return tmuxPath, append([]string(nil), tmuxArgs...), nil
	}
	if err := validateSSHTransport(machine.Transport); err != nil {
		return "", nil, err
	}
	transport := machine.Transport
	target := *transport.Host
	if transport.User != nil {
		target = *transport.User + "@" + target
	}
	remote := make([]string, 0, len(tmuxArgs)+1)
	remote = append(remote, tmuxPath)
	remote = append(remote, tmuxArgs...)
	quoted := make([]string, len(remote))
	for i, value := range remote {
		quoted[i] = shellQuote(value)
	}
	remoteCommand := strings.Join(quoted, " ")
	args := []string{"-T", "-o", "BatchMode=yes"}
	if allocateTTY {
		args[0] = "-tt"
	}
	if transport.Port != nil {
		args = append(args, "-p", fmt.Sprint(*transport.Port))
	}
	if transport.IdentityFile != nil {
		args = append(args, "-i", *transport.IdentityFile)
	}
	if transport.KnownHostsFile != nil {
		args = append(args, "-o", "UserKnownHostsFile="+*transport.KnownHostsFile)
	}
	if transport.StrictHostKeyChecking != nil {
		args = append(args, "-o", "StrictHostKeyChecking="+*transport.StrictHostKeyChecking)
	}
	args = append(args, target, remoteCommand)
	return "ssh", args, nil
}

func validateSSHTransport(transport domain.MachineTransport) error {
	if transport.Host == nil || *transport.Host == "" || !allASCII(*transport.Host, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.@:_-") {
		host := ""
		if transport.Host != nil {
			host = *transport.Host
		}
		return fmt.Errorf("SSH host contains unsupported characters: %s", host)
	}
	if transport.User != nil && (*transport.User == "" || !allASCII(*transport.User, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")) {
		return fmt.Errorf("SSH user contains unsupported characters: %s", *transport.User)
	}
	if transport.Port != nil && *transport.Port == 0 {
		return errors.New("SSH port must be positive")
	}
	if transport.StrictHostKeyChecking != nil && !oneOf(*transport.StrictHostKeyChecking, "yes", "accept-new", "no") {
		return fmt.Errorf("unsupported SSH StrictHostKeyChecking value: %s", *transport.StrictHostKeyChecking)
	}
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func allASCII(value, allowed string) bool {
	return len(value) > 0 && strings.IndexFunc(value, func(r rune) bool { return r > 127 || !strings.ContainsRune(allowed, r) }) == -1
}
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// FakeMachineAccess records calls and returns preconfigured command results.
type FakeMachineAccess struct {
	mu            sync.Mutex
	Calls         []MachineAccessCall
	ShellResults  map[string]MachineAccessResult
	Executables   map[string]string
	Home          string
	WriteErrors   map[string]error
	ProvisionPath string
}
type MachineAccessCall struct {
	Operation string
	MachineID int64
	Value     string
	Contents  []byte
	Timeout   time.Duration
}
type MachineAccessResult struct {
	Value string
	Err   error
}

func NewFakeMachineAccess() *FakeMachineAccess {
	return &FakeMachineAccess{ShellResults: map[string]MachineAccessResult{}, Executables: map[string]string{}, Home: "/home/fake", WriteErrors: map[string]error{}}
}
func (f *FakeMachineAccess) record(call MachineAccessCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call.Contents = bytes.Clone(call.Contents)
	f.Calls = append(f.Calls, call)
}
func (f *FakeMachineAccess) RunShell(m domain.Machine, command string) (string, error) {
	f.record(MachineAccessCall{Operation: "run_shell", MachineID: m.ID, Value: command})
	result := f.ShellResults[command]
	return result.Value, result.Err
}
func (f *FakeMachineAccess) RunShellWithTimeout(m domain.Machine, command string, timeout time.Duration) (string, error) {
	f.record(MachineAccessCall{Operation: "run_shell_timeout", MachineID: m.ID, Value: command, Timeout: timeout})
	result := f.ShellResults[command]
	return result.Value, result.Err
}
func (f *FakeMachineAccess) RunShellWithInput(m domain.Machine, command string, input []byte) (string, error) {
	f.record(MachineAccessCall{Operation: "run_shell_stdin", MachineID: m.ID, Value: command, Contents: input})
	result := f.ShellResults[command]
	return result.Value, result.Err
}
func (f *FakeMachineAccess) FindExecutable(m domain.Machine, name string) (string, error) {
	f.record(MachineAccessCall{Operation: "find_executable", MachineID: m.ID, Value: name})
	path := f.Executables[name]
	if path == "" {
		return "", fs.ErrNotExist
	}
	return path, nil
}
func (f *FakeMachineAccess) HomeDirectory(m domain.Machine) (string, error) {
	f.record(MachineAccessCall{Operation: "home_directory", MachineID: m.ID})
	if f.Home == "" {
		return "", os.ErrNotExist
	}
	return f.Home, nil
}
func (f *FakeMachineAccess) HomePath(m domain.Machine) string {
	if !f.IsLocal(m) {
		return "~"
	}
	return f.Home
}
func (f *FakeMachineAccess) IsLocal(m domain.Machine) bool {
	return m.Transport.Kind == domain.TransportLocal
}
func (f *FakeMachineAccess) ResolvePath(m domain.Machine, path string) (string, error) {
	f.record(MachineAccessCall{Operation: "resolve_path", MachineID: m.ID, Value: path})
	return resolveMachinePath(m, path, f.Home)
}
func (f *FakeMachineAccess) WriteFile(m domain.Machine, path string, contents []byte) error {
	f.record(MachineAccessCall{Operation: "write_file", MachineID: m.ID, Value: path, Contents: contents})
	return f.WriteErrors[path]
}
func (f *FakeMachineAccess) ProvisionPstackTree(m domain.Machine) (string, error) {
	f.record(MachineAccessCall{Operation: "provision_pstack_tree", MachineID: m.ID})
	return f.ProvisionPath, nil
}

type MachineReadiness struct {
	Reachable                *bool              `json:"reachable"`
	TmuxAvailable            *bool              `json:"tmuxAvailable"`
	BunAvailable             *bool              `json:"bunAvailable"`
	BunError                 *string            `json:"bunError"`
	ClaudeExecutableResolved *bool              `json:"claudeExecutableResolved"`
	CodexExecutableResolved  *bool              `json:"codexExecutableResolved"`
	StateDirectoryWritable   *bool              `json:"stateDirectoryWritable"`
	ClaudeHooks              AgentHookReadiness `json:"claudeHooks"`
	CodexHooks               AgentHookReadiness `json:"codexHooks"`
	LastProvisioningError    *string            `json:"lastProvisioningError"`
	Error                    *string            `json:"error"`
}
type AgentHookReadiness struct {
	Provisioned *bool   `json:"provisioned"`
	Current     *bool   `json:"current"`
	Error       *string `json:"error"`
}

type MachineCheckFunc func(domain.Machine) MachineReadiness

type TmuxTerminalRuntime struct{ Access MachineAccess }

type ObservedPane struct {
	SessionName string `json:"sessionName"`
	PaneID      string `json:"paneId"`
}

func (t TmuxTerminalRuntime) ObserveMachine(machine domain.Machine) ([]ObservedPane, error) {
	access := t.Access
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	command := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " list-panes -a -F " + shellQuote("#{session_name}\t#{pane_id}")
	output, err := access.RunShell(machine, command)
	if err != nil {
		return nil, fmt.Errorf("could not observe tmux panes on Machine %s: %w", machine.Name, err)
	}
	return parseObservedPanes(output), nil
}

func parseObservedPanes(output string) []ObservedPane {
	panes := make([]ObservedPane, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			panes = append(panes, ObservedPane{SessionName: parts[0], PaneID: parts[1]})
		}
	}
	return panes
}

func (t TmuxTerminalRuntime) CheckMachine(machine domain.Machine) MachineReadiness {
	ready := MachineReadiness{}
	access := t.Access
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	home, err := access.HomeDirectory(machine)
	if err != nil {
		var homeError machineHomeError
		if errors.As(err, &homeError) {
			setString(&ready.Error, homeError.message)
			setBool(&ready.Reachable, homeError.reachable)
		} else {
			setString(&ready.Error, fmt.Sprintf("Could not reach Machine %s: %v", machine.Name, err))
			setBool(&ready.Reachable, false)
		}
		return ready
	}
	setBool(&ready.Reachable, true)
	if access.IsLocal(machine) {
		_, err := access.FindExecutable(machine, "bun")
		setBool(&ready.BunAvailable, err == nil)
	} else if output, err := access.RunShell(machine, "command -v bun"); err != nil {
		setBool(&ready.BunAvailable, false)
		setString(&ready.BunError, fmt.Sprintf("Could not check Bun on Machine %s: %v", machine.Name, err))
	} else {
		setBool(&ready.BunAvailable, strings.TrimSpace(output) != "")
	}
	if _, err := access.RunShell(machine, "tmux -V"); err == nil {
		setBool(&ready.TmuxAvailable, true)
	} else {
		setBool(&ready.TmuxAvailable, false)
		setString(&ready.Error, fmt.Sprintf("Machine %s does not have a working tmux runtime: %v", machine.Name, err))
	}
	stateDirectory := filepath.Join(home, ".local", "state", "ai-mission-manager", "runs")
	if access.IsLocal(machine) {
		if err := os.MkdirAll(stateDirectory, 0o700); err == nil {
			probe, createErr := os.CreateTemp(stateDirectory, ".preflight-*")
			if createErr == nil {
				_ = probe.Close()
				_ = os.Remove(probe.Name())
				setBool(&ready.StateDirectoryWritable, true)
			} else {
				setBool(&ready.StateDirectoryWritable, false)
				if ready.Error == nil {
					setString(&ready.Error, fmt.Sprintf("Agent state directory is not writable on Machine %s: %v", machine.Name, createErr))
				}
			}
		} else {
			setBool(&ready.StateDirectoryWritable, false)
			if ready.Error == nil {
				setString(&ready.Error, fmt.Sprintf("Agent state directory is not writable on Machine %s: %v", machine.Name, err))
			}
		}
	} else if _, err := access.RunShell(machine, `set -eu; state_dir="$HOME/.local/state/ai-mission-manager/runs"; mkdir -p -- "$state_dir"; probe=$(mktemp "$state_dir/.preflight.XXXXXXXX"); rm -f -- "$probe"`); err == nil {
		setBool(&ready.StateDirectoryWritable, true)
	} else {
		setBool(&ready.StateDirectoryWritable, false)
		if ready.Error == nil {
			setString(&ready.Error, fmt.Sprintf("Agent state directory is not writable on Machine %s: %v", machine.Name, err))
		}
	}
	return ready
}
func setBool(target **bool, value bool)       { v := value; *target = &v }
func setString(target **string, value string) { v := value; *target = &v }
func machineNow() int64                       { return time.Now().Unix() }
