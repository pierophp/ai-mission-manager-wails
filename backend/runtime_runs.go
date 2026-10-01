package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

const agentStatePaneOption = "@ai_mission_manager_run_state"

type AgentStateRecord struct {
	Agent     domain.AgentKind `json:"agent"`
	RunID     string           `json:"runId"`
	State     domain.RunState  `json:"state"`
	UpdatedAt string           `json:"updatedAt"`
	Sequence  *int64           `json:"sequence,omitempty"`
}

type RunStateChangedEvent struct {
	RunID int64           `json:"runId"`
	State domain.RunState `json:"state"`
}

type RunDeletionResult struct {
	RunID int64 `json:"runId"`
}
type RunReconciliationFailure struct {
	MachineID   int64  `json:"machineId"`
	MachineName string `json:"machineName"`
	Kind        string `json:"kind"`
	Message     string `json:"message"`
}
type RunReconciliationResult struct {
	Failures []RunReconciliationFailure `json:"failures"`
	Changed  bool                       `json:"changed"`
}

type RunSuggestion struct {
	MachineID      int64            `json:"machineId"`
	MachineName    string           `json:"machineName"`
	Agent          domain.AgentKind `json:"agent"`
	SessionName    string           `json:"sessionName"`
	PaneID         string           `json:"paneId"`
	CurrentPath    string           `json:"currentPath"`
	ItemID         int64            `json:"itemId"`
	ItemIdentifier string           `json:"itemIdentifier"`
	ItemTitle      string           `json:"itemTitle"`
	ContextID      int64            `json:"contextId"`
	ContextName    string           `json:"contextName"`
	WorkspaceID    *int64           `json:"workspaceId"`
	RepositoryID   *int64           `json:"repositoryId"`
	WorktreeID     *int64           `json:"worktreeId"`
	LocationPath   *string          `json:"locationPath"`
}

func parseAgentStateRecord(raw string) (AgentStateRecord, error) {
	var record AgentStateRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &record); err != nil {
		return record, err
	}
	if record.Agent != domain.AgentClaude && record.Agent != domain.AgentCodex {
		return record, errors.New("agent state has an unsupported agent")
	}
	if record.State != domain.RunWorking && record.State != domain.RunBlocked && record.State != domain.RunFinished {
		return record, errors.New("agent state has an unsupported state")
	}
	if strings.TrimSpace(record.UpdatedAt) == "" {
		return record, errors.New("agent state has no update time")
	}
	if id, err := strconv.ParseInt(record.RunID, 10, 64); err != nil || id < 1 {
		return record, errors.New("agent state has an invalid Run ID")
	}
	if record.Sequence != nil && *record.Sequence < 0 {
		return record, errors.New("agent state has a negative sequence")
	}
	return record, nil
}

func stateSequenceAccepts(run domain.Run, record AgentStateRecord) bool {
	if id, err := strconv.ParseInt(record.RunID, 10, 64); err != nil || id != run.ID || record.Agent != run.Agent {
		return false
	}
	if record.Sequence == nil {
		return run.LastAppliedAgentStateSequence == nil
	}
	return run.LastAppliedAgentStateSequence == nil || *record.Sequence > *run.LastAppliedAgentStateSequence
}

func (r *Runtime) applyAgentStateRecord(runID int64, record AgentStateRecord) (bool, bool, error) {
	return r.applyAgentStateObservation(runID, record, nil)
}

