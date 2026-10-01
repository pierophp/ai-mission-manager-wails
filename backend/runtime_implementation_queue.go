package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/gitcli"
)

func (r *Runtime) checkImplementationQueue(queueID int64) (domain.ImplementationQueue, error) {
	r.implementationQueueMu.Lock()
	defer r.implementationQueueMu.Unlock()
	if _, err := r.transition(domain.Event{Kind: "implementation_queue_action", QueueID: queueID, QueueAction: "check"}); err != nil {
		return domain.ImplementationQueue{}, err
	}
	if err := r.advanceImplementationQueueLocked(queueID, 0); err != nil {
		return domain.ImplementationQueue{}, err
	}
	return r.queueCopy(queueID), nil
}

func (r *Runtime) skipImplementationQueueEntry(queueID int64) (domain.ImplementationQueue, error) {
	r.implementationQueueMu.Lock()
	defer r.implementationQueueMu.Unlock()
	queue := r.queueCopy(queueID)
	if queue.ID == 0 || !queue.Active {
		return domain.ImplementationQueue{}, fmt.Errorf("active Implementation Queue %d does not exist", queueID)
	}
	if entry := domain.CurrentImplementationQueueEntry(&queue); entry != nil && entry.RunID != nil {
		run := r.runCopy(*entry.RunID)
		if run.ID == 0 {
			return queue, fmt.Errorf("Run %d referenced by the active queue does not exist; cannot skip safely", *entry.RunID)
		}
		if run.PaneStatus != domain.PaneMissing {
			machine, ok := r.runMachine(run)
			if !ok {
				return queue, fmt.Errorf("Machine %d does not exist; cannot skip Run %d safely", run.MachineID, run.ID)
			}
			r.mu.Lock()
			executor := r.runExecutor
			r.mu.Unlock()
			if executor == nil {
				return queue, errors.New("Run executor is not configured; cannot skip the ticket safely")
			}
			if err := executor.Kill(machine, run.SessionName); err != nil {
				return queue, fmt.Errorf("could not close skipped Run %d: %w", run.ID, err)
			}
			if err := r.persistRunPaneStatus(run, domain.PaneMissing); err != nil {
				return queue, err
			}
		}
		if run.State != domain.RunFinished {
			if err := r.setRunStateWithoutQueue(run.ID, domain.RunFinished, true); err != nil {
				return queue, err
			}
		}
	}
	if _, err := r.transition(domain.Event{Kind: "implementation_queue_action", QueueID: queueID, QueueAction: "skip"}); err != nil {
		return domain.ImplementationQueue{}, err
	}
	if err := r.advanceImplementationQueueLocked(queueID, 0); err != nil {
		return domain.ImplementationQueue{}, err
	}
	return r.queueCopy(queueID), nil
}

func (r *Runtime) cancelImplementationQueue(queueID int64) (domain.ImplementationQueue, error) {
	r.implementationQueueMu.Lock()
	defer r.implementationQueueMu.Unlock()
	if _, err := r.transition(domain.Event{Kind: "implementation_queue_action", QueueID: queueID, QueueAction: "cancel"}); err != nil {
		return domain.ImplementationQueue{}, err
	}
	return r.queueCopy(queueID), nil
}

func (r *Runtime) advanceImplementationQueue(queueID, changedRunID int64) error {
	r.implementationQueueMu.Lock()
	defer r.implementationQueueMu.Unlock()
	return r.advanceImplementationQueueLocked(queueID, changedRunID)
}

