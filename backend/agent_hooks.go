package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func decodeBase64(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("could not decode provider hook settings: %w", err)
	}
	return decoded, nil
}

const agentStateHookScript = `#!/bin/sh
umask 077

# Provider payloads are deliberately opaque; the state is fixed in argv.
cat >/dev/null || exit 1

run_id=${AI_MISSION_MANAGER_RUN_ID-}
[ -n "$run_id" ] || exit 0
case "$run_id" in *[!0-9]*) exit 0 ;; esac

state=${1-}
agent=${2-}
case "$state" in working|blocked|finished) ;; *) exit 0 ;; esac
case "$agent" in claude|codex) ;; *) exit 0 ;; esac
home=${HOME-}
[ -n "$home" ] || exit 0
state_file=${AI_MISSION_MANAGER_STATE_FILE-}
[ -n "$state_file" ] || exit 0
case "$state_file" in /*) ;; *) exit 1 ;; esac
[ "${state_file##*/}" = "run-$run_id.json" ] || exit 1
case "$state_file" in "$home"/.local/state/ai-mission-manager/runs/run-"$run_id".json) ;; *) exit 1 ;; esac

runs_dir=${state_file%/*}
app_dir=${runs_dir%/*}
state_base=${app_dir%/*}
mkdir -p "$state_base" || exit 1
for dir in "$app_dir" "$runs_dir"; do
    if [ ! -d "$dir" ]; then
        if mkdir "$dir" 2>/dev/null; then
            chmod 700 "$dir" || exit 1
        elif [ ! -d "$dir" ]; then
            exit 1
        fi
    fi
done

temporary="$state_file.tmp.$$"
updated_at=$(date +%s) || exit 1
previous_sequence=0
if [ -f "$state_file" ]; then
    previous_sequence=$(sed -n 's/.*"sequence"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\)[[:space:]]*[,}].*/\1/p' \
        "$state_file" 2>/dev/null | head -n 1)
    case "$previous_sequence" in
        ''|*[!0-9]*) previous_sequence=0 ;;
    esac
fi
sequence=$((previous_sequence + 1))
record=$(printf '{"agent":"%s","runId":"%s","state":"%s","updatedAt":"%s","sequence":%s}' \
    "$agent" "$run_id" "$state" "$updated_at" "$sequence") || exit 1

(umask 077; set -C; printf '%s\n' "$record" >"$temporary") || exit 1
chmod 600 "$temporary" || { rm -f "$temporary"; exit 1; }
if ! mv -f "$temporary" "$state_file"; then
    rm -f "$temporary"
    exit 1
fi

tmux_path=${AI_MISSION_MANAGER_TMUX_PATH-}
socket_name=${AI_MISSION_MANAGER_TMUX_SOCKET-}
pane_id=${AI_MISSION_MANAGER_PANE_ID-}
if [ -n "$tmux_path" ] && [ -n "$socket_name" ] && [ -n "$pane_id" ]; then
    "$tmux_path" -f /dev/null -L "$socket_name" set-option -p -t "$pane_id" \
        @ai_mission_manager_run_state "$record" >/dev/null 2>&1 || :
fi
exit 0
`

func mergeAgentStateHooks(existing []byte, hookPath string, agent domain.AgentKind) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(existing))) != 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("could not read provider hooks as JSON: %w", err)
		}
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		if _, exists := root["hooks"]; exists {
			return nil, fmt.Errorf("hooks in provider settings must be an object")
		}
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for name, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(groups))
		for _, group := range groups {
			gm, ok := group.(map[string]any)
			if !ok {
				kept = append(kept, group)
				continue
			}
			commands, ok := gm["hooks"].([]any)
			if !ok {
				kept = append(kept, group)
				continue
			}
			cleaned := make([]any, 0, len(commands))
			removed := false
			for _, rawHook := range commands {
				hm, ok := rawHook.(map[string]any)
				command, _ := hm["command"].(string)
				if ok && oldAgentHookCommand(command, agent) {
					removed = true
					continue
				}
				cleaned = append(cleaned, rawHook)
			}
			if removed && len(cleaned) == 0 {
				continue
			}
			gm["hooks"] = cleaned
			kept = append(kept, gm)
		}
		hooks[name] = kept
	}
	for _, event := range agentHookEvents(agent) {
		groups, ok := hooks[event.name].([]any)
		if !ok {
			if _, exists := hooks[event.name]; exists {
				return nil, fmt.Errorf("hooks.%s must be an array", event.name)
			}
			groups = []any{}
		}
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": shellQuote(hookPath) + " " + event.state + " " + string(agent)}}}
		if event.name == "Notification" {
			group["matcher"] = "permission_prompt|elicitation_dialog|elicitation_url_dialog"
		}
		hooks[event.name] = append(groups, group)
	}
	contents, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(contents, '\n'), nil
}

