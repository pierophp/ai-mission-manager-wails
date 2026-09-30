package backend

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func TestBuildMachineShellCommandMatchesSSHTransportContract(t *testing.T) {
	host, user := "build.example.com", "runner"
	port := uint16(2222)
	identity, knownHosts, strict := "~/.ssh/id key", "/tmp/known hosts", "accept-new"
	machine := domain.Machine{ID: 4, Transport: domain.MachineTransport{
		Kind: domain.TransportSSH, Host: &host, User: &user, Port: &port,
		IdentityFile: &identity, KnownHostsFile: &knownHosts, StrictHostKeyChecking: &strict,
	}}
	program, args, err := buildMachineShellCommand(machine, `printf '%s' "hello world"`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-T", "-o", "BatchMode=yes", "-p", "2222", "-i", identity, "-o", "UserKnownHostsFile=" + knownHosts, "-o", "StrictHostKeyChecking=accept-new", "runner@build.example.com", `printf '%s' "hello world"`}
	if program != "ssh" || !reflect.DeepEqual(args, want) {
		t.Fatalf("command = %s %q, want ssh %q", program, args, want)
	}
}

func TestTmuxReadinessMatchesMachineCheckProjection(t *testing.T) {
	access := NewFakeMachineAccess()
	access.Home = "/home/runner"
	access.Executables["bun"] = "/home/runner/.bun/bin/bun"
	access.ShellResults["command -v bun"] = MachineAccessResult{Value: "/home/runner/.bun/bin/bun"}
	host := "runner.example"
	machine := domain.Machine{ID: 5, Name: "Runner", Transport: domain.MachineTransport{Kind: domain.TransportSSH, Host: &host}}
	readiness := (TmuxTerminalRuntime{Access: access}).CheckMachine(machine)
	if readiness.Reachable == nil || !*readiness.Reachable || readiness.TmuxAvailable == nil || !*readiness.TmuxAvailable || readiness.BunAvailable == nil || !*readiness.BunAvailable || readiness.StateDirectoryWritable == nil || !*readiness.StateDirectoryWritable {
		t.Fatalf("readiness = %#v", readiness)
	}
	if readiness.ClaudeExecutableResolved != nil || readiness.CodexExecutableResolved != nil || readiness.ClaudeHooks.Provisioned != nil || readiness.CodexHooks.Provisioned != nil {
		t.Fatalf("check_machine should leave run-only readiness fields null: %#v", readiness)
	}
	encoded, err := json.Marshal(MachineSettingsView{Machine: machine, Readiness: &readiness})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "context_id", "socket_name", "transport", "last_observed", "readiness"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("MachineSettingsView omitted flattened field %q: %s", key, encoded)
		}
	}
	var projected map[string]json.RawMessage
	if err := json.Unmarshal(wire["readiness"], &projected); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reachable", "tmuxAvailable", "bunAvailable", "stateDirectoryWritable", "claudeHooks", "codexHooks", "lastProvisioningError"} {
		if _, ok := projected[key]; !ok {
			t.Errorf("readiness omitted Tauri field %q", key)
		}
	}
}

func TestBuildMachineShellCommandRejectsUnsafeSSHSettings(t *testing.T) {
	for name, transport := range map[string]domain.MachineTransport{
		"shell syntax in host":        {Kind: domain.TransportSSH, Host: stringPointer("host; touch /tmp/pwned")},
		"shell syntax in user":        {Kind: domain.TransportSSH, Host: stringPointer("host"), User: stringPointer("user@host")},
		"unsupported strict checking": {Kind: domain.TransportSSH, Host: stringPointer("host"), StrictHostKeyChecking: stringPointer("ask")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := buildMachineShellCommand(domain.Machine{Transport: transport}, "true"); err == nil {
				t.Fatal("expected invalid SSH transport error")
			}
		})
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got, want := shellQuote("one'two"), `'one'\''two'`; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestBuildTmuxProcessCommandQuotesRemoteArguments(t *testing.T) {
	host, user := "machine.example", "runner"
	port := uint16(2200)
	strict := "no"
	machine := domain.Machine{Transport: domain.MachineTransport{Kind: domain.TransportSSH, Host: &host, User: &user, Port: &port, StrictHostKeyChecking: &strict}}
	program, args, err := buildTmuxProcessCommand(machine, true, []string{"-f", "/dev/null", "-L", "mission; socket", "list-sessions"}, "/opt/tmux path")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-tt", "-o", "BatchMode=yes", "-p", "2200", "-o", "StrictHostKeyChecking=no", "runner@machine.example", `'/opt/tmux path' '-f' '/dev/null' '-L' 'mission; socket' 'list-sessions'`}
	if program != "ssh" || !reflect.DeepEqual(args, want) {
		t.Fatalf("command = %s %q, want ssh %q", program, args, want)
	}
}

func TestFakeMachineAccessRecordsCallsAndCopiesFileContents(t *testing.T) {
	fake := NewFakeMachineAccess()
	machine := domain.Machine{ID: 9}
	data := []byte("before")
	if err := fake.WriteFile(machine, "/tmp/profile", data); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if len(fake.Calls) != 1 || fake.Calls[0].Operation != "write_file" || fake.Calls[0].MachineID != 9 || string(fake.Calls[0].Contents) != "before" {
		t.Fatalf("calls = %#v", fake.Calls)
	}
}

func TestResolveMachinePathExpandsRemoteHomeBeforeQuoting(t *testing.T) {
	host := "runner.example"
	machine := domain.Machine{Transport: domain.MachineTransport{Kind: domain.TransportSSH, Host: &host}}
	got, err := resolveMachinePath(machine, "~/config/agent profile", "/home/runner")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/runner/config/agent profile" {
		t.Fatalf("resolved remote path = %q", got)
	}
	if _, err := resolveMachinePath(machine, "relative/profile", "/home/runner"); err == nil {
		t.Fatal("expected ambiguous remote relative path to fail")
	}
}

func TestTmuxObserveMachineUsesSocketAndParsesPanes(t *testing.T) {
	access := NewFakeMachineAccess()
	machine := domain.Machine{ID: 3, Name: "Runner", SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	command := "tmux -f /dev/null -L 'mission' list-panes -a -F '#{session_name}\t#{pane_id}'"
	access.ShellResults[command] = MachineAccessResult{Value: "build\t%0\nreview\t%4\n"}
	panes, err := (TmuxTerminalRuntime{Access: access}).ObserveMachine(machine)
	if err != nil {
		t.Fatal(err)
	}
	want := []ObservedPane{{SessionName: "build", PaneID: "%0"}, {SessionName: "review", PaneID: "%4"}}
	if !reflect.DeepEqual(panes, want) {
		t.Fatalf("panes = %#v, want %#v", panes, want)
	}
	if len(access.Calls) != 1 || access.Calls[0].Value != command {
		t.Fatalf("calls = %#v", access.Calls)
	}
}

func stringPointer(value string) *string { return &value }
