// Package domain contains the shared in-memory model loaded from SQLite.
package domain

import "encoding/json"

type AgentKind string
type Workflow string
type ItemStatus string
type ExecutionMode string
type WorkspacePreparationState string
type RunState string
type RunPaneStatus string
type ExecutionProfile string
type GrillPhase string
type PlanPhase string
type MachineObservation string
type MachineTransportKind string
type ExternalProvider string
type ExternalObjectKind string
type ItemRelationKind string
type LinkPurpose string
type ExternalChangeKind string
type GrillContinuationAction string
type ImplementationQueuePauseReasonKind string
type AttentionEntryKind string
type RunLaunchTargetKind string

const (
	AgentClaude                  AgentKind                 = "claude"
	AgentCodex                   AgentKind                 = "codex"
	WorkflowMattPocock           Workflow                  = "matt-pocock"
	WorkflowPstack               Workflow                  = "pstack"
	StatusInbox                  ItemStatus                = "Inbox"
	StatusActive                 ItemStatus                = "Active"
	StatusWaiting                ItemStatus                = "Waiting"
	StatusDone                   ItemStatus                = "Done"
	ExecutionDirect              ExecutionMode             = "direct"
	ExecutionWorktree            ExecutionMode             = "worktree"
	WorkspacePending             WorkspacePreparationState = "pending"
	WorkspaceResumable           WorkspacePreparationState = "resumable"
	WorkspaceReady               WorkspacePreparationState = "ready"
	RunUnknown                   RunState                  = "unknown"
	RunWorking                   RunState                  = "working"
	RunBlocked                   RunState                  = "blocked"
	RunFinished                  RunState                  = "finished"
	RelationBlocks               ItemRelationKind          = "Blocks"
	RelationBlockedBy            ItemRelationKind          = "BlockedBy"
	RelationRelatedTo            ItemRelationKind          = "RelatedTo"
	AttentionExternalChange      AttentionEntryKind        = "external_change"
	AttentionReview              AttentionEntryKind        = "review"
	AttentionReminder            AttentionEntryKind        = "reminder"
	AttentionBlockedRun          AttentionEntryKind        = "blocked_run"
	AttentionImplementationQueue AttentionEntryKind        = "ImplementationQueue"
	PaneUnknown                  RunPaneStatus             = "unknown"
	PaneAvailable                RunPaneStatus             = "available"
	PaneMissing                  RunPaneStatus             = "missing"
	ExecutionProfileInvestigate  ExecutionProfile          = "investigate"
	ExecutionProfileImplement    ExecutionProfile          = "implement"
	ExecutionProfileReview       ExecutionProfile          = "review"
	ExecutionProfileCustomPrompt ExecutionProfile          = "custom"
	ExecutionProfileAutonomous   ExecutionProfile          = "autonomous"
	ExecutionProfilePlan         ExecutionProfile          = "plan"
	ExecutionProfilePstackReview ExecutionProfile          = "pstack-review"
	ExecutionProfileGrill        ExecutionProfile          = "grill"
	RunTargetCheckout            RunLaunchTargetKind       = "checkout"
	RunTargetWorktree            RunLaunchTargetKind       = "worktree"
	TransportLocal               MachineTransportKind      = "local"
	TransportSSH                 MachineTransportKind      = "ssh"
)

type RunPromptSelection struct {
	IncludeObjective  bool    `json:"includeObjective"`
	ExternalObjectIDs []int64 `json:"externalObjectIds"`
}

type RunLaunchProfileOption struct {
	ExecutionProfile      ExecutionProfile   `json:"executionProfile"`
	Configuration         GrillConfiguration `json:"configuration"`
	RequiresInitialPrompt bool               `json:"requiresInitialPrompt"`
}

type RunLaunchWorkflowOptions struct {
	Workflow       Workflow                 `json:"workflow"`
	DefaultProfile ExecutionProfile         `json:"defaultProfile"`
	Profiles       []RunLaunchProfileOption `json:"profiles"`
}

