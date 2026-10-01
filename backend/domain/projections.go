package domain

import (
	"fmt"
	"strings"
)

// HomeView calculates the home projection at the supplied local YYYY-MM-DDTHH:MM
// minute. Schedule values are compared as strings and are never timezone shifted.
func HomeViewFor(state DomainState, contextID *int64, now string) HomeView {
	view := HomeView{NeedsAttention: []ItemView{}, AttentionEntries: AttentionEntries(state, contextID, now), Running: []ItemView{}, Waiting: []ItemView{}, Due: []ItemView{}, Completed: []ItemView{}}
	for _, item := range itemViewsAt(state, contextID, &now) {
		due := itemHasDueReminder(item.Item, now) && item.Item.Status != StatusDone
		if due {
			view.Due = append(view.Due, item)
		}
		if due || item.Item.Status == StatusInbox || hasAttentionForItem(view.AttentionEntries, item.Item.ID) {
			view.NeedsAttention = append(view.NeedsAttention, item)
		}
		switch item.Item.Status {
		case StatusActive:
			view.Running = append(view.Running, item)
		case StatusWaiting:
			view.Waiting = append(view.Waiting, item)
		case StatusDone:
			view.Completed = append(view.Completed, item)
		}
	}
	return view
}

func SearchItems(state DomainState, query string, contextID *int64) []ItemView {
	query = strings.ToLower(strings.TrimSpace(query))
	items := itemViewsAt(state, contextID, nil)
	result := make([]ItemView, 0, len(items))
	for _, v := range items {
		if query == "" || strings.Contains(strings.ToLower(v.Item.HumanIdentifier), query) || strings.Contains(strings.ToLower(v.Item.Title), query) || strings.Contains(strings.ToLower(v.Item.Notes), query) || strings.Contains(strings.ToLower(v.ContextName), query) || strings.Contains(strings.ToLower(v.ProjectName), query) {
			result = append(result, v)
		}
	}
	return result
}

func ListInboxItems(state DomainState) []Item {
	items := []Item{}
	for _, item := range state.Items {
		if item.Status == StatusInbox {
			items = append(items, item)
		}
	}
	return items
}

