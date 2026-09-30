package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Effect is the persistence part of a domain effect. InsertedSequence is the
// metadata key whose counter advances after this insert; it is empty otherwise.
// Feature slices can add typed effect builders while sharing this transaction
// and sequence/audit machinery.
type Effect struct {
	SQL              string
	Args             []any
	InsertedSequence string
	InsertedID       int64
}

// AuditAction is a serialized tagged domain AuditAction (tag key: "action").
type AuditAction json.RawMessage

var insertSequenceKeys = map[string]bool{
	"next_context_id": true, "next_project_id": true, "next_item_id": true, "next_repository_id": true,
	"next_workspace_id": true, "next_worktree_id": true, "next_machine_id": true, "next_cli_profile_id": true,
	"next_run_id": true, "next_external_object_id": true, "next_link_id": true, "next_activity_id": true, "next_reminder_id": true,
}

// Apply commits effects, sequence changes, and audit entries atomically.
func (s *Store) Apply(effects []Effect, auditActions []AuditAction) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin persistence transaction: %w", err)
	}
	defer tx.Rollback()
	for _, effect := range effects {
		if effect.SQL == "" {
			return errors.New("persistence effect SQL is required")
		}
		if _, err := tx.Exec(effect.SQL, effect.Args...); err != nil {
			return fmt.Errorf("apply persistence effect: %w", err)
		}
		if effect.InsertedSequence != "" {
			if !insertSequenceKeys[effect.InsertedSequence] || effect.InsertedID < 1 {
				return fmt.Errorf("invalid inserted sequence update %q for id %d", effect.InsertedSequence, effect.InsertedID)
			}
			if _, err := tx.Exec(`UPDATE metadata SET value=MAX(value,?) WHERE key=?`, effect.InsertedID+1, effect.InsertedSequence); err != nil {
				return fmt.Errorf("advance %s: %w", effect.InsertedSequence, err)
			}
		}
	}
	var nextAuditID int64
	if err := tx.QueryRow(`SELECT value FROM metadata WHERE key='next_audit_id'`).Scan(&nextAuditID); err != nil {
		return fmt.Errorf("read next_audit_id: %w", err)
	}
	if nextAuditID < 1 {
		return fmt.Errorf("next_audit_id must be at least 1 (got %d)", nextAuditID)
	}
	for _, action := range auditActions {
		raw := json.RawMessage(action)
		if err := validateAuditAction(raw); err != nil {
			return fmt.Errorf("invalid audit action: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO audit_entries(id,recorded_at,action_json) VALUES (?,strftime('%s','now'),?)`, nextAuditID, string(raw)); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}
		nextAuditID++
	}
	if len(auditActions) > 0 {
		if _, err := tx.Exec(`UPDATE metadata SET value=? WHERE key='next_audit_id'`, nextAuditID); err != nil {
			return fmt.Errorf("advance next_audit_id: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM audit_entries WHERE id NOT IN (SELECT id FROM audit_entries ORDER BY id DESC LIMIT 200)`); err != nil {
			return fmt.Errorf("trim audit history: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit persistence transaction: %w", err)
	}
	return nil
}