func (r *Runtime) applyAgentStateObservation(runID int64, record AgentStateRecord, generation *uint64) (bool, bool, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	if generation != nil && r.runObservationGeneration[runID] != *generation {
		r.mu.Unlock()
		return false, false, nil
	}
	index := -1
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == runID {
			index = i
			break
		}
	}
	if index < 0 || !stateSequenceAccepts(r.state.Runs[index], record) {
		r.mu.Unlock()
		return false, false, nil
	}
	if generation == nil {
		r.runObservationGeneration[runID]++
	}
	previous := r.state.Runs[index]
	next := previous
	next.State = record.State
	if record.Sequence != nil {
		sequence := *record.Sequence
		next.LastAppliedAgentStateSequence = &sequence
	}
	stateChanged := previous.State != next.State
	if stateChanged {
		applyObservedRunState(&next, record.State)
	}
	r.mu.Unlock()
	if r.store == nil {
		return false, false, errors.New("runtime is not configured")
	}
	actions := []persistence.AuditAction{}
	if stateChanged {
		raw, _ := json.Marshal(map[string]any{"action": "runStateChanged", "run_id": runID, "from": previous.State, "to": next.State})
		actions = append(actions, persistence.AuditAction(raw))
	}
	if err := r.store.Apply([]persistence.Effect{{SQL: `UPDATE runs SET state=?,last_applied_agent_state_sequence=?,grill_phase=?,plan_phase=? WHERE id=?`, Args: []any{next.State, next.LastAppliedAgentStateSequence, next.GrillPhase, next.PlanPhase, runID}}}, actions); err != nil {
		return false, false, err
	}
	r.mu.Lock()
	r.state.Runs[index] = next
	r.mu.Unlock()
	if stateChanged {
		r.EmitEvent("run-state-changed", RunStateChangedEvent{RunID: runID, State: next.State})
	}
	return true, stateChanged || previous.LastAppliedAgentStateSequence != next.LastAppliedAgentStateSequence, nil
}

func applyObservedRunState(run *domain.Run, state domain.RunState) {
	if run.ExecutionProfile == "plan" {
		if run.PlanPhase == nil || *run.PlanPhase != "executing" {
			if state == domain.RunFinished {
				phase := domain.PlanPhase("awaiting_go")
				run.PlanPhase = &phase
			} else {
				run.PlanPhase = nil
			}
		}
	}
	if run.ExecutionProfile != "grill" {
		return
	}
	phase := domain.GrillPhase("starting")
	switch state {
	case domain.RunWorking:
		phase = "working"
	case domain.RunBlocked:
		phase = "waiting_for_answers"
	case domain.RunFinished:
		phase = "awaiting_next_action"
		if run.GrillQuestionGroup != nil && run.GrillResponse == nil {
			phase = "waiting_for_answers"
		}
	}
	run.GrillPhase = &phase
}

func (r *Runtime) runCopy(runID int64) domain.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.state.Runs {
		if run.ID == runID {
			return run
		}
	}
	return domain.Run{}
}

func (r *Runtime) runMachine(run domain.Run) (domain.Machine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, machine := range r.state.Machines {
		if machine.ID == run.MachineID {
			return machine, true
		}
	}
	return domain.Machine{}, false
}

func readMachineRunState(access MachineAccess, machine domain.Machine, runID int64) (*AgentStateRecord, error) {
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	home, err := access.HomeDirectory(machine)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".local", "state", "ai-mission-manager", "runs", fmt.Sprintf("run-%d.json", runID))
	contents, err := access.RunShell(machine, "cat -- "+shellQuote(path)+" 2>/dev/null || true")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(contents) == "" {
		return nil, nil
	}
	record, err := parseAgentStateRecord(contents)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func paneStateRecord(raw string) *AgentStateRecord {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return nil
	}
	record, err := parseAgentStateRecord(raw)
	if err != nil {
		return nil
	}
	return &record
}

type runPaneObservation struct {
	Session string
	Pane    string
	Path    string
	Command string
	Title   string
	State   *AgentStateRecord
}

func (r *Runtime) recoverLegacyRunStates() error {
	if r == nil || r.store == nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(home, ".ai-mission-manager", "agent-state")
	r.mu.Lock()
	runs := append([]domain.Run(nil), r.state.Runs...)
	r.mu.Unlock()
	for _, run := range runs {
		path := filepath.Join(directory, fmt.Sprintf("run-%d.json", run.ID))
		contents, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		record, err := parseAgentStateRecord(string(contents))
		if err != nil {
			continue
		}
		if record.Agent != run.Agent || record.RunID != strconv.FormatInt(run.ID, 10) {
			continue
		}
		if _, _, err := r.applyAgentStateRecord(run.ID, record); err != nil {
			return fmt.Errorf("recover legacy state for Run %d: %w", run.ID, err)
		}
	}
	return nil
}

