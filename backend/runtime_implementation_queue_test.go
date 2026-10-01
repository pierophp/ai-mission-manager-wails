package backend

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func TestCancelImplementationQueueCommandPersistsInactiveQueue(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "queue.sqlite")
	runtime, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	if _, err := service.Invoke("create_item", `{"title":"Queue item","contextId":1,"projectId":1,"notes":""}`); err != nil {
		t.Fatal(err)
	}
	queue := domain.ImplementationQueue{ID: 41, ItemID: 1, SpecExternalObjectID: 9, SpecURL: "https://github.com/acme/app/issues/9", WorkspaceID: 4, RepositoryID: 3, Configuration: domain.GrillConfiguration{Agent: domain.AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, Workflow: domain.WorkflowMattPocock, Active: true, Entries: []domain.ImplementationQueueEntry{{Position: 0, TicketNumber: 10, TicketTitle: "Ticket", TicketURL: "https://github.com/acme/app/issues/10", TicketState: "OPEN"}}}
	encoded, err := json.Marshal(queue)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.store.Apply([]persistence.Effect{{SQL: `INSERT INTO implementation_queues(id,item_id,queue_json) VALUES(?,?,?)`, Args: []any{queue.ID, queue.ItemID, string(encoded)}}}, nil); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.state.ImplementationQueues = append(runtime.state.ImplementationQueues, queue)
	runtime.mu.Unlock()
	if _, err := service.Invoke("cancel_implementation_queue", `{"queueId":41}`); err != nil {
		t.Fatal(err)
	}
	if got := runtime.queueCopy(queue.ID); got.Active || got.PausedReason != nil {
		t.Fatalf("cancel result=%#v", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRuntime(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.queueCopy(queue.ID); got.Active {
		t.Fatalf("cancel was not persisted: %#v", got)
	}
}

func TestSkipImplementationQueueEntryFailsClosedForMissingRun(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	queue := domain.ImplementationQueue{ID: 41, Active: true, PausedReason: &domain.ImplementationQueuePauseReason{Kind: "run_stopped"}, Entries: []domain.ImplementationQueueEntry{{Position: 0, TicketNumber: 12, TicketTitle: "Ticket", TicketURL: "https://github.com/acme/app/issues/12", TicketState: "OPEN", RunID: queueTestInt64Ptr(99)}}}
	runtime.mu.Lock()
	runtime.state.ImplementationQueues = append(runtime.state.ImplementationQueues, queue)
	runtime.mu.Unlock()
	if _, err := runtime.skipImplementationQueueEntry(queue.ID); err == nil {
		t.Fatal("skip advanced a queue whose referenced Run no longer exists")
	}
	got := runtime.queueCopy(queue.ID)
	if !got.Active || got.Entries[0].Skipped || got.PausedReason == nil || got.PausedReason.Kind != "run_stopped" {
		t.Fatalf("orphan Run changed queue state: %#v", got)
	}
}

func queueTestInt64Ptr(v int64) *int64 { return &v }
