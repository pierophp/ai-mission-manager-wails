package backend

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
	"github.com/piero/ai-mission-manager-wails/backend/persistence"
)

func (r *Runtime) searchItems(query string, contextID *int64) []domain.ItemView {
	r.mu.Lock()
	state := r.state
	r.mu.Unlock()
	return domain.SearchItems(state, query, contextID)
}

func (r *Runtime) listInboxItems() []domain.Item {
	r.mu.Lock()
	state := r.state
	r.mu.Unlock()
	return domain.ListInboxItems(state)
}

func itemEventResult(decision domain.Decision, event domain.Event) (any, error) {
	switch event.Kind {
	case "create_item":
		return decision.State.Items[len(decision.State.Items)-1], nil
	case "set_item_status", "set_item_title", "set_item_notes", "add_item_reminder", "remove_item_reminder":
		for _, item := range decision.State.Items {
			if item.ID == event.ItemID {
				return item, nil
			}
		}
	case "set_item_relation":
		for _, relation := range decision.State.Relationships {
			if relation.FromItemID == event.FromItemID && relation.ToItemID == event.ToItemID && relation.Kind == event.RelationKind {
				return relation, nil
			}
		}
	}
	return nil, fmt.Errorf("command produced no result")
}

func itemPersistenceEffects(effect domain.Effect) ([]persistence.Effect, []persistence.AuditAction, bool, error) {
	var out []persistence.Effect
	switch effect.Kind {
	case "persist_item":
		item := effect.Item
		if item == nil {
			return nil, nil, true, errors.New("item effect has no Item")
		}
		out = append(out, persistence.Effect{SQL: `INSERT INTO items(id,human_identifier,title,project_id,status,notes) VALUES(?,?,?,?,?,?)`, Args: []any{item.ID, item.HumanIdentifier, item.Title, item.ProjectID, item.Status, item.Notes}, InsertedSequence: "next_item_id", InsertedID: item.ID})
		out = append(out, persistence.Effect{SQL: `UPDATE metadata SET value=MAX(value,?) WHERE key='next_item_number'`, Args: []any{parseItemNumber(item.HumanIdentifier) + 1}})
	case "set_item_status", "set_item_title", "set_item_notes", "add_item_reminder", "remove_item_reminder":
		item := effect.Item
		if item == nil {
			return nil, nil, true, errors.New("item effect has no Item")
		}
		out = append(out, persistence.Effect{SQL: `UPDATE items SET title=?,status=?,notes=? WHERE id=?`, Args: []any{item.Title, item.Status, item.Notes, item.ID}})
		if effect.Kind == "add_item_reminder" || effect.Kind == "remove_item_reminder" {
			out = append(out, persistence.Effect{SQL: `DELETE FROM reminders WHERE item_id=?`, Args: []any{item.ID}})
			for _, reminder := range item.Reminders {
				out = append(out, persistence.Effect{SQL: `INSERT INTO reminders(id,item_id,remind_at) VALUES(?,?,?)`, Args: []any{reminder.ID, item.ID, reminder.RemindAt}})
			}
			out = append(out, persistence.Effect{SQL: `UPDATE metadata SET value=MAX(value,?) WHERE key='next_reminder_id'`, Args: []any{nextReminderID(item.Reminders)}})
		}
	case "persist_item_relation":
		relation := effect.Relation
		if relation == nil {
			return nil, nil, true, errors.New("relation effect has no relation")
		}
		kind := ""
		switch relation.Kind {
		case domain.RelationBlocks:
			kind = "blocks"
		case domain.RelationBlockedBy:
			kind = "blocked_by"
		case domain.RelationRelatedTo:
			kind = "related_to"
		}
		out = append(out, persistence.Effect{SQL: `INSERT INTO item_relationships(from_item_id,to_item_id,kind) VALUES(?,?,?)`, Args: []any{relation.FromItemID, relation.ToItemID, kind}})
	default:
		return nil, nil, false, nil
	}
	return out, nil, true, nil
}

func parseItemNumber(identifier string) int64 {
	value, _ := strconv.ParseInt(strings.TrimPrefix(identifier, "MC-"), 10, 64)
	return value
}
func nextReminderID(reminders []domain.Reminder) int64 {
	next := int64(1)
	for _, reminder := range reminders {
		if reminder.ID >= next {
			next = reminder.ID + 1
		}
	}
	return next
}
