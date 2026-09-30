package persistence

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLoadReconstructsDomainStateAndObservedSQLiteCodecs(t *testing.T) {
	s := testStore(t)
	_, err := s.db.Exec(`
		INSERT INTO contexts(id,name,pstack_roles_json) VALUES(2,'Engineering','[{"role":"code-delegate","configuration":{"agent":"codex","model":"gpt-6-sol","effort":"high"}}]');
		INSERT INTO machines(id,context_id,name,socket_name,transport_json,last_observed,last_observed_at) VALUES(1,1,'Remote','mission','{"kind":"ssh","host":"build.example","user":"piero","port":2222,"identity_file":"~/.ssh/id_ed25519","known_hosts_file":null,"strict_host_key_checking":"yes"}','available',1720000000);
		INSERT INTO cli_configuration_profiles(id,machine_id,provider,name,directory,app_managed) VALUES(3,1,'codex','work','/profiles/codex',1);
		INSERT INTO repositories(id,project_id,name,remote_url,base_branch) VALUES(4,1,'app','git@github.com:me/app.git','trunk');
		INSERT INTO repository_locations(repository_id,machine_id,checkout_path,worktree_root) VALUES(4,1,'/src/app','/src/worktrees');
		INSERT INTO items(id,human_identifier,title,project_id,status,notes) VALUES(5,'MC-5','Ship this',1,'Active','notes');
		INSERT INTO reminders(id,item_id,remind_at) VALUES(7,5,'2026-09-30T09:05');
		INSERT INTO workspaces(id,item_id,preparation_state) VALUES(6,5,'ready');
		INSERT INTO workspace_repositories(workspace_id,repository_id,branch,base_branch) VALUES(6,4,'feature','trunk');
		INSERT INTO worktrees(id,workspace_id,repository_id,machine_id,path,branch,base_branch,is_dirty) VALUES(8,6,4,1,'/src/worktrees/feature','feature','trunk',1);
		INSERT INTO runs(id,item_id,workspace_id,repository_id,worktree_id,machine_id,agent,cli_configuration_profile_json,execution_profile,workflow,model,effort,skill_snapshot,prompt,working_directory,session_name,pane_id,started_at,state,last_applied_agent_state_sequence,pane_status,direct_checkouts_json,transcript,reported_pull_requests_json,grill_question_group_json,grill_answers_json,grill_decisions_json,grill_response,grill_phase,grill_action,grill_action_started_at,plan_phase,plan_path)
		VALUES(9,5,6,4,8,1,'codex','{"profileId":3,"provider":"codex","name":"work"}','implement','pstack','gpt-6-sol','high',NULL,'prompt','/src/app','session','%1',1720000001,'working',4,'available','[{"repositoryId":4,"path":"/src/app","branch":"feature","isDirty":true}]','transcript','["https://github.com/me/app/pull/2"]','{"round":1,"questions":[{"number":1,"title":"Risk?","prompt":"What risk?","recommendation":null,"options":[{"key":"a","label":"Low"}]}]}','[{"questionNumber":1,"answer":"low"}]','[]',NULL,'waiting_for_answers','to-spec',1720000002,'awaiting_go','/tmp/plan.md');
		INSERT INTO implementation_queues(id,item_id,queue_json) VALUES(9,5,'{"id":9,"itemId":5,"specExternalObjectId":10,"specUrl":"https://github.com/me/app/issues/10","workspaceId":6,"repositoryId":4,"configuration":{"agent":"codex","model":"gpt-6-sol","effort":"high"},"allowDirty":false,"allowSharedCheckouts":true,"entries":[{"position":1,"ticketNumber":11,"ticketTitle":"Ticket","ticketUrl":"https://github.com/me/app/issues/11","ticketState":"OPEN","runId":null,"done":false,"skipped":false}],"active":true,"pausedReason":{"kind":"launch_failed","message":"offline"}}');
		INSERT INTO item_relationships(from_item_id,to_item_id,kind) VALUES(5,5,'blocks');
		INSERT INTO external_objects(id,provider,kind,external_key,canonical_url) VALUES(10,'github','issue','me/app#10','https://github.com/me/app/issues/10');
		INSERT INTO external_links(id,item_id,external_object_id,purpose,spec_external_object_id) VALUES(12,5,10,'to-spec',10);
		INSERT INTO link_attention_state(link_id,reviewed_activity_id,title_attention,state_attention,metadata_attention,watch_until,review_at,provenance_json) VALUES(12,2,NULL,1,1,'2026-10-01T09:00',NULL,'{"run_id":9,"action":"to-spec","discovery":"structured-event","ordinal":1,"blocked_by":[]}');
		INSERT INTO external_snapshots(external_object_id,title,state,metadata_json,fetched_at) VALUES(10,'Spec','open','[{"key":"labels","value":"migration"}]',1720000003);
		INSERT INTO activities(id,external_object_id,observed_at,changes_json) VALUES(13,10,1720000004,'[{"kind":"metadata","key":"labels","previous":null,"current":"migration"}]');
		INSERT INTO context_attention_defaults(context_id,object_kind,title_attention,state_attention,metadata_attention) VALUES(1,'issue',1,0,1);
		INSERT INTO audit_entries(id,recorded_at,action_json) VALUES(1,1720000005,'{"action":"itemCreated","item_id":5}');
		UPDATE metadata SET value=6 WHERE key='next_item_id';
		UPDATE metadata SET value=2 WHERE key='next_audit_id';`)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if state.NextItemID != 6 || state.NextAuditID != 2 {
		t.Fatalf("sequences=%d/%d", state.NextItemID, state.NextAuditID)
	}
	if len(state.Contexts) != 2 || state.Contexts[0].PstackRoles[0].Role != "code-delegate" {
		t.Fatalf("contexts/roles=%+v", state.Contexts)
	}
	if len(state.Items) != 1 || state.Items[0].Reminders[0].RemindAt != "2026-09-30T09:05" {
		t.Fatalf("items/reminders=%+v", state.Items)
	}
	if state.Machines[0].Transport.Kind != "ssh" || state.Machines[0].Transport.Port == nil || *state.Machines[0].Transport.Port != 2222 {
		t.Fatalf("transport=%+v", state.Machines[0].Transport)
	}
	run := state.Runs[0]
	if run.Workflow != "pstack" || run.GrillPhase == nil || *run.GrillPhase != "waiting_for_answers" || len(run.DirectCheckouts) != 1 || run.CLIConfigurationProfile == nil {
		t.Fatalf("run codecs=%+v", run)
	}
	if len(state.Workspaces) != 1 || state.Workspaces[0].Repositories[0].BaseBranch != "trunk" || len(state.ImplementationQueues) != 1 || state.ImplementationQueues[0].PausedReason.Kind != "launch_failed" {
		t.Fatalf("workspace/queue=%+v %+v", state.Workspaces, state.ImplementationQueues)
	}
	if state.Links[0].AttentionPolicy != nil || state.Links[0].Purpose != "to-spec" || state.Links[0].Provenance == nil || len(state.Snapshots[0].Metadata) != 1 || state.Activities[0].Changes[0].Kind != "metadata" || len(state.AuditEntries) != 1 {
		t.Fatalf("external state did not round trip: %+v", state)
	}
}

