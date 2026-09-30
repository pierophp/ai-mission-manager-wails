CREATE TABLE activities (
             id INTEGER PRIMARY KEY NOT NULL,
             external_object_id INTEGER NOT NULL REFERENCES external_objects(id) ON DELETE CASCADE,
             observed_at INTEGER NOT NULL,
             changes_json TEXT NOT NULL
         );

CREATE TABLE audit_entries (
             id INTEGER PRIMARY KEY NOT NULL,
             recorded_at INTEGER NOT NULL,
             action_json TEXT NOT NULL
         );

CREATE TABLE cli_configuration_profiles (
             id INTEGER PRIMARY KEY NOT NULL,
             machine_id INTEGER NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
             provider TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
             name TEXT NOT NULL,
             directory TEXT NOT NULL,
             app_managed INTEGER NOT NULL,
             UNIQUE (machine_id, provider, name)
         );

CREATE TABLE context_attention_defaults (
             context_id INTEGER NOT NULL REFERENCES contexts(id) ON DELETE CASCADE,
             object_kind TEXT NOT NULL CHECK (object_kind IN ('issue', 'pull_request', 'document', 'generic')),
             title_attention INTEGER NOT NULL,
             state_attention INTEGER NOT NULL,
             metadata_attention INTEGER NOT NULL,
             PRIMARY KEY (context_id, object_kind)
         );

CREATE TABLE contexts (
             id INTEGER PRIMARY KEY NOT NULL,
             name TEXT NOT NULL UNIQUE,
             execution_machine_id INTEGER,
             check_dirty_checkouts INTEGER NOT NULL DEFAULT 1,
             grill_agent TEXT NOT NULL DEFAULT 'claude',
             grill_model TEXT NOT NULL DEFAULT 'claude-sonnet-5',
             grill_effort TEXT NOT NULL DEFAULT 'high',
             implement_agent TEXT NOT NULL DEFAULT 'claude',
             implement_model TEXT NOT NULL DEFAULT 'claude-sonnet-5',
             implement_effort TEXT NOT NULL DEFAULT 'high',
             default_workflow TEXT NOT NULL DEFAULT 'matt-pocock',
             pstack_agent TEXT NOT NULL DEFAULT 'claude',
             pstack_model TEXT NOT NULL DEFAULT 'claude-sonnet-5',
             pstack_effort TEXT NOT NULL DEFAULT 'high',
             pstack_roles_json TEXT NOT NULL DEFAULT '',
             claude_profile_id INTEGER,
             codex_profile_id INTEGER,
             gh_executable_path TEXT,
             twg_executable_path TEXT,
             az_executable_path TEXT,
             atlassian_site TEXT,
             azure_devops_organization TEXT,
             bitbucket_workspace TEXT
         );

CREATE TABLE external_links (
             id INTEGER PRIMARY KEY NOT NULL,
             item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
             external_object_id INTEGER NOT NULL REFERENCES external_objects(id) ON DELETE CASCADE,
             purpose TEXT NOT NULL DEFAULT 'others',
             spec_external_object_id INTEGER REFERENCES external_objects(id) ON DELETE SET NULL,
             UNIQUE (item_id, external_object_id)
         );

CREATE TABLE external_objects (
             id INTEGER PRIMARY KEY NOT NULL,
             provider TEXT NOT NULL CHECK (provider IN ('github', 'atlassian', 'azure_dev_ops', 'generic')),
             kind TEXT NOT NULL CHECK (kind IN ('issue', 'pull_request', 'document', 'generic')),
             external_key TEXT NOT NULL,
             canonical_url TEXT NOT NULL,
             UNIQUE (provider, external_key)
         );

CREATE TABLE external_snapshots (
             external_object_id INTEGER PRIMARY KEY NOT NULL REFERENCES external_objects(id) ON DELETE CASCADE,
             title TEXT NOT NULL,
             state TEXT NOT NULL,
             metadata_json TEXT NOT NULL,
             fetched_at INTEGER NOT NULL
         );