type hookEvent struct{ name, state string }

func agentHookEvents(agent domain.AgentKind) []hookEvent {
	if agent == domain.AgentClaude {
		return []hookEvent{{"SessionStart", "working"}, {"UserPromptSubmit", "working"}, {"Notification", "blocked"}, {"Stop", "finished"}, {"SessionEnd", "finished"}}
	}
	return []hookEvent{{"SessionStart", "working"}, {"UserPromptSubmit", "working"}, {"PermissionRequest", "blocked"}, {"Stop", "finished"}, {"SessionEnd", "finished"}}
}

func oldAgentHookCommand(command string, agent domain.AgentKind) bool {
	if strings.Contains(command, "ai-mission-manager-agent-state-hook.sh") {
		return true
	}
	if !strings.Contains(command, "--agent-state-hook") {
		return false
	}
	return strings.Contains(command, string(agent))
}

func installAgentHooks(access MachineAccess, machine domain.Machine, home string, agent domain.AgentKind, profileDirectory string) error {
	hook := filepath.Join(home, ".local", "share", "ai-mission-manager", "hooks", "ai-mission-manager-agent-state-hook.sh")
	providerDir, file := ".claude", "settings.json"
	if agent == domain.AgentCodex {
		providerDir, file = ".codex", "hooks.json"
	}
	providerDirectory := filepath.Join(home, providerDir)
	if profileDirectory != "" {
		providerDirectory = profileDirectory
	}
	path := filepath.Join(providerDirectory, file)
	if access.IsLocal(machine) {
		if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
			return err
		}
		if err := rejectHookPathIfUnsafe(hook); err != nil {
			return err
		}
		installed := false
		if info, err := os.Stat(hook); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			contents, readErr := os.ReadFile(hook)
			if readErr != nil {
				return readErr
			}
			installed = string(contents) == agentStateHookScript
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !installed {
			if err := atomicWriteFile(hook, []byte(agentStateHookScript)); err != nil {
				return err
			}
			if err := os.Chmod(hook, 0o700); err != nil {
				return err
			}
		}
		if err := rejectHookPathIfUnsafe(path); err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		merged, err := mergeAgentStateHooks(contents, hook, agent)
		if err != nil {
			return err
		}
		if err := atomicWriteFile(path, merged); err != nil {
			return err
		}
		return nil
	}
	if err := access.WriteFile(machine, hook, []byte(agentStateHookScript)); err != nil {
		return fmt.Errorf("could not provision the agent state hook script: %w", err)
	}
	if _, err := access.RunShell(machine, "chmod 700 -- "+shellQuote(hook)); err != nil {
		return fmt.Errorf("could not make the agent state hook executable: %w", err)
	}
	output, err := access.RunShell(machine, "target="+shellQuote(path)+"; if [ -L \"$target\" ] || { [ -e \"$target\" ] && [ ! -f \"$target\" ]; }; then printf '%s\\n' 'agent hook settings target is not a regular file' >&2; exit 1; fi; if [ -f \"$target\" ]; then base64 < \"$target\"; fi")
	if err != nil {
		return err
	}
	var existing []byte
	if strings.TrimSpace(output) != "" {
		existing, err = decodeBase64(strings.TrimSpace(output))
		if err != nil {
			return err
		}
	}
	merged, err := mergeAgentStateHooks(existing, hook, agent)
	if err != nil {
		return err
	}
	return access.WriteFile(machine, path, merged)
}

func rejectHookPathIfUnsafe(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("agent hook target is not a regular file: %s", path)
	}
	return nil
}
