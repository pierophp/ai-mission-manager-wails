package backend

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func TestMachineAndProfileCommandsUseInjectedAdapters(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	access := NewFakeMachineAccess()
	runtime.SetMachineAdapters(access, func(domain.Machine) MachineReadiness {
		return MachineReadiness{Reachable: boolPointer(true), TmuxAvailable: boolPointer(true)}
	})
	service := CommandService{Runtime: runtime}
	registered, err := service.Invoke("register_machine", `{"contextId":1,"name":"Local","socketName":"mission","transport":{"kind":"local"}}`)
	if err != nil {
		t.Fatal(err)
	}
	var machine domain.Machine
	if err := json.Unmarshal(registered, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.ID != 1 || machine.Transport.Kind != domain.TransportLocal {
		t.Fatalf("registered Machine = %s", registered)
	}
	if _, err := service.Invoke("set_context_execution_machine", `{"contextId":1,"machineId":1}`); err != nil {
		t.Fatal(err)
	}
	checked, err := service.Invoke("check_machine", `{"machineId":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var view MachineSettingsView
	if err := json.Unmarshal(checked, &view); err != nil {
		t.Fatal(err)
	}
	if view.Readiness == nil || view.Readiness.TmuxAvailable == nil || !*view.Readiness.TmuxAvailable || view.LastObserved != "available" {
		t.Fatalf("check result = %s", checked)
	}
	profileJSON, err := service.Invoke("create_cli_configuration_profile", `{"machineId":1,"provider":"codex","name":"Work","appManaged":true,"existingDirectory":null}`)
	if err != nil {
		t.Fatal(err)
	}
	var profileView CliProfileSettingsView
	if err := json.Unmarshal(profileJSON, &profileView); err != nil {
		t.Fatal(err)
	}
	if profileView.Profile.Directory != "~/.config/ai-mission-manager/cli-profiles/codex/1" || profileView.SignInCommand == nil || *profileView.SignInCommand != `CODEX_HOME="$HOME/.config/ai-mission-manager/cli-profiles/codex/1" codex login` {
		t.Fatalf("profile result = %s", profileJSON)
	}
	if _, err := service.Invoke("set_context_cli_configuration_profile", `{"contextId":1,"provider":"codex","profileId":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("delete_cli_configuration_profile", `{"profileId":1}`); err == nil {
		t.Fatal("expected deletion of selected profile to fail")
	}
	if _, err := service.Invoke("set_context_cli_configuration_profile", `{"contextId":1,"provider":"codex","profileId":null}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("delete_cli_configuration_profile", `{"profileId":1}`); err != nil {
		t.Fatal(err)
	}
	removedProfile := false
	for _, call := range access.Calls {
		if call.Operation == "run_shell" && call.Value == "rm -rf -- '/home/fake/.config/ai-mission-manager/cli-profiles/codex/1'" {
			removedProfile = true
		}
	}
	if !removedProfile {
		t.Fatalf("app-managed profile directory was not removed: %#v", access.Calls)
	}
	if _, err := service.Invoke("update_machine", `{"machineId":1,"name":"Local renamed","socketName":"mission-2","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	listed, err := service.Invoke("list_machines", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(listed) {
		t.Fatalf("machine list is not JSON: %s", listed)
	}
	var machines []MachineSettingsView
	if err := json.Unmarshal(listed, &machines); err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 || machines[0].Name != "Local renamed" || machines[0].SocketName != "mission-2" {
		t.Fatalf("updated Machine was not retained: %#v", machines)
	}
	if len(access.Calls) == 0 {
		t.Fatal("expected injected MachineAccess to record calls")
	}
}

func TestMachineCommandsRejectInvalidSSHAndCrossMachineProfiles(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("register_machine", `{"contextId":1,"name":"Bad","socketName":"bad","transport":{"kind":"ssh","host":"bad;host","user":null,"port":null,"identity_file":null,"known_hosts_file":null,"strict_host_key_checking":"accept-new"}}`); err == nil {
		t.Fatal("expected unsafe SSH host to be rejected")
	}
	if _, err := service.Invoke("register_machine", `{"contextId":1,"name":"One","socketName":"one","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("create_cli_configuration_profile", `{"machineId":1,"provider":"claude","name":"External","appManaged":false,"existingDirectory":"~/claude"}`); err != nil {
		t.Fatalf("profile configuration should be retained for launch-time validation: %v", err)
	}
}

func TestMachineUpdatePersistsThroughReducerEffect(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	runtime, err := OpenRuntime(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("register_machine", `{"contextId":1,"name":"Runner","socketName":"mission","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("update_machine", `{"machineId":1,"name":"Renamed","socketName":"renamed-socket","transport":{"kind":"local"}}`); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = OpenRuntime(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	machines := runtime.listMachines()
	if len(machines) != 1 || machines[0].Name != "Renamed" || machines[0].SocketName != "renamed-socket" {
		t.Fatalf("persisted Machines = %#v", machines)
	}
}

func boolPointer(value bool) *bool { return &value }
