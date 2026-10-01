package domain

import (
	"fmt"
	"math"
	"sort"
)

func decideLinkExternalObject(state DomainState, itemID int64, input ExternalObject, snapshot *ExternalSnapshot) (Decision, error) {
	if itemID < 1 {
		return Decision{}, DomainError("Item identifier must be positive")
	}
	if !itemExists(state, itemID) {
		return Decision{}, DomainError(fmt.Sprintf("Item %d does not exist", itemID))
	}
	if input.ExternalKey == "" || input.CanonicalURL == "" {
		return Decision{}, DomainError("External Object identity and canonical URL are required")
	}
	if err := validateExternalIdentity(input.Provider, input.Kind); err != nil {
		return Decision{}, err
	}
	for _, existing := range state.ExternalObjects {
		if existing.Provider == input.Provider && existing.ExternalKey == input.ExternalKey {
			// Keep the Rust external-key contract and SQLite unique index. Azure
			// work-item/PR IDs can collide under that contract, so reject a different
			// canonical object instead of attaching the new Link to the wrong object.
			if existing.Kind != input.Kind || existing.CanonicalURL != input.CanonicalURL {
				return Decision{}, DomainError(fmt.Sprintf("External Object identity collision for %s %q", input.Provider, input.ExternalKey))
			}
			input = existing
			break
		}
	}
	effects := []Effect{}
	if input.ID == 0 {
		if state.NextExternalObjectID < 1 || state.NextExternalObjectID == math.MaxInt64 {
			return Decision{}, DomainError("External Object identifier sequence is exhausted")
		}
		input.ID = state.NextExternalObjectID
		state.NextExternalObjectID++
		state.ExternalObjects = append(state.ExternalObjects, input)
		effects = append(effects, Effect{Kind: "persist_external_object", ExternalObject: &input})
	}
	var link Link
	for _, existing := range state.Links {
		if existing.ItemID == itemID && existing.ExternalObjectID == input.ID {
			link = existing
			break
		}
	}
	if link.ID == 0 {
		if state.NextLinkID < 1 || state.NextLinkID == math.MaxInt64 {
			return Decision{}, DomainError("Link identifier sequence is exhausted")
		}
		link = Link{ID: state.NextLinkID, ItemID: itemID, ExternalObjectID: input.ID, Purpose: "others"}
		state.NextLinkID++
		state.Links = append(state.Links, link)
		effects = append(effects, Effect{Kind: "persist_external_link", ExternalLink: &link})
	}
	if snapshot != nil {
		value := *snapshot
		value.ExternalObjectID = input.ID
		if !hasExternalSnapshot(state, input.ID) {
			state.Snapshots = append(state.Snapshots, value)
			effects = append(effects, Effect{Kind: "persist_external_snapshot", ExternalSnapshot: &value})
		}
	}
	return Decision{State: state, Effects: effects}, nil
}

func decideRefreshExternalObject(state DomainState, id int64, input *ExternalSnapshot) (Decision, error) {
	if input == nil {
		return Decision{}, DomainError("External snapshot is required")
	}
	if id < 1 {
		return Decision{}, DomainError("External Object identifier must be positive")
	}
	if !externalObjectExists(state, id) {
		return Decision{}, DomainError(fmt.Sprintf("External Object %d does not exist", id))
	}
	snapshot := *input
	snapshot.ExternalObjectID = id
	var previous *ExternalSnapshot
	for i := range state.Snapshots {
		if state.Snapshots[i].ExternalObjectID == id {
			previous = &state.Snapshots[i]
			break
		}
	}
	changes := []ExternalChange{}
	if previous != nil {
		if snapshot.FetchedAt < previous.FetchedAt {
			return Decision{}, DomainError(fmt.Sprintf("A newer snapshot for External Object %d was already applied; refresh it again", id))
		}
		changes = snapshotChanges(*previous, snapshot)
	}
	if previous == nil {
		state.Snapshots = append(state.Snapshots, snapshot)
	} else {
		for i := range state.Snapshots {
			if state.Snapshots[i].ExternalObjectID == id {
				state.Snapshots[i] = snapshot
				break
			}
		}
	}
	effects := []Effect{}
	if len(changes) > 0 {
		if state.NextActivityID < 1 || state.NextActivityID == math.MaxInt64 {
			return Decision{}, DomainError("Activity identifier sequence is exhausted")
		}
		activity := Activity{ID: state.NextActivityID, ExternalObjectID: id, ObservedAt: snapshot.FetchedAt, Changes: changes}
		state.NextActivityID++
		state.Activities = append(state.Activities, activity)
		effects = append(effects, Effect{Kind: "persist_external_activity", ExternalActivity: &activity})
	}
	effects = append(effects, Effect{Kind: "persist_external_snapshot", ExternalSnapshot: &snapshot}, Effect{Kind: "external_object_refreshed", ExternalObjectID: id})
	return Decision{State: state, Effects: effects}, nil
}

func validateExternalIdentity(provider ExternalProvider, kind ExternalObjectKind) error {
	valid := false
	switch provider {
	case ProviderGitHub:
		valid = kind == ObjectIssue || kind == ObjectPullRequest
	case ProviderAtlassian:
		valid = kind == ObjectIssue || kind == ObjectPullRequest || kind == ObjectDocument
	case ProviderAzureDevOps:
		valid = kind == ObjectIssue || kind == ObjectPullRequest
	case ProviderGeneric:
		valid = kind == ObjectGeneric
	}
	if !valid {
		return DomainError(fmt.Sprintf("invalid External Object kind %q for provider %q", kind, provider))
	}
	return nil
}
func itemExists(state DomainState, id int64) bool {
	for _, item := range state.Items {
		if item.ID == id {
			return true
		}
	}
	return false
}
func externalObjectExists(state DomainState, id int64) bool {
	for _, object := range state.ExternalObjects {
		if object.ID == id {
			return true
		}
	}
	return false
}
func hasExternalSnapshot(state DomainState, id int64) bool {
	for _, snapshot := range state.Snapshots {
		if snapshot.ExternalObjectID == id {
			return true
		}
	}
	return false
}
func snapshotChanges(previous, current ExternalSnapshot) []ExternalChange {
	changes := []ExternalChange{}
	if previous.Title != current.Title {
		old, new := previous.Title, current.Title
		changes = append(changes, ExternalChange{Kind: "title", Previous: &old, Current: &new})
	}
	if previous.State != current.State {
		old, new := previous.State, current.State
		changes = append(changes, ExternalChange{Kind: "state", Previous: &old, Current: &new})
	}
	oldValues, newValues := map[string]string{}, map[string]string{}
	for _, value := range previous.Metadata {
		oldValues[value.Key] = value.Value
	}
	for _, value := range current.Metadata {
		newValues[value.Key] = value.Value
	}
	keys := map[string]bool{}
	for key := range oldValues {
		keys[key] = true
	}
	for key := range newValues {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		old, hadOld := oldValues[key]
		next, hadNew := newValues[key]
		if hadOld == hadNew && old == next {
			continue
		}
		changeKey := key
		change := ExternalChange{Kind: "metadata", Key: &changeKey}
		if hadOld {
			change.Previous = &old
		}
		if hadNew {
			change.Current = &next
		}
		changes = append(changes, change)
	}
	return changes
}
