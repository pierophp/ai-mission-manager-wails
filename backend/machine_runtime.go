package backend

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type MachineSettingsView struct {
	domain.Machine
	Readiness *MachineReadiness `json:"readiness"`
}

type CliProfileSettingsView struct {
	Profile       domain.CLIConfigurationProfile `json:"profile"`
	SignInCommand *string                        `json:"signInCommand"`
}

func cliProfileSettingsView(profile domain.CLIConfigurationProfile) CliProfileSettingsView {
	view := CliProfileSettingsView{Profile: profile}
	if profile.AppManaged {
		command := cliSignInCommand(profile.Provider, profile.Directory)
		view.SignInCommand = &command
	}
	return view
}

func cliSignInCommand(provider domain.AgentKind, directory string) string {
	variable, invocation := "", ""
	switch provider {
	case domain.AgentClaude:
		variable, invocation = "CLAUDE_CONFIG_DIR", "claude"
	case domain.AgentCodex:
		variable, invocation = "CODEX_HOME", "codex login"
	}
	if relative, ok := strings.CutPrefix(directory, "~/"); ok {
		return fmt.Sprintf("%s=\"$HOME/%s\" %s", variable, relative, invocation)
	}
	return fmt.Sprintf("%s=%s %s", variable, shellQuote(directory), invocation)
}

func (r *Runtime) listMachines() []MachineSettingsView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MachineSettingsView, 0, len(r.state.Machines))
	for _, machine := range r.state.Machines {
		view := MachineSettingsView{Machine: machine}
		if readiness, ok := r.machineReadiness[machine.ID]; ok {
			value := readiness
			view.Readiness = &value
		}
		out = append(out, view)
	}
	return out
}

func (r *Runtime) listCLIConfigurationProfiles() []CliProfileSettingsView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CliProfileSettingsView, 0, len(r.state.CLIConfigurationProfiles))
	for _, profile := range r.state.CLIConfigurationProfiles {
		out = append(out, cliProfileSettingsView(profile))
	}
	return out
}

func (r *Runtime) registerMachine(contextID int64, name, socketName string, transport domain.MachineTransport) (domain.Machine, error) {
	if transport.Kind == domain.TransportSSH {
		if err := validateSSHTransport(transport); err != nil {
			return domain.Machine{}, err
		}
	}
	value, err := r.runEvent(domain.Event{Kind: "register_machine", ContextID: contextID, Name: name, SocketName: socketName, Transport: transport})
	if err != nil {
		return domain.Machine{}, err
	}
	machine, ok := value.(domain.Machine)
	if !ok {
		return domain.Machine{}, errors.New("Machine registration returned an invalid result")
	}
	return machine, nil
}

func (r *Runtime) updateMachine(machineID int64, name, socketName string, transport domain.MachineTransport) (domain.Machine, error) {
	if transport.Kind == domain.TransportSSH {
		if err := validateSSHTransport(transport); err != nil {
			return domain.Machine{}, err
		}
	}
	value, err := r.runEvent(domain.Event{Kind: "update_machine", MachineID: machineID, Name: name, SocketName: socketName, Transport: transport})
	if err != nil {
		return domain.Machine{}, err
	}
	machine, ok := value.(domain.Machine)
	if !ok {
		return domain.Machine{}, errors.New("Machine update returned an invalid result")
	}
	r.mu.Lock()
	delete(r.machineReadiness, machineID)
	r.mu.Unlock()
	return machine, nil
}

func (r *Runtime) setContextExecutionMachine(contextID int64, machineID *int64) (Context, error) {
	value, err := r.runEvent(domain.Event{Kind: "set_context_execution_machine", ContextID: contextID, ExecutionMachineID: machineID})
	if err != nil {
		return Context{}, err
	}
	context, ok := value.(Context)
	if !ok {
		return Context{}, errors.New("Context update returned an invalid result")
	}
	return context, nil
}

func (r *Runtime) setContextCLIConfigurationProfile(contextID int64, provider domain.AgentKind, profileID *int64) (Context, error) {
	value, err := r.runEvent(domain.Event{Kind: "set_context_cli_configuration_profile", ContextID: contextID, Provider: provider, CLIProfileID: profileID})
	if err != nil {
		return Context{}, err
	}
	context, ok := value.(Context)
	if !ok {
		return Context{}, errors.New("Context update returned an invalid result")
	}
	return context, nil
}