CREATE TABLE implementation_queues (
             id INTEGER PRIMARY KEY NOT NULL,
             item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
             queue_json TEXT NOT NULL
         );

CREATE TABLE item_relationships (
             from_item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
             to_item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
             kind TEXT NOT NULL CHECK (kind IN ('blocks', 'blocked_by', 'related_to')),
             PRIMARY KEY (from_item_id, to_item_id, kind)
         );

CREATE TABLE items (
             id INTEGER PRIMARY KEY NOT NULL,
             human_identifier TEXT NOT NULL UNIQUE,
             title TEXT NOT NULL,
             project_id INTEGER NOT NULL REFERENCES projects(id),
             status TEXT NOT NULL CHECK (status IN ('Inbox', 'Active', 'Waiting', 'Done')),
             notes TEXT NOT NULL DEFAULT ''
         );

CREATE TABLE link_attention_state (
             link_id INTEGER PRIMARY KEY NOT NULL REFERENCES external_links(id) ON DELETE CASCADE,
             reviewed_activity_id INTEGER NOT NULL DEFAULT 0,
             title_attention INTEGER,
             state_attention INTEGER,
             metadata_attention INTEGER,
             watch_until TEXT,
             review_at TEXT,
             provenance_json TEXT
         );

CREATE TABLE machines (
             id INTEGER PRIMARY KEY NOT NULL,
             context_id INTEGER NOT NULL REFERENCES contexts(id),
             name TEXT NOT NULL,
             socket_name TEXT NOT NULL,
             transport_json TEXT NOT NULL DEFAULT '{"kind":"local"}',
             last_observed TEXT NOT NULL DEFAULT 'unknown',
             last_observed_at INTEGER,
             UNIQUE (context_id, name)
         );

CREATE TABLE metadata (
             key TEXT PRIMARY KEY NOT NULL,
             value INTEGER NOT NULL
         );

CREATE TABLE projects (
             id INTEGER PRIMARY KEY NOT NULL,
             context_id INTEGER NOT NULL REFERENCES contexts(id),
             name TEXT NOT NULL,
             default_item_status TEXT NOT NULL
                 CHECK (default_item_status IN ('Inbox', 'Active', 'Waiting', 'Done')),
             default_execution_mode TEXT NOT NULL DEFAULT 'worktree'
                 CHECK (default_execution_mode IN ('direct', 'worktree')),
             UNIQUE (context_id, name)
         );

CREATE TABLE reminders (
             id INTEGER PRIMARY KEY NOT NULL,
             item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
             remind_at TEXT NOT NULL
         );

CREATE TABLE repositories (
             id INTEGER PRIMARY KEY NOT NULL,
             project_id INTEGER NOT NULL REFERENCES projects(id),
             name TEXT NOT NULL,
             remote_url TEXT NOT NULL,
             base_branch TEXT NOT NULL DEFAULT 'main',
             UNIQUE (project_id, name)
         );

CREATE TABLE repository_locations (
             repository_id INTEGER NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
             machine_id INTEGER NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
             checkout_path TEXT NOT NULL,
             worktree_root TEXT NOT NULL,
             PRIMARY KEY (repository_id, machine_id)
         );