func readObservedRunPanes(access MachineAccess, machine domain.Machine) ([]runPaneObservation, error) {
	if access == nil {
		access = LocalSSHMachineAccess{}
	}
	command := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " list-panes -a -F " + shellQuote("#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}\t#{@ai_mission_manager_run_state}")
	output, err := access.RunShell(machine, command)
	if err != nil {
		return nil, err
	}
	panes := []runPaneObservation{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 6)
		if len(fields) < 5 {
			continue
		}
		pane := runPaneObservation{Session: fields[0], Pane: fields[1], Command: fields[2], Title: fields[3], Path: fields[4]}
		if len(fields) == 6 {
			pane.State = paneStateRecord(fields[5])
		}
		panes = append(panes, pane)
	}
	return panes, nil
}

func (r *Runtime) reconcileRuns() (RunReconciliationResult, error) {
	result := RunReconciliationResult{Failures: []RunReconciliationFailure{}}
	if r == nil || r.store == nil {
		return result, errors.New("runtime is not configured")
	}
	r.mu.Lock()
	if r.runReconciliationInProgress {
		r.mu.Unlock()
		return result, nil
	}
	r.runReconciliationInProgress = true
	runs := append([]domain.Run(nil), r.state.Runs...)
	machines := append([]domain.Machine(nil), r.state.Machines...)
	for _, run := range runs {
		r.runObservationGeneration[run.ID]++
	}
	generations := make(map[int64]uint64, len(runs))
	for _, run := range runs {
		generations[run.ID] = r.runObservationGeneration[run.ID]
	}
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.runReconciliationInProgress = false; r.mu.Unlock() }()
	panesByMachine := map[int64][]runPaneObservation{}
	for _, machine := range machines {
		panes, err := readObservedRunPanes(r.machineAccess, machine)
		if err != nil {
			result.Failures = append(result.Failures, RunReconciliationFailure{MachineID: machine.ID, MachineName: machine.Name, Kind: "unreachable", Message: err.Error()})
			continue
		}
		panesByMachine[machine.ID] = panes
	}
	for _, run := range runs {
		machine, found := findMachineIn(machines, run.MachineID)
		if !found {
			continue
		}
		panes, observed := panesByMachine[machine.ID]
		if !observed {
			continue
		}
		var pane *runPaneObservation
		for i := range panes {
			if panes[i].Session == run.SessionName && panes[i].Pane == run.PaneID {
				pane = &panes[i]
				break
			}
		}
		paneStatus := domain.PaneMissing
		hasPaneStatus := true
		if pane != nil {
			paneStatus = domain.PaneAvailable
		} else {
			for i := range panes {
				if panes[i].Pane == run.PaneID {
					hasPaneStatus = false
					break
				}
			}
		}
		r.mu.Lock()
		generationMatches := generations[run.ID] == r.runObservationGeneration[run.ID]
		r.mu.Unlock()
		if current := r.runCopy(run.ID); !generationMatches || current.ID != run.ID {
			continue
		}
		if hasPaneStatus && run.PaneStatus != paneStatus {
			if err := r.persistRunPaneStatus(run, paneStatus); err != nil {
				return result, err
			}
			result.Changed = true
		}
		var fileRecord *AgentStateRecord
		fileRecord, _ = readMachineRunState(r.machineAccess, machine, run.ID)
		var paneRecord *AgentStateRecord
		if pane != nil {
			paneRecord = pane.State
		}
		record := newerStateRecord(paneRecord, fileRecord)
		if record != nil {
			generation := generations[run.ID]
			accepted, changed, err := r.applyAgentStateObservation(run.ID, *record, &generation)
			if err != nil {
				return result, err
			}
			result.Changed = result.Changed || (accepted && changed)
		}
		if pane != nil && run.Workflow == domain.WorkflowPstack {
			transcript, err := r.machineAccess.RunShell(machine, "tmux -f /dev/null -L "+shellQuote(machine.SocketName)+" capture-pane -p -J -S - -t "+shellQuote(run.PaneID))
			if err == nil {
				transcriptChanged, err := r.applyRunTranscript(run, transcript)
				if err != nil {
					return result, err
				}
				result.Changed = result.Changed || transcriptChanged
			}
		}
	}
	return result, nil
}