func AttentionEntries(state DomainState, contextID *int64, now string) []AttentionEntry {
	entries := []AttentionEntry{}
	for _, link := range state.Links {
		if contextID != nil {
			ctx, ok := contextForItem(state, link.ItemID)
			if !ok || ctx != *contextID {
				continue
			}
		}
		object, ok := externalObjectByID(state, link.ExternalObjectID)
		if !ok {
			continue
		}
		if entry := attentionEntryForLink(state, link, object, &now); entry != nil {
			entries = append(entries, *entry)
		}
		if link.ReviewAt != nil && *link.ReviewAt <= now {
			title := object.CanonicalURL
			if snapshot, ok := snapshotByID(state, object.ID); ok {
				title = snapshot.Title
			}
			entries = append(entries, AttentionEntry{Kind: AttentionReview, LinkID: link.ID, ItemID: link.ItemID, ExternalObjectID: object.ID, SourceTitle: title, SourceURL: object.CanonicalURL, Activities: []Activity{}, Summary: "Review scheduled for " + *link.ReviewAt})
		}
	}
	for _, item := range state.Items {
		if contextID != nil {
			ctx, ok := contextForItem(state, item.ID)
			if !ok || ctx != *contextID {
				continue
			}
		}
		for _, reminder := range item.Reminders {
			if reminder.RemindAt <= now {
				id := reminder.ID
				entries = append(entries, AttentionEntry{Kind: AttentionReminder, ReminderID: &id, ItemID: item.ID, SourceTitle: item.Title, SourceURL: "", Activities: []Activity{}, Summary: "Reminder due at " + reminder.RemindAt})
			}
		}
	}
	for _, run := range state.Runs {
		if run.State != RunBlocked {
			continue
		}
		if contextID != nil {
			ctx, ok := contextForItem(state, run.ItemID)
			if !ok || ctx != *contextID {
				continue
			}
		}
		item, ok := itemByID(state, run.ItemID)
		if !ok {
			continue
		}
		id := run.ID
		entries = append(entries, AttentionEntry{Kind: AttentionBlockedRun, RunID: &id, ItemID: item.ID, SourceTitle: item.Title, SourceURL: "", Activities: []Activity{}, Summary: fmt.Sprintf("Run #%d is blocked and needs your input", run.ID)})
	}
	for _, queue := range state.ImplementationQueues {
		if !queue.Active || queue.PausedReason == nil {
			continue
		}
		if contextID != nil {
			queueContext, ok := contextForItem(state, queue.ItemID)
			if !ok || queueContext != *contextID {
				continue
			}
		}
		var queued *ImplementationQueueEntry
		for i := range queue.Entries {
			if !queue.Entries[i].Done && !queue.Entries[i].Skipped {
				queued = &queue.Entries[i]
				break
			}
		}
		if queued == nil {
			continue
		}
		var link *Link
		for i := range state.Links {
			if state.Links[i].ItemID == queue.ItemID && state.Links[i].ExternalObjectID == queue.SpecExternalObjectID {
				link = &state.Links[i]
				break
			}
		}
		if link == nil {
			continue
		}
		object, ok := externalObjectByID(state, queue.SpecExternalObjectID)
		if !ok {
			continue
		}
		reason := string(queue.PausedReason.Kind)
		switch queue.PausedReason.Kind {
		case "ticket_still_open":
			reason = "ticket is still open"
		case "checkout_dirty":
			reason = "checkout is dirty"
		case "run_stopped":
			reason = "Run was stopped"
		case "pane_missing":
			reason = "Run Pane is missing"
		case "launch_failed":
			if queue.PausedReason.Message != nil {
				reason = "next Run failed to launch: " + *queue.PausedReason.Message
			}
		}
		queueID := queue.ID
		entries = append(entries, AttentionEntry{Kind: AttentionImplementationQueue, LinkID: link.ID, RunID: queued.RunID, QueueID: &queueID, ItemID: queue.ItemID, ExternalObjectID: object.ID, SourceTitle: queued.TicketTitle, SourceURL: queued.TicketURL, Activities: []Activity{}, Summary: fmt.Sprintf("Implementation Queue ticket #%d paused: %s", queued.TicketNumber, reason)})
	}
	return entries
}

