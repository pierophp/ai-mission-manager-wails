package backend

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func launchExternalTerminal(command string) error {
	program, args, err := linuxExternalTerminalCommand(command)
	if err != nil {
		return err
	}
	process := exec.Command(program, args...)
	// The app may have been launched inside tmux. The new terminal must attach
	// as an independent client rather than inherit that client's Pane identity.
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "TMUX=") && !strings.HasPrefix(variable, "TMUX_PANE=") {
			process.Env = append(process.Env, variable)
		}
	}
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return fmt.Errorf("could not open external terminal %s: %w", program, err)
	}
	// Catch immediate launcher failures (for example a missing desktop session).
	// Long-lived emulators are reaped without waiting for the user to detach.
	done := make(chan error, 1)
	go func() {
		done <- process.Wait()
	}()
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("external terminal %s failed to start: %w (see application stderr for details)", program, err)
		}
	case <-timer.C:
		go func() {
			if err := <-done; err != nil {
				log.Printf("external terminal %s exited: %v", program, err)
			}
		}()
	}
	return nil
}

func linuxExternalTerminalCommand(command string) (string, []string, error) {
	if preferred := strings.TrimSpace(os.Getenv("AI_MISSION_MANAGER_TERMINAL")); preferred != "" {
		args, supported := linuxTerminalArgs(filepath.Base(preferred), command)
		if !supported {
			return "", nil, fmt.Errorf("unsupported AI_MISSION_MANAGER_TERMINAL %q: use a supported executable without arguments, or configure xdg-terminal-exec", preferred)
		}
		program, err := exec.LookPath(preferred)
		if err != nil {
			return "", nil, fmt.Errorf("configured external terminal %q is unavailable: %w", preferred, err)
		}
		return program, args, nil
	}

	// Prefer the desktop's configured terminal when its launcher is installed.
	candidates := []string{"xdg-terminal-exec"}
	for _, desktop := range strings.Split(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), ":") {
		switch desktop {
		case "kde":
			candidates = append(candidates, "konsole")
		case "gnome":
			candidates = append(candidates, "gnome-terminal")
		case "xfce":
			candidates = append(candidates, "xfce4-terminal")
		}
	}
	candidates = append(candidates, "kitty", "alacritty", "gnome-terminal", "konsole", "xfce4-terminal")
	// foot is Wayland-only; do not select it on an X11 desktop just because it
	// is installed alongside another emulator.
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		candidates = append(candidates, "foot")
	}
	candidates = append(candidates, "xterm")
	for _, candidate := range candidates {
		if program, err := exec.LookPath(candidate); err == nil {
			args, _ := linuxTerminalArgs(candidate, command)
			return program, args, nil
		}
	}
	return "", nil, fmt.Errorf("no supported external terminal found in PATH: install xdg-terminal-exec and a terminal emulator, or kitty, alacritty, gnome-terminal, konsole, xfce4-terminal, foot (Wayland), or xterm")
}

func linuxTerminalArgs(program, command string) ([]string, bool) {
	var prefix []string
	switch program {
	case "xdg-terminal-exec", "gnome-terminal":
		prefix = []string{"--"}
	case "konsole", "alacritty", "xterm":
		prefix = []string{"-e"}
	case "xfce4-terminal":
		prefix = []string{"--execute"}
	case "kitty", "foot":
		// These emulators accept the program and its arguments directly.
	default:
		return nil, false
	}
	// Keep the entire validated local/SSH command as one shell argument. Do not
	// ask an emulator to parse or re-quote a concatenated command line.
	return append(prefix, "sh", "-lc", command), true
}