func findMachineIn(machines []domain.Machine, id int64) (domain.Machine, bool) {
	for _, m := range machines {
		if m.ID == id {
			return m, true
		}
	}
	return domain.Machine{}, false
}
func newerStateRecord(pane, file *AgentStateRecord) *AgentStateRecord {
	if pane == nil {
		return file
	}
	if file == nil {
		return pane
	}
	if file.Sequence != nil && (pane.Sequence == nil || *file.Sequence > *pane.Sequence) {
		return file
	}
	return pane
}

func (r *Runtime) persistRunPaneStatus(previous domain.Run, status domain.RunPaneStatus) error {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	current := r.runCopy(previous.ID)
	if current.ID == 0 {
		return fmt.Errorf("Run %d does not exist", previous.ID)
	}
	if current.PaneStatus == status {
		return nil
	}
	previous = current
	raw, _ := json.Marshal(map[string]any{"action": "runPaneStatusChanged", "run_id": previous.ID, "from": previous.PaneStatus, "to": status})
	if err := r.store.Apply([]persistence.Effect{{SQL: `UPDATE runs SET pane_status=? WHERE id=?`, Args: []any{status, previous.ID}}}, []persistence.AuditAction{persistence.AuditAction(raw)}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == previous.ID {
			r.state.Runs[i].PaneStatus = status
			break
		}
	}
	return nil
}