func itemViewsAt(state DomainState, contextID *int64, now *string) []ItemView {
	out := []ItemView{}
	for _, item := range state.Items {
		project, ok := projectByID(state, item.ProjectID)
		if !ok {
			continue
		}
		if contextID != nil && project.ContextID != *contextID {
			continue
		}
		ctx, ok := contextByID(state, project.ContextID)
		if !ok {
			continue
		}
		v := ItemView{Item: item, ContextID: ctx.ID, ContextName: ctx.Name, ProjectName: project.Name, Relationships: []ItemRelation{}, Workspaces: []Workspace{}, Worktrees: []Worktree{}, Runs: []Run{}, RunProjections: []RunProjection{}, ImplementationQueues: []ImplementationQueue{}, Links: []ExternalLinkView{}}
		for _, r := range state.Relationships {
			if r.FromItemID == item.ID || r.ToItemID == item.ID {
				v.Relationships = append(v.Relationships, r)
			}
		}
		projectRepos := []WorkspaceRepository{}
		for _, repo := range state.Repositories {
			if repo.ProjectID == item.ProjectID {
				projectRepos = append(projectRepos, WorkspaceRepository{RepositoryID: repo.ID, Branch: "mission-" + item.HumanIdentifier, BaseBranch: repo.BaseBranch})
			}
		}
		for _, w := range state.Workspaces {
			if w.ItemID == item.ID {
				w.Repositories = projectRepos
				v.Workspaces = append(v.Workspaces, w)
			}
		}
		for _, w := range state.Worktrees {
			for _, workspace := range state.Workspaces {
				if workspace.ID == w.WorkspaceID && workspace.ItemID == item.ID {
					v.Worktrees = append(v.Worktrees, w)
					break
				}
			}
		}
		for _, run := range state.Runs {
			if run.ItemID == item.ID {
				run = canonicalRunForView(run)
				v.Runs = append(v.Runs, run)
				v.RunProjections = append(v.RunProjections, runProjection(run))
			}
		}
		v.RunSignals = itemRunSignals(v.Runs)
		for _, queue := range state.ImplementationQueues {
			if queue.ItemID == item.ID {
				v.ImplementationQueues = append(v.ImplementationQueues, queue)
			}
		}
		for _, link := range state.Links {
			if link.ItemID != item.ID {
				continue
			}
			object, ok := externalObjectByID(state, link.ExternalObjectID)
			if !ok {
				continue
			}
			var snapshot *ExternalSnapshot
			if snap, ok := snapshotByID(state, object.ID); ok {
				if snap.Metadata == nil {
					snap.Metadata = []ExternalMetadata{}
				}
				snapshot = &snap
			}
			if link.Provenance != nil {
				provenance := *link.Provenance
				if provenance.BlockedBy == nil {
					provenance.BlockedBy = []string{}
				}
				link.Provenance = &provenance
			}
			var current *string
			if now != nil {
				current = now
			}
			v.Links = append(v.Links, ExternalLinkView{Link: link, Object: object, Snapshot: snapshot, AttentionPolicy: effectiveAttentionPolicy(state, link, object), AttentionEntry: attentionEntryForLink(state, link, object, current), SupportsImplementationSpec: supportsImplementationSpec(object), SupportsImplementationTicket: supportsImplementationTicket(object)})
		}
		out = append(out, v)
	}
	return out
}

func runProjection(run Run) RunProjection {
	active := runActive(run)
	phase := string(run.State)
	if run.ExecutionProfile == "plan" && run.PlanPhase != nil && *run.PlanPhase == "awaitingGo" {
		phase = "awaitingGo"
	} else if run.ExecutionProfile == "grill" && run.GrillPhase != nil {
		switch *run.GrillPhase {
		case "starting":
			phase = "grillStarting"
		case "working":
			phase = "grillWorking"
		case "waitingForAnswers":
			phase = "grillWaitingForAnswers"
		case "awaitingNextAction":
			phase = "grillAwaitingNextAction"
		case "recoverablePaneLoss":
			phase = "grillRecoverablePaneLoss"
		}
	}
	actions := []GrillContinuationAction{}
	for _, action := range []GrillContinuationAction{"to-spec", "to-tickets", "implement"} {
		available := false
		if run.GrillPhase != nil {
			switch *run.GrillPhase {
			case "awaitingNextAction":
				available = true
			case "waitingForAnswers":
				previous := ""
				if run.GrillAction != nil {
					previous = string(*run.GrillAction)
				}
				available = previous == "" && action == "to-spec" || previous == "to-spec" && action == "to-tickets" || previous == "to-tickets" && action == "implement"
			}
		}
		if available && !(*run.GrillPhase == "awaitingNextAction" && action == "to-spec" && run.GrillAction != nil && *run.GrillAction == action) {
			actions = append(actions, action)
		}
	}
	return RunProjection{RunID: run.ID, Status: map[bool]string{true: "active", false: "finished"}[active], Phase: phase, Continuations: RunContinuations{GoPlan: run.ExecutionProfile == "plan" && run.PlanPhase != nil && *run.PlanPhase == "awaitingGo", GrillActions: actions, Stop: active && run.PaneStatus != PaneMissing, Finish: active, Delete: !active}}
}
func runActive(run Run) bool {
	return run.State != RunFinished || (run.ExecutionProfile == "grill" && (run.GrillPhase == nil || *run.GrillPhase != "finished")) || (run.ExecutionProfile == "plan" && run.PlanPhase != nil && *run.PlanPhase == "awaitingGo")
}

