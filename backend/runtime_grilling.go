package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func (r *Runtime) composeGrillPrompt(itemID int64, configuration domain.GrillConfiguration, language domain.GrillLanguage, initialPrompt string) (string, error) {
	state, err := r.stateSnapshot()
	if err != nil {
		return "", err
	}
	return domain.ComposeGrillPrompt(state, itemID, configuration, language, initialPrompt)
}

func (r *Runtime) prepareGrillRun(itemID, workspaceID int64, machineID *int64) (DirectRunPreview, error) {
	return r.prepareDirectRun(itemID, workspaceID, machineID)
}

func (r *Runtime) grillOperationLock(runID int64) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.grillOperations == nil {
		r.grillOperations = map[int64]*sync.Mutex{}
	}
	lock := r.grillOperations[runID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.grillOperations[runID] = lock
	}
	return lock
}

func (r *Runtime) setGrillPaneTerminal(terminal grillPaneTerminal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.grillTerminal = terminal
}

func (r *Runtime) submitGrillAnswers(runID int64, answers []domain.GrillAnswer) (domain.Run, error) {
	lock := r.grillOperationLock(runID)
	lock.Lock()
	defer lock.Unlock()
	run := r.runCopy(runID)
	if run.ID == 0 {
		return domain.Run{}, fmt.Errorf("Run %d does not exist", runID)
	}
	if run.ExecutionProfile != domain.ExecutionProfileGrill {
		return domain.Run{}, fmt.Errorf("Run %d is not a Grill Run", runID)
	}
	canSubmit := run.State == domain.RunBlocked || run.State == domain.RunFinished && run.GrillPhase != nil && *run.GrillPhase == "waiting_for_answers"
	if !canSubmit {
		return domain.Run{}, fmt.Errorf("Run %d is not waiting for Grill answers", runID)
	}
	if run.GrillResponse != nil {
		return domain.Run{}, fmt.Errorf("Grill response for Run %d was already submitted", runID)
	}
	if run.GrillQuestionGroup == nil || len(answers) != len(run.GrillQuestionGroup.Questions) {
		return domain.Run{}, errors.New("answers must include every question in the current Grill group")
	}
	seen := map[uint32]bool{}
	for _, answer := range answers {
		found := false
		for _, question := range run.GrillQuestionGroup.Questions {
			if question.Number == answer.QuestionNumber {
				found = true
				break
			}
		}
		if !found {
			return domain.Run{}, fmt.Errorf("unknown Grill question %d", answer.QuestionNumber)
		}
		if seen[answer.QuestionNumber] {
			return domain.Run{}, fmt.Errorf("duplicate Grill answer for question %d", answer.QuestionNumber)
		}
		seen[answer.QuestionNumber] = true
	}
	response, err := domain.FormatGrillResponse(answers)
	if err != nil {
		return domain.Run{}, err
	}
	run.GrillAnswers = append([]domain.GrillAnswer(nil), answers...)
	run.GrillDecisions = append(append([]domain.GrillAnswer(nil), run.GrillDecisions...), answers...)
	if err := r.persistGrillRun(run, runID); err != nil {
		return domain.Run{}, err
	}
	machine, found := r.runMachine(run)
	if !found {
		return domain.Run{}, fmt.Errorf("Machine %d does not exist", run.MachineID)
	}
	if err := r.sendGrillText(machine, run.PaneID, response); err != nil {
		return domain.Run{}, fmt.Errorf("could not send Grill answers to Run %d: %w", runID, err)
	}
	latest := r.runCopy(runID)
	value := response
	latest.GrillResponse = &value
	phase := domain.GrillPhase("working")
	latest.GrillPhase = &phase
	latest.State = domain.RunWorking
	if err := r.persistGrillRun(latest, runID); err != nil {
		return domain.Run{}, fmt.Errorf("Grill answers reached Run %d, but the response state could not be persisted: %w", runID, err)
	}
	r.EmitEvent("run-state-changed", RunStateChangedEvent{RunID: runID, State: domain.RunWorking})
	return domain.CanonicalRunForView(latest), nil
}