func (r *Runtime) applyRunTranscript(previous domain.Run, transcript string) (bool, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	current := r.runCopy(previous.ID)
	if current.ID == 0 {
		return false, nil
	}
	previous = current
	prs := append([]string(nil), previous.ReportedPullRequests...)
	attention := previous.AttentionSummary
	planPath := previous.PlanPath
	for _, line := range strings.Split(transcript, "\n") {
		payload, ok := strings.CutPrefix(strings.TrimSpace(line), "AI_MISSION_MANAGER_EVENT ")
		if !ok {
			continue
		}
		var report struct {
			Event   string `json:"event"`
			URL     string `json:"url"`
			Summary string `json:"summary"`
			Path    string `json:"path"`
		}
		if json.Unmarshal([]byte(payload), &report) != nil {
			continue
		}
		switch report.Event {
		case "pull_request.opened":
			if validReportedValue(report.URL) {
				object, err := domain.ClassifyExternalURL(strings.TrimSpace(report.URL))
				if err == nil && object.Kind == domain.ObjectPullRequest && !containsString(prs, object.CanonicalURL) {
					prs = append(prs, object.CanonicalURL)
				}
			}
		case "attention.final":
			if validReportedValue(report.Summary) {
				value := strings.TrimSpace(report.Summary)
				attention = &value
			}
		case "plan.ready":
			if previous.ExecutionProfile == "plan" && validReportedValue(report.Path) {
				value := strings.TrimSpace(report.Path)
				planPath = &value
			}
		}
	}
	if strings.Join(prs, "\n") == strings.Join(previous.ReportedPullRequests, "\n") && equalStringPointer(attention, previous.AttentionSummary) && equalStringPointer(planPath, previous.PlanPath) {
		return false, nil
	}
	prsJSON, _ := json.Marshal(prs)
	if err := r.store.Apply([]persistence.Effect{{SQL: `UPDATE runs SET reported_pull_requests_json=?,attention_summary=?,plan_path=? WHERE id=?`, Args: []any{string(prsJSON), attention, planPath, previous.ID}}}, nil); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == previous.ID {
			r.state.Runs[i].ReportedPullRequests = prs
			r.state.Runs[i].AttentionSummary = attention
			r.state.Runs[i].PlanPath = planPath
			break
		}
	}
	return true, nil
}
func validReportedValue(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && !strings.HasPrefix(v, "<")
}
func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
func equalStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (r *Runtime) stopRun(runID int64) (domain.Run, error) {
	run := r.runCopy(runID)
	if run.ID == 0 {
		return run, fmt.Errorf("Run %d does not exist", runID)
	}
	machine, ok := r.runMachine(run)
	if !ok {
		return run, fmt.Errorf("Machine %d does not exist", run.MachineID)
	}
	panes, err := readObservedRunPanes(r.machineAccess, machine)
	if err != nil {
		return run, fmt.Errorf("Could not inspect Run %d: %w", runID, err)
	}
	found := false
	for _, p := range panes {
		if p.Session == run.SessionName && p.Pane == run.PaneID {
			found = true
			break
		}
	}
	if found {
		command := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " kill-pane -t " + shellQuote(run.PaneID)
		if _, err := r.machineAccess.RunShell(machine, command); err != nil {
			return run, fmt.Errorf("Could not stop Run %d: %w", runID, err)
		}
	}
	if err := r.persistRunPaneStatus(run, domain.PaneMissing); err != nil {
		return run, err
	}
	run.PaneStatus = domain.PaneMissing
	if raw, e := json.Marshal(map[string]any{"action": "runStopped", "run_id": runID}); e == nil {
		_ = r.store.Apply(nil, []persistence.AuditAction{persistence.AuditAction(raw)})
	}
	return run, nil
}

func (r *Runtime) finishRun(runID int64) (domain.Run, error) {
	run := r.runCopy(runID)
	if run.ID == 0 {
		return run, fmt.Errorf("Run %d does not exist", runID)
	}
	if err := r.setRunState(runID, domain.RunFinished, true); err != nil {
		return run, err
	}
	return r.runCopy(runID), nil
}

func (r *Runtime) setRunState(runID int64, state domain.RunState, manual bool) error {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	index := -1
	var previous domain.Run
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == runID {
			index = i
			previous = r.state.Runs[i]
			break
		}
	}
	if index < 0 {
		r.mu.Unlock()
		return fmt.Errorf("Run %d does not exist", runID)
	}
	next := previous
	next.State = state
	applyObservedRunState(&next, state)
	if manual && state == domain.RunFinished && previous.ExecutionProfile == "grill" {
		phase := domain.GrillPhase("finished")
		next.GrillPhase = &phase
	}
	r.mu.Unlock()
	var audit []persistence.AuditAction
	if manual && state == domain.RunFinished && previous.State != state {
		raw, _ := json.Marshal(map[string]any{"action": "runFinished", "run_id": runID})
		audit = append(audit, persistence.AuditAction(raw))
	}
	if err := r.store.Apply([]persistence.Effect{{SQL: `UPDATE runs SET state=?,grill_phase=?,plan_phase=? WHERE id=?`, Args: []any{next.State, next.GrillPhase, next.PlanPhase, runID}}}, audit); err != nil {
		return err
	}
	r.mu.Lock()
	r.state.Runs[index] = next
	r.runObservationGeneration[runID]++
	r.mu.Unlock()
	if previous.State != state {
		r.EmitEvent("run-state-changed", RunStateChangedEvent{RunID: runID, State: state})
	}
	return nil
}

