// Package pstack provisions the vendored pstack tree and embedded workflow skills.
package pstack

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

//go:embed all:assets/pstack all:assets/skills/grilling all:assets/skills/to-spec all:assets/skills/to-tickets all:assets/skills/implement
var assets embed.FS

//go:generate go run ./internal/generate

type File struct {
	Path       string
	Executable bool
}

// Files, Version, UpstreamCommit, and TreeHash are generated from the vendor tree.
func ReadFile(name string) ([]byte, error) {
	return assets.ReadFile(name)
}

func Skill(name string) ([]byte, error) {
	if name != "grilling" && name != "to-spec" && name != "to-tickets" && name != "implement" {
		return nil, fmt.Errorf("unknown embedded workflow skill %q", name)
	}
	return assets.ReadFile(filepath.ToSlash(filepath.Join("assets/skills", name, "SKILL.md")))
}

func Payload() ([]byte, error) {
	var out strings.Builder
	for _, file := range Files {
		contents, err := assets.ReadFile("assets/pstack/" + file.Path)
		if err != nil {
			return nil, err
		}
		out.WriteString(file.Path)
		out.WriteByte('\n')
		if file.Executable {
			out.WriteString("1\n")
		} else {
			out.WriteString("0\n")
		}
		out.WriteString(base64.StdEncoding.EncodeToString(contents))
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	return []byte(out.String()), nil
}

func RemoteInstallCommand(target string) string {
	quoted := "'" + strings.ReplaceAll(target, "'", "'\"'\"'") + "'"
	return "set -eu; target=" + quoted + "; if [ -d \"$target\" ]; then exit 0; fi; parent=${target%/*}; mkdir -p \"$parent\"; temporary=\"$target.tmp.$$\"; trap 'rm -rf \"$temporary\"' EXIT HUP INT TERM; mkdir -p \"$temporary\"; while IFS= read -r relative && [ -n \"$relative\" ]; do IFS= read -r executable || exit 1; IFS= read -r contents || exit 1; case \"$relative\" in /*|*..*) exit 1;; esac; file=\"$temporary/$relative\"; mkdir -p \"${file%/*}\"; printf '%s' \"$contents\" | base64 -d > \"$file\"; if [ \"$executable\" = 1 ]; then chmod 755 \"$file\"; else chmod 644 \"$file\"; fi; done; if [ -d \"$target\" ]; then exit 0; fi; mv \"$temporary\" \"$target\"; trap - EXIT HUP INT TERM"
}

func InstallLocal(target string) error {
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("pstack target exists but is not a directory: %s", target)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".pstack-"+TreeHash+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	for _, file := range Files {
		if filepath.IsAbs(file.Path) || strings.Contains(filepath.ToSlash(file.Path), "../") || file.Path == ".." {
			return fmt.Errorf("invalid embedded pstack path: %s", file.Path)
		}
		data, err := assets.ReadFile("assets/pstack/" + file.Path)
		if err != nil {
			return err
		}
		name := filepath.Join(temporary, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if file.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(name, data, mode); err != nil {
			return err
		}
		if err := os.Chmod(name, mode); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, target); err != nil {
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			return nil
		}
		return fmt.Errorf("could not install pstack tree: %w", err)
	}
	return nil
}

func RoleFilePath(root string, context domain.Context) string {
	roles, _ := json.Marshal(context.PstackRoles)
	debugOption := func(value *int64) string {
		if value == nil {
			return "None"
		}
		return "Some(" + strconv.FormatInt(*value, 10) + ")"
	}
	identity := fmt.Sprintf("%s|%s|%s|%d", roles, debugOption(context.ClaudeProfileID), debugOption(context.CodexProfileID), context.ID)
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(identity))
	return filepath.Join(root, "roles", fmt.Sprintf("context-%d-%016x.md", context.ID, hash.Sum64()))
}

func SkillSnapshot() string {
	return fmt.Sprintf("pstack version %s; upstream commit %s; tree hash %s", Version, UpstreamCommit, TreeHash)
}

func ComposeRoleFile(parent domain.AgentKind, roles domain.PstackRoleTable, claude, codex *domain.CLIConfigurationProfile) []byte {
	var out strings.Builder
	out.WriteString("# pstack role assignments\n\nDelegate each task according to its role row. Use the configured model and effort. When the role CLI matches the active parent CLI, spawn the role inside the current harness. When it differs, invoke that CLI as shown, preserving the selected Context profile.\n\n")
	for _, row := range roles {
		config := row.Configuration
		profile := claude
		if config.Agent == domain.AgentCodex {
			profile = codex
		}
		profileLine := "the standard CLI configuration (no Context profile selected)"
		profileEnv := ""
		if profile != nil {
			profileLine = fmt.Sprintf("Context CLI profile #%d (%q) at `%s`", profile.ID, profile.Name, profile.Directory)
			if config.Agent == domain.AgentClaude {
				profileEnv = "CLAUDE_CONFIG_DIR=" + shellQuote(profile.Directory) + " "
			} else {
				profileEnv = "CODEX_HOME=" + shellQuote(profile.Directory) + " "
			}
		}
		invocation := fmt.Sprintf("`%sclaude -p --model %s --effort %s`", profileEnv, config.Model, config.Effort)
		if config.Agent == domain.AgentCodex {
			invocation = fmt.Sprintf("`%scodex exec --model %s -c model_reasoning_effort=%s`", profileEnv, config.Model, config.Effort)
		}
		delegation := fmt.Sprintf("This role uses the other CLI; invoke it using %s with %s.", invocation, profileLine)
		if config.Agent == parent {
			delegation = fmt.Sprintf("Spawn this role inside the active %s harness.", config.Agent)
		}
		fmt.Fprintf(&out, "## %s\n- CLI: %s\n- Model: `%s`\n- Effort: `%s`\n- %s\n\n", roleLabel(row.Role), config.Agent, config.Model, config.Effort, delegation)
	}
	return []byte(out.String())
}

func roleLabel(role domain.PstackRole) string {
	switch role {
	case "code-delegate":
		return "Code delegate"
	case "judge-and-prose":
		return "Judge and prose"
	case "review-panel":
		return "Review panel"
	case "explorers":
		return "Explorers"
	default:
		return string(role)
	}
}
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
