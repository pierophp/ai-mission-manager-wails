package backend

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type terminalSession struct {
	generation uint64
	connection terminalConnection
}

type terminalConnection interface {
	sendInput([]byte) error
	resize(uint16, uint16) error
	snapshot() ([]byte, error)
	close() error
}

type terminalOpenFunc func(machine domain.Machine, sessionName, paneID string, onOutput func([]byte), onSubscription func(string), onExit func(*int)) (terminalConnection, error)

type PaneSummary struct {
	PaneID         string `json:"paneId"`
	SessionName    string `json:"sessionName"`
	RunID          int64  `json:"runId"`
	Label          string `json:"label"`
	Available      bool   `json:"available"`
	PaneIndex      uint32 `json:"paneIndex"`
	PID            uint32 `json:"pid"`
	Columns        uint16 `json:"columns"`
	Rows           uint16 `json:"rows"`
	Title          string `json:"title"`
	CurrentCommand string `json:"currentCommand"`
	CurrentPath    string `json:"currentPath"`
}

type TerminalAttachment struct {
	TerminalID  string        `json:"terminalId"`
	Generation  uint64        `json:"generation"`
	SessionName string        `json:"sessionName"`
	PaneID      string        `json:"paneId"`
	Snapshot    []int         `json:"snapshot"`
	Panes       []PaneSummary `json:"panes"`
}

type TerminalOutputEvent struct {
	TerminalID string `json:"terminalId"`
	Generation uint64 `json:"generation"`
	PaneID     string `json:"paneId"`
	Data       []int  `json:"data"`
}

type TerminalExitEvent struct {
	TerminalID string `json:"terminalId"`
	Generation uint64 `json:"generation"`
	PaneID     string `json:"paneId"`
	Code       *int   `json:"code"`
}

func (r *Runtime) openTerminal(runID int64, terminalID, sessionName, paneID string) (TerminalAttachment, error) {
	if strings.TrimSpace(terminalID) == "" || len(terminalID) > 256 {
		return TerminalAttachment{}, errors.New("terminalId is invalid")
	}
	if err := validateTmuxTarget(sessionName); err != nil {
		return TerminalAttachment{}, err
	}
	if err := validatePaneIdentity(paneID); err != nil {
		return TerminalAttachment{}, err
	}
	run, machine, runFound := r.runAndMachine(runID)
	if !runFound {
		return TerminalAttachment{}, fmt.Errorf("Run %d was not found", runID)
	}
	if run.SessionName != sessionName {
		return TerminalAttachment{}, errors.New("Pane is not in the Run's stored session")
	}
	access, opener := r.machineAccess, r.terminalOpen
	if opener == nil {
		opener = func(machine domain.Machine, sessionName, paneID string, output func([]byte), subscription func(string), exit func(*int)) (terminalConnection, error) {
			return openTmuxControlPane(machine, access, sessionName, paneID, nil, output, subscription, exit)
		}
	}
	panes, err := listTerminalPanes(access, machine, sessionName, runID)
	if err != nil {
		return TerminalAttachment{}, err
	}
	paneFound := false
	for _, pane := range panes {
		if pane.PaneID == paneID {
			paneFound = true
			break
		}
	}
	if !paneFound {
		return TerminalAttachment{}, fmt.Errorf("Pane %s was not found in session %s", paneID, sessionName)
	}
	r.terminalMu.Lock()
	r.terminalGenerations[terminalID]++
	generation := r.terminalGenerations[terminalID]
	previous := r.terminalConnections[terminalID]
	delete(r.terminalConnections, terminalID)
	r.terminalMu.Unlock()
	if previous.connection != nil {
		_ = previous.connection.close()
	}
	connection, err := opener(machine, sessionName, paneID,
		func(data []byte) {
			if r.isCurrentTerminalGeneration(terminalID, generation) {
				r.EmitEvent("terminal-output", TerminalOutputEvent{terminalID, generation, paneID, byteList(data)})
			}
		},
		func(value string) {
			if !r.isCurrentTerminalGeneration(terminalID, generation) {
				return
			}
			record, err := parseAgentStateRecord(value)
			if err != nil {
				return
			}
			runID, err := strconv.ParseInt(record.RunID, 10, 64)
			if err != nil || runID != run.ID {
				return
			}
			_, _, _ = r.applyAgentStateRecord(runID, record)
		},
		func(code *int) {
			if r.isCurrentTerminalGeneration(terminalID, generation) {
				r.EmitEvent("terminal-exit", TerminalExitEvent{TerminalID: terminalID, Generation: generation, PaneID: paneID, Code: code})
			}
		})
	if err != nil {
		return TerminalAttachment{}, err
	}
	r.terminalMu.Lock()
	if r.terminalGenerations[terminalID] != generation {
		r.terminalMu.Unlock()
		_ = connection.close()
		return TerminalAttachment{}, errors.New("terminal open request was superseded")
	}
	r.terminalConnections[terminalID] = terminalSession{generation: generation, connection: connection}
	r.terminalMu.Unlock()
	snapshot, err := connection.snapshot()
	if err != nil {
		_ = connection.close()
		r.terminalMu.Lock()
		if current := r.terminalConnections[terminalID]; current.generation == generation {
			delete(r.terminalConnections, terminalID)
		}
		r.terminalMu.Unlock()
		return TerminalAttachment{}, err
	}
	return TerminalAttachment{TerminalID: terminalID, Generation: generation, SessionName: sessionName, PaneID: paneID, Snapshot: byteList(snapshot), Panes: panes}, nil
}

func (r *Runtime) isCurrentTerminalGeneration(terminalID string, generation uint64) bool {
	r.terminalMu.Lock()
	defer r.terminalMu.Unlock()
	return r.terminalGenerations[terminalID] == generation
}

