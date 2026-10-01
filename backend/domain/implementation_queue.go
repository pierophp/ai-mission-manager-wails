package domain

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ImplementationQueueStart is the launch-time selection captured by the
// first Run. Queue configuration is supplied separately by the Run strategy.
type ImplementationQueueStart struct {
	SpecExternalObjectID int64                      `json:"specExternalObjectId"`
	SpecURL              string                     `json:"specUrl"`
	Entries              []ImplementationQueueEntry `json:"entries"`
}

// CreateImplementationQueue validates the user's selection against the
// persisted Spec and captures a clean queue snapshot before launch effects.
func CreateImplementationQueue(state DomainState, queueID, itemID, workspaceID, repositoryID int64, configuration GrillConfiguration, allowDirty, allowSharedCheckouts bool, start ImplementationQueueStart) (ImplementationQueue, error) {
	if queueID < 1 || itemID < 1 || workspaceID < 1 || repositoryID < 1 {
		return ImplementationQueue{}, DomainError("Implementation Queue identifiers must be positive")
	}
	for _, queue := range state.ImplementationQueues {
		if queue.ItemID == itemID && queue.Active {
			return ImplementationQueue{}, DomainError(fmt.Sprintf("Item %d already has an active Implementation Queue", itemID))
		}
	}
	if err := validateGrill(configuration); err != nil {
		return ImplementationQueue{}, err
	}
	var spec *ExternalObject
	for i := range state.ExternalObjects {
		object := &state.ExternalObjects[i]
		if object.ID == start.SpecExternalObjectID && implementationSpecObject(*object) {
			spec = object
			break
		}
	}
	if spec == nil {
		return ImplementationQueue{}, DomainError("Implementation Spec does not exist")
	}
	linked := false
	for _, link := range state.Links {
		if link.ItemID == itemID && link.ExternalObjectID == spec.ID && link.Purpose == LinkPurpose("to-spec") {
			linked = true
			break
		}
	}
	if !linked {
		return ImplementationQueue{}, DomainError("Implementation Spec is not linked to this Item")
	}
	if start.SpecURL != spec.CanonicalURL {
		return ImplementationQueue{}, DomainError("Implementation Spec URL does not match the linked Spec")
	}
	entries := append([]ImplementationQueueEntry(nil), start.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Position < entries[j].Position })
	if len(entries) == 0 {
		return ImplementationQueue{}, DomainError("Implementation Queue must contain at least one open Ticket")
	}
	seenNumbers, seenURLs := map[int64]bool{}, map[string]bool{}
	for i, entry := range entries {
		if entry.Position != int64(i) || !ImplementationTicketIsOpen(entry.TicketState) || entry.TicketNumber < 1 || strings.TrimSpace(entry.TicketTitle) == "" || !implementationTicketURL(entry.TicketURL) || seenNumbers[entry.TicketNumber] || seenURLs[entry.TicketURL] {
			return ImplementationQueue{}, DomainError("Implementation Queue entries must be unique, ordered, open Tickets with supported URLs")
		}
		seenNumbers[entry.TicketNumber] = true
		seenURLs[entry.TicketURL] = true
		entries[i].RunID = nil
		entries[i].Done = false
		entries[i].Skipped = false
	}
	return ImplementationQueue{ID: queueID, ItemID: itemID, SpecExternalObjectID: spec.ID, SpecURL: spec.CanonicalURL, WorkspaceID: workspaceID, RepositoryID: repositoryID, Configuration: configuration, AllowDirty: allowDirty, AllowSharedCheckouts: allowSharedCheckouts, Entries: entries, Active: true}, nil
}

func implementationSpecObject(object ExternalObject) bool {
	return object.Provider == ProviderGitHub && object.Kind == ObjectIssue ||
		object.Provider == ProviderAtlassian && (object.Kind == ObjectIssue || object.Kind == ObjectDocument) ||
		object.Provider == ProviderGeneric && strings.HasPrefix(object.ExternalKey, "local:")
}

func implementationTicketURL(value string) bool {
	return strings.HasPrefix(value, "local:") || strings.HasPrefix(value, "file://") ||
		(strings.HasPrefix(value, "https://github.com/") && strings.Contains(value, "/issues/")) ||
		(strings.HasPrefix(value, "https://") && strings.Contains(value, ".atlassian.net/browse/"))
}

// ImplementationTicketIsOpen follows the provider terminal-state vocabulary
// used when a queue checks whether its current Ticket has closed.
func ImplementationTicketIsOpen(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "closed", "done", "resolved", "completed", "removed", "cancelled", "canceled":
		return false
	default:
		return true
	}
}

