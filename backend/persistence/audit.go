package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func loadAudit(ctx context.Context, db *sql.DB) ([]domain.AuditEntry, error) {
	return rowsOf(ctx, db, `SELECT id,recorded_at,action_json FROM audit_entries ORDER BY id DESC LIMIT 200`, func(r rowScanner) (domain.AuditEntry, error) {
		var value domain.AuditEntry
		var raw string
		if err := r.Scan(&value.ID, &value.RecordedAt, &raw); err != nil {
			return value, err
		}
		value.Action = json.RawMessage(raw)
		if err := validateAuditAction(value.Action); err != nil {
			return value, fmt.Errorf("decode audit_entries.action_json: %w", err)
		}
		return value, nil
	})
}

func validateAuditAction(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var kind string
	if err := json.Unmarshal(fields["action"], &kind); err != nil || kind == "" {
		return fmt.Errorf("missing action tag")
	}
	allowed, required, enumFields := auditActionFields(kind)
	if allowed == nil {
		return fmt.Errorf("unknown audit action %q", kind)
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown field %q for audit action %q", key, kind)
		}
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("missing field %q for audit action %q", key, kind)
		}
	}
	requiredFields := map[string]bool{}
	for _, field := range required {
		requiredFields[field] = true
	}
	numericFields := map[string]bool{}
	for _, field := range []string{"context_id", "project_id", "repository_id", "item_id", "from_item_id", "to_item_id", "workspace_id", "repository_count", "machine_id", "run_count", "worktree_count", "run_id", "external_object_id", "link_id", "link_count", "snapshot_count", "activity_count", "workspace_count"} {
		numericFields[field] = true
	}
	nullableFields := map[string]bool{"external_object_id": true, "repository_count": true, "run_count": true, "worktree_count": true, "link_count": true, "snapshot_count": true, "activity_count": true, "workspace_count": true}
	for field, rawValue := range fields {
		if field == "action" {
			continue
		}
		if field == "summary" {
			if err := validateAuditSummary(kind, rawValue); err != nil {
				return err
			}
			continue
		}
		if numericFields[field] {
			if string(rawValue) == "null" {
				if nullableFields[field] && !requiredFields[field] {
					continue
				}
				return fmt.Errorf("null numeric %s in audit action %q", field, kind)
			}
			var number int64
			if err := json.Unmarshal(rawValue, &number); err != nil {
				return fmt.Errorf("invalid numeric %s in audit action %q", field, kind)
			}
			if strings.HasSuffix(field, "_count") && number < 0 {
				return fmt.Errorf("negative %s in audit action %q", field, kind)
			}
		}
		if field == "external_object_deleted" && string(rawValue) != "null" {
			var boolean bool
			if err := json.Unmarshal(rawValue, &boolean); err != nil {
				return fmt.Errorf("invalid external_object_deleted in audit action %q", kind)
			}
		}
	}
	for field, values := range enumFields {
		if value, ok := fields[field]; ok {
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return fmt.Errorf("invalid %s in audit action %q", field, kind)
			}
			valid := false
			for _, candidate := range values {
				if text == candidate {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("unknown %s value %q in audit action %q", field, text, kind)
			}
		}
	}
	return nil
}

func validateAuditSummary(kind string, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("invalid summary for audit action %q", kind)
	}
	var allowed []string
	var required []string
	if kind == "itemDeleted" {
		allowed = []string{"itemId", "reminderCount", "relationshipCount", "workspaceCount", "runCount", "linkCount", "externalObjectCount", "snapshotCount", "activityCount"}
		required = allowed
	} else {
		allowed = []string{"contextId", "projectId", "projectCount", "itemCount", "repositoryCount", "machineCount", "workspaceCount", "runCount", "reminderCount", "relationshipCount", "linkCount", "attentionDefaultCount", "externalObjectCount", "snapshotCount", "activityCount"}
		required = []string{"projectCount", "itemCount", "repositoryCount", "machineCount", "workspaceCount", "runCount", "reminderCount", "relationshipCount", "linkCount", "attentionDefaultCount", "externalObjectCount", "snapshotCount", "activityCount"}
	}
	valid := map[string]bool{}
	for _, key := range allowed {
		valid[key] = true
	}
	for key := range fields {
		if !valid[key] {
			return fmt.Errorf("unknown summary field %q for audit action %q", key, kind)
		}
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("missing summary field %q for audit action %q", key, kind)
		}
	}
	for key, value := range fields {
		if key == "contextId" || key == "projectId" {
			if string(value) == "null" {
				continue
			}
		}
		var number int64
		if err := json.Unmarshal(value, &number); err != nil {
			return fmt.Errorf("invalid summary number %s for audit action %q", key, kind)
		}
		if (strings.HasSuffix(key, "_count") || strings.HasSuffix(key, "Count")) && number < 0 {
			return fmt.Errorf("negative summary count %s for audit action %q", key, kind)
		}
	}
	return nil
}

