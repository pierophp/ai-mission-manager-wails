package backend

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type controlResponse struct {
	body []byte
	err  error
}

type controlCommand struct {
	response chan controlResponse
}

// tmuxControlParser demultiplexes tmux's line-oriented control mode stream.
// Pane output and subscriptions can arrive between command response blocks.
type tmuxControlParser struct {
	paneID         string
	onOutput       func([]byte)
	onSubscription func(string)
	mu             sync.Mutex
	pending        []*controlCommand
	current        *controlCommand
	header         []byte
	body           []byte
}

func newTmuxControlParser(paneID string, onOutput func([]byte), onSubscription func(string)) *tmuxControlParser {
	return &tmuxControlParser{paneID: paneID, onOutput: onOutput, onSubscription: onSubscription}
}

func (p *tmuxControlParser) enqueue() <-chan controlResponse {
	command := &controlCommand{response: make(chan controlResponse, 1)}
	p.mu.Lock()
	p.pending = append(p.pending, command)
	p.mu.Unlock()
	return command.response
}

func (p *tmuxControlParser) handleLine(line []byte) {
	trimmed := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
	p.mu.Lock()
	if p.current == nil {
		if header, ok := bytes.CutPrefix(trimmed, []byte("%begin ")); ok {
			p.header = append(p.header[:0], header...)
			if len(p.pending) > 0 {
				p.current = p.pending[0]
				p.pending = p.pending[1:]
			}
			p.body = p.body[:0]
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		p.handleEvent(trimmed)
		return
	}
	if header, ok := bytes.CutPrefix(trimmed, []byte("%end ")); ok && bytes.Equal(header, p.header) {
		command := p.current
		body := bytes.Clone(p.body)
		p.current = nil
		p.header = p.header[:0]
		p.body = p.body[:0]
		p.mu.Unlock()
		if command != nil {
			command.response <- controlResponse{body: body}
		}
		return
	}
	if header, ok := bytes.CutPrefix(trimmed, []byte("%error ")); ok && bytes.Equal(header, p.header) {
		command := p.current
		detail := string(bytes.TrimSpace(p.body))
		if detail == "" {
			detail = "tmux control command failed"
		}
		p.current = nil
		p.header = p.header[:0]
		p.body = p.body[:0]
		p.mu.Unlock()
		if command != nil {
			command.response <- controlResponse{err: errors.New(detail)}
		}
		return
	}
	// tmux control notifications are asynchronous and may be interleaved with
	// the response body for a command. They are protocol records, not body text.
	if bytes.HasPrefix(trimmed, []byte("%output ")) || bytes.HasPrefix(trimmed, []byte("%subscription-changed ")) {
		p.mu.Unlock()
		p.handleEvent(trimmed)
		return
	}
	p.body = append(p.body, line...)
	p.mu.Unlock()
}

func (p *tmuxControlParser) handleEvent(line []byte) {
	if rest, ok := bytes.CutPrefix(line, []byte("%output ")); ok {
		pane, encoded, ok := bytes.Cut(rest, []byte(" "))
		if !ok || string(pane) != p.paneID {
			return
		}
		if p.onOutput != nil {
			p.onOutput(decodeTmuxControlOutput(encoded))
		}
		return
	}
	rest, ok := bytes.CutPrefix(line, []byte("%subscription-changed "))
	if !ok {
		return
	}
	header, value, ok := bytes.Cut(rest, []byte(" : "))
	if !ok {
		return
	}
	fields := bytes.Fields(header)
	if len(fields) < 5 || string(fields[0]) != "mission-manager-agent-state" || string(fields[4]) != p.paneID {
		return
	}
	if p.onSubscription != nil {
		p.onSubscription(string(bytes.TrimSpace(value)))
	}
}

func (p *tmuxControlParser) finish(err error) {
	if err == nil {
		err = errors.New("tmux control client closed")
	}
	p.mu.Lock()
	commands := append([]*controlCommand(nil), p.pending...)
	p.pending = nil
	if p.current != nil {
		commands = append(commands, p.current)
		p.current = nil
	}
	p.mu.Unlock()
	for _, command := range commands {
		command.response <- controlResponse{err: err}
	}
}

func decodeTmuxControlOutput(encoded []byte) []byte {
	decoded := make([]byte, 0, len(encoded))
	for i := 0; i < len(encoded); {
		if i+3 < len(encoded) && encoded[i] == '\\' && encoded[i+1] >= '0' && encoded[i+1] <= '7' && encoded[i+2] >= '0' && encoded[i+2] <= '7' && encoded[i+3] >= '0' && encoded[i+3] <= '7' {
			decoded = append(decoded, (encoded[i+1]-'0')*64+(encoded[i+2]-'0')*8+encoded[i+3]-'0')
			i += 4
		} else {
			decoded = append(decoded, encoded[i])
			i++
		}
	}
	return decoded
}

func renderPaneSnapshot(screen, cursor []byte) []byte {
	screen = bytes.TrimSuffix(screen, []byte("\n"))
	rows := bytes.Split(screen, []byte("\n"))
	for len(rows) > 1 && len(rows[len(rows)-1]) == 0 {
		rows = rows[:len(rows)-1]
	}
	result := bytes.Join(rows, []byte("\r\n"))
	result = append(result, []byte("\x1b[0m")...)
	var column, row uint16
	if _, err := fmt.Fscanf(bytes.NewReader(cursor), "%d %d", &column, &row); err == nil {
		result = append(result, []byte(fmt.Sprintf("\x1b[%d;%dH", row+1, column+1))...)
	}
	return result
}

type terminalProcess interface {
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
	kill() error
}

type commandTerminalProcess struct{ *exec.Cmd }

func (p commandTerminalProcess) kill() error {
	if p.Process == nil {
		return errors.New("terminal process has not started")
	}
	return p.Process.Kill()
}

type tmuxControlPane struct {
	stdin     io.WriteCloser
	process   terminalProcess
	parser    *tmuxControlParser
	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
	closed    chan struct{}
	paneID    string
}

func openTmuxControlPane(machine domain.Machine, access MachineAccess, sessionName, paneID string, process terminalProcess, onOutput func([]byte), onSubscription func(string), onExit func(*int)) (*tmuxControlPane, error) {
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	if err := validateTmuxTarget(sessionName); err != nil {
		return nil, err
	}
	if err := validatePaneIdentity(paneID); err != nil {
		return nil, err
	}
	tmuxPath, err := access.FindExecutable(machine, "tmux")
	if err != nil {
		return nil, err
	}
	args := []string{"-C", "-f", "/dev/null", "-L", machine.SocketName, "attach-session", "-t", sessionName}
	program, arguments, err := buildTmuxProcessCommand(machine, false, args, tmuxPath)
	if err != nil {
		return nil, err
	}
	if process == nil {
		process = commandTerminalProcess{exec.Command(program, arguments...)}
	}
	stdin, err := process.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := process.Start(); err != nil {
		return nil, fmt.Errorf("could not attach to Pane on Machine %s: %w", machine.Name, err)
	}
	pane := &tmuxControlPane{stdin: stdin, process: process, done: make(chan struct{}), closed: make(chan struct{}), paneID: paneID}
	pane.parser = newTmuxControlParser(paneID, onOutput, onSubscription)
	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, readErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				pane.parser.handleLine(line)
			}
			if readErr != nil {
				break
			}
		}
		pane.parser.finish(errors.New("tmux control client closed"))
		waitErr := process.Wait()
		close(pane.done)
		if onExit != nil {
			var code *int
			if waitErr == nil {
				status := 0
				code = &status
			} else if exitErr, ok := waitErr.(*exec.ExitError); ok && exitErr.ExitCode() >= 0 {
				status := exitErr.ExitCode()
				code = &status
			}
			onExit(code)
		}
	}()
	_, err = pane.request("list-panes", 2*time.Second)
	if err != nil {
		_ = pane.close()
		return nil, fmt.Errorf("tmux control client did not become ready: %w", err)
	}
	if _, err := pane.request(fmt.Sprintf("refresh-client -B 'mission-manager-agent-state:%s:#{@ai_mission_manager_run_state}'", paneID), 2*time.Second); err != nil {
		_ = pane.close()
		return nil, fmt.Errorf("could not subscribe to Run state: %w", err)
	}
	return pane, nil
}