func canonicalRunForView(run Run) Run {
	if run.ExecutionProfile == "pstack_review" {
		run.ExecutionProfile = "pstack-review"
	}
	if run.GrillPhase != nil {
		value := strings.ReplaceAll(string(*run.GrillPhase), "_", "")
		for _, pair := range [][2]string{{"waitingforanswers", "waitingForAnswers"}, {"awaitingnextaction", "awaitingNextAction"}, {"recoverablepaneloss", "recoverablePaneLoss"}} {
			if strings.EqualFold(value, pair[0]) {
				value = pair[1]
			}
		}
		phase := GrillPhase(value)
		run.GrillPhase = &phase
	}
	if run.PlanPhase != nil {
		value := strings.ReplaceAll(string(*run.PlanPhase), "_", "")
		if strings.EqualFold(value, "awaitinggo") {
			value = "awaitingGo"
		}
		phase := PlanPhase(value)
		run.PlanPhase = &phase
	}
	if run.DirectCheckouts == nil {
		run.DirectCheckouts = []RunCheckout{}
	}
	if run.ReportedPullRequests == nil {
		run.ReportedPullRequests = []string{}
	}
	if run.ExecutionProfile == ExecutionProfileGrill {
		run.DownstreamIssueCandidates = DiscoverDownstreamIssueCandidates(run.Transcript)
	} else {
		run.DownstreamIssueCandidates = []DownstreamIssueCandidate{}
	}
	if run.GrillAnswers == nil {
		run.GrillAnswers = []GrillAnswer{}
	}
	if run.GrillDecisions == nil {
		run.GrillDecisions = []GrillAnswer{}
	}
	return run
}

func CanonicalRunForView(run Run) Run { return canonicalRunForView(run) }
func itemRunSignals(runs []Run) ItemRunSignals {
	signals := ItemRunSignals{}
	for _, run := range runs {
		waiting := run.ExecutionProfile == "grill" && run.GrillPhase != nil && *run.GrillPhase == "waitingForAnswers" && run.GrillResponse == nil
		signals.GrillWaiting = signals.GrillWaiting || waiting
		signals.RunActive = signals.RunActive || (runActive(run) && run.PaneStatus != PaneMissing && !waiting)
	}
	return signals
}
func itemHasDueReminder(item Item, now string) bool {
	for _, reminder := range item.Reminders {
		if reminder.RemindAt <= now {
			return true
		}
	}
	return false
}
func hasAttentionForItem(entries []AttentionEntry, itemID int64) bool {
	for _, entry := range entries {
		if entry.ItemID == itemID {
			return true
		}
	}
	return false
}

