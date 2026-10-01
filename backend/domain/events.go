package domain

type EventKind string
type EffectKind string

// Event is a value describing one requested domain transition. Fields unused by
// a particular Kind are ignored. It is intentionally free of adapter data.
type Event struct {
	Kind                 EventKind
	ContextID            int64
	MachineID            int64
	ProfileID            int64
	ExecutionMachineID   *int64
	CLIProfileID         *int64
	Provider             AgentKind
	ProfileName          string
	ProfileDirectory     string
	AppManaged           bool
	SocketName           string
	Transport            MachineTransport
	MachineObservation   MachineObservation
	ObservedAt           int64
	ProjectID            int64
	Name                 string
	Configuration        ContextConfiguration
	Defaults             ProjectDefaults
	ObjectKind           ExternalObjectKind
	Policy               ExternalChangePolicy
	GrillConfiguration   GrillConfiguration
	Enabled              bool
	ItemID               int64
	Title                string
	Notes                string
	Status               ItemStatus
	RemindAt             string
	ReminderID           int64
	FromItemID           int64
	ToItemID             int64
	RelationKind         ItemRelationKind
	RepositoryID         int64
	PreviousMachineID    *int64
	RemoteURL            string
	BaseBranch           string
	CheckoutPath         string
	WorktreeRoot         string
	ExternalObject       *ExternalObject
	ExternalSnapshot     *ExternalSnapshot
	ExternalObjectID     int64
	LinkID               int64
	AttentionPolicy      *ExternalChangePolicy
	AttentionOverrides   *LinkAttentionOverrides
	Purpose              LinkPurpose
	SpecExternalObjectID *int64
	Timestamp            string
	Confirmed            bool
	StateFingerprint     string
	Worktree             *Worktree
	Run                  *Run
	WorkspaceID          int64
	WorktreeID           int64
	DestructiveConfirmed bool
	DestructiveRequired  bool
}

type ContextConfiguration struct {
	Name                    string                    `json:"name"`
	ExecutionMachineID      *int64                    `json:"executionMachineId"`
	ClaudeProfileID         *int64                    `json:"claudeProfileId"`
	CodexProfileID          *int64                    `json:"codexProfileId"`
	CheckDirtyCheckouts     bool                      `json:"checkDirtyCheckouts"`
	GrillDefaults           GrillConfiguration        `json:"grillDefaults"`
	ImplementDefaults       GrillConfiguration        `json:"implementDefaults"`
	DefaultWorkflow         Workflow                  `json:"defaultWorkflow"`
	PstackDefaults          GrillConfiguration        `json:"pstackDefaults"`
	PstackRoles             PstackRoleTable           `json:"pstackRoles"`
	GHExecutablePath        *string                   `json:"ghExecutablePath"`
	TWGExecutablePath       *string                   `json:"twgExecutablePath"`
	AZExecutablePath        *string                   `json:"azExecutablePath"`
	AtlassianSite           *string                   `json:"atlassianSite"`
	AzureDevOpsOrganization *string                   `json:"azureDevopsOrganization"`
	BitbucketWorkspace      *string                   `json:"bitbucketWorkspace"`
	AttentionDefaults       []ContextAttentionDefault `json:"attentionDefaults"`
}

type Effect struct {
	Kind               EffectKind
	Context            *Context
	Project            *Project
	Machine            *Machine
	CLIProfile         *CLIConfigurationProfile
	Item               *Item
	Relation           *ItemRelation
	Repository         *Repository
	RepositoryLocation *RepositoryLocation
	PreviousMachineID  *int64
	AttentionDefaults  []ContextAttentionDefault
	Workspace          *Workspace
	Worktree           *Worktree
	Run                *Run
	ExternalObject     *ExternalObject
	ExternalLink       *Link
	ExternalSnapshot   *ExternalSnapshot
	ExternalActivity   *Activity
	ExternalObjectID   int64
}

type Decision struct {
	State   DomainState
	Effects []Effect
}

type DomainError string

func (e DomainError) Error() string { return string(e) }

type ObservedActivity struct {
	Activity Activity       `json:"activity"`
	Object   ExternalObject `json:"object"`
}
type ActivityTabView struct {
	AuditEntries []AuditEntry       `json:"audit_entries"`
	Activities   []ObservedActivity `json:"activities"`
}
