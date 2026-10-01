package backend

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type fakeEmbeddedTerminal struct {
	onOutput      func([]byte)
	onExit        func(*int)
	input         []byte
	columns, rows uint16
	closed        bool
	snapshotBytes []byte
}

func (f *fakeEmbeddedTerminal) sendInput(input []byte) error {
	f.input = append([]byte(nil), input...)
	return nil
}
func (f *fakeEmbeddedTerminal) resize(columns, rows uint16) error {
	f.columns, f.rows = columns, rows
	return nil
}
func (f *fakeEmbeddedTerminal) snapshot() ([]byte, error) {
	return append([]byte(nil), f.snapshotBytes...), nil
}
func (f *fakeEmbeddedTerminal) close() error { f.closed = true; return nil }

func TestTerminalCommandsOpenLiveRunAndPublishByteListEvents(t *testing.T) {
	access := NewFakeMachineAccess()
	machine := domain.Machine{ID: 4, Name: "Local", SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	run := domain.Run{ID: 23, MachineID: machine.ID, SessionName: "run-23", PaneID: "%17"}
	state := domain.DomainState{Machines: []domain.Machine{machine}, Runs: []domain.Run{run}}
	access.ShellResults["tmux -f /dev/null -L 'mission' list-panes -t 'run-23' -F '#{pane_id}\t#{pane_index}\t#{pane_pid}\t#{pane_width}\t#{pane_height}\t#{pane_title}\t#{pane_current_command}\t#{pane_current_path}'"] = MachineAccessResult{Value: "%17\t0\t345\t90\t30\tAgent\tbash\t/tmp/work\n"}
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "terminal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.mu.Lock()
	runtime.state = state
	runtime.machineAccess = access
	runtime.mu.Unlock()
	var emitted []struct {
		name    string
		payload any
	}
	runtime.SetEventEmitter(EventEmitterFunc(func(name string, payload any) {
		emitted = append(emitted, struct {
			name    string
			payload any
		}{name, payload})
	}))
	var opened []*fakeEmbeddedTerminal
	runtime.mu.Lock()
	runtime.terminalOpen = func(_ domain.Machine, _, _ string, output func([]byte), _ func(string), exit func(*int)) (terminalConnection, error) {
		connection := &fakeEmbeddedTerminal{onOutput: output, onExit: exit, snapshotBytes: []byte("prompt\x1b[0m\x1b[1;1H")}
		opened = append(opened, connection)
		return connection, nil
	}
	runtime.mu.Unlock()
	service := CommandService{Runtime: runtime}
	openedJSON, err := service.Invoke("open_terminal", `{"runId":23,"terminalId":"t-23","sessionName":"run-23","paneId":"%17"}`)
	if err != nil {
		t.Fatal(err)
	}
	var attachment TerminalAttachment
	if err := json.Unmarshal(openedJSON, &attachment); err != nil {
		t.Fatal(err)
	}
	if attachment.TerminalID != "t-23" || attachment.Generation != 1 || attachment.PaneID != "%17" || !reflect.DeepEqual(attachment.Snapshot, []int{112, 114, 111, 109, 112, 116, 27, 91, 48, 109, 27, 91, 49, 59, 49, 72}) {
		t.Fatalf("attachment = %#v", attachment)
	}
	if len(attachment.Panes) != 1 || attachment.Panes[0].Label != "Agent" || attachment.Panes[0].RunID != run.ID {
		t.Fatalf("Panes = %#v", attachment.Panes)
	}
	opened[0].onOutput([]byte{0, 255})
	if len(emitted) != 1 || emitted[0].name != "terminal-output" {
		t.Fatalf("events = %#v", emitted)
	}
	var event TerminalOutputEvent
	if err := json.Unmarshal([]byte(mustJSON(t, emitted[0].payload)), &event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event.Data, []int{0, 255}) || event.Generation != 1 || event.PaneID != "%17" {
		t.Fatalf("output event = %#v", event)
	}
	if _, err := service.Invoke("terminal_input", `{"terminalId":"t-23","input":[0,65,255]}`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened[0].input, []byte{0, 65, 255}) {
		t.Fatalf("input = %v", opened[0].input)
	}
	if _, err := service.Invoke("terminal_resize", `{"terminalId":"t-23","columns":100,"rows":35}`); err != nil {
		t.Fatal(err)
	}
	if opened[0].columns != 100 || opened[0].rows != 35 {
		t.Fatalf("size = %dx%d", opened[0].columns, opened[0].rows)
	}
	if _, err := service.Invoke("close_terminal", `{"terminalId":"t-23"}`); err != nil {
		t.Fatal(err)
	}
	if !opened[0].closed {
		t.Fatal("close_terminal did not close the connection")
	}
}

func TestTerminalGenerationDropsEventsFromSupersededConnection(t *testing.T) {
	access := NewFakeMachineAccess()
	machine := domain.Machine{ID: 1, Name: "Local", SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	run := domain.Run{ID: 1, MachineID: 1, SessionName: "session", PaneID: "%1"}
	access.ShellResults["tmux -f /dev/null -L 'mission' list-panes -t 'session' -F '#{pane_id}\t#{pane_index}\t#{pane_pid}\t#{pane_width}\t#{pane_height}\t#{pane_title}\t#{pane_current_command}\t#{pane_current_path}'"] = MachineAccessResult{Value: "%1\t0\t1\t80\t24\t\tbash\t/tmp\n"}
	runtime := newRuntime(nil, nil, domain.DomainState{Machines: []domain.Machine{machine}, Runs: []domain.Run{run}}, access)
	var outputs []TerminalOutputEvent
	runtime.SetEventEmitter(EventEmitterFunc(func(name string, payload any) {
		if name == "terminal-output" {
			var event TerminalOutputEvent
			_ = json.Unmarshal([]byte(mustJSON(t, payload)), &event)
			outputs = append(outputs, event)
		}
	}))
	var connections []*fakeEmbeddedTerminal
	runtime.mu.Lock()
	runtime.terminalOpen = func(_ domain.Machine, _, _ string, output func([]byte), _ func(string), exit func(*int)) (terminalConnection, error) {
		c := &fakeEmbeddedTerminal{onOutput: output, onExit: exit, snapshotBytes: []byte("s")}
		connections = append(connections, c)
		return c, nil
	}
	runtime.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := runtime.openTerminal(1, "same", "session", "%1"); err != nil {
			t.Fatal(err)
		}
	}
	connections[0].onOutput([]byte("stale"))
	connections[1].onOutput([]byte("current"))
	if len(outputs) != 1 || outputs[0].Generation != 2 || !reflect.DeepEqual(outputs[0].Data, []int{99, 117, 114, 114, 101, 110, 116}) {
		t.Fatalf("outputs = %#v", outputs)
	}
	if err := runtime.closeTerminal("same"); err != nil {
		t.Fatal(err)
	}
	connections[1].onOutput([]byte("after-close"))
	if len(outputs) != 1 {
		t.Fatalf("close should invalidate callbacks, got %#v", outputs)
	}
	if !connections[0].closed {
		t.Fatal("reopening terminal did not close its earlier connection")
	}
}

func TestOpenTerminalStreamsInputAndResizeAgainstIsolatedTmuxServer(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	socket := "ai-mm-issue16-" + strings.ReplaceAll(filepath.Base(t.TempDir()), "_", "-")
	session := "live-run"
	server := func(args ...string) *exec.Cmd {
		return exec.Command(tmux, append([]string{"-f", "/dev/null", "-L", socket}, args...)...)
	}
	if output, err := server("new-session", "-d", "-s", session, "sh").CombinedOutput(); err != nil {
		t.Fatalf("start isolated tmux session: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = server("kill-server").Run() })
	database := filepath.Join(t.TempDir(), "mission-manager.sqlite")
	seed, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO machines(id,context_id,name,socket_name,transport_json) VALUES(9,1,'Local',?,'{"kind":"local"}')`, socket)
	if err == nil {
		_, err = db.Exec(`INSERT INTO items(id,human_identifier,title,project_id,status) VALUES(44,'IT-44','Terminal smoke',1,'Active')`)
	}
	if err == nil {
		_, err = db.Exec(`INSERT INTO runs(id,item_id,machine_id,agent,execution_profile,prompt,working_directory,session_name,pane_id,started_at,state,pane_status) VALUES(44,44,9,'codex','implement','Terminal smoke','/tmp',?,'%0',1,'working','available')`, session)
	}
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	outputEvents := make(chan TerminalOutputEvent, 16)
	runtime, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.SetEventEmitter(EventEmitterFunc(func(name string, payload any) {
		if name == "terminal-output" {
			var event TerminalOutputEvent
			raw, _ := json.Marshal(payload)
			_ = json.Unmarshal(raw, &event)
			outputEvents <- event
		}
	}))
	service := CommandService{Runtime: runtime}
	opened, err := service.Invoke("open_terminal", `{"runId":44,"terminalId":"terminal-live","sessionName":"`+session+`","paneId":"%0"}`)
	if err != nil {
		t.Fatal(err)
	}
	var attachment TerminalAttachment
	if err := json.Unmarshal(opened, &attachment); err != nil {
		t.Fatal(err)
	}
	if attachment.Generation != 1 || attachment.PaneID != "%0" || len(attachment.Snapshot) == 0 {
		t.Fatalf("attachment = %#v", attachment)
	}
	if _, err := service.Invoke("terminal_resize", `{"terminalId":"terminal-live","columns":100,"rows":35}`); err != nil {
		t.Fatal(err)
	}
	if output, err := server("display-message", "-p", "-t", "%0", "#{pane_width} #{pane_height}").CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "100 35" {
		t.Fatalf("tmux resize = %q, err=%v", output, err)
	}
	if _, err := service.Invoke("terminal_input", `{"terminalId":"terminal-live","input":[101,99,104,111,32,105,115,115,117,101,49,54,45,111,107,10]}`); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Second)
	var streamed strings.Builder
	for !strings.Contains(streamed.String(), "issue16-ok") {
		select {
		case event := <-outputEvents:
			if event.TerminalID != "terminal-live" || event.Generation != 1 {
				continue
			}
			for _, value := range event.Data {
				streamed.WriteByte(byte(value))
			}
		case <-deadline:
			t.Fatalf("did not receive Pane output; received %q", streamed.String())
		}
	}
	if _, err := service.Invoke("close_terminal", `{"terminalId":"terminal-live"}`); err != nil {
		t.Fatal(err)
	}
}

func TestExternalPaneCommandUsesTheStoredPaneAndSSHTransport(t *testing.T) {
	run := domain.Run{SessionName: "stored-session", PaneID: "%17"}
	local := domain.Machine{SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	localCommand, err := buildExternalPaneCommand(local, run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(localCommand, "display-message -p -t '%17'") || !strings.Contains(localCommand, "attach-session -t '%17'") || !strings.Contains(localCommand, "actual_session") {
		t.Fatalf("local exact-Pane command = %q", localCommand)
	}
	host, user := "build.example", "runner"
	port := uint16(2222)
	identity := "~/.ssh/id key"
	remote := domain.Machine{SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportSSH, Host: &host, User: &user, Port: &port, IdentityFile: &identity}}
	remoteCommand, err := buildExternalPaneCommand(remote, run)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ssh", "'-tt'", "'-p' '2222'", "'-i'", "id key", "runner@build.example", "actual_session", "attach-session -t", "%17"} {
		if !strings.Contains(remoteCommand, expected) {
			t.Errorf("remote command %q omitted %q", remoteCommand, expected)
		}
	}
	unsafeHost := "build.example; touch /tmp/no"
	remote.Transport.Host = &unsafeHost
	if _, err := buildExternalPaneCommand(remote, run); err == nil {
		t.Fatal("unsafe SSH target should be rejected")
	}
}