func (r *Runtime) deleteRun(runID int64, confirmed bool) (RunDeletionResult, error) {
	if !confirmed {
		return RunDeletionResult{}, errors.New("Run deletion requires explicit confirmation")
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	found := false
	for _, run := range r.state.Runs {
		if run.ID == runID {
			found = true
			break
		}
	}
	r.mu.Unlock()
	if !found {
		return RunDeletionResult{}, fmt.Errorf("Run %d does not exist", runID)
	}
	raw, _ := json.Marshal(map[string]any{"action": "runDeleted", "run_id": runID})
	if err := r.store.Apply([]persistence.Effect{{SQL: `DELETE FROM runs WHERE id=?`, Args: []any{runID}}}, []persistence.AuditAction{persistence.AuditAction(raw)}); err != nil {
		return RunDeletionResult{}, err
	}
	r.mu.Lock()
	kept := r.state.Runs[:0]
	for _, run := range r.state.Runs {
		if run.ID != runID {
			kept = append(kept, run)
		}
	}
	r.state.Runs = kept
	delete(r.runObservationGeneration, runID)
	r.mu.Unlock()
	return RunDeletionResult{RunID: runID}, nil
}

func (r *Runtime) listRunSuggestions() ([]RunSuggestion, error) {
	r.mu.Lock()
	snapshot := r.state
	machines := append([]domain.Machine(nil), r.state.Machines...)
	r.mu.Unlock()
	suggestions := []RunSuggestion{}
	for _, machine := range machines {
		selected := false
		for _, ctx := range snapshot.Contexts {
			if ctx.ID == machine.ContextID && ctx.ExecutionMachineID != nil && *ctx.ExecutionMachineID == machine.ID {
				selected = true
				break
			}
		}
		if !selected {
			continue
		}
		panes, err := readObservedRunPanes(r.machineAccess, machine)
		if err != nil {
			continue
		}
		home, _ := r.machineAccess.HomeDirectory(machine)
		for _, pane := range panes {
			agent := paneAgentKind(pane.Command, pane.Title)
			if agent == "" {
				continue
			}
			if runAlreadyUsesPane(snapshot.Runs, machine.ID, pane.Session, pane.Pane) {
				continue
			}
			suggestion := suggestRunForPane(snapshot, machine, pane, agent, home)
			if suggestion != nil {
				suggestions = append(suggestions, *suggestion)
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !reflect.DeepEqual(r.state, snapshot) {
		return nil, errors.New("Run or Machine state changed while agent Panes were inspected; refresh suggestions again")
	}
	return suggestions, nil
}

func paneAgentKind(values ...string) domain.AgentKind {
	for _, value := range values {
		command := strings.Fields(value)
		if len(command) == 0 {
			continue
		}
		name := strings.ToLower(filepath.Base(command[0]))
		switch name {
		case "claude", "claude-code":
			return domain.AgentClaude
		case "codex":
			return domain.AgentCodex
		}
	}
	return ""
}

func runAlreadyUsesPane(runs []domain.Run, machine int64, session, pane string) bool {
	for _, r := range runs {
		if r.MachineID == machine && r.SessionName == session && r.PaneID == pane {
			return true
		}
	}
	return false
}

func suggestRunForPane(state domain.DomainState, machine domain.Machine, pane runPaneObservation, agent domain.AgentKind, home string) *RunSuggestion {
	type candidate struct {
		workspace    domain.Workspace
		repositoryID int64
		worktreeID   *int64
		path         string
		length       int
	}
	var best *candidate
	for _, workspace := range state.Workspaces {
		for _, worktree := range state.Worktrees {
			if worktree.WorkspaceID == workspace.ID && worktree.MachineID == machine.ID && isPathWithin(worktree.Path, pane.Path, home) {
				id := worktree.ID
				c := candidate{workspace, worktree.RepositoryID, &id, worktree.Path, len(worktree.Path)}
				if best == nil || c.length > best.length {
					best = &c
				}
			}
		}
		if best != nil && best.workspace.ID == workspace.ID {
			continue
		}
		item := findItem(state, workspace.ItemID)
		if item == nil {
			continue
		}
		projectItems := 0
		for _, i := range state.Items {
			if i.ProjectID == item.ProjectID {
				projectItems++
			}
		}
		for _, repo := range state.Repositories {
			if repo.ProjectID != item.ProjectID {
				continue
			}
			for _, location := range state.RepositoryLocations {
				if location.RepositoryID == repo.ID && location.MachineID == machine.ID && projectItems == 1 && isPathWithin(location.CheckoutPath, pane.Path, home) {
					c := candidate{workspace, repo.ID, nil, location.CheckoutPath, len(location.CheckoutPath)}
					if best == nil || c.length > best.length {
						best = &c
					}
				}
			}
		}
	}
	if best == nil {
		return nil
	}
	item := findItem(state, best.workspace.ItemID)
	if item == nil {
		return nil
	}
	var project *domain.Project
	for i := range state.Projects {
		if state.Projects[i].ID == item.ProjectID {
			project = &state.Projects[i]
			break
		}
	}
	if project == nil {
		return nil
	}
	var ctx *domain.Context
	for i := range state.Contexts {
		if state.Contexts[i].ID == project.ContextID {
			ctx = &state.Contexts[i]
			break
		}
	}
	if ctx == nil {
		return nil
	}
	return &RunSuggestion{MachineID: machine.ID, MachineName: machine.Name, Agent: agent, SessionName: pane.Session, PaneID: pane.Pane, CurrentPath: pane.Path, ItemID: item.ID, ItemIdentifier: item.HumanIdentifier, ItemTitle: item.Title, ContextID: ctx.ID, ContextName: ctx.Name, WorkspaceID: int64Ptr(best.workspace.ID), RepositoryID: int64Ptr(best.repositoryID), WorktreeID: best.worktreeID, LocationPath: stringPtr(best.path)}
}
func findItem(s domain.DomainState, id int64) *domain.Item {
	for i := range s.Items {
		if s.Items[i].ID == id {
			return &s.Items[i]
		}
	}
	return nil
}
func isPathWithin(root, path, home string) bool {
	expand := func(v string) string {
		v = strings.TrimRight(v, "/")
		if v == "~" {
			return strings.TrimRight(home, "/")
		}
		if strings.HasPrefix(v, "~/") {
			return strings.TrimRight(home, "/") + strings.TrimPrefix(v, "~")
		}
		return v
	}
	root = expand(root)
	path = expand(path)
	return root != "" && (path == root || strings.HasPrefix(path, root+"/"))
}

func (r *Runtime) operateUntrackedAgent(suggestion RunSuggestion, deletePane bool) error {
	machine, ok := findMachineIn(r.domainSnapshot().Machines, suggestion.MachineID)
	if !ok {
		return fmt.Errorf("Machine %d does not exist", suggestion.MachineID)
	}
	panes, err := readObservedRunPanes(r.machineAccess, machine)
	if err != nil {
		return err
	}
	valid := false
	for _, current := range panes {
		if current.Session == suggestion.SessionName && current.Pane == suggestion.PaneID && current.Path == suggestion.CurrentPath {
			valid = true
			break
		}
	}
	if !valid {
		return errors.New("The suggested agent no longer matches its registered working location; refresh suggestions")
	}
	command := "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " send-keys -t " + shellQuote(suggestion.PaneID) + " C-c"
	if deletePane {
		command = "tmux -f /dev/null -L " + shellQuote(machine.SocketName) + " kill-pane -t " + shellQuote(suggestion.PaneID)
	}
	_, err = r.machineAccess.RunShell(machine, command)
	return err
}

func (r *Runtime) attachRun(s RunSuggestion) (domain.Run, error) {
	current, err := r.listRunSuggestions()
	if err != nil {
		return domain.Run{}, err
	}
	valid := false
	for _, candidate := range current {
		if sameRunSuggestion(candidate, s) {
			valid = true
			break
		}
	}
	if !valid {
		return domain.Run{}, errors.New("The suggested agent no longer matches its registered working location; refresh suggestions")
	}
	if s.WorkspaceID == nil || s.RepositoryID == nil || s.LocationPath == nil {
		return domain.Run{}, errors.New("The suggested Item execution location has no Repository or Workspace")
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	snapshot := r.state
	id := snapshot.NextRunID
	if id < 1 {
		id = 1
	}
	run := domain.Run{ID: id, ItemID: s.ItemID, WorkspaceID: s.WorkspaceID, RepositoryID: s.RepositoryID, WorktreeID: s.WorktreeID, MachineID: s.MachineID, Agent: s.Agent, ExecutionProfile: "custom", Workflow: domain.WorkflowMattPocock, Prompt: "Attached existing agent", WorkingDirectory: *s.LocationPath, SessionName: s.SessionName, PaneID: s.PaneID, StartedAt: time.Now().Unix(), State: domain.RunUnknown, PaneStatus: domain.PaneAvailable, DirectCheckouts: []domain.RunCheckout{}, ReportedPullRequests: []string{}, GrillAnswers: []domain.GrillAnswer{}, GrillDecisions: []domain.GrillAnswer{}}
	for _, existing := range snapshot.Runs {
		if existing.MachineID == run.MachineID && existing.SessionName == run.SessionName && existing.PaneID == run.PaneID {
			r.mu.Unlock()
			return domain.Run{}, errors.New("Pane is already attached to a Run")
		}
	}
	r.mu.Unlock()
	values := []any{run.ID, run.ItemID, run.WorkspaceID, run.RepositoryID, run.WorktreeID, run.MachineID, run.Agent, run.ExecutionProfile, run.Model, run.Effort, run.SkillSnapshot, run.Prompt, run.WorkingDirectory, run.SessionName, run.PaneID, run.StartedAt, run.State, run.PaneStatus, "[]", "", nil, "[]", "[]", nil, nil, nil, nil, nil, nil, string(run.Workflow), "[]", nil, nil, nil}
	raw, _ := json.Marshal(map[string]any{"action": "runCreated", "run_id": run.ID})
	sql := `INSERT INTO runs(id,item_id,workspace_id,repository_id,worktree_id,machine_id,agent,execution_profile,model,effort,skill_snapshot,prompt,working_directory,session_name,pane_id,started_at,state,pane_status,direct_checkouts_json,transcript,grill_question_group_json,grill_answers_json,grill_decisions_json,grill_response,grill_phase,grill_action,last_applied_agent_state_sequence,grill_action_started_at,cli_configuration_profile_json,workflow,reported_pull_requests_json,attention_summary,plan_phase,plan_path) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if err := r.store.Apply([]persistence.Effect{{SQL: sql, Args: values}, {SQL: `UPDATE metadata SET value=? WHERE key='next_run_id'`, Args: []any{id + 1}}}, []persistence.AuditAction{persistence.AuditAction(raw)}); err != nil {
		return domain.Run{}, err
	}
	r.mu.Lock()
	r.state.Runs = append(r.state.Runs, run)
	r.state.NextRunID = id + 1
	r.mu.Unlock()
	return run, nil
}

func sameRunSuggestion(a, b RunSuggestion) bool {
	return a.MachineID == b.MachineID && a.MachineName == b.MachineName && a.Agent == b.Agent && a.SessionName == b.SessionName && a.PaneID == b.PaneID && a.CurrentPath == b.CurrentPath && a.ItemID == b.ItemID && a.ItemIdentifier == b.ItemIdentifier && a.ItemTitle == b.ItemTitle && a.ContextID == b.ContextID && a.ContextName == b.ContextName && equalInt64Pointer(a.WorkspaceID, b.WorkspaceID) && equalInt64Pointer(a.RepositoryID, b.RepositoryID) && equalInt64Pointer(a.WorktreeID, b.WorktreeID) && equalStringPointer(a.LocationPath, b.LocationPath)
}
func equalInt64Pointer(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