func (p *tmuxControlPane) request(command string, timeout time.Duration) ([]byte, error) {
	response := p.parser.enqueue()
	p.writeMu.Lock()
	select {
	case <-p.closed:
		p.writeMu.Unlock()
		return nil, errors.New("tmux control client is closed")
	default:
	}
	_, writeErr := io.WriteString(p.stdin, command+"\n")
	p.writeMu.Unlock()
	if writeErr != nil {
		return nil, fmt.Errorf("could not send command to Pane: %w", writeErr)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-response:
		return result.body, result.err
	case <-timer.C:
		_ = p.close()
		return nil, fmt.Errorf("timed out waiting for tmux command: %s", command)
	case <-p.done:
		return nil, errors.New("tmux control client closed during a command")
	}
}

func (p *tmuxControlPane) requestPair(first, second string) ([]byte, []byte, error) {
	firstResponse, secondResponse := p.parser.enqueue(), p.parser.enqueue()
	p.writeMu.Lock()
	select {
	case <-p.closed:
		p.writeMu.Unlock()
		return nil, nil, errors.New("tmux control client is closed")
	default:
	}
	_, writeErr := io.WriteString(p.stdin, first+" ; "+second+"\n")
	p.writeMu.Unlock()
	if writeErr != nil {
		return nil, nil, writeErr
	}
	wait := func(response <-chan controlResponse) ([]byte, error) {
		select {
		case result := <-response:
			return result.body, result.err
		case <-time.After(15 * time.Second):
			_ = p.close()
			return nil, errors.New("timed out capturing Pane snapshot")
		case <-p.done:
			return nil, errors.New("tmux control client closed during snapshot")
		}
	}
	one, err := wait(firstResponse)
	if err != nil {
		return nil, nil, err
	}
	two, err := wait(secondResponse)
	return one, two, err
}