func TestLoadRejectsInvalidCounterEnumAndJSON(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		want      string
	}{
		{"counter below one", `UPDATE metadata SET value=0 WHERE key='next_context_id'`, "at least 1"},
		{"unknown enum", `UPDATE contexts SET grill_agent='sideways' WHERE id=1`, "unknown contexts.grill_agent"},
		{"unknown enum in JSON", `UPDATE contexts SET pstack_roles_json='[{"role":"new-role","configuration":{"agent":"claude","model":"x","effort":"high"}}]' WHERE id=1`, "unknown contexts.pstack_roles_json.role"},
		{"unknown JSON field", `INSERT INTO machines(id,context_id,name,socket_name,transport_json) VALUES(1,1,'m','s','{"kind":"local","extra":true}')`, "unknown field"},
		{"unknown audit field", `INSERT INTO audit_entries(id,recorded_at,action_json) VALUES(1,1,'{"action":"itemCreated","item_id":1,"extra":true}')`, "unknown field"},
		{"invalid audit field type", `INSERT INTO audit_entries(id,recorded_at,action_json) VALUES(1,1,'{"action":"itemCreated","item_id":"oops"}')`, "invalid numeric item_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			if _, e := s.db.Exec(tc.sql); e != nil {
				t.Fatal(e)
			}
			_, e := s.Load()
			if e == nil || !strings.Contains(e.Error(), tc.want) {
				t.Fatalf("Load error=%v want containing %q", e, tc.want)
			}
		})
	}
}