func (r *Runtime) advanceQueuesForRun(runID int64) error {
	state, err := r.stateSnapshot()
	if err != nil {
		return err
	}
	ids := []int64{}
	for _, queue := range state.ImplementationQueues {
		if !queue.Active {
			continue
		}
		entry := domain.CurrentImplementationQueueEntry(&queue)
		if entry != nil && entry.RunID != nil && *entry.RunID == runID {
			ids = append(ids, queue.ID)
		}
	}
	for _, id := range ids {
		if err := r.advanceImplementationQueue(id, runID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) advanceImplementationQueueLocked(queueID, changedRunID int64) error {
	queue := r.queueCopy(queueID)
	if queue.ID == 0 || !queue.Active {
		return nil
	}
	entry := domain.CurrentImplementationQueueEntry(&queue)
	if entry == nil {
		queue.Active = false
		queue.PausedReason = nil
		return r.updateImplementationQueue(queue)
	}
	if changedRunID > 0 && (entry.RunID == nil || *entry.RunID != changedRunID) {
		return nil
	}
	if entry.RunID == nil {
		return r.launchNextQueueEntry(queue, entry)
	}
	run := r.runCopy(*entry.RunID)
	if run.ID == 0 {
		return r.pauseImplementationQueue(queue, "pane_missing", "")
	}
	if run.State != domain.RunFinished {
		if run.PaneStatus == domain.PaneMissing {
			return r.pauseImplementationQueue(queue, "run_stopped", "")
		}
		return nil
	}
	state, err := r.stateSnapshot()
	if err != nil {
		return err
	}
	_, context, err := itemAndContext(state, queue.ItemID)
	if err != nil {
		return r.pauseImplementationQueue(queue, "launch_failed", err.Error())
	}
	status, err := r.queueTicketState(state, queue.ItemID, context, entry.TicketURL)
	if err != nil {
		return r.pauseImplementationQueue(queue, "launch_failed", err.Error())
	}
	entry.TicketState = status
	if domain.ImplementationTicketIsOpen(status) {
		return r.pauseImplementationQueue(queue, "ticket_still_open", "")
	}
	if context.CheckDirtyCheckouts {
		machine, found := r.runMachine(run)
		if !found {
			return r.pauseImplementationQueue(queue, "launch_failed", fmt.Sprintf("Machine %d does not exist", run.MachineID))
		}
		for _, checkout := range run.DirectCheckouts {
			observed, inspectErr := gitcli.New(r.machineAccess).Inspect(machine, checkout.Path)
			if inspectErr != nil {
				return r.pauseImplementationQueue(queue, "launch_failed", inspectErr.Error())
			}
			if observed.Dirty {
				return r.pauseImplementationQueue(queue, "checkout_dirty", "")
			}
		}
	}
	machine, found := r.runMachine(run)
	if !found {
		return r.pauseImplementationQueue(queue, "launch_failed", fmt.Sprintf("Machine %d does not exist", run.MachineID))
	}
	r.mu.Lock()
	executor := r.runExecutor
	r.mu.Unlock()
	if executor == nil {
		return r.pauseImplementationQueue(queue, "launch_failed", "Run executor is not configured")
	}
	if run.PaneStatus != domain.PaneMissing {
		if err := executor.Kill(machine, run.SessionName); err != nil {
			return r.pauseImplementationQueue(queue, "launch_failed", err.Error())
		}
		if err := r.persistRunPaneStatus(run, domain.PaneMissing); err != nil {
			return err
		}
	}
	entry.Done = true
	queue.PausedReason = nil
	if domain.CurrentImplementationQueueEntry(&queue) == nil {
		queue.Active = false
		return r.updateImplementationQueue(queue)
	}
	if err := r.updateImplementationQueue(queue); err != nil {
		return err
	}
	return r.launchNextQueueEntry(queue, domain.CurrentImplementationQueueEntry(&queue))
}

func (r *Runtime) queueTicketState(state domain.DomainState, itemID int64, context domain.Context, url string) (string, error) {
	if r.implementationQueueTicketState != nil {
		return r.implementationQueueTicketState(state, context, url)
	}
	object, err := r.classifyExternalForItem(state, itemID, url, context)
	if err != nil {
		return "", fmt.Errorf("could not identify ticket %q: %w", url, err)
	}
	if strings.HasPrefix(object.Key, "local:") {
		path, err := r.localMarkdownPath(state, context, object.Key)
		if err != nil {
			return "", err
		}
		snapshot, err := readLocalMarkdownSnapshot(path, currentUnixSeconds())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(snapshot.State), nil
	}
	snapshot, err := r.fetchSnapshotForObject(state, context, object)
	if err != nil {
		return "", err
	}
	if snapshot == nil || strings.TrimSpace(snapshot.State) == "" {
		return "", errors.New("ticket provider returned no state")
	}
	return strings.TrimSpace(snapshot.State), nil
}

func (r *Runtime) launchNextQueueEntry(queue domain.ImplementationQueue, entry *domain.ImplementationQueueEntry) error {
	if entry == nil {
		queue.Active = false
		queue.PausedReason = nil
		return r.updateImplementationQueue(queue)
	}
	preview, err := r.prepareDirectRun(queue.ItemID, queue.WorkspaceID, nil)
	if err != nil {
		return r.pauseImplementationQueue(queue, "launch_failed", err.Error())
	}
	workflow := queue.Workflow
	if workflow != domain.WorkflowPstack {
		workflow = domain.WorkflowMattPocock
	}
	profile := domain.ExecutionProfileImplement
	if workflow == domain.WorkflowPstack {
		profile = domain.ExecutionProfileAutonomous
	}
	prompt := domain.ComposeImplementationQueuePrompt(entry.TicketNumber, entry.TicketURL, queue.SpecURL)
	selection := domain.RunPromptSelection{IncludeObjective: true, ExternalObjectIDs: []int64{}}
	configuration := queue.Configuration
	strategy := runLaunchStrategy{Kind: "direct", MachineID: &preview.MachineID, PrimaryRepositoryID: queue.RepositoryID, Agent: configuration.Agent, Configuration: &configuration, ExecutionProfile: profile, Workflow: workflow, Prompt: prompt, PromptSelection: selection, ExpectedCheckouts: preview.Checkouts, AllowDirty: queue.AllowDirty, AllowSharedCheckouts: queue.AllowSharedCheckouts}
	run, err := r.launchRun(runLaunchRequest{ItemID: queue.ItemID, WorkspaceID: queue.WorkspaceID, Strategy: strategy})
	if run.ID != 0 {
		entry.RunID = &run.ID
		entry.Done = false
		entry.Skipped = false
		queue.PausedReason = nil
	}
	if err != nil {
		return r.pauseImplementationQueue(queue, "launch_failed", err.Error())
	}
	if run.ID == 0 {
		return r.pauseImplementationQueue(queue, "launch_failed", "Run launcher returned no Run")
	}
	return r.updateImplementationQueue(queue)
}

func (r *Runtime) pauseImplementationQueue(queue domain.ImplementationQueue, kind, message string) error {
	pause := &domain.ImplementationQueuePauseReason{Kind: domain.ImplementationQueuePauseReasonKind(kind)}
	if kind == "launch_failed" {
		pause.Message = &message
	}
	queue.PausedReason = pause
	return r.updateImplementationQueue(queue)
}

func (r *Runtime) updateImplementationQueue(queue domain.ImplementationQueue) error {
	_, err := r.transition(domain.Event{Kind: "implementation_queue_update", ImplementationQueue: &queue})
	return err
}

func (r *Runtime) queueCopy(queueID int64) domain.ImplementationQueue {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, queue := range r.state.ImplementationQueues {
		if queue.ID == queueID {
			raw, err := json.Marshal(queue)
			if err != nil {
				return domain.ImplementationQueue{}
			}
			var copy domain.ImplementationQueue
			if err := json.Unmarshal(raw, &copy); err != nil {
				return domain.ImplementationQueue{}
			}
			return copy
		}
	}
	return domain.ImplementationQueue{}
}
