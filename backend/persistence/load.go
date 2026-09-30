package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

// Load reads one coherent in-memory snapshot. Unknown persisted enum values and
// malformed JSON are errors; known columns left by the Rust app are ignored.
func (s *Store) Load() (domain.DomainState, error) {
	return s.load(context.Background())
}

func (s *Store) load(ctx context.Context) (domain.DomainState, error) {
	var state domain.DomainState
	sequences := []struct {
		key    string
		target *int64
	}{
		{"next_context_id", &state.NextContextID}, {"next_project_id", &state.NextProjectID}, {"next_item_id", &state.NextItemID}, {"next_item_number", &state.NextItemNumber},
		{"next_repository_id", &state.NextRepositoryID}, {"next_workspace_id", &state.NextWorkspaceID}, {"next_worktree_id", &state.NextWorktreeID}, {"next_machine_id", &state.NextMachineID},
		{"next_cli_profile_id", &state.NextCLIProfileID}, {"next_run_id", &state.NextRunID}, {"next_external_object_id", &state.NextExternalObjectID}, {"next_link_id", &state.NextLinkID},
		{"next_activity_id", &state.NextActivityID}, {"next_reminder_id", &state.NextReminderID}, {"next_audit_id", &state.NextAuditID},
	}
	for _, seq := range sequences {
		if err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = ?`, seq.key).Scan(seq.target); err != nil {
			return state, fmt.Errorf("load sequence %s: %w", seq.key, err)
		}
		if *seq.target < 1 {
			return state, fmt.Errorf("load sequence %s: value must be at least 1 (got %d)", seq.key, *seq.target)
		}
	}
	var err error
	state.Contexts, err = loadContexts(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Projects, err = loadProjects(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Repositories, err = loadRepositories(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.RepositoryLocations, err = loadRepositoryLocations(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Machines, err = loadMachines(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.CLIConfigurationProfiles, err = loadProfiles(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Items, err = loadItems(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Workspaces, err = loadWorkspaces(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Worktrees, err = loadWorktrees(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Runs, err = loadRuns(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.ImplementationQueues, err = loadQueues(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Relationships, err = loadRelationships(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.ExternalObjects, err = loadExternalObjects(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Links, err = loadLinks(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Snapshots, err = loadSnapshots(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.Activities, err = loadActivities(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.AttentionDefaults, err = loadAttentionDefaults(ctx, s.db)
	if err != nil {
		return state, err
	}
	state.AuditEntries, err = loadAudit(ctx, s.db)
	if err != nil {
		return state, err
	}
	if err := loadReminders(ctx, s.db, &state.Items); err != nil {
		return state, err
	}
	if err := loadWorkspaceRepositories(ctx, s.db, &state.Workspaces); err != nil {
		return state, err
	}
	return state, nil
}

type rowScanner interface{ Scan(...any) error }

func rowsOf[T any](ctx context.Context, db *sql.DB, query string, decode func(rowScanner) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]T, 0)
	for rows.Next() {
		v, e := decode(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func boolInt(value int64) bool { return value != 0 }

func loadContexts(ctx context.Context, db *sql.DB) ([]domain.Context, error) {
	return rowsOf(ctx, db, `SELECT id,name,execution_machine_id,check_dirty_checkouts,grill_agent,grill_model,grill_effort,implement_agent,implement_model,implement_effort,default_workflow,pstack_agent,pstack_model,pstack_effort,claude_profile_id,codex_profile_id,gh_executable_path,twg_executable_path,az_executable_path,atlassian_site,azure_devops_organization,bitbucket_workspace,pstack_roles_json FROM contexts ORDER BY id`, func(r rowScanner) (domain.Context, error) {
		var v domain.Context
		var dirty int64
		var ga, ia, pa, wf, roles string
		if err := r.Scan(&v.ID, &v.Name, &v.ExecutionMachineID, &dirty, &ga, &v.GrillDefaults.Model, &v.GrillDefaults.Effort, &ia, &v.ImplementDefaults.Model, &v.ImplementDefaults.Effort, &wf, &pa, &v.PstackDefaults.Model, &v.PstackDefaults.Effort, &v.ClaudeProfileID, &v.CodexProfileID, &v.GHExecutablePath, &v.TWGExecutablePath, &v.AZExecutablePath, &v.AtlassianSite, &v.AzureDevOpsOrganization, &v.BitbucketWorkspace, &roles); err != nil {
			return v, err
		}
		var err error
		if v.GrillDefaults.Agent, err = enum("contexts.grill_agent", ga, domain.AgentClaude, domain.AgentCodex); err != nil {
			return v, err
		}
		if v.ImplementDefaults.Agent, err = enum("contexts.implement_agent", ia, domain.AgentClaude, domain.AgentCodex); err != nil {
			return v, err
		}
		if v.PstackDefaults.Agent, err = enum("contexts.pstack_agent", pa, domain.AgentClaude, domain.AgentCodex); err != nil {
			return v, err
		}
		if v.DefaultWorkflow, err = enum("contexts.default_workflow", wf, domain.WorkflowMattPocock, domain.WorkflowPstack); err != nil {
			return v, err
		}
		v.CheckDirtyCheckouts = boolInt(dirty)
		if roles == "" {
			v.PstackRoles = domain.DefaultPstackRoles()
		} else if v.PstackRoles, err = jsonColumn[domain.PstackRoleTable]("contexts.pstack_roles_json", roles); err != nil {
			return v, err
		}
		for _, role := range v.PstackRoles {
			if _, err = enum("contexts.pstack_roles_json.role", string(role.Role), "code-delegate", "judge-and-prose", "review-panel", "explorers"); err != nil {
				return v, err
			}
			if _, err = enum("contexts.pstack_roles_json.configuration.agent", string(role.Configuration.Agent), domain.AgentClaude, domain.AgentCodex); err != nil {
				return v, err
			}
		}
		return v, nil
	})
}
func loadProjects(ctx context.Context, db *sql.DB) ([]domain.Project, error) {
	return rowsOf(ctx, db, `SELECT id,context_id,name,default_item_status,default_execution_mode FROM projects ORDER BY id`, func(r rowScanner) (domain.Project, error) {
		var v domain.Project
		var st, mode string
		if e := r.Scan(&v.ID, &v.ContextID, &v.Name, &st, &mode); e != nil {
			return v, e
		}
		var e error
		if v.Defaults.ItemStatus, e = enum("projects.default_item_status", st, domain.StatusInbox, domain.StatusActive, domain.StatusWaiting, domain.StatusDone); e != nil {
			return v, e
		}
		v.Defaults.ExecutionMode, e = enum("projects.default_execution_mode", mode, domain.ExecutionDirect, domain.ExecutionWorktree)
		return v, e
	})
}
func loadRepositories(ctx context.Context, db *sql.DB) ([]domain.Repository, error) {
	return rowsOf(ctx, db, `SELECT id,project_id,name,remote_url,base_branch FROM repositories ORDER BY id`, func(r rowScanner) (domain.Repository, error) {
		var v domain.Repository
		e := r.Scan(&v.ID, &v.ProjectID, &v.Name, &v.RemoteURL, &v.BaseBranch)
		return v, e
	})
}
func loadRepositoryLocations(ctx context.Context, db *sql.DB) ([]domain.RepositoryLocation, error) {
	return rowsOf(ctx, db, `SELECT repository_id,machine_id,checkout_path,worktree_root FROM repository_locations ORDER BY repository_id,machine_id`, func(r rowScanner) (domain.RepositoryLocation, error) {
		var v domain.RepositoryLocation
		e := r.Scan(&v.RepositoryID, &v.MachineID, &v.CheckoutPath, &v.WorktreeRoot)
		return v, e
	})
}
func loadMachines(ctx context.Context, db *sql.DB) ([]domain.Machine, error) {
	return rowsOf(ctx, db, `SELECT id,context_id,name,socket_name,transport_json,last_observed,last_observed_at FROM machines ORDER BY id`, func(r rowScanner) (domain.Machine, error) {
		var v domain.Machine
		var raw, obs string
		if e := r.Scan(&v.ID, &v.ContextID, &v.Name, &v.SocketName, &raw, &obs, &v.LastObservedAt); e != nil {
			return v, e
		}
		var e error
		if v.Transport, e = jsonColumn[domain.MachineTransport]("machines.transport_json", raw); e != nil {
			return v, e
		}
		if v.Transport.Kind != "local" && v.Transport.Kind != "ssh" {
			return v, fmt.Errorf("unknown machines.transport_json kind %q", v.Transport.Kind)
		}
		if v.Transport.Kind == "ssh" && (v.Transport.Host == nil || *v.Transport.Host == "") {
			return v, fmt.Errorf("machines.transport_json ssh host is required")
		}
		if v.LastObserved, e = enum("machines.last_observed", obs, domain.MachineObservation("unknown"), domain.MachineObservation("available"), domain.MachineObservation("offline")); e != nil {
			return v, e
		}
		return v, nil
	})
}
func loadProfiles(ctx context.Context, db *sql.DB) ([]domain.CLIConfigurationProfile, error) {
	return rowsOf(ctx, db, `SELECT id,machine_id,provider,name,directory,app_managed FROM cli_configuration_profiles ORDER BY id`, func(r rowScanner) (domain.CLIConfigurationProfile, error) {
		var v domain.CLIConfigurationProfile
		var p string
		var b int64
		if e := r.Scan(&v.ID, &v.MachineID, &p, &v.Name, &v.Directory, &b); e != nil {
			return v, e
		}
		var e error
		v.Provider, e = enum("cli_configuration_profiles.provider", p, domain.AgentClaude, domain.AgentCodex)
		v.AppManaged = boolInt(b)
		return v, e
	})
}
func loadItems(ctx context.Context, db *sql.DB) ([]domain.Item, error) {
	return rowsOf(ctx, db, `SELECT id,human_identifier,title,project_id,status,notes FROM items ORDER BY id`, func(r rowScanner) (domain.Item, error) {
		var v domain.Item
		var st string
		if e := r.Scan(&v.ID, &v.HumanIdentifier, &v.Title, &v.ProjectID, &st, &v.Notes); e != nil {
			return v, e
		}
		v.Reminders = make([]domain.Reminder, 0)
		var e error
		v.Status, e = enum("items.status", st, domain.StatusInbox, domain.StatusActive, domain.StatusWaiting, domain.StatusDone)
		return v, e
	})
}
func loadReminders(ctx context.Context, db *sql.DB, items *[]domain.Item) error {
	rs, e := rowsOf(ctx, db, `SELECT id,item_id,remind_at FROM reminders ORDER BY item_id,id`, func(r rowScanner) (struct {
		item  int64
		value domain.Reminder
	}, error) {
		var v struct {
			item  int64
			value domain.Reminder
		}
		e := r.Scan(&v.value.ID, &v.item, &v.value.RemindAt)
		return v, e
	})
	if e != nil {
		return e
	}
	for _, x := range rs {
		for i := range *items {
			if (*items)[i].ID == x.item {
				(*items)[i].Reminders = append((*items)[i].Reminders, x.value)
				break
			}
		}
	}
	return nil
}
func loadWorkspaces(ctx context.Context, db *sql.DB) ([]domain.Workspace, error) {
	return rowsOf(ctx, db, `SELECT id,item_id,preparation_state FROM workspaces ORDER BY id`, func(r rowScanner) (domain.Workspace, error) {
		var v domain.Workspace
		var p string
		if e := r.Scan(&v.ID, &v.ItemID, &p); e != nil {
			return v, e
		}
		v.Repositories = make([]domain.WorkspaceRepository, 0)
		var e error
		v.PreparationState, e = enum("workspaces.preparation_state", p, domain.WorkspacePending, domain.WorkspaceResumable, domain.WorkspaceReady)
		return v, e
	})
}
func loadWorkspaceRepositories(ctx context.Context, db *sql.DB, workspaces *[]domain.Workspace) error {
	rs, e := rowsOf(ctx, db, `SELECT workspace_id,repository_id,branch,base_branch FROM workspace_repositories ORDER BY workspace_id,repository_id`, func(r rowScanner) (struct {
		id    int64
		value domain.WorkspaceRepository
	}, error) {
		var v struct {
			id    int64
			value domain.WorkspaceRepository
		}
		e := r.Scan(&v.id, &v.value.RepositoryID, &v.value.Branch, &v.value.BaseBranch)
		return v, e
	})
	if e != nil {
		return e
	}
	for _, x := range rs {
		for i := range *workspaces {
			if (*workspaces)[i].ID == x.id {
				(*workspaces)[i].Repositories = append((*workspaces)[i].Repositories, x.value)
				break
			}
		}
	}
	return nil
}
func loadWorktrees(ctx context.Context, db *sql.DB) ([]domain.Worktree, error) {
	return rowsOf(ctx, db, `SELECT id,workspace_id,repository_id,machine_id,path,branch,base_branch,is_dirty FROM worktrees ORDER BY id`, func(r rowScanner) (domain.Worktree, error) {
		var v domain.Worktree
		var b int64
		e := r.Scan(&v.ID, &v.WorkspaceID, &v.RepositoryID, &v.MachineID, &v.Path, &v.Branch, &v.BaseBranch, &b)
		v.IsDirty = boolInt(b)
		return v, e
	})
}
func loadRelationships(ctx context.Context, db *sql.DB) ([]domain.ItemRelation, error) {
	return rowsOf(ctx, db, `SELECT from_item_id,to_item_id,kind FROM item_relationships ORDER BY from_item_id,to_item_id,kind`, func(r rowScanner) (domain.ItemRelation, error) {
		var v domain.ItemRelation
		var k string
		if e := r.Scan(&v.FromItemID, &v.ToItemID, &k); e != nil {
			return v, e
		}
		var e error
		switch k {
		case "blocks":
			v.Kind = domain.RelationBlocks
		case "blocked_by":
			v.Kind = domain.RelationBlockedBy
		case "related_to":
			v.Kind = domain.RelationRelatedTo
		default:
			e = fmt.Errorf("unknown item_relationships.kind value %q", k)
		}
		return v, e
	})
}
func loadExternalObjects(ctx context.Context, db *sql.DB) ([]domain.ExternalObject, error) {
	return rowsOf(ctx, db, `SELECT id,provider,kind,external_key,canonical_url FROM external_objects ORDER BY id`, func(r rowScanner) (domain.ExternalObject, error) {
		var v domain.ExternalObject
		var p, k string
		if e := r.Scan(&v.ID, &p, &k, &v.ExternalKey, &v.CanonicalURL); e != nil {
			return v, e
		}
		var e error
		if v.Provider, e = enum("external_objects.provider", p, domain.ProviderGitHub, domain.ProviderAtlassian, domain.ProviderAzureDevOps, domain.ProviderGeneric); e != nil {
			return v, e
		}
		v.Kind, e = enum("external_objects.kind", k, domain.ObjectIssue, domain.ObjectPullRequest, domain.ObjectDocument, domain.ObjectGeneric)
		return v, e
	})
}
func loadAttentionDefaults(ctx context.Context, db *sql.DB) ([]domain.ContextAttentionDefault, error) {
	return rowsOf(ctx, db, `SELECT context_id,object_kind,title_attention,state_attention,metadata_attention FROM context_attention_defaults ORDER BY context_id,object_kind`, func(r rowScanner) (domain.ContextAttentionDefault, error) {
		var v domain.ContextAttentionDefault
		var k string
		var a, b, c int64
		if e := r.Scan(&v.ContextID, &k, &a, &b, &c); e != nil {
			return v, e
		}
		var e error
		v.ObjectKind, e = enum("context_attention_defaults.object_kind", k, domain.ObjectIssue, domain.ObjectPullRequest, domain.ObjectDocument, domain.ObjectGeneric)
		v.Policy = domain.ExternalChangePolicy{Title: boolInt(a), State: boolInt(b), Metadata: boolInt(c)}
		return v, e
	})
}

func loadRuns(ctx context.Context, db *sql.DB) ([]domain.Run, error) {
	return rowsOf(ctx, db, `SELECT id,item_id,workspace_id,repository_id,worktree_id,machine_id,agent,execution_profile,model,effort,skill_snapshot,prompt,working_directory,session_name,pane_id,started_at,state,last_applied_agent_state_sequence,pane_status,direct_checkouts_json,transcript,grill_question_group_json,grill_answers_json,grill_decisions_json,grill_response,grill_phase,grill_action,grill_action_started_at,cli_configuration_profile_json,workflow,reported_pull_requests_json,attention_summary,plan_phase,plan_path FROM runs ORDER BY id`, func(r rowScanner) (domain.Run, error) {
		var v domain.Run
		var agent, profile, state, pane, direct, workflow, reported, answers, decisions string
		var grillQuestion, grillPhase, grillAction, cliProfile, planPhase sql.NullString
		if err := r.Scan(&v.ID, &v.ItemID, &v.WorkspaceID, &v.RepositoryID, &v.WorktreeID, &v.MachineID, &agent, &profile, &v.Model, &v.Effort, &v.SkillSnapshot, &v.Prompt, &v.WorkingDirectory, &v.SessionName, &v.PaneID, &v.StartedAt, &state, &v.LastAppliedAgentStateSequence, &pane, &direct, &v.Transcript, &grillQuestion, &answers, &decisions, &v.GrillResponse, &grillPhase, &grillAction, &v.GrillActionStartedAt, &cliProfile, &workflow, &reported, &v.AttentionSummary, &planPhase, &v.PlanPath); err != nil {
			return v, err
		}
		var err error
		if v.Agent, err = enum("runs.agent", agent, domain.AgentClaude, domain.AgentCodex); err != nil {
			return v, err
		}
		if v.ExecutionProfile, err = enum("runs.execution_profile", profile, domain.ExecutionProfile("investigate"), domain.ExecutionProfile("implement"), domain.ExecutionProfile("review"), domain.ExecutionProfile("custom"), domain.ExecutionProfile("grill"), domain.ExecutionProfile("autonomous"), domain.ExecutionProfile("plan"), domain.ExecutionProfile("pstack-review")); err != nil {
			return v, err
		}
		if v.State, err = enum("runs.state", state, domain.RunUnknown, domain.RunWorking, domain.RunBlocked, domain.RunFinished); err != nil {
			return v, err
		}
		if v.PaneStatus, err = enum("runs.pane_status", pane, domain.PaneUnknown, domain.PaneAvailable, domain.PaneMissing); err != nil {
			return v, err
		}
		if v.Workflow, err = enum("runs.workflow", workflow, domain.WorkflowMattPocock, domain.WorkflowPstack); err != nil {
			return v, err
		}
		if v.DirectCheckouts, err = jsonColumn[[]domain.RunCheckout]("runs.direct_checkouts_json", direct); err != nil {
			return v, err
		}
		if v.ReportedPullRequests, err = jsonColumn[[]string]("runs.reported_pull_requests_json", reported); err != nil {
			return v, err
		}
		if v.GrillAnswers, err = jsonColumn[[]domain.GrillAnswer]("runs.grill_answers_json", answers); err != nil {
			return v, err
		}
		if v.GrillDecisions, err = jsonColumn[[]domain.GrillAnswer]("runs.grill_decisions_json", decisions); err != nil {
			return v, err
		}
		if v.GrillQuestionGroup, err = nullableJSON[domain.GrillQuestionGroup]("runs.grill_question_group_json", grillQuestion); err != nil {
			return v, err
		}
		if v.CLIConfigurationProfile, err = nullableJSON[domain.CLIConfigurationProfileIdentity]("runs.cli_configuration_profile_json", cliProfile); err != nil {
			return v, err
		}
		if v.CLIConfigurationProfile != nil {
			if _, err = enum("runs.cli_configuration_profile_json.provider", string(v.CLIConfigurationProfile.Provider), domain.AgentClaude, domain.AgentCodex); err != nil {
				return v, err
			}
		}
		if grillPhase.Valid {
			q, e := enum("runs.grill_phase", grillPhase.String, domain.GrillPhase("starting"), domain.GrillPhase("working"), domain.GrillPhase("waiting_for_answers"), domain.GrillPhase("awaiting_next_action"), domain.GrillPhase("recoverable_pane_loss"), domain.GrillPhase("finished"))
			if e != nil {
				return v, e
			}
			v.GrillPhase = &q
		}
		if grillAction.Valid {
			q, e := enum("runs.grill_action", grillAction.String, domain.GrillContinuationAction("to-spec"), domain.GrillContinuationAction("to-tickets"), domain.GrillContinuationAction("implement"))
			if e != nil {
				return v, e
			}
			v.GrillAction = &q
		}
		if planPhase.Valid {
			q, e := enum("runs.plan_phase", planPhase.String, domain.PlanPhase("awaiting_go"), domain.PlanPhase("executing"))
			if e != nil {
				return v, e
			}
			v.PlanPhase = &q
		}
		return v, nil
	})
}

func loadQueues(ctx context.Context, db *sql.DB) ([]domain.ImplementationQueue, error) {
	return rowsOf(ctx, db, `SELECT queue_json FROM implementation_queues ORDER BY id`, func(r rowScanner) (domain.ImplementationQueue, error) {
		var raw string
		if e := r.Scan(&raw); e != nil {
			return domain.ImplementationQueue{}, e
		}
		v, e := jsonColumn[domain.ImplementationQueue]("implementation_queues.queue_json", raw)
		if e != nil {
			return v, e
		}
		if _, e = enum("implementation_queues.queue_json.configuration.agent", string(v.Configuration.Agent), domain.AgentClaude, domain.AgentCodex); e != nil {
			return v, e
		}
		if v.PausedReason != nil {
			if _, e = enum("implementation_queues.queue_json.pausedReason.kind", string(v.PausedReason.Kind), "ticket_still_open", "checkout_dirty", "run_stopped", "pane_missing", "launch_failed"); e != nil {
				return v, e
			}
			if (v.PausedReason.Kind == "launch_failed") != (v.PausedReason.Message != nil) {
				return v, fmt.Errorf("invalid implementation queue pause reason message")
			}
		}
		return v, nil
	})
}
func loadLinks(ctx context.Context, db *sql.DB) ([]domain.Link, error) {
	return rowsOf(ctx, db, `SELECT l.id,l.item_id,l.external_object_id,COALESCE(a.reviewed_activity_id,0),a.title_attention,a.state_attention,a.metadata_attention,a.watch_until,a.review_at,a.provenance_json,l.purpose,l.spec_external_object_id FROM external_links l LEFT JOIN link_attention_state a ON a.link_id=l.id ORDER BY l.id`, func(r rowScanner) (domain.Link, error) {
		var v domain.Link
		var title, state, metadata sql.NullInt64
		var watch, review, provenance sql.NullString
		var purpose string
		if e := r.Scan(&v.ID, &v.ItemID, &v.ExternalObjectID, &v.ReviewedActivityID, &title, &state, &metadata, &watch, &review, &provenance, &purpose, &v.SpecExternalObjectID); e != nil {
			return v, e
		}
		var e error
		if v.Purpose, e = enum("external_links.purpose", purpose, domain.LinkPurpose("to-spec"), domain.LinkPurpose("to-tickets"), domain.LinkPurpose("others")); e != nil {
			return v, e
		}
		attention := domain.LinkAttentionState{LinkID: v.ID, ReviewedActivityID: v.ReviewedActivityID}
		if title.Valid && state.Valid && metadata.Valid {
			attention.AttentionPolicy = &domain.ExternalChangePolicy{Title: title.Int64 != 0, State: state.Int64 != 0, Metadata: metadata.Int64 != 0}
		}
		if watch.Valid {
			attention.WatchUntil = &watch.String
		}
		if review.Valid {
			attention.ReviewAt = &review.String
		}
		if attention.Provenance, e = nullableJSON[domain.LinkProvenance]("link_attention_state.provenance_json", provenance); e != nil {
			return v, e
		}
		if attention.Provenance != nil {
			if _, e = enum("link_attention_state.provenance_json.action", string(attention.Provenance.Action), "to-spec", "to-tickets", "implement"); e != nil {
				return v, e
			}
			if _, e = enum("link_attention_state.provenance_json.discovery", attention.Provenance.Discovery, "structured-event", "output-url"); e != nil {
				return v, e
			}
		}
		v.ReviewedActivityID = attention.ReviewedActivityID
		v.AttentionPolicy = attention.AttentionPolicy
		v.WatchUntil = attention.WatchUntil
		v.ReviewAt = attention.ReviewAt
		v.Provenance = attention.Provenance
		return v, nil
	})
}
func loadSnapshots(ctx context.Context, db *sql.DB) ([]domain.ExternalSnapshot, error) {
	return rowsOf(ctx, db, `SELECT external_object_id,title,state,metadata_json,fetched_at FROM external_snapshots ORDER BY external_object_id`, func(r rowScanner) (domain.ExternalSnapshot, error) {
		var v domain.ExternalSnapshot
		var raw string
		if e := r.Scan(&v.ExternalObjectID, &v.Title, &v.State, &raw, &v.FetchedAt); e != nil {
			return v, e
		}
		var e error
		v.Metadata, e = jsonColumn[[]domain.ExternalMetadata]("external_snapshots.metadata_json", raw)
		return v, e
	})
}
func loadActivities(ctx context.Context, db *sql.DB) ([]domain.Activity, error) {
	return rowsOf(ctx, db, `SELECT id,external_object_id,observed_at,changes_json FROM activities ORDER BY id`, func(r rowScanner) (domain.Activity, error) {
		var v domain.Activity
		var raw string
		if e := r.Scan(&v.ID, &v.ExternalObjectID, &v.ObservedAt, &raw); e != nil {
			return v, e
		}
		var e error
		v.Changes, e = jsonColumn[[]domain.ExternalChange]("activities.changes_json", raw)
		if e != nil {
			return v, e
		}
		for i := range v.Changes {
			v.Changes[i].Kind, e = enum("activities.changes_json.kind", string(v.Changes[i].Kind), domain.ExternalChangeKind("title"), domain.ExternalChangeKind("state"), domain.ExternalChangeKind("metadata"))
			if e != nil {
				return v, e
			}
		}
		return v, nil
	})
}