func (r *Runtime) continueGrill(runID int64, action domain.GrillContinuationAction) (domain.Run, error) {
	lock := r.grillOperationLock(runID)
	lock.Lock()
	defer lock.Unlock()
	state, err := r.stateSnapshot()
	if err != nil {
		return domain.Run{}, err
	}
	run := domain.Run{}
	found := false
	for _, candidate := range state.Runs {
		if candidate.ID == runID {
			run = candidate
			found = true
			break
		}
	}
	if !found {
		return domain.Run{}, fmt.Errorf("Run %d does not exist", runID)
	}
	if run.ExecutionProfile != domain.ExecutionProfileGrill || !grillContinuationAvailable(run.GrillPhase, run.GrillAction, action) || run.PaneStatus != domain.PaneAvailable {
		return domain.Run{}, fmt.Errorf("Grill continuation %q is not available for Run %d", action, runID)
	}
	prompt, err := domain.ComposeGrillContinuationPrompt(state, runID, action)
	if err != nil {
		return domain.Run{}, err
	}
	machine, found := r.runMachine(run)
	if !found {
		return domain.Run{}, fmt.Errorf("Machine %d does not exist", run.MachineID)
	}
	if err := r.sendGrillText(machine, run.PaneID, prompt); err != nil {
		return domain.Run{}, fmt.Errorf("could not send Grill continuation to Run %d: %w", runID, err)
	}
	latest := r.runCopy(runID)
	phase := domain.GrillPhase("working")
	latest.GrillPhase = &phase
	latest.State = domain.RunWorking
	latest.GrillQuestionGroup = nil
	latest.GrillAnswers = []domain.GrillAnswer{}
	latest.GrillResponse = nil
	latest.GrillAction = &action
	started := time.Now().Unix()
	latest.GrillActionStartedAt = &started
	if err := r.persistGrillRun(latest, runID); err != nil {
		return domain.Run{}, fmt.Errorf("Grill continuation reached Run %d, but its state could not be persisted: %w", runID, err)
	}
	r.EmitEvent("run-state-changed", RunStateChangedEvent{RunID: runID, State: domain.RunWorking})
	return domain.CanonicalRunForView(latest), nil
}

func (r *Runtime) goPlan(runID int64) (domain.Run, error) {
	lock := r.grillOperationLock(runID)
	lock.Lock()
	defer lock.Unlock()
	run := r.runCopy(runID)
	if run.ID == 0 {
		return domain.Run{}, fmt.Errorf("Run %d does not exist", runID)
	}
	if run.PaneStatus != domain.PaneAvailable {
		return domain.Run{}, fmt.Errorf("Plan Go is unavailable because Run %d has no Pane", runID)
	}
	prompt, err := domain.ComposePlanGoPrompt(run)
	if err != nil {
		return domain.Run{}, err
	}
	machine, found := r.runMachine(run)
	if !found {
		return domain.Run{}, fmt.Errorf("Machine %d does not exist", run.MachineID)
	}
	if err := r.sendGrillText(machine, run.PaneID, prompt); err != nil {
		return domain.Run{}, fmt.Errorf("could not send Go to Plan Run %d: %w", runID, err)
	}
	latest := r.runCopy(runID)
	phase := domain.PlanPhase("executing")
	latest.PlanPhase = &phase
	latest.State = domain.RunWorking
	if err := r.persistGrillRun(latest, runID); err != nil {
		return domain.Run{}, fmt.Errorf("Go reached Run %d, but its state could not be persisted: %w", runID, err)
	}
	r.EmitEvent("run-state-changed", RunStateChangedEvent{RunID: runID, State: domain.RunWorking})
	return domain.CanonicalRunForView(latest), nil
}

