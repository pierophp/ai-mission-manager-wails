package persistence

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const worktreeRemovalIntentPrefix = "internal:pending-worktree-removal:"

type WorktreeRemovalIntent struct {
	WorktreeID int64
	Path       string
}

// SaveWorktreeRemovalIntent durably records a user-confirmed removal before
// Git mutates the checkout, so startup can finish or cancel the operation.
func (s *Store) SaveWorktreeRemovalIntent(intent WorktreeRemovalIntent) error {
	if intent.WorktreeID < 1 || strings.TrimSpace(intent.Path) == "" {
		return fmt.Errorf("Worktree removal intent requires an ID and path")
	}
	return s.SetSettings(map[string]string{worktreeRemovalIntentKey(intent.WorktreeID): intent.Path})
}

func (s *Store) DeleteWorktreeRemovalIntent(worktreeID int64) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, worktreeRemovalIntentKey(worktreeID))
	return err
}

func (s *Store) ListWorktreeRemovalIntents() ([]WorktreeRemovalIntent, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT key,value FROM settings WHERE key GLOB ?`, worktreeRemovalIntentPrefix+"*")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []WorktreeRemovalIntent
	for rows.Next() {
		var key, path string
		if err := rows.Scan(&key, &path); err != nil {
			return nil, err
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, worktreeRemovalIntentPrefix), 10, 64)
		if err != nil || id < 1 || strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("invalid pending Worktree removal intent %q", key)
		}
		intents = append(intents, WorktreeRemovalIntent{WorktreeID: id, Path: path})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return intents, nil
}

func worktreeRemovalIntentKey(worktreeID int64) string {
	return worktreeRemovalIntentPrefix + strconv.FormatInt(worktreeID, 10)
}
