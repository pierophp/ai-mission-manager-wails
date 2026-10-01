package backend

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

type fakeGrillPaneTerminal struct {
	calls []string
	text  string
	fail  string
}

func (f *fakeGrillPaneTerminal) SetBuffer(_ domain.Machine, buffer, text string) error {
	f.calls = append(f.calls, "set:"+buffer)
	f.text = text
	if f.fail == "set" {
		return errFakePane
	}
	return nil
}
func (f *fakeGrillPaneTerminal) PasteBuffer(_ domain.Machine, buffer, pane string) error {
	f.calls = append(f.calls, "paste:"+buffer+":"+pane)
	if f.fail == "paste" {
		return errFakePane
	}
	return nil
}
func (f *fakeGrillPaneTerminal) SendEnter(_ domain.Machine, pane string) error {
	f.calls = append(f.calls, "enter:"+pane)
	if f.fail == "enter" {
		return errFakePane
	}
	return nil
}

func grillRunFixture(t *testing.T, profile, workflow, state, phase string) (*Runtime, *CommandService, *fakeGrillPaneTerminal) {
	t.Helper()
	database := filepath.Join(t.TempDir(), "grill.sqlite")
	seedPersistedRun(t, database)
	store, err := persistence.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	questionJSON := `{"round":0,"questions":[{"number":1,"title":"Choice","prompt":"Which path?","recommendation":"Use A","options":[]}]}`
	planPhase := any(nil)
	if profile == "plan" {
		planPhase = "awaiting_go"
	}
	if err := store.Apply([]persistence.Effect{{SQL: `UPDATE runs SET execution_profile=?,workflow=?,state=?,pane_status='available',reported_pull_requests_json='[]',grill_phase=?,grill_question_group_json=?,grill_answers_json='[]',grill_decisions_json='[]',plan_phase=?,plan_path='/repo/plan.md' WHERE id=9`, Args: []any{profile, workflow, state, nullableString(phase), questionJSON, planPhase}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenRuntime(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	fake := &fakeGrillPaneTerminal{}
	runtime.setGrillPaneTerminal(fake)
	return runtime, &CommandService{Runtime: runtime}, fake
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var errFakePane = fakePaneError{}

type fakePaneError struct{}

func (fakePaneError) Error() string { return "fake Pane failure" }

func TestGrillAnswerCommandUsesPasteSequencePersistsDecisionAndEmitsQuestionEvent(t *testing.T) {
	runtime, service, fake := grillRunFixture(t, "grill", "matt-pocock", "finished", "waiting_for_answers")
	encoded, err := service.Invoke("submit_grill_answers", `{"runId":9,"answers":[{"questionNumber":1,"answer":"Choose A"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.Unmarshal(encoded, &run); err != nil {
		t.Fatal(err)
	}
	if run.State != domain.RunWorking || run.GrillPhase == nil || *run.GrillPhase != "working" || run.GrillResponse == nil || *run.GrillResponse != "1. Choose A" || len(run.GrillDecisions) != 1 {
		t.Fatalf("submitted Run = %#v", run)
	}
	if !reflect.DeepEqual(fake.calls, []string{"set:ai-mission-manager-input-4", "paste:ai-mission-manager-input-4:%4", "enter:%4"}) || fake.text != "1. Choose A" {
		t.Fatalf("pane calls=%v text=%q", fake.calls, fake.text)
	}
	loaded, err := runtime.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Runs) != 1 || loaded.Runs[0].GrillResponse == nil || *loaded.Runs[0].GrillResponse != "1. Choose A" {
		t.Fatalf("persisted run=%#v", loaded.Runs)
	}
}

func TestGrillContinueCommandIsPerRunAndUsesPanePasteSequence(t *testing.T) {
	_, service, fake := grillRunFixture(t, "grill", "matt-pocock", "finished", "awaiting_next_action")
	encoded, err := service.Invoke("continue_grill", `{"runId":9,"action":"to-spec"}`)
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.Unmarshal(encoded, &run); err != nil {
		t.Fatal(err)
	}
	if run.GrillAction == nil || *run.GrillAction != "to-spec" || run.State != domain.RunWorking || run.GrillQuestionGroup != nil {
		t.Fatalf("continued Run=%#v", run)
	}
	if !reflect.DeepEqual(fake.calls, []string{"set:ai-mission-manager-input-4", "paste:ai-mission-manager-input-4:%4", "enter:%4"}) {
		t.Fatalf("pane calls=%v", fake.calls)
	}
	if !strings.Contains(fake.text, "Downstream skill snapshot") || !strings.Contains(fake.text, "run_id") || !strings.Contains(fake.text, "to-spec") {
		t.Fatalf("continuation prompt omitted contract: %q", fake.text)
	}
}

func TestGoPlanCommandUsesPlanPathPrompt(t *testing.T) {
	runtime, service, fake := grillRunFixture(t, "plan", "pstack", "finished", "")
	phase := domain.PlanPhase("awaiting_go")
	run := runtime.runCopy(9)
	run.PlanPhase = &phase
	if err := runtime.persistGrillRun(run, 9); err != nil {
		t.Fatal(err)
	}
	encoded, err := service.Invoke("go_plan", `{"runId":9}`)
	if err != nil {
		t.Fatal(err)
	}
	var got domain.Run
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != domain.RunWorking || got.PlanPhase == nil || *got.PlanPhase != "executing" || !strings.Contains(fake.text, "/repo/plan.md") {
		t.Fatalf("Go result=%#v prompt=%q", got, fake.text)
	}
}

func TestRevealPlanCommandRoutesTheReportedPathThroughOpenRevealAdapter(t *testing.T) {
	runtime, service, _ := grillRunFixture(t, "plan", "pstack", "finished", "")
	phase := domain.PlanPhase("awaiting_go")
	run := runtime.runCopy(9)
	run.PlanPhase = &phase
	if err := runtime.persistGrillRun(run, 9); err != nil {
		t.Fatal(err)
	}
	var program string
	var args []string
	service.Platform = &PlatformService{OpenFileFunc: func(name string, arguments ...string) error {
		program = name
		args = append([]string(nil), arguments...)
		return nil
	}}
	if _, err := service.Invoke("reveal_plan", `{"runId":9}`); err != nil {
		t.Fatal(err)
	}
	if program != "open" || !reflect.DeepEqual(args, []string{"-R", "/repo/plan.md"}) {
		t.Fatalf("opener invocation = %s %v", program, args)
	}
}

func TestReconcileGrillTranscriptPersistsQuestionGroupAndEmitsBareRunID(t *testing.T) {
	runtime, _, _ := grillRunFixture(t, "grill", "matt-pocock", "finished", "")
	var event any
	runtime.SetEventEmitter(EventEmitterFunc(func(name string, payload any) {
		if name == "run-questions-changed" {
			event = payload
		}
	}))
	changed, err := runtime.applyRunTranscript(runtime.runCopy(9), "Checking the plan.\n❓ Q1: Which path?\n➡️ Use A\n")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if id, ok := event.(int64); !ok || id != 9 {
		t.Fatalf("question event payload=%#v", event)
	}
	loaded, err := runtime.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	group := loaded.Runs[0].GrillQuestionGroup
	if group == nil || len(group.Questions) != 1 || group.Questions[0].Recommendation == nil || *group.Questions[0].Recommendation != "Use A" || loaded.Runs[0].GrillPhase == nil || *loaded.Runs[0].GrillPhase != "waiting_for_answers" {
		t.Fatalf("captured Run=%#v", loaded.Runs[0])
	}
}

func TestComposeGrillPromptCommandAndPendingRegistry(t *testing.T) {
	runtime, service, _ := grillRunFixture(t, "grill", "matt-pocock", "finished", "")
	runtime.mu.Lock()
	runtime.state.Items[0].Title = "Prompt item"
	runtime.mu.Unlock()
	got, err := service.Invoke("compose_grill_prompt", `{"itemId":9,"configuration":{"agent":"claude","model":"claude-sonnet-5","effort":"high"},"language":"english","initialPrompt":"Decide this"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "GRILL_RESPONSE_LANGUAGE=english") || !strings.Contains(string(got), "User's initial prompt:\\nDecide this") {
		t.Fatalf("composed prompt=%s", got)
	}
}

func TestGrillDispatcherFlowContinuesIntoPlanGoAndReveal(t *testing.T) {
	runtime, service, pane := grillRunFixture(t, "grill", "matt-pocock", "finished", "waiting_for_answers")
	if _, err := service.Invoke("submit_grill_answers", `{"runId":9,"answers":[{"questionNumber":1,"answer":"Choose A"}]}`); err != nil {
		t.Fatal(err)
	}
	if changed, err := runtime.applyRunTranscript(runtime.runCopy(9), "❓ Q2: Which ticket grouping?\n➡️ One ticket per task\n"); err != nil || !changed {
		t.Fatalf("next question transcript changed=%v err=%v", changed, err)
	}
	if _, _, err := runtime.applyAgentStateRecord(9, AgentStateRecord{RunID: "9", Agent: "claude", State: domain.RunBlocked}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("submit_grill_answers", `{"runId":9,"answers":[{"questionNumber":2,"answer":"One per task"}]}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.applyAgentStateRecord(9, AgentStateRecord{RunID: "9", Agent: "claude", State: domain.RunFinished}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invoke("continue_grill", `{"runId":9,"action":"to-spec"}`); err != nil {
		t.Fatal(err)
	}
	transcript := "AI_MISSION_MANAGER_EVENT {\"event\":\"external.object.created\",\"url\":\"https://github.com/acme/app/issues/14\",\"run_id\":9,\"action\":\"to-spec\"}"
	if changed, err := runtime.applyRunTranscript(runtime.runCopy(9), transcript); err != nil || !changed {
		t.Fatalf("downstream transcript changed=%v err=%v", changed, err)
	}
	continued := domain.CanonicalRunForView(runtime.runCopy(9))
	if len(continued.DownstreamIssueCandidates) != 1 || continued.DownstreamIssueCandidates[0].URL != "https://github.com/acme/app/issues/14" {
		t.Fatalf("projected downstream candidates = %#v", continued.DownstreamIssueCandidates)
	}

	if err := runtime.store.Apply([]persistence.Effect{{SQL: `INSERT INTO runs(id,item_id,workspace_id,repository_id,worktree_id,machine_id,agent,execution_profile,model,effort,skill_snapshot,prompt,working_directory,session_name,pane_id,started_at,state,pane_status,direct_checkouts_json,transcript,grill_question_group_json,grill_answers_json,grill_decisions_json,grill_response,grill_phase,grill_action,last_applied_agent_state_sequence,grill_action_started_at,cli_configuration_profile_json,workflow,reported_pull_requests_json,attention_summary,plan_phase,plan_path) SELECT 10,item_id,workspace_id,repository_id,worktree_id,machine_id,agent,'plan',model,effort,skill_snapshot,prompt,working_directory,session_name||'-plan',pane_id,started_at,'finished','available',direct_checkouts_json,'',NULL,'[]','[]',NULL,NULL,NULL,last_applied_agent_state_sequence,NULL,cli_configuration_profile_json,'pstack',reported_pull_requests_json,attention_summary,'awaiting_go','/repo/plan.md' FROM runs WHERE id=9`}}, nil); err != nil {
		t.Fatal(err)
	}
	plan := runtime.runCopy(9)
	plan.ID = 10
	plan.ExecutionProfile = domain.ExecutionProfilePlan
	plan.Workflow = domain.WorkflowPstack
	plan.SessionName += "-plan"
	plan.State = domain.RunFinished
	plan.GrillPhase = nil
	plan.GrillQuestionGroup = nil
	plan.GrillAnswers = []domain.GrillAnswer{}
	plan.GrillDecisions = []domain.GrillAnswer{}
	plan.GrillResponse = nil
	plan.GrillAction = nil
	plan.GrillActionStartedAt = nil
	plan.PlanPhase = phasePointer(domain.PlanPhase("awaiting_go"))
	plan.PlanPath = stringPointerForRuntime("/repo/plan.md")
	runtime.mu.Lock()
	runtime.state.Runs = append(runtime.state.Runs, plan)
	runtime.mu.Unlock()
	if encoded, err := service.Invoke("go_plan", `{"runId":10}`); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(encoded), `"state":"working"`) {
		t.Fatalf("Go Plan result = %s", encoded)
	}
	var openerProgram string
	var openerArgs []string
	service.Platform = &PlatformService{OpenFileFunc: func(program string, args ...string) error {
		openerProgram = program
		openerArgs = append([]string(nil), args...)
		return nil
	}}
	if _, err := service.Invoke("reveal_plan", `{"runId":10}`); err != nil {
		t.Fatal(err)
	}
	if openerProgram != "open" || !reflect.DeepEqual(openerArgs, []string{"-R", "/repo/plan.md"}) {
		t.Fatalf("reveal invoked %q %v", openerProgram, openerArgs)
	}
	if len(pane.calls) != 12 {
		t.Fatalf("expected two answer sends, Grill continuation and Plan Go sends; got %v", pane.calls)
	}
}

func phasePointer(value domain.PlanPhase) *domain.PlanPhase { return &value }

func stringPointerForRuntime(value string) *string { return &value }