type RunLaunchOptions struct {
	DefaultWorkflow Workflow                   `json:"defaultWorkflow"`
	Workflows       []RunLaunchWorkflowOptions `json:"workflows"`
}

type GrillConfiguration struct {
	Agent  AgentKind `json:"agent"`
	Model  string    `json:"model"`
	Effort string    `json:"effort"`
}
type PstackRole string
type PstackRoleConfiguration struct {
	Role          PstackRole         `json:"role"`
	Configuration GrillConfiguration `json:"configuration"`
}
type PstackRoleTable []PstackRoleConfiguration

func DefaultPstackRoles() PstackRoleTable {
	return PstackRoleTable{
		{Role: "code-delegate", Configuration: GrillConfiguration{Agent: AgentClaude, Model: "claude-opus-5", Effort: "high"}},
		{Role: "judge-and-prose", Configuration: GrillConfiguration{Agent: AgentCodex, Model: "gpt-6-sol", Effort: "high"}},
		{Role: "review-panel", Configuration: GrillConfiguration{Agent: AgentCodex, Model: "gpt-6-sol", Effort: "high"}},
		{Role: "explorers", Configuration: GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "medium"}},
	}
}

type Context struct {
	ID                      int64              `json:"id"`
	Name                    string             `json:"name"`
	ExecutionMachineID      *int64             `json:"execution_machine_id"`
	ClaudeProfileID         *int64             `json:"claude_profile_id"`
	CodexProfileID          *int64             `json:"codex_profile_id"`
	CheckDirtyCheckouts     bool               `json:"check_dirty_checkouts"`
	GrillDefaults           GrillConfiguration `json:"grill_defaults"`
	ImplementDefaults       GrillConfiguration `json:"implement_defaults"`
	DefaultWorkflow         Workflow           `json:"default_workflow"`
	PstackDefaults          GrillConfiguration `json:"pstack_defaults"`
	PstackRoles             PstackRoleTable    `json:"pstack_roles"`
	GHExecutablePath        *string            `json:"gh_executable_path"`
	TWGExecutablePath       *string            `json:"twg_executable_path"`
	AZExecutablePath        *string            `json:"az_executable_path"`
	AtlassianSite           *string            `json:"atlassian_site"`
	AzureDevOpsOrganization *string            `json:"azure_devops_organization"`
	BitbucketWorkspace      *string            `json:"bitbucket_workspace"`
}
type ProjectDefaults struct {
	ItemStatus    ItemStatus    `json:"item_status"`
	ExecutionMode ExecutionMode `json:"execution_mode"`
}
type Project struct {
	ID        int64           `json:"id"`
	ContextID int64           `json:"context_id"`
	Name      string          `json:"name"`
	Defaults  ProjectDefaults `json:"defaults"`
}
type Repository struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"project_id"`
	Name       string `json:"name"`
	RemoteURL  string `json:"remote_url"`
	BaseBranch string `json:"base_branch"`
}
type RepositoryLocation struct {
	RepositoryID int64  `json:"repository_id"`
	MachineID    int64  `json:"machine_id"`
	CheckoutPath string `json:"checkout_path"`
	WorktreeRoot string `json:"worktree_root"`
}
type WorkspaceRepository struct {
	RepositoryID int64  `json:"repositoryId"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"baseBranch"`
}
type Workspace struct {
	ID               int64                     `json:"id"`
	ItemID           int64                     `json:"item_id"`
	Repositories     []WorkspaceRepository     `json:"repositories"`
	PreparationState WorkspacePreparationState `json:"preparation_state"`
}
type Worktree struct {
	ID           int64  `json:"id"`
	WorkspaceID  int64  `json:"workspaceId"`
	RepositoryID int64  `json:"repositoryId"`
	MachineID    int64  `json:"machineId"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"baseBranch"`
	IsDirty      bool   `json:"isDirty"`
}
type MachineTransport struct {
	Kind                  MachineTransportKind `json:"kind"`
	Host                  *string              `json:"host,omitempty"`
	User                  *string              `json:"user,omitempty"`
	Port                  *uint16              `json:"port,omitempty"`
	IdentityFile          *string              `json:"identity_file,omitempty"`
	KnownHostsFile        *string              `json:"known_hosts_file,omitempty"`
	StrictHostKeyChecking *string              `json:"strict_host_key_checking,omitempty"`
}
type Machine struct {
	ID             int64              `json:"id"`
	ContextID      int64              `json:"context_id"`
	Name           string             `json:"name"`
	SocketName     string             `json:"socket_name"`
	Transport      MachineTransport   `json:"transport"`
	LastObserved   MachineObservation `json:"last_observed"`
	LastObservedAt *int64             `json:"last_observed_at"`
}
type CLIConfigurationProfile struct {
	ID         int64     `json:"id"`
	MachineID  int64     `json:"machineId"`
	Provider   AgentKind `json:"provider"`
	Name       string    `json:"name"`
	Directory  string    `json:"directory"`
	AppManaged bool      `json:"app_managed"`
}
type CLIConfigurationProfileIdentity struct {
	ProfileID int64     `json:"profileId"`
	Provider  AgentKind `json:"provider"`
	Name      string    `json:"name"`
}
type Reminder struct {
	ID       int64  `json:"id"`
	RemindAt string `json:"remind_at"`
}
type Item struct {
	ID              int64      `json:"id"`
	HumanIdentifier string     `json:"human_identifier"`
	Title           string     `json:"title"`
	ProjectID       int64      `json:"project_id"`
	Status          ItemStatus `json:"status"`
	Notes           string     `json:"notes"`
	Reminders       []Reminder `json:"reminders"`
}
type ItemRelation struct {
	FromItemID int64            `json:"from_item_id"`
	ToItemID   int64            `json:"to_item_id"`
	Kind       ItemRelationKind `json:"kind"`
}
type ImplementationQueueEntry struct {
	Position     int64  `json:"position"`
	TicketNumber int64  `json:"ticketNumber"`
	TicketTitle  string `json:"ticketTitle"`
	TicketURL    string `json:"ticketUrl"`
	TicketState  string `json:"ticketState"`
	RunID        *int64 `json:"runId"`
	Done         bool   `json:"done"`
	Skipped      bool   `json:"skipped"`
}
type ImplementationQueuePauseReason struct {
	Kind    ImplementationQueuePauseReasonKind `json:"kind"`
	Message *string                            `json:"message,omitempty"`
}
type ImplementationQueue struct {
	ID                   int64                           `json:"id"`
	ItemID               int64                           `json:"itemId"`
	SpecExternalObjectID int64                           `json:"specExternalObjectId"`
	SpecURL              string                          `json:"specUrl"`
	WorkspaceID          int64                           `json:"workspaceId"`
	RepositoryID         int64                           `json:"repositoryId"`
	Configuration        GrillConfiguration              `json:"configuration"`
	AllowDirty           bool                            `json:"allowDirty"`
	AllowSharedCheckouts bool                            `json:"allowSharedCheckouts"`
	Entries              []ImplementationQueueEntry      `json:"entries"`
	Active               bool                            `json:"active"`
	PausedReason         *ImplementationQueuePauseReason `json:"pausedReason"`
}
type RunCheckout struct {
	RepositoryID int64  `json:"repositoryId"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	IsDirty      bool   `json:"isDirty"`
}
type GrillOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}
type GrillQuestion struct {
	Number         uint32        `json:"number"`
	Title          *string       `json:"title"`
	Prompt         string        `json:"prompt"`
	Recommendation *string       `json:"recommendation"`
	Options        []GrillOption `json:"options"`
}
type GrillQuestionGroup struct {
	Round     uint            `json:"round"`
	Questions []GrillQuestion `json:"questions"`
}
type GrillAnswer struct {
	QuestionNumber uint32 `json:"questionNumber"`
	Answer         string `json:"answer"`
}
type Run struct {
	ID                            int64                            `json:"id"`
	ItemID                        int64                            `json:"item_id"`
	WorkspaceID                   *int64                           `json:"workspace_id"`
	RepositoryID                  *int64                           `json:"repository_id"`
	WorktreeID                    *int64                           `json:"worktree_id"`
	MachineID                     int64                            `json:"machine_id"`
	Agent                         AgentKind                        `json:"agent"`
	CLIConfigurationProfile       *CLIConfigurationProfileIdentity `json:"cli_configuration_profile"`
	ExecutionProfile              ExecutionProfile                 `json:"execution_profile"`
	Workflow                      Workflow                         `json:"workflow"`
	Model                         *string                          `json:"model"`
	Effort                        *string                          `json:"effort"`
	SkillSnapshot                 *string                          `json:"skill_snapshot"`
	Prompt                        string                           `json:"prompt"`
	WorkingDirectory              string                           `json:"working_directory"`
	SessionName                   string                           `json:"session_name"`
	PaneID                        string                           `json:"pane_id"`
	StartedAt                     int64                            `json:"started_at"`
	State                         RunState                         `json:"state"`
	LastAppliedAgentStateSequence *int64                           `json:"last_applied_agent_state_sequence"`
	PaneStatus                    RunPaneStatus                    `json:"pane_status"`
	DirectCheckouts               []RunCheckout                    `json:"direct_checkouts"`
	Transcript                    string                           `json:"transcript"`
	DownstreamIssueCandidates     []DownstreamIssueCandidate       `json:"downstream_issue_candidates"`
	ReportedPullRequests          []string                         `json:"reported_pull_requests"`
	AttentionSummary              *string                          `json:"attention_summary"`
	GrillQuestionGroup            *GrillQuestionGroup              `json:"grill_question_group"`
	GrillAnswers                  []GrillAnswer                    `json:"grill_answers"`
	GrillDecisions                []GrillAnswer                    `json:"grill_decisions"`
	GrillResponse                 *string                          `json:"grill_response"`
	GrillPhase                    *GrillPhase                      `json:"grill_phase"`
	GrillAction                   *GrillContinuationAction         `json:"grill_action"`
	GrillActionStartedAt          *int64                           `json:"grill_action_started_at"`
	PlanPhase                     *PlanPhase                       `json:"plan_phase"`
	PlanPath                      *string                          `json:"plan_path"`
}