func byteList(data []byte) []int {
	out := make([]int, len(data))
	for i, b := range data {
		out[i] = int(b)
	}
	return out
}

func (r *Runtime) terminalInput(terminalID string, input []int) error {
	connection, err := r.terminalConnection(terminalID)
	if err != nil {
		return err
	}
	bytes := make([]byte, len(input))
	for i, value := range input {
		if value < 0 || value > 255 {
			return fmt.Errorf("terminal input byte %d is outside 0..255", value)
		}
		bytes[i] = byte(value)
	}
	return connection.sendInput(bytes)
}

func (r *Runtime) terminalResize(terminalID string, columns, rows int) error {
	if columns <= 0 || rows <= 0 || columns > 65535 || rows > 65535 {
		return errors.New("Pane dimensions must be positive 16-bit values")
	}
	connection, err := r.terminalConnection(terminalID)
	if err != nil {
		return err
	}
	return connection.resize(uint16(columns), uint16(rows))
}

func (r *Runtime) terminalConnection(terminalID string) (terminalConnection, error) {
	r.terminalMu.Lock()
	defer r.terminalMu.Unlock()
	session, ok := r.terminalConnections[terminalID]
	if !ok {
		return nil, fmt.Errorf("terminal %s is not open", terminalID)
	}
	return session.connection, nil
}

func (r *Runtime) closeTerminal(terminalID string) error {
	r.terminalMu.Lock()
	session, ok := r.terminalConnections[terminalID]
	r.terminalGenerations[terminalID]++
	delete(r.terminalConnections, terminalID)
	r.terminalMu.Unlock()
	if !ok {
		return nil
	}
	return session.connection.close()
}

func listTerminalPanes(access MachineAccess, machine domain.Machine, sessionName string, runID int64) ([]PaneSummary, error) {
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	format := "#{pane_id}\t#{pane_index}\t#{pane_pid}\t#{pane_width}\t#{pane_height}\t#{pane_title}\t#{pane_current_command}\t#{pane_current_path}"
	command := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " list-panes -t " + shellQuote(sessionName) + " -F " + shellQuote(format)
	output, err := access.RunShell(machine, command)
	if err != nil {
		return nil, fmt.Errorf("could not list Panes in session %s: %w", sessionName, err)
	}
	panes := make([]PaneSummary, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 8 {
			return nil, fmt.Errorf("tmux returned an invalid Pane description: %s", line)
		}
		var index, pid, columns, rows uint64
		if _, err := fmt.Sscan(fields[1], &index); err != nil {
			return nil, fmt.Errorf("invalid Pane index: %s", fields[1])
		}
		if _, err := fmt.Sscan(fields[2], &pid); err != nil {
			return nil, fmt.Errorf("invalid Pane process ID: %s", fields[2])
		}
		if _, err := fmt.Sscan(fields[3], &columns); err != nil {
			return nil, fmt.Errorf("invalid Pane columns: %s", fields[3])
		}
		if _, err := fmt.Sscan(fields[4], &rows); err != nil {
			return nil, fmt.Errorf("invalid Pane rows: %s", fields[4])
		}
		label := fields[5]
		if label == "" {
			label = fields[6]
		}
		if label == "" {
			label = fields[0]
		}
		panes = append(panes, PaneSummary{PaneID: fields[0], SessionName: sessionName, RunID: runID, Label: label, Available: true, PaneIndex: uint32(index), PID: uint32(pid), Columns: uint16(columns), Rows: uint16(rows), Title: fields[5], CurrentCommand: fields[6], CurrentPath: fields[7]})
	}
	return panes, nil
}

func (r *Runtime) openExternalTerminal(runID int64) error {
	run, machine, found := r.runAndMachine(runID)
	if !found {
		return fmt.Errorf("Run %d was not found", runID)
	}
	if err := validateTmuxTarget(run.SessionName); err != nil {
		return err
	}
	if err := validatePaneIdentity(run.PaneID); err != nil {
		return err
	}
	remote, err := buildExternalPaneCommand(machine, run)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("osascript"); err != nil {
		return fmt.Errorf("macOS Terminal is unavailable: %w", err)
	}
	script := "tell application \"Terminal\"\nactivate\ndo script " + appleScriptString(remote) + "\nend tell"
	return exec.Command("osascript", "-e", script).Run()
}

func buildExternalPaneCommand(machine domain.Machine, run domain.Run) (string, error) {
	lookup := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " display-message -p -t " + shellQuote(run.PaneID) + " " + shellQuote("#{session_name}")
	attach := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " attach-session -t " + shellQuote(run.PaneID)
	remote := "actual_session=$(" + lookup + " 2>/dev/null); [ \"$actual_session\" = " + shellQuote(run.SessionName) + " ] || { echo 'Pane identity is no longer in the stored session'; exit 1; }; exec " + attach
	if machine.Transport.Kind == domain.TransportLocal {
		return remote, nil
	}
	program, args, err := buildTmuxProcessCommand(machine, true, []string{"sh", "-lc", remote}, "sh")
	if err != nil {
		return "", err
	}
	return strings.Join(append([]string{shellQuote(program)}, shellQuotedArgs(args)...), " "), nil
}

func shellQuotedArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = shellQuote(arg)
	}
	return out
}

func (r *Runtime) runAndMachine(runID int64) (domain.Run, domain.Machine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var run *domain.Run
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == runID {
			value := r.state.Runs[i]
			run = &value
			break
		}
	}
	if run == nil {
		return domain.Run{}, domain.Machine{}, false
	}
	for i := range r.state.Machines {
		if r.state.Machines[i].ID == run.MachineID {
			return *run, r.state.Machines[i], true
		}
	}
	return domain.Run{}, domain.Machine{}, false
}
func appleScriptString(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(value) + "\""
}