func auditActionFields(kind string) (map[string]bool, []string, map[string][]string) {
	sets := map[string][]string{
		"contextCreated": {"context_id"}, "projectCreated": {"project_id"}, "repositoryRegistered": {"repository_id"}, "itemCreated": {"item_id"},
		"itemStatusChanged": {"item_id", "from", "to"}, "itemTitleChanged": {"item_id"}, "itemNotesChanged": {"item_id"}, "itemRemindersChanged": {"item_id"}, "itemRelationChanged": {"from_item_id", "to_item_id", "kind"},
		"workspaceCreated": {"workspace_id"}, "workspaceUpdated": {"workspace_id"}, "workspaceRemoved": {"workspace_id", "repository_count"}, "machineRegistered": {"machine_id"}, "machineObserved": {"machine_id", "observation"}, "machineDeleted": {"machine_id", "run_count", "worktree_count"},
		"runCreated": {"run_id"}, "runStopped": {"run_id"}, "runFinished": {"run_id"}, "runStateChanged": {"run_id", "from", "to"}, "runDeleted": {"run_id"}, "runPaneStatusChanged": {"run_id", "from", "to"},
		"externalObjectCreated": {"external_object_id"}, "externalObjectRefreshed": {"external_object_id"}, "linkCreated": {"link_id"}, "linkUpdated": {"link_id"}, "linkDeleted": {"link_id", "external_object_id", "external_object_deleted"}, "externalObjectDeleted": {"external_object_id", "link_count", "snapshot_count", "activity_count"},
		"contextAttentionDefaultChanged": {"context_id", "object_kind"}, "itemDeleted": {"summary"}, "repositoryDeleted": {"repository_id", "workspace_count"}, "projectDeleted": {"summary"}, "contextDeleted": {"summary"}, "resetBoundary": {"context_id", "project_id"},
	}
	optional := map[string][]string{"workspaceRemoved": {"repository_count"}, "machineDeleted": {"run_count", "worktree_count"}, "linkDeleted": {"external_object_id", "external_object_deleted"}, "externalObjectDeleted": {"link_count", "snapshot_count", "activity_count"}, "repositoryDeleted": {"workspace_count"}}
	keys, ok := sets[kind]
	if !ok {
		return nil, nil, nil
	}
	allowed := map[string]bool{"action": true}
	required := make([]string, 0, len(keys))
	opt := map[string]bool{}
	for _, field := range optional[kind] {
		opt[field] = true
	}
	for _, field := range keys {
		allowed[field] = true
		if !opt[field] {
			required = append(required, field)
		}
	}
	enums := map[string][]string{}
	switch kind {
	case "itemStatusChanged":
		enums["from"] = []string{"Inbox", "Active", "Waiting", "Done"}
		enums["to"] = enums["from"]
	case "itemRelationChanged":
		enums["kind"] = []string{"blocks", "blocked_by", "related_to"}
	case "machineObserved":
		enums["observation"] = []string{"unknown", "available", "offline"}
	case "runStateChanged":
		enums["from"] = []string{"unknown", "working", "blocked", "finished"}
		enums["to"] = enums["from"]
	case "runPaneStatusChanged":
		enums["from"] = []string{"unknown", "available", "missing"}
		enums["to"] = enums["from"]
	case "contextAttentionDefaultChanged":
		enums["object_kind"] = []string{"issue", "pull_request", "document", "generic"}
	}
	return allowed, required, enums
}