func (r *Runtime) planRevealPath(runID int64) (string, error) {
	run := r.runCopy(runID)
	if run.ID == 0 {
		return "", fmt.Errorf("Run %d does not exist", runID)
	}
	if run.ExecutionProfile != domain.ExecutionProfilePlan || run.Workflow != domain.WorkflowPstack || run.PlanPath == nil || strings.TrimSpace(*run.PlanPath) == "" {
		return "", fmt.Errorf("Run %d has no reported plan path", runID)
	}
	machine, ok := r.runMachine(run)
	if !ok {
		return "", fmt.Errorf("Machine %d does not exist", run.MachineID)
	}
	if machine.Transport.Kind != domain.TransportLocal {
		return "", fmt.Errorf("Run %d is on a remote Machine", runID)
	}
	path := strings.TrimSpace(*run.PlanPath)
	if !filepath.IsAbs(path) {
		path = filepath.Join(run.WorkingDirectory, path)
	}
	return filepath.Clean(path), nil
}

func grillContinuationAvailable(phase *domain.GrillPhase, previous *domain.GrillContinuationAction, action domain.GrillContinuationAction) bool {
	if action != "to-spec" && action != "to-tickets" && action != "implement" {
		return false
	}
	if phase == nil {
		return false
	}
	if *phase == "awaiting_next_action" {
		return !(action == "to-spec" && previous != nil && *previous == action)
	}
	if *phase != "waiting_for_answers" {
		return false
	}
	last := ""
	if previous != nil {
		last = string(*previous)
	}
	return last == "" && action == "to-spec" || last == "to-spec" && action == "to-tickets" || last == "to-tickets" && action == "implement"
}

func (r *Runtime) sendGrillText(machine domain.Machine, paneID, text string) error {
	r.mu.Lock()
	terminal := r.grillTerminal
	access := r.machineAccess
	r.mu.Unlock()
	if terminal == nil {
		terminal = tmuxGrillPaneTerminal{access: access}
	}
	if err := terminal.SetBuffer(machine, "ai-mission-manager-input-"+strings.TrimLeft(paneID, "%"), text); err != nil {
		return err
	}
	if err := terminal.PasteBuffer(machine, "ai-mission-manager-input-"+strings.TrimLeft(paneID, "%"), paneID); err != nil {
		return err
	}
	return terminal.SendEnter(machine, paneID)
}

func (r *Runtime) persistGrillRun(run domain.Run, runID int64) error {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	index := -1
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == runID {
			index = i
			break
		}
	}
	if index < 0 {
		r.mu.Unlock()
		return fmt.Errorf("Run %d does not exist", runID)
	}
	if r.store == nil {
		r.mu.Unlock()
		return errors.New("runtime is not configured")
	}
	r.mu.Unlock()
	question, err := optionalGrillQuestionJSON(run.GrillQuestionGroup)
	if err != nil {
		return err
	}
	answers, err := json.Marshal(run.GrillAnswers)
	if err != nil {
		return err
	}
	decisions, err := json.Marshal(run.GrillDecisions)
	if err != nil {
		return err
	}
	effect := persistence.Effect{SQL: `UPDATE runs SET state=?,transcript=?,grill_question_group_json=?,grill_answers_json=?,grill_decisions_json=?,grill_response=?,grill_phase=?,grill_action=?,grill_action_started_at=?,plan_phase=?,plan_path=? WHERE id=?`, Args: []any{run.State, run.Transcript, question, string(answers), string(decisions), run.GrillResponse, run.GrillPhase, run.GrillAction, run.GrillActionStartedAt, run.PlanPhase, run.PlanPath, runID}}
	if err := r.store.Apply([]persistence.Effect{effect}, nil); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == runID {
			r.state.Runs[i] = run
			return nil
		}
	}
	return fmt.Errorf("Run %d disappeared after persistence", runID)
}

func optionalGrillQuestionJSON(value *domain.GrillQuestionGroup) (any, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}
