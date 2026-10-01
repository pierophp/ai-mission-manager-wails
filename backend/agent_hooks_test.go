package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func TestMergeAgentHooksPreservesUserHooksAndRemovesLegacyEntries(t *testing.T) {
	existing := []byte(`{"permissions":{"allow":["Bash(*)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"keep-me"},{"type":"command","command":"'/old/.local/share/ai-mission-manager/hooks/ai-mission-manager-agent-state-hook.sh' blocked claude"}]}]}}`)
	got, err := mergeAgentStateHooks(existing, "/home/test/.local/share/ai-mission-manager/hooks/ai-mission-manager-agent-state-hook.sh", domain.AgentClaude)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatal(err)
	}
	if root["permissions"] == nil {
		t.Fatal("non-hook settings were lost")
	}
	text := string(got)
	if !strings.Contains(text, "keep-me") || strings.Contains(text, "'/old/.local/share") {
		t.Fatalf("hook merge = %s", got)
	}
	for _, event := range agentHookEvents(domain.AgentClaude) {
		if !strings.Contains(text, `"`+event.name+`"`) {
			t.Errorf("event %s missing", event.name)
		}
	}
}

func TestRunPreflightChecksProfileAndInstallsByteExactHooksInTempHome(t *testing.T) {
	home := t.TempDir()
	profileDir := filepath.Join(home, "profile")
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	access := NewFakeMachineAccess()
	access.Home = home
	access.Executables["bun"] = "/usr/bin/bun"
	access.Executables["claude"] = "/usr/bin/claude"
	machine := domain.Machine{ID: 1, Name: "temp", SocketName: "launch-test", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	profile := domain.CLIConfigurationProfile{ID: 3, MachineID: 1, Provider: domain.AgentClaude, Name: "temp profile", Directory: profileDir}
	executable, stateFile, resolved, readiness, err := (tmuxAgentRunExecutor{access: access}).Preflight(machine, domain.AgentClaude, 23, &profile)
	if err != nil {
		t.Fatal(err)
	}
	if executable != "/usr/bin/claude" || resolved != profileDir || stateFile != filepath.Join(home, ".local/state/ai-mission-manager/runs/run-23.json") || readiness.ClaudeHooks.Current == nil || !*readiness.ClaudeHooks.Current {
		t.Fatalf("preflight result executable=%q state=%q profile=%q readiness=%#v", executable, stateFile, resolved, readiness)
	}
	hook, err := os.ReadFile(filepath.Join(home, ".local/share/ai-mission-manager/hooks/ai-mission-manager-agent-state-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(hook) != agentStateHookScript {
		t.Fatal("installed agent state hook differs from the embedded source")
	}
	settings, err := os.ReadFile(filepath.Join(profileDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "SessionStart") || !strings.Contains(string(settings), "ai-mission-manager-agent-state-hook.sh") {
		t.Fatalf("Claude profile settings missing registered hook: %s", settings)
	}
	foundAuth := false
	for _, call := range access.Calls {
		if call.Operation == "run_shell" && strings.Contains(call.Value, "auth") {
			foundAuth = true
			if !strings.Contains(call.Value, "unset ANTHROPIC_API_KEY") || !strings.Contains(call.Value, "CLAUDE_CONFIG_DIR") {
				t.Errorf("profile auth command did not scrub credentials/configure profile: %s", call.Value)
			}
		}
	}
	if !foundAuth {
		t.Fatal("profile authentication was not checked")
	}
}

func TestRemoteRunPreflightScrubsInheritedCredentialsAndMakesHookExecutable(t *testing.T) {
	access := NewFakeMachineAccess()
	access.Home = "/home/runner"
	access.Executables["bun"] = "/opt/bun"
	access.Executables["codex"] = "/opt/codex"
	machine := domain.Machine{ID: 2, Name: "remote", SocketName: "remote-test", Transport: domain.MachineTransport{Kind: domain.TransportSSH, Host: stringPtr("runner.example")}}
	profile := domain.CLIConfigurationProfile{ID: 4, MachineID: 2, Provider: domain.AgentCodex, Name: "remote profile", Directory: "~/profiles/codex"}
	_, _, _, _, err := (tmuxAgentRunExecutor{access: access}).Preflight(machine, domain.AgentCodex, 44, &profile)
	if err != nil {
		t.Fatal(err)
	}
	var auth, chmod bool
	for _, call := range access.Calls {
		if call.Operation != "run_shell" {
			continue
		}
		if strings.Contains(call.Value, "login") {
			auth = true
			for _, credential := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"} {
				if !strings.Contains(call.Value, "unset "+credential) {
					t.Errorf("remote profile auth did not scrub %s: %s", credential, call.Value)
				}
			}
		}
		if strings.Contains(call.Value, "chmod 700 --") && strings.Contains(call.Value, "ai-mission-manager-agent-state-hook.sh") {
			chmod = true
		}
	}
	if !auth || !chmod {
		t.Fatalf("remote preflight auth=%v hook chmod=%v calls=%#v", auth, chmod, access.Calls)
	}
}