func (r *Runtime) createCLIConfigurationProfile(machineID int64, provider domain.AgentKind, name string, appManaged bool, existingDirectory *string) (CliProfileSettingsView, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	state := r.state
	var machine *domain.Machine
	for i := range state.Machines {
		if state.Machines[i].ID == machineID {
			value := state.Machines[i]
			machine = &value
			break
		}
	}
	access := r.machineAccess
	r.mu.Unlock()
	if machine == nil {
		return CliProfileSettingsView{}, fmt.Errorf("Machine %d does not exist", machineID)
	}
	if access == nil {
		return CliProfileSettingsView{}, errors.New("Machine access is not configured")
	}
	if provider != domain.AgentClaude && provider != domain.AgentCodex {
		return CliProfileSettingsView{}, fmt.Errorf("unknown AgentKind variant %q", provider)
	}
	profileID := state.NextCLIProfileID
	directory := ""
	if appManaged {
		directory = fmt.Sprintf("~/.config/ai-mission-manager/cli-profiles/%s/%d", provider, profileID)
	} else if existingDirectory != nil {
		directory = strings.TrimSpace(*existingDirectory)
	}
	event := domain.Event{Kind: "create_cli_configuration_profile", MachineID: machineID, Provider: provider, ProfileName: name, ProfileDirectory: directory, AppManaged: appManaged}
	decision, err := domain.Decide(state, event)
	if err != nil {
		return CliProfileSettingsView{}, err
	}
	if appManaged {
		resolved, err := access.ResolvePath(*machine, directory)
		if err != nil {
			return CliProfileSettingsView{}, err
		}
		if _, err := access.RunShell(*machine, "mkdir -p -- "+shellQuote(resolved)); err != nil {
			return CliProfileSettingsView{}, err
		}
	}
	if err := r.persistDecisionLocked(state, &decision); err != nil {
		return CliProfileSettingsView{}, err
	}
	return cliProfileSettingsView(decision.State.CLIConfigurationProfiles[len(decision.State.CLIConfigurationProfiles)-1]), nil
}

func (r *Runtime) deleteCLIConfigurationProfile(profileID int64) error {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	state := r.state
	access := r.machineAccess
	r.mu.Unlock()
	decision, err := domain.Decide(state, domain.Event{Kind: "delete_cli_configuration_profile", ProfileID: profileID})
	if err != nil {
		return err
	}
	for _, profile := range state.CLIConfigurationProfiles {
		if profile.ID != profileID || !profile.AppManaged {
			continue
		}
		if access == nil {
			return errors.New("Machine access is not configured")
		}
		var machine *domain.Machine
		for i := range state.Machines {
			if state.Machines[i].ID == profile.MachineID {
				value := state.Machines[i]
				machine = &value
				break
			}
		}
		if machine == nil {
			return fmt.Errorf("Machine %d does not exist", profile.MachineID)
		}
		expected := fmt.Sprintf("~/.config/ai-mission-manager/cli-profiles/%s/%d", profile.Provider, profile.ID)
		if profile.Directory != expected {
			return errors.New("app-managed CLI profile directory is invalid")
		}
		resolved, err := access.ResolvePath(*machine, expected)
		if err != nil {
			return err
		}
		if _, err := access.RunShell(*machine, "rm -rf -- "+shellQuote(resolved)); err != nil {
			return err
		}
		break
	}
	return r.persistDecisionLocked(state, &decision)
}

func (r *Runtime) checkMachine(machineID int64) (MachineSettingsView, error) {
	r.mu.Lock()
	var machine *domain.Machine
	for i := range r.state.Machines {
		if r.state.Machines[i].ID == machineID {
			value := r.state.Machines[i]
			machine = &value
			break
		}
	}
	checkMachine := r.machineChecker
	r.machineCheckGenerations[machineID]++
	generation := r.machineCheckGenerations[machineID]
	r.mu.Unlock()
	if machine == nil {
		return MachineSettingsView{}, fmt.Errorf("Machine %d does not exist", machineID)
	}
	if checkMachine == nil {
		return MachineSettingsView{}, errors.New("Machine check is not configured")
	}
	readiness := checkMachine(*machine)
	observation := domain.MachineObservation("offline")
	if readiness.Reachable != nil && *readiness.Reachable && readiness.TmuxAvailable != nil && *readiness.TmuxAvailable {
		observation = "available"
	}
	if err := r.recordMachineObservation(*machine, generation, observation); err != nil {
		return MachineSettingsView{}, err
	}
	r.mu.Lock()
	r.machineReadiness[machineID] = readiness
	r.mu.Unlock()
	for _, view := range r.listMachines() {
		if view.ID == machineID {
			return view, nil
		}
	}
	return MachineSettingsView{}, fmt.Errorf("Machine %d no longer exists", machineID)
}

func (r *Runtime) recordMachineObservation(checked domain.Machine, generation uint64, observation domain.MachineObservation) error {
	machineID := checked.ID
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	if r.machineCheckGenerations[machineID] != generation {
		r.mu.Unlock()
		return fmt.Errorf("Machine %d check result was superseded by a newer check", machineID)
	}
	state := r.state
	index := -1
	for i, m := range state.Machines {
		if m.ID == machineID {
			index = i
			if !reflect.DeepEqual(m, checked) {
				r.mu.Unlock()
				return fmt.Errorf("Machine %d changed while it was being checked; check it again", machineID)
			}
			break
		}
	}
	r.mu.Unlock()
	if index < 0 {
		return fmt.Errorf("Machine %d no longer exists", machineID)
	}
	if r.store == nil {
		return errors.New("runtime is not configured")
	}
	now := machineNow()
	decision, err := domain.Decide(state, domain.Event{Kind: "observe_machine", MachineID: machineID, MachineObservation: observation, ObservedAt: now})
	if err != nil {
		return err
	}
	return r.persistDecisionLocked(state, &decision)
}