// ComposeImplementationQueuePrompt produces the same Ticket-scoped
// instructions used by the Tauri queue implementation.
func ComposeImplementationQueuePrompt(ticketNumber int64, ticketURL, specURL string) string {
	return composeImplementationPrompt(stripSkillFrontmatter(implementSkillSource), ticketNumber, ticketURL, specURL)
}

func composeImplementationPrompt(skillDocument string, ticketNumber int64, ticketURL, specURL string) string {
	readCommand := implementationTicketReadCommand(ticketNumber, ticketURL)
	completion := implementationTicketCompletionInstruction(ticketNumber, ticketURL)
	return fmt.Sprintf("%s\n\n## Ticket #%d\n%s\n\nRead the ticket using `%s` before making changes.\n\nParent spec: %s\n\n%s", strings.TrimSpace(skillDocument), ticketNumber, ticketURL, readCommand, specURL, completion)
}

func implementationTicketReadCommand(ticketNumber int64, ticketURL string) string {
	if reference, ok := strings.CutPrefix(ticketURL, "local:"); ok {
		if _, path, found := strings.Cut(reference, "#"); found {
			return "cat " + shellQuote(path)
		}
	}
	if strings.HasPrefix(ticketURL, "file://") {
		if parsed, err := url.Parse(ticketURL); err == nil {
			if path, err := url.PathUnescape(parsed.Path); err == nil {
				return "cat " + shellQuote(path)
			}
		}
	}
	if strings.Contains(ticketURL, "github.com/") {
		return fmt.Sprintf("gh issue view %d --comments", ticketNumber)
	}
	if parsed, err := url.Parse(ticketURL); err == nil && strings.Contains(parsed.Path, "/browse/") {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "browse" && strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".atlassian.net") {
				return fmt.Sprintf("twg jira workitem get %s --site https://%s --output json", parts[i+1], parsed.Host)
			}
		}
	}
	return fmt.Sprintf("read %s (ticket #%d)", ticketURL, ticketNumber)
}

func implementationTicketCompletionInstruction(ticketNumber int64, ticketURL string) string {
	if strings.HasPrefix(ticketURL, "local:") || strings.HasPrefix(ticketURL, "file://") {
		return fmt.Sprintf("Reference #%d in the commit message and set this ticket's `Status:` line to `Closed` when the work is complete. Never change the parent Spec's status.", ticketNumber)
	}
	if strings.Contains(ticketURL, "github.com/") {
		return fmt.Sprintf("Reference #%d in the commit message and close this sub-issue when the work is complete. Never close the parent spec or any other issue.", ticketNumber)
	}
	return fmt.Sprintf("Reference #%d in the commit message and mark this ticket complete in its provider when the work is complete. Never close or change the parent spec.", ticketNumber)
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

// ApplyImplementationQueueAction is the pure state transition for the
// queue's user commands. Machine I/O and launching remain Runtime effects.
func ApplyImplementationQueueAction(state DomainState, queueID int64, action string) (DomainState, error) {
	state = cloneState(state)
	index := -1
	for i := range state.ImplementationQueues {
		if state.ImplementationQueues[i].ID == queueID {
			index = i
			break
		}
	}
	if index < 0 {
		return DomainState{}, DomainError(fmt.Sprintf("Implementation Queue %d does not exist", queueID))
	}
	queue := state.ImplementationQueues[index]
	if !queue.Active {
		return DomainState{}, DomainError(fmt.Sprintf("Implementation Queue %d is not active", queueID))
	}
	switch action {
	case "check":
		if queue.PausedReason == nil {
			return DomainState{}, DomainError(fmt.Sprintf("Implementation Queue %d is not paused", queueID))
		}
		queue.PausedReason = nil
	case "skip":
		if queue.PausedReason == nil {
			return DomainState{}, DomainError(fmt.Sprintf("Implementation Queue %d is not paused", queueID))
		}
		entry := CurrentImplementationQueueEntry(&queue)
		if entry == nil {
			return DomainState{}, DomainError("Implementation Queue has no pending ticket")
		}
		entry.Skipped = true
		queue.PausedReason = nil
	case "cancel":
		queue.Active = false
		queue.PausedReason = nil
	default:
		return DomainState{}, DomainError(fmt.Sprintf("unknown Implementation Queue action %q", action))
	}
	state.ImplementationQueues[index] = queue
	return state, nil
}

// CurrentImplementationQueueEntry returns the first queued entry that is
// neither complete nor skipped, using its captured ticket order.
func CurrentImplementationQueueEntry(queue *ImplementationQueue) *ImplementationQueueEntry {
	indices := make([]int, len(queue.Entries))
	for i := range queue.Entries {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool { return queue.Entries[indices[i]].Position < queue.Entries[indices[j]].Position })
	for _, index := range indices {
		entry := &queue.Entries[index]
		if !entry.Done && !entry.Skipped {
			return entry
		}
	}
	return nil
}
