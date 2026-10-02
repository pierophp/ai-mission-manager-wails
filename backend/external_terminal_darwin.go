package backend

import (
	"fmt"
	"os/exec"
	"strings"
)

func launchExternalTerminal(command string) error {
	if _, err := exec.LookPath("osascript"); err != nil {
		return fmt.Errorf("macOS Terminal is unavailable: %w", err)
	}
	script := "tell application \"Terminal\"\nactivate\ndo script " + appleScriptString(command) + "\nend tell"
	return exec.Command("osascript", "-e", script).Run()
}

func appleScriptString(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(value) + "\""
}