CREATE TABLE runs (
             id INTEGER PRIMARY KEY NOT NULL,
             item_id INTEGER NOT NULL REFERENCES items(id),
             workspace_id INTEGER REFERENCES workspaces(id),
             repository_id INTEGER REFERENCES repositories(id),
             worktree_id INTEGER REFERENCES worktrees(id),
             machine_id INTEGER NOT NULL REFERENCES machines(id),
             agent TEXT NOT NULL CHECK (agent IN ('claude', 'codex')),
             cli_configuration_profile_json TEXT,
             execution_profile TEXT NOT NULL
                 CHECK (execution_profile IN ('investigate', 'implement', 'review', 'custom', 'grill', 'autonomous', 'plan', 'pstack-review')),
             workflow TEXT NOT NULL DEFAULT 'matt-pocock',
             model TEXT,
             effort TEXT,
             skill_snapshot TEXT,
             prompt TEXT NOT NULL,
             working_directory TEXT NOT NULL,
             session_name TEXT NOT NULL,
             pane_id TEXT NOT NULL,
             started_at INTEGER NOT NULL,
             state TEXT NOT NULL DEFAULT 'unknown',
             last_applied_agent_state_sequence INTEGER,
             pane_status TEXT NOT NULL DEFAULT 'unknown',
             direct_checkouts_json TEXT NOT NULL DEFAULT '[]',
             transcript TEXT NOT NULL DEFAULT '',
             reported_pull_requests_json TEXT NOT NULL DEFAULT '[]',
             attention_summary TEXT,
             grill_question_group_json TEXT,
             grill_answers_json TEXT NOT NULL DEFAULT '[]',
             grill_decisions_json TEXT NOT NULL DEFAULT '[]',
             grill_response TEXT,
             grill_phase TEXT,
             grill_action TEXT,
             grill_action_started_at INTEGER
             ,implementation_queue_id INTEGER
             ,implementation_queue_position INTEGER
             ,plan_phase TEXT
             ,plan_path TEXT
         );

CREATE TABLE settings (
             key TEXT PRIMARY KEY NOT NULL,
             value TEXT NOT NULL
         );

CREATE TABLE workspace_repositories (
             workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
             repository_id INTEGER NOT NULL REFERENCES repositories(id),
             branch TEXT NOT NULL,
             base_branch TEXT NOT NULL,
             PRIMARY KEY (workspace_id, repository_id)
         );

CREATE TABLE workspaces (
             id INTEGER PRIMARY KEY NOT NULL,
             item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
             preparation_state TEXT NOT NULL DEFAULT 'pending'
         );

CREATE TABLE worktrees (
             id INTEGER PRIMARY KEY NOT NULL,
             workspace_id INTEGER NOT NULL REFERENCES workspaces(id),
             repository_id INTEGER NOT NULL REFERENCES repositories(id),
             machine_id INTEGER NOT NULL REFERENCES machines(id),
             path TEXT NOT NULL,
             branch TEXT NOT NULL,
             base_branch TEXT NOT NULL,
             is_dirty INTEGER NOT NULL DEFAULT 0
         );

CREATE INDEX activities_by_object
             ON activities (external_object_id, id);

CREATE INDEX audit_entries_by_recorded_at
             ON audit_entries (recorded_at, id);

CREATE INDEX external_links_by_item
             ON external_links (item_id);

CREATE INDEX external_links_by_object
             ON external_links (external_object_id);

CREATE INDEX items_by_project_and_status
             ON items (project_id, status);

CREATE INDEX machines_by_context
             ON machines (context_id, id);

CREATE INDEX projects_by_context
             ON projects (context_id);

CREATE INDEX relationships_by_target
             ON item_relationships (to_item_id);

CREATE INDEX reminders_by_item
             ON reminders (item_id, id);

CREATE INDEX repositories_by_project
             ON repositories (project_id, id);

CREATE INDEX repository_locations_by_machine
             ON repository_locations (machine_id, repository_id);

CREATE INDEX runs_by_item
             ON runs (item_id, id);

CREATE INDEX runs_by_workspace
             ON runs (workspace_id, id);

CREATE INDEX workspace_repositories_by_repository
             ON workspace_repositories (repository_id);

CREATE INDEX workspaces_by_item
             ON workspaces (item_id, id);

CREATE INDEX worktrees_by_repository
             ON worktrees (repository_id, id);

CREATE INDEX worktrees_by_workspace
             ON worktrees (workspace_id, id);
