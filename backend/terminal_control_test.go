package backend

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func TestTmuxControlParserRoutesPaneOutputAndSubscriptionUpdates(t *testing.T) {
	var output []byte
	var state string
	parser := newTmuxControlParser("%17", func(data []byte) { output = append(output, data...) }, func(value string) { state = value })
	parser.handleLine([]byte("%output %8 ignored\\015\n"))
	responseChannel := parser.enqueue()
	parser.handleLine([]byte("%begin 12 1 0\n"))
	parser.handleLine([]byte("snapshot block\n"))
	parser.handleLine([]byte("%output %17 hello\\015\\303\\251\\000\\377\n"))
	parser.handleLine([]byte("%subscription-changed mission-manager-agent-state pane 1 1 %17 : {\"status\":\"working\"}\n"))
	parser.handleLine([]byte("%end 12 1 0\n"))
	if !bytes.Equal(output, []byte("hello\r\303\251\000\377")) {
		t.Fatalf("decoded output = %v", output)
	}
	if state != `{"status":"working"}` {
		t.Fatalf("subscription value = %q", state)
	}
	response := <-responseChannel
	if response.err != nil || string(response.body) != "snapshot block\n" {
		t.Fatalf("interleaved events contaminated the command body: %#v", response)
	}
}

func TestTmuxControlParserCompletesResponsesAndErrors(t *testing.T) {
	parser := newTmuxControlParser("%17", nil, nil)
	success := parser.enqueue()
	parser.handleLine([]byte("%begin 123 4 0\n"))
	parser.handleLine([]byte("captured bytes\n"))
	parser.handleLine([]byte("%end 123 4 0\n"))
	response := <-success
	if response.err != nil || string(response.body) != "captured bytes\n" {
		t.Fatalf("success response = %#v", response)
	}

	failure := parser.enqueue()
	parser.handleLine([]byte("%begin 123 5 0\n"))
	parser.handleLine([]byte("can't find pane: %17\n"))
	parser.handleLine([]byte("%error 123 5 0\n"))
	response = <-failure
	if response.err == nil || response.err.Error() != "can't find pane: %17" {
		t.Fatalf("error response = %#v", response)
	}
}

func TestRenderPaneSnapshotRestoresRowsAndCursor(t *testing.T) {
	got := renderPaneSnapshot([]byte("linha1\n  linha2\n\n"), []byte("3 2\n"))
	want := []byte("linha1\r\n  linha2\x1b[0m\x1b[3;4H")
	if !bytes.Equal(got, want) {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

type fakeControlProcess struct {
	inputReader  *io.PipeReader
	inputWriter  *io.PipeWriter
	outputReader *io.PipeReader
	outputWriter *io.PipeWriter
	done         chan struct{}
	commands     []string
	mu           sync.Mutex
}

func newFakeControlProcess() *fakeControlProcess {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	return &fakeControlProcess{inputReader: inputReader, inputWriter: inputWriter, outputReader: outputReader, outputWriter: outputWriter, done: make(chan struct{})}
}
func (p *fakeControlProcess) StdinPipe() (io.WriteCloser, error) { return p.inputWriter, nil }
func (p *fakeControlProcess) StdoutPipe() (io.ReadCloser, error) { return p.outputReader, nil }
func (p *fakeControlProcess) Start() error {
	go func() {
		defer close(p.done)
		defer p.outputWriter.Close()
		reader := bufio.NewReader(p.inputReader)
		id := 0
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				command := strings.TrimSpace(line)
				p.mu.Lock()
				p.commands = append(p.commands, command)
				p.mu.Unlock()
				commands := strings.Split(command, " ; ")
				for _, part := range commands {
					id++
					fmt.Fprintf(p.outputWriter, "%%begin %d %d 0\n", id, id)
					switch {
					case strings.HasPrefix(part, "capture-pane"):
						io.WriteString(p.outputWriter, "row one\n  row two\n")
					case strings.HasPrefix(part, "display-message"):
						io.WriteString(p.outputWriter, "3 2\n")
					}
					fmt.Fprintf(p.outputWriter, "%%end %d %d 0\n", id, id)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return nil
}
func (p *fakeControlProcess) Wait() error { <-p.done; return nil }
func (p *fakeControlProcess) kill() error {
	_ = p.inputWriter.Close()
	_ = p.outputWriter.Close()
	return nil
}

func TestTmuxControlConnectionHandshakesSubscribesAndSendsPaneCommands(t *testing.T) {
	access := NewFakeMachineAccess()
	access.Executables["tmux"] = "/usr/bin/tmux"
	machine := domain.Machine{ID: 3, Name: "Local", SocketName: "mission", Transport: domain.MachineTransport{Kind: domain.TransportLocal}}
	process := newFakeControlProcess()
	var output []byte
	pane, err := openTmuxControlPane(machine, access, "session", "%17", process, func(data []byte) { output = append(output, data...) }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pane.sendInput([]byte{0, 65, 255}); err != nil {
		t.Fatal(err)
	}
	if err := pane.resize(100, 35); err != nil {
		t.Fatal(err)
	}
	snapshot, err := pane.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot := []byte("row one\r\n  row two\x1b[0m\x1b[3;4H")
	if !bytes.Equal(snapshot, wantSnapshot) {
		t.Fatalf("snapshot=%q want=%q", snapshot, wantSnapshot)
	}
	process.mu.Lock()
	commands := append([]string(nil), process.commands...)
	process.mu.Unlock()
	want := []string{"list-panes", "refresh-client -B 'mission-manager-agent-state:%17:#{@ai_mission_manager_run_state}'", "send-keys -t %17 -H 0x00 0x41 0xff", "refresh-client -C 100,35", "capture-pane -p -e -t %17 ; display-message -p -t %17 '#{cursor_x} #{cursor_y}'"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands=%q want=%q", commands, want)
	}
	_ = pane.close()
	select {
	case <-process.done:
	case <-time.After(time.Second):
		t.Fatal("control process did not stop")
	}
}
