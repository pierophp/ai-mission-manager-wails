package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func cloneState(state DomainState) DomainState {
	raw, _ := json.Marshal(state)
	var clone DomainState
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func DefaultGrillConfiguration() GrillConfiguration {
	return GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "high"}
}

func NewContextConfiguration() ContextConfiguration {
	policy := ExternalChangePolicy{Title: true, State: true, Metadata: true}
	return ContextConfiguration{
		CheckDirtyCheckouts: true,
		GrillDefaults:       DefaultGrillConfiguration(),
		ImplementDefaults:   DefaultGrillConfiguration(),
		DefaultWorkflow:     WorkflowMattPocock,
		PstackDefaults:      DefaultGrillConfiguration(),
		PstackRoles:         DefaultPstackRoles(),
		AttentionDefaults: []ContextAttentionDefault{
			{ObjectKind: ObjectIssue, Policy: policy},
			{ObjectKind: ObjectPullRequest, Policy: policy},
			{ObjectKind: ObjectGeneric, Policy: policy},
		},
	}
}

// Decide applies a deterministic transition. It performs no I/O and reads no
// clock; all timestamps used by effects are supplied by adapters.
func Decide(input DomainState, event Event) (Decision, error) {
	state := cloneState(input)
	clean := func(value string) string { return strings.TrimSpace(value) }
	switch event.Kind {
	case "create_context":
		name := clean(event.Name)
		if name == "" {
			return Decision{}, DomainError("a Context name cannot be blank")
		}
		for _, c := range state.Contexts {
			if c.Name == name {
				return Decision{}, DomainError("Context name already exists: " + name)
			}
		}
		if state.NextContextID < 1 || state.NextProjectID < 1 || state.NextContextID == 1<<63-1 || state.NextProjectID == 1<<63-1 {
			return Decision{}, DomainError("the Item identifier sequence is exhausted")
		}
		configuration := NewContextConfiguration()
		c := Context{ID: state.NextContextID, Name: name, CheckDirtyCheckouts: configuration.CheckDirtyCheckouts, GrillDefaults: configuration.GrillDefaults, ImplementDefaults: configuration.ImplementDefaults, DefaultWorkflow: configuration.DefaultWorkflow, PstackDefaults: configuration.PstackDefaults, PstackRoles: configuration.PstackRoles}
		p := Project{ID: state.NextProjectID, ContextID: c.ID, Name: "Default", Defaults: ProjectDefaults{ItemStatus: StatusInbox, ExecutionMode: ExecutionWorktree}}
		state.NextContextID++
		state.NextProjectID++
		state.Contexts = append(state.Contexts, c)
		state.Projects = append(state.Projects, p)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_context", Context: &c}, {Kind: "persist_project", Project: &p}}}, nil
	case "create_context_configuration":
		cfg := event.Configuration
		cfg.Name = clean(cfg.Name)
		created, err := Decide(state, Event{Kind: "create_context", Name: cfg.Name})
		if err != nil {
			return Decision{}, err
		}
		id := created.State.Contexts[len(created.State.Contexts)-1].ID
		for i := range cfg.AttentionDefaults {
			cfg.AttentionDefaults[i].ContextID = id
		}
		updated, err := Decide(created.State, Event{Kind: "update_context_configuration", ContextID: id, Configuration: cfg})
		if err != nil {
			return Decision{}, err
		}
		return Decision{State: updated.State, Effects: append(created.Effects, updated.Effects...)}, nil
	case "update_context", "update_context_configuration":
		idx := -1
		for i, c := range state.Contexts {
			if c.ID == event.ContextID {
				idx = i
			}
			name := event.Name
			if event.Kind == "update_context_configuration" {
				name = event.Configuration.Name
			}
			if c.ID != event.ContextID && c.Name == clean(name) {
				return Decision{}, DomainError("Context name already exists: " + clean(name))
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		c := state.Contexts[idx]
		if event.Kind == "update_context" {
			c.Name = clean(event.Name)
			if c.Name == "" {
				return Decision{}, DomainError("a Context name cannot be blank")
			}
		} else {
			cfg := event.Configuration
			cfg.Name = clean(cfg.Name)
			if cfg.Name == "" {
				return Decision{}, DomainError("a Context name cannot be blank")
			}
			if len(cfg.AttentionDefaults) != 3 {
				return Decision{}, DomainError("Context attention defaults must contain one policy for Issue, Pull Request, and generic External Objects")
			}
			if cfg.ExecutionMachineID != nil && !hasMachine(state, *cfg.ExecutionMachineID) {
				return Decision{}, DomainError(fmt.Sprintf("Machine %d does not exist", *cfg.ExecutionMachineID))
			}
			if c.ExecutionMachineID != cfg.ExecutionMachineID {
				active := activeContextRunIDs(state, event.ContextID)
				if len(active) > 0 {
					return Decision{}, DomainError(fmt.Sprintf("Context %d has active Runs: %v", event.ContextID, active))
				}
			}
			if cfg.ClaudeProfileID != nil {
				if err := validateContextProfile(state, cfg.ExecutionMachineID, AgentClaude, *cfg.ClaudeProfileID); err != nil {
					return Decision{}, err
				}
			}
			if cfg.CodexProfileID != nil {
				if err := validateContextProfile(state, cfg.ExecutionMachineID, AgentCodex, *cfg.CodexProfileID); err != nil {
					return Decision{}, err
				}
			}
			if err := validateGrill(cfg.GrillDefaults); err != nil {
				return Decision{}, err
			}
			if err := validateGrill(cfg.ImplementDefaults); err != nil {
				return Decision{}, err
			}
			if err := validateGrill(cfg.PstackDefaults); err != nil {
				return Decision{}, err
			}
			if cfg.DefaultWorkflow != WorkflowMattPocock && cfg.DefaultWorkflow != WorkflowPstack {
				return Decision{}, DomainError(fmt.Sprintf("unknown contexts.default_workflow value %q", cfg.DefaultWorkflow))
			}
			if err := validatePstackRoles(cfg.PstackRoles); err != nil {
				return Decision{}, err
			}
			seen := map[ExternalObjectKind]bool{}
			for _, d := range cfg.AttentionDefaults {
				if d.ContextID != event.ContextID || seen[d.ObjectKind] {
					return Decision{}, DomainError("Context attention defaults must contain one policy for Issue, Pull Request, and generic External Objects")
				}
				seen[d.ObjectKind] = true
			}
			for _, kind := range []ExternalObjectKind{ObjectIssue, ObjectPullRequest, ObjectGeneric} {
				if !seen[kind] {
					return Decision{}, DomainError("Context attention defaults must contain one policy for Issue, Pull Request, and generic External Objects")
				}
			}
			c.Name = cfg.Name
			c.ExecutionMachineID = cfg.ExecutionMachineID
			c.ClaudeProfileID = cfg.ClaudeProfileID
			c.CodexProfileID = cfg.CodexProfileID
			c.CheckDirtyCheckouts = cfg.CheckDirtyCheckouts
			c.GrillDefaults = cfg.GrillDefaults
			c.ImplementDefaults = cfg.ImplementDefaults
			c.DefaultWorkflow = cfg.DefaultWorkflow
			c.PstackDefaults = cfg.PstackDefaults
			c.PstackRoles = cfg.PstackRoles
			c.GHExecutablePath = cleanOptional(cfg.GHExecutablePath)
			c.TWGExecutablePath = cleanOptional(cfg.TWGExecutablePath)
			c.AZExecutablePath = cleanOptional(cfg.AZExecutablePath)
			c.AtlassianSite = cleanOptional(cfg.AtlassianSite)
			c.AzureDevOpsOrganization = cleanOptional(cfg.AzureDevOpsOrganization)
			c.BitbucketWorkspace = cleanOptional(cfg.BitbucketWorkspace)
			state.AttentionDefaults = filterDefaults(state.AttentionDefaults, event.ContextID)
			state.AttentionDefaults = append(state.AttentionDefaults, cfg.AttentionDefaults...)
		}
		state.Contexts[idx] = c
		if event.Kind == "update_context_configuration" {
			return Decision{State: state, Effects: []Effect{{Kind: "persist_context_configuration", Context: &c, AttentionDefaults: event.Configuration.AttentionDefaults}}}, nil
		}
		return Decision{State: state, Effects: []Effect{{Kind: "update_context", Context: &c}}}, nil
	case "set_context_grill_defaults", "set_context_implement_defaults", "set_context_dirty_checkout_check":
		idx := -1
		for i, c := range state.Contexts {
			if c.ID == event.ContextID {
				idx = i
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		c := state.Contexts[idx]
		switch event.Kind {
		case "set_context_grill_defaults":
			if err := validateGrill(event.GrillConfiguration); err != nil {
				return Decision{}, err
			}
			c.GrillDefaults = event.GrillConfiguration
		case "set_context_implement_defaults":
			if err := validateGrill(event.GrillConfiguration); err != nil {
				return Decision{}, err
			}
			c.ImplementDefaults = event.GrillConfiguration
		case "set_context_dirty_checkout_check":
			c.CheckDirtyCheckouts = event.Enabled
		}
		state.Contexts[idx] = c
		return Decision{State: state, Effects: []Effect{{Kind: EffectKind(event.Kind), Context: &c}}}, nil
	case "create_project":
		name := clean(event.Name)
		if name == "" {
			return Decision{}, DomainError("a Project name cannot be blank")
		}
		if !hasContext(state, event.ContextID) {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		for _, p := range state.Projects {
			if p.ContextID == event.ContextID && p.Name == name {
				return Decision{}, DomainError("Project name already exists in Context " + itoa(event.ContextID) + ": " + name)
			}
		}
		if state.NextProjectID < 1 || state.NextProjectID == 1<<63-1 {
			return Decision{}, DomainError("the Item identifier sequence is exhausted")
		}
		if err := validateProjectDefaults(event.Defaults); err != nil {
			return Decision{}, err
		}
		p := Project{ID: state.NextProjectID, ContextID: event.ContextID, Name: name, Defaults: event.Defaults}
		if p.Defaults.ItemStatus == "" {
			p.Defaults.ItemStatus = StatusInbox
		}
		if p.Defaults.ExecutionMode == "" {
			p.Defaults.ExecutionMode = ExecutionWorktree
		}
		state.NextProjectID++
		state.Projects = append(state.Projects, p)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_project", Project: &p}}}, nil
	case "update_project":
		idx := -1
		for i, p := range state.Projects {
			if p.ID == event.ProjectID {
				idx = i
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Project " + itoa(event.ProjectID) + " does not exist")
		}
		p := state.Projects[idx]
		name := clean(event.Name)
		if name == "" {
			return Decision{}, DomainError("a Project name cannot be blank")
		}
		for _, other := range state.Projects {
			if other.ID != p.ID && other.ContextID == p.ContextID && other.Name == name {
				return Decision{}, DomainError("Project name already exists in Context " + itoa(p.ContextID) + ": " + name)
			}
		}
		if err := validateProjectDefaults(event.Defaults); err != nil {
			return Decision{}, err
		}
		p.Name = name
		p.Defaults = event.Defaults
		if p.Defaults.ItemStatus == "" {
			p.Defaults.ItemStatus = StatusInbox
		}
		if p.Defaults.ExecutionMode == "" {
			p.Defaults.ExecutionMode = ExecutionWorktree
		}
		state.Projects[idx] = p
		return Decision{State: state, Effects: []Effect{{Kind: "update_project", Project: &p}}}, nil
	case "create_item":
		title := strings.TrimSpace(event.Name)
		if title == "" {
			return Decision{}, DomainError("an Item title cannot be blank")
		}
		if !hasContext(state, event.ContextID) {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		var project *Project
		for i := range state.Projects {
			if state.Projects[i].ID == event.ProjectID {
				project = &state.Projects[i]
				break
			}
		}
		if project == nil {
			return Decision{}, DomainError("Project " + itoa(event.ProjectID) + " does not exist")
		}
		if project.ContextID != event.ContextID {
			return Decision{}, DomainError(fmt.Sprintf("Project %d belongs to another Context", event.ProjectID))
		}
		if state.NextItemID < 1 || state.NextItemID == math.MaxInt64 || state.NextItemNumber < 1 || state.NextItemNumber == math.MaxInt64 {
			return Decision{}, DomainError("the Item identifier sequence is exhausted")
		}
		item := Item{ID: state.NextItemID, HumanIdentifier: fmt.Sprintf("MC-%d", state.NextItemNumber), Title: title, ProjectID: event.ProjectID, Status: project.Defaults.ItemStatus, Notes: event.Notes, Reminders: []Reminder{}}
		state.NextItemID++
		state.NextItemNumber++
		state.Items = append(append([]Item(nil), state.Items...), item)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_item", Item: &item}}}, nil
	case "set_item_status", "set_item_title", "set_item_notes", "add_item_reminder", "remove_item_reminder":
		idx := -1
		for i := range state.Items {
			if state.Items[i].ID == event.ItemID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Decision{}, DomainError("Item " + itoa(event.ItemID) + " does not exist")
		}
		items := append([]Item(nil), state.Items...)
		item := items[idx]
		switch event.Kind {
		case "set_item_status":
			if event.Status != StatusInbox && event.Status != StatusActive && event.Status != StatusWaiting && event.Status != StatusDone {
				return Decision{}, DomainError(fmt.Sprintf("unknown ItemStatus variant %q; expected Inbox, Active, Waiting, or Done", event.Status))
			}
			item.Status = event.Status
		case "set_item_title":
			item.Title = strings.TrimSpace(event.Name)
			if item.Title == "" {
				return Decision{}, DomainError("an Item title cannot be blank")
			}
		case "set_item_notes":
			item.Notes = event.Notes
		case "add_item_reminder":
			remindAt := strings.TrimSpace(event.RemindAt)
			if remindAt == "" {
				return Decision{}, DomainError("a Reminder date cannot be blank")
			}
			if state.NextReminderID < 1 || state.NextReminderID == math.MaxInt64 {
				return Decision{}, DomainError("the Item identifier sequence is exhausted")
			}
			item.Reminders = append(append([]Reminder(nil), item.Reminders...), Reminder{ID: state.NextReminderID, RemindAt: remindAt})
			state.NextReminderID++
		case "remove_item_reminder":
			reminders := make([]Reminder, 0, len(item.Reminders))
			found := false
			for _, reminder := range item.Reminders {
				if reminder.ID == event.ReminderID {
					found = true
				} else {
					reminders = append(reminders, reminder)
				}
			}
			if !found {
				return Decision{}, DomainError(fmt.Sprintf("Reminder %d does not exist on Item %d", event.ReminderID, event.ItemID))
			}
			item.Reminders = reminders
		}
		items[idx] = item
		state.Items = items
		return Decision{State: state, Effects: []Effect{{Kind: EffectKind(event.Kind), Item: &item}}}, nil
	case "set_item_relation":
		if event.FromItemID == event.ToItemID {
			return Decision{}, DomainError(fmt.Sprintf("an Item cannot relate to itself: %d", event.FromItemID))
		}
		fromContext, fromOK := contextForItem(state, event.FromItemID)
		toContext, toOK := contextForItem(state, event.ToItemID)
		if !fromOK {
			return Decision{}, DomainError("Item " + itoa(event.FromItemID) + " does not exist")
		}
		if !toOK {
			return Decision{}, DomainError("Item " + itoa(event.ToItemID) + " does not exist")
		}
		if fromContext != toContext {
			return Decision{}, DomainError(fmt.Sprintf("Items %d and %d belong to different Contexts", event.FromItemID, event.ToItemID))
		}
		if event.RelationKind != RelationBlocks && event.RelationKind != RelationBlockedBy && event.RelationKind != RelationRelatedTo {
			return Decision{}, DomainError(fmt.Sprintf("unknown ItemRelationKind variant %q", event.RelationKind))
		}
		relation := ItemRelation{FromItemID: event.FromItemID, ToItemID: event.ToItemID, Kind: event.RelationKind}
		for _, existing := range state.Relationships {
			if existing == relation {
				return Decision{}, DomainError("the relationship already exists")
			}
		}
		state.Relationships = append(append([]ItemRelation(nil), state.Relationships...), relation)
		return Decision{State: state, Effects: []Effect{{Kind: "persist_item_relation", Relation: &relation}}}, nil
	case "set_context_attention_default":
		if !hasContext(state, event.ContextID) {
			return Decision{}, DomainError("Context " + itoa(event.ContextID) + " does not exist")
		}
		if err := validateObjectKind(event.ObjectKind); err != nil {
			return Decision{}, err
		}
		d := ContextAttentionDefault{ContextID: event.ContextID, ObjectKind: event.ObjectKind, Policy: event.Policy}
		found := false
		for i, v := range state.AttentionDefaults {
			if v.ContextID == event.ContextID && v.ObjectKind == event.ObjectKind {
				state.AttentionDefaults[i] = d
				found = true
			}
		}
		if !found {
			state.AttentionDefaults = append(state.AttentionDefaults, d)
		}
		return Decision{State: state, Effects: []Effect{{Kind: "persist_attention_default", AttentionDefaults: []ContextAttentionDefault{d}}}}, nil
	default:
		return Decision{}, DomainError("unsupported domain event: " + string(event.Kind))
	}
}

func contextForItem(s DomainState, itemID int64) (int64, bool) {
	for _, item := range s.Items {
		if item.ID != itemID {
			continue
		}
		for _, project := range s.Projects {
			if project.ID == item.ProjectID {
				return project.ContextID, true
			}
		}
		return 0, false
	}
	return 0, false
}

func filterDefaults(values []ContextAttentionDefault, id int64) []ContextAttentionDefault {
	out := make([]ContextAttentionDefault, 0, len(values))
	for _, v := range values {
		if v.ContextID != id {
			out = append(out, v)
		}
	}
	return out
}
func hasContext(s DomainState, id int64) bool {
	for _, v := range s.Contexts {
		if v.ID == id {
			return true
		}
	}
	return false
}
func itoa(value int64) string { return strconv.FormatInt(value, 10) }

func hasMachine(s DomainState, id int64) bool {
	for _, m := range s.Machines {
		if m.ID == id {
			return true
		}
	}
	return false
}
func activeContextRunIDs(s DomainState, contextID int64) []int64 {
	var ids []int64
	for _, run := range s.Runs {
		active := run.State != RunFinished || (run.ExecutionProfile == ExecutionProfile("grill") && (run.GrillPhase == nil || *run.GrillPhase != GrillPhase("Finished"))) || (run.ExecutionProfile == ExecutionProfile("plan") && run.PlanPhase != nil && *run.PlanPhase == PlanPhase("awaitingGo"))
		if !active {
			continue
		}
		for _, item := range s.Items {
			if item.ID == run.ItemID {
				for _, project := range s.Projects {
					if project.ID == item.ProjectID && hasContext(s, contextID) && contextForProject(s, project.ID) == contextID {
						ids = append(ids, run.ID)
					}
				}
			}
		}
	}
	return ids
}
func contextForProject(s DomainState, projectID int64) int64 {
	for _, p := range s.Projects {
		if p.ID == projectID {
			return p.ContextID
		}
	}
	return 0
}
func validateContextProfile(s DomainState, machineID *int64, provider AgentKind, profileID int64) error {
	var profile *CLIConfigurationProfile
	for i := range s.CLIConfigurationProfiles {
		if s.CLIConfigurationProfiles[i].ID == profileID {
			profile = &s.CLIConfigurationProfiles[i]
			break
		}
	}
	if profile == nil {
		return DomainError(fmt.Sprintf("CLI configuration profile %d does not exist", profileID))
	}
	if profile.Provider != provider {
		return DomainError(fmt.Sprintf("CLI configuration profile %d is for %s, not %s", profileID, profile.Provider, provider))
	}
	if machineID == nil || *machineID != profile.MachineID {
		return DomainError(fmt.Sprintf("CLI configuration profile %d belongs to another Machine", profileID))
	}
	if !hasMachine(s, *machineID) {
		return DomainError(fmt.Sprintf("Machine %d does not exist", *machineID))
	}
	return nil
}
func validateGrill(c GrillConfiguration) error {
	valid := strings.TrimSpace(c.Model) != "" && strings.TrimSpace(c.Effort) != "" && (c.Agent == AgentClaude || c.Agent == AgentCodex)
	if c.Agent == AgentClaude {
		models := map[string]bool{"claude-opus-5": true, "claude-sonnet-5": true, "claude-opus-4-8": true, "claude-sonnet-4-6": true, "claude-opus-4-5-20251101": true, "claude-sonnet-4-5-20250929": true, "claude-haiku-4-5-20251001": true, "claude-sonnet-4-5": true, "claude-haiku-4-5": true}
		valid = valid && models[c.Model]
	}
	if !valid {
		return DomainError(fmt.Sprintf("Grill configuration is invalid for %s: model %s, effort %s", agentDebug(c.Agent), c.Model, c.Effort))
	}
	return nil
}
func agentDebug(agent AgentKind) string {
	switch agent {
	case AgentClaude:
		return "Claude"
	case AgentCodex:
		return "Codex"
	default:
		return string(agent)
	}
}
func validatePstackRoles(roles PstackRoleTable) error {
	if len(roles) != 4 {
		return DomainError("pstack role table must contain each supported role exactly once")
	}
	expected := map[string]bool{"code-delegate": true, "judge-and-prose": true, "review-panel": true, "explorers": true}
	for _, role := range roles {
		if !expected[string(role.Role)] {
			return DomainError("pstack role table must contain each supported role exactly once")
		}
		delete(expected, string(role.Role))
		if err := validateGrill(role.Configuration); err != nil {
			return err
		}
	}
	if len(expected) > 0 {
		return DomainError("pstack role table must contain each supported role exactly once")
	}
	return nil
}
func validateProjectDefaults(d ProjectDefaults) error {
	if d.ItemStatus != StatusInbox && d.ItemStatus != StatusActive && d.ItemStatus != StatusWaiting && d.ItemStatus != StatusDone {
		return DomainError(fmt.Sprintf("unknown projects.default_item_status value %q", d.ItemStatus))
	}
	if d.ExecutionMode != ExecutionDirect && d.ExecutionMode != ExecutionWorktree {
		return DomainError(fmt.Sprintf("unknown projects.default_execution_mode value %q", d.ExecutionMode))
	}
	return nil
}
func validateObjectKind(kind ExternalObjectKind) error {
	if kind != ObjectIssue && kind != ObjectPullRequest && kind != ObjectDocument && kind != ObjectGeneric {
		return DomainError(fmt.Sprintf("unknown ExternalObjectKind variant %q; expected issue, pull_request, document, or generic", kind))
	}
	return nil
}
func cleanOptional(value *string) *string {
	if value == nil {
		return nil
	}
	clean := strings.TrimSpace(*value)
	if clean == "" {
		return nil
	}
	return &clean
}