func (p *tmuxControlPane) sendInput(input []byte) error {
	if len(input) == 0 {
		return nil
	}
	parts := make([]string, len(input))
	for i, value := range input {
		parts[i] = fmt.Sprintf("0x%02x", value)
	}
	_, err := p.request("send-keys -t "+p.paneID+" -H "+strings.Join(parts, " "), 2*time.Second)
	return err
}

func (p *tmuxControlPane) resize(columns, rows uint16) error {
	if columns == 0 || rows == 0 {
		return errors.New("Pane dimensions must be positive")
	}
	_, err := p.request(fmt.Sprintf("refresh-client -C %d,%d", columns, rows), 2*time.Second)
	return err
}

func (p *tmuxControlPane) snapshot() ([]byte, error) {
	screen, cursor, err := p.requestPair("capture-pane -p -e -t "+p.paneID, "display-message -p -t "+p.paneID+" '#{cursor_x} #{cursor_y}'")
	if err != nil {
		return nil, err
	}
	return renderPaneSnapshot(screen, cursor), nil
}

func (p *tmuxControlPane) close() error {
	var result error
	p.closeOnce.Do(func() {
		close(p.closed)
		_ = p.stdin.Close()
		select {
		case <-p.done:
		case <-time.After(500 * time.Millisecond):
			result = p.process.kill()
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
			}
		}
	})
	return result
}

func validatePaneIdentity(value string) error {
	if len(value) < 2 || value[0] != '%' {
		return fmt.Errorf("invalid tmux Pane identity: %s", value)
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return fmt.Errorf("invalid tmux Pane identity: %s", value)
		}
	}
	return nil
}

func validateTmuxTarget(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("tmux target cannot be blank")
	}
	if strings.ContainsAny(value, "\n\r\x00") {
		return errors.New("tmux target contains unsupported characters")
	}
	return nil
}