const (
	ProviderGitHub      ExternalProvider = "github"
	ProviderAtlassian   ExternalProvider = "atlassian"
	ProviderAzureDevOps ExternalProvider = "azure_dev_ops"
	ProviderGeneric     ExternalProvider = "generic"
)
const (
	ObjectIssue       ExternalObjectKind = "issue"
	ObjectPullRequest ExternalObjectKind = "pull_request"
	ObjectDocument    ExternalObjectKind = "document"
	ObjectGeneric     ExternalObjectKind = "generic"
)

type ExternalObject struct {
	ID           int64              `json:"id"`
	Provider     ExternalProvider   `json:"provider"`
	Kind         ExternalObjectKind `json:"kind"`
	ExternalKey  string             `json:"external_key"`
	CanonicalURL string             `json:"canonical_url"`
}
type LinkProvenance struct {
	RunID     int64                   `json:"run_id"`
	Action    GrillContinuationAction `json:"action"`
	Discovery string                  `json:"discovery"`
	Ordinal   *uint                   `json:"ordinal"`
	BlockedBy []string                `json:"blocked_by"`
}
type LinkAttentionOverrides struct {
	TitleAttention    *bool `json:"title_attention,omitempty"`
	StateAttention    *bool `json:"state_attention,omitempty"`
	MetadataAttention *bool `json:"metadata_attention,omitempty"`
}
type ExternalChangePolicy struct {
	Title    bool `json:"title"`
	State    bool `json:"state"`
	Metadata bool `json:"metadata"`
}
type Link struct {
	ID                 int64                 `json:"id"`
	ItemID             int64                 `json:"item_id"`
	ExternalObjectID   int64                 `json:"external_object_id"`
	ReviewedActivityID int64                 `json:"reviewed_activity_id"`
	AttentionPolicy    *ExternalChangePolicy `json:"attention_policy"`
	LinkAttentionOverrides
	WatchUntil           *string         `json:"watch_until"`
	ReviewAt             *string         `json:"review_at"`
	Purpose              LinkPurpose     `json:"purpose"`
	SpecExternalObjectID *int64          `json:"spec_external_object_id"`
	Provenance           *LinkProvenance `json:"provenance"`
}
type LinkAttentionState struct {
	LinkAttentionOverrides
	LinkID             int64           `json:"link_id"`
	ReviewedActivityID int64           `json:"reviewed_activity_id"`
	WatchUntil         *string         `json:"watch_until"`
	ReviewAt           *string         `json:"review_at"`
	Provenance         *LinkProvenance `json:"provenance"`
}