func TestLoadToleratesKnownLegacyColumnsAndSequence(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`ALTER TABLE runs ADD COLUMN implementation_queue_legacy_note TEXT; INSERT INTO metadata(key,value) VALUES('next_workset_id',77)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("Load() rejected tolerated legacy data: %v", err)
	}
}

func TestLoadDecodesCamelCaseParentDeletionAuditSummary(t *testing.T) {
	for _, action := range []string{"itemDeleted", "projectDeleted", "contextDeleted"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			var summary string
			if action == "itemDeleted" {
				summary = `{"itemId":1,"reminderCount":0,"relationshipCount":0,"workspaceCount":0,"runCount":0,"linkCount":0,"externalObjectCount":0,"snapshotCount":0,"activityCount":0}`
			} else {
				summary = `{"contextId":null,"projectId":1,"projectCount":1,"itemCount":0,"repositoryCount":0,"machineCount":0,"workspaceCount":0,"runCount":0,"reminderCount":0,"relationshipCount":0,"linkCount":0,"attentionDefaultCount":0,"externalObjectCount":0,"snapshotCount":0,"activityCount":0}`
			}
			raw := `{"action":"` + action + `","summary":` + summary + `}`
			if _, err := s.db.Exec(`INSERT INTO audit_entries(id,recorded_at,action_json) VALUES(1,1,?)`, raw); err != nil {
				t.Fatal(err)
			}
			state, err := s.Load()
			if err != nil {
				t.Fatalf("Load() rejected valid camelCase summary: %v", err)
			}
			if len(state.AuditEntries) != 1 {
				t.Fatalf("audit entries=%d, want 1", len(state.AuditEntries))
			}
		})
	}
}

func TestApplyIsAtomicAdvancesInsertCountersAndCapsAudit(t *testing.T) {
	s := testStore(t)
	action := AuditAction(`{"action":"itemCreated","item_id":1}`)
	if err := s.Apply([]Effect{{SQL: `INSERT INTO items(id,human_identifier,title,project_id,status) VALUES(2,'MC-2','test',1,'Inbox')`, InsertedSequence: "next_item_id", InsertedID: 2}}, []AuditAction{action}); err != nil {
		t.Fatal(err)
	}
	var next int64
	if e := s.db.QueryRow(`SELECT value FROM metadata WHERE key='next_item_id'`).Scan(&next); e != nil {
		t.Fatal(e)
	}
	if next != 3 {
		t.Fatalf("next_item_id=%d, want 3", next)
	}
	bad := []Effect{{SQL: `INSERT INTO items(id,human_identifier,title,project_id,status) VALUES(3,'MC-3','rollback',1,'Inbox')`, InsertedSequence: "not_a_counter", InsertedID: 3}}
	if e := s.Apply(bad, []AuditAction{action}); e == nil {
		t.Fatal("expected invalid sequence failure")
	}
	var count int
	if e := s.db.QueryRow(`SELECT count(*) FROM items WHERE id=3`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatal("failed transaction inserted item")
	}
	actions := make([]AuditAction, 201)
	for i := range actions {
		actions[i] = action
	}
	if e := s.Apply(nil, actions); e != nil {
		t.Fatal(e)
	}
	if e := s.db.QueryRow(`SELECT count(*) FROM audit_entries`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 200 {
		t.Fatalf("audit count=%d, want 200", count)
	}
	state, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if len(state.AuditEntries) != 200 || state.NextAuditID != 203 {
		t.Fatalf("audit state count=%d next=%d", len(state.AuditEntries), state.NextAuditID)
	}
	if !json.Valid(state.AuditEntries[0].Action) {
		t.Fatal("audit action should remain valid JSON")
	}
}