func attentionEntryForLink(state DomainState, link Link, object ExternalObject, now *string) *AttentionEntry {
	policy := effectiveAttentionPolicy(state, link, object)
	watchActive := true
	if now != nil && link.WatchUntil != nil && *link.WatchUntil <= *now {
		watchActive = false
	}
	activities := []Activity{}
	for _, activity := range state.Activities {
		if !watchActive || activity.ExternalObjectID != object.ID || activity.ID <= link.ReviewedActivityID {
			continue
		}
		filtered := []ExternalChange{}
		for _, change := range activity.Changes {
			if change.Kind == "title" && policy.Title || change.Kind == "state" && policy.State || change.Kind == "metadata" && policy.Metadata {
				filtered = append(filtered, change)
			}
		}
		if len(filtered) > 0 {
			activity.Changes = filtered
			activities = append(activities, activity)
		}
	}
	if len(activities) == 0 {
		return nil
	}
	title := object.CanonicalURL
	if snap, ok := snapshotByID(state, object.ID); ok {
		title = snap.Title
	}
	descriptions := []string{}
	for _, activity := range activities {
		for _, change := range activity.Changes {
			label := strings.Title(string(change.Kind))
			if change.Kind == "metadata" {
				key := "value"
				if change.Key != nil {
					key = *change.Key
				}
				label = "Metadata " + key
			}
			switch {
			case change.Previous != nil && change.Current != nil:
				descriptions = append(descriptions, fmt.Sprintf("%s changed from %s to %s", label, *change.Previous, *change.Current))
			case change.Current != nil:
				descriptions = append(descriptions, fmt.Sprintf("%s added as %s", label, *change.Current))
			case change.Previous != nil:
				descriptions = append(descriptions, fmt.Sprintf("%s removed (was %s)", label, *change.Previous))
			default:
				descriptions = append(descriptions, label+" changed")
			}
		}
	}
	return &AttentionEntry{Kind: AttentionExternalChange, LinkID: link.ID, ItemID: link.ItemID, ExternalObjectID: object.ID, SourceTitle: title, SourceURL: object.CanonicalURL, Activities: activities, Summary: strings.Join(descriptions, "; ")}
}
func effectiveAttentionPolicy(state DomainState, link Link, object ExternalObject) ExternalChangePolicy {
	policy := ExternalChangePolicy{}
	foundDefault := false
	if ctx, ok := contextForItem(state, link.ItemID); ok {
		for _, d := range state.AttentionDefaults {
			if d.ContextID == ctx && d.ObjectKind == object.Kind {
				policy = d.Policy
				foundDefault = true
				break
			}
		}
	}
	if !foundDefault {
		policy = ExternalChangePolicy{Title: true, State: true, Metadata: true}
	}
	if link.TitleAttention != nil {
		policy.Title = *link.TitleAttention
	} else if link.AttentionPolicy != nil {
		policy.Title = link.AttentionPolicy.Title
	}
	if link.StateAttention != nil {
		policy.State = *link.StateAttention
	} else if link.AttentionPolicy != nil {
		policy.State = link.AttentionPolicy.State
	}
	if link.MetadataAttention != nil {
		policy.Metadata = *link.MetadataAttention
	} else if link.AttentionPolicy != nil {
		policy.Metadata = link.AttentionPolicy.Metadata
	}
	return policy
}
func supportsImplementationSpec(o ExternalObject) bool {
	return o.Provider == ProviderGitHub && o.Kind == ObjectIssue || o.Provider == ProviderAtlassian && (o.Kind == ObjectIssue || o.Kind == ObjectDocument) || o.Provider == ProviderGeneric && strings.HasPrefix(o.ExternalKey, "local:")
}
func supportsImplementationTicket(o ExternalObject) bool {
	return o.Provider == ProviderGitHub && o.Kind == ObjectIssue || o.Provider == ProviderAtlassian && o.Kind == ObjectIssue || o.Provider == ProviderGeneric && strings.HasPrefix(o.ExternalKey, "local:")
}
func externalObjectByID(s DomainState, id int64) (ExternalObject, bool) {
	for _, o := range s.ExternalObjects {
		if o.ID == id {
			return o, true
		}
	}
	return ExternalObject{}, false
}
func snapshotByID(s DomainState, id int64) (ExternalSnapshot, bool) {
	for _, v := range s.Snapshots {
		if v.ExternalObjectID == id {
			return v, true
		}
	}
	return ExternalSnapshot{}, false
}
func itemByID(s DomainState, id int64) (Item, bool) {
	for _, v := range s.Items {
		if v.ID == id {
			return v, true
		}
	}
	return Item{}, false
}
func projectByID(s DomainState, id int64) (Project, bool) {
	for _, v := range s.Projects {
		if v.ID == id {
			return v, true
		}
	}
	return Project{}, false
}
func contextByID(s DomainState, id int64) (Context, bool) {
	for _, v := range s.Contexts {
		if v.ID == id {
			return v, true
		}
	}
	return Context{}, false
}