type ExternalObjectDeletionPreview struct {
	StateFingerprint string                     `json:"state_fingerprint"`
	Plan             ExternalObjectDeletionPlan `json:"plan"`
}
type ExternalObjectDeletionPlan struct {
	ExternalObjectID   int64              `json:"externalObjectId"`
	Provider           ExternalProvider   `json:"provider"`
	Kind               ExternalObjectKind `json:"kind"`
	ExternalKey        string             `json:"externalKey"`
	CanonicalURL       string             `json:"canonicalUrl"`
	LinkIDs            []int64            `json:"linkIds"`
	ClearedSpecLinkIDs []int64            `json:"clearedSpecLinkIds"`
	SnapshotCount      int                `json:"snapshotCount"`
	ActivityCount      int                `json:"activityCount"`
}
type ExternalLinkDeletionResult struct {
	LinkID                int64 `json:"linkId"`
	ExternalObjectID      int64 `json:"externalObjectId"`
	ExternalObjectDeleted bool  `json:"externalObjectDeleted"`
}
type ExternalMetadata struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type ExternalSnapshot struct {
	ExternalObjectID int64              `json:"external_object_id"`
	Title            string             `json:"title"`
	State            string             `json:"state"`
	Metadata         []ExternalMetadata `json:"metadata"`
	FetchedAt        int64              `json:"fetched_at"`
}
type ExternalChange struct {
	Kind     ExternalChangeKind `json:"kind"`
	Key      *string            `json:"key"`
	Previous *string            `json:"previous"`
	Current  *string            `json:"current"`
}
type Activity struct {
	ID               int64            `json:"id"`
	ExternalObjectID int64            `json:"external_object_id"`
	ObservedAt       int64            `json:"observed_at"`
	Changes          []ExternalChange `json:"changes"`
}
type AttentionEntry struct {
	Kind             AttentionEntryKind `json:"kind"`
	LinkID           int64              `json:"link_id"`
	ReminderID       *int64             `json:"reminder_id"`
	RunID            *int64             `json:"run_id"`
	QueueID          *int64             `json:"queue_id"`
	ItemID           int64              `json:"item_id"`
	ExternalObjectID int64              `json:"external_object_id"`
	SourceTitle      string             `json:"source_title"`
	SourceURL        string             `json:"source_url"`
	Activities       []Activity         `json:"activities"`
	Summary          string             `json:"summary"`
}
type ExternalLinkView struct {
	Link                         Link                 `json:"link"`
	Object                       ExternalObject       `json:"object"`
	Snapshot                     *ExternalSnapshot    `json:"snapshot"`
	AttentionPolicy              ExternalChangePolicy `json:"attention_policy"`
	AttentionEntry               *AttentionEntry      `json:"attention_entry"`
	SupportsImplementationSpec   bool                 `json:"supports_implementation_spec"`
	SupportsImplementationTicket bool                 `json:"supports_implementation_ticket"`
}
type ItemRunSignals struct {
	GrillWaiting bool `json:"grillWaiting"`
	RunActive    bool `json:"runActive"`
}
type RunContinuations struct {
	GoPlan       bool                      `json:"goPlan"`
	GrillActions []GrillContinuationAction `json:"grillActions"`
	Stop         bool                      `json:"stop"`
	Finish       bool                      `json:"finish"`
	Delete       bool                      `json:"delete"`
}
type RunProjection struct {
	RunID         int64            `json:"runId"`
	Status        string           `json:"status"`
	Phase         string           `json:"phase"`
	Continuations RunContinuations `json:"continuations"`
}
type ItemView struct {
	Item                 Item                  `json:"item"`
	ContextID            int64                 `json:"context_id"`
	ContextName          string                `json:"context_name"`
	ProjectName          string                `json:"project_name"`
	Relationships        []ItemRelation        `json:"relationships"`
	Workspaces           []Workspace           `json:"workspaces"`
	Worktrees            []Worktree            `json:"worktrees"`
	Runs                 []Run                 `json:"runs"`
	RunProjections       []RunProjection       `json:"run_projections"`
	RunSignals           ItemRunSignals        `json:"run_signals"`
	ImplementationQueues []ImplementationQueue `json:"implementation_queues"`
	Links                []ExternalLinkView    `json:"links"`
}
type HomeView struct {
	NeedsAttention   []ItemView       `json:"needs_attention"`
	AttentionEntries []AttentionEntry `json:"attention_entries"`
	Running          []ItemView       `json:"running"`
	Waiting          []ItemView       `json:"waiting"`
	Due              []ItemView       `json:"due"`
	Completed        []ItemView       `json:"completed"`
}
type ContextAttentionDefault struct {
	ContextID  int64                `json:"context_id"`
	ObjectKind ExternalObjectKind   `json:"object_kind"`
	Policy     ExternalChangePolicy `json:"policy"`
}
type AuditEntry struct {
	ID         int64           `json:"id"`
	RecordedAt int64           `json:"recorded_at"`
	Action     json.RawMessage `json:"action"`
}

type DomainState struct {
	NextContextID            int64                     `json:"next_context_id"`
	NextProjectID            int64                     `json:"next_project_id"`
	NextItemID               int64                     `json:"next_item_id"`
	NextItemNumber           int64                     `json:"next_item_number"`
	NextRepositoryID         int64                     `json:"next_repository_id"`
	NextWorkspaceID          int64                     `json:"next_workspace_id"`
	NextWorktreeID           int64                     `json:"next_worktree_id"`
	NextMachineID            int64                     `json:"next_machine_id"`
	NextCLIProfileID         int64                     `json:"next_cli_profile_id"`
	NextRunID                int64                     `json:"next_run_id"`
	NextExternalObjectID     int64                     `json:"next_external_object_id"`
	NextLinkID               int64                     `json:"next_link_id"`
	NextActivityID           int64                     `json:"next_activity_id"`
	NextReminderID           int64                     `json:"next_reminder_id"`
	NextAuditID              int64                     `json:"next_audit_id"`
	Contexts                 []Context                 `json:"contexts"`
	Projects                 []Project                 `json:"projects"`
	Repositories             []Repository              `json:"repositories"`
	RepositoryLocations      []RepositoryLocation      `json:"repository_locations"`
	Items                    []Item                    `json:"items"`
	Workspaces               []Workspace               `json:"workspaces"`
	Worktrees                []Worktree                `json:"worktrees"`
	Machines                 []Machine                 `json:"machines"`
	CLIConfigurationProfiles []CLIConfigurationProfile `json:"cli_configuration_profiles"`
	Runs                     []Run                     `json:"runs"`
	ImplementationQueues     []ImplementationQueue     `json:"implementation_queues"`
	Relationships            []ItemRelation            `json:"relationships"`
	ExternalObjects          []ExternalObject          `json:"external_objects"`
	Links                    []Link                    `json:"links"`
	Snapshots                []ExternalSnapshot        `json:"snapshots"`
	Activities               []Activity                `json:"activities"`
	AttentionDefaults        []ContextAttentionDefault `json:"attention_defaults"`
	AuditEntries             []AuditEntry              `json:"audit_entries"`
}
