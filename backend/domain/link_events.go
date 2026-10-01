package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

func decideLinkMutation(state DomainState, event Event) (Decision, error) {
	i := linkIndex(state, event.LinkID)
	if i < 0 {
		return Decision{}, DomainError(fmt.Sprintf("Link %d does not exist", event.LinkID))
	}
	link := state.Links[i]
	switch event.Kind {
	case "set_link_attention_policy":
		link.LinkAttentionOverrides = LinkAttentionOverrides{}
		link.AttentionPolicy = nil
		if event.AttentionOverrides != nil {
			link.LinkAttentionOverrides = *event.AttentionOverrides
			if event.AttentionOverrides.TitleAttention != nil && event.AttentionOverrides.StateAttention != nil && event.AttentionOverrides.MetadataAttention != nil {
				link.AttentionPolicy = &ExternalChangePolicy{Title: *event.AttentionOverrides.TitleAttention, State: *event.AttentionOverrides.StateAttention, Metadata: *event.AttentionOverrides.MetadataAttention}
			}
		}
	case "set_link_purpose":
		if event.Purpose != "to-spec" && event.Purpose != "to-tickets" && event.Purpose != "others" {
			return Decision{}, DomainError("invalid Link purpose")
		}
		if event.Purpose == "to-tickets" && event.SpecExternalObjectID == nil {
			return Decision{}, DomainError("a Spec is required for the to-tickets purpose")
		}
		if event.SpecExternalObjectID != nil {
			if !externalObjectExists(state, *event.SpecExternalObjectID) {
				return Decision{}, DomainError(fmt.Sprintf("External Object %d does not exist", *event.SpecExternalObjectID))
			}
			if *event.SpecExternalObjectID == link.ExternalObjectID {
				return Decision{}, DomainError("a Link cannot be its own Spec")
			}
		}
		link.Purpose, link.SpecExternalObjectID = event.Purpose, event.SpecExternalObjectID
	case "set_link_watch_until":
		link.WatchUntil = linkEventStringPointer(event.Timestamp)
	case "set_link_review_at":
		link.ReviewAt = linkEventStringPointer(event.Timestamp)
	case "clear_link_review_at":
		link.ReviewAt = nil
	case "mark_link_reviewed":
		for _, activity := range state.Activities {
			if activity.ExternalObjectID == link.ExternalObjectID && activity.ID > link.ReviewedActivityID {
				link.ReviewedActivityID = activity.ID
			}
		}
		link.ReviewAt = nil
	}
	state.Links[i] = link
	return Decision{State: state, Effects: []Effect{{Kind: "update_external_link", ExternalLink: &link}}}, nil
}

func decideUnlinkExternalLink(state DomainState, event Event) (Decision, error) {
	i := linkIndex(state, event.LinkID)
	if i < 0 {
		return Decision{}, DomainError(fmt.Sprintf("Link %d does not exist", event.LinkID))
	}
	link := state.Links[i]
	if !event.Confirmed {
		return Decision{}, DomainError("unlinking a Link requires confirmation")
	}
	state.Links = append(state.Links[:i], state.Links[i+1:]...)
	deleted := false
	if !hasLinksForObject(state, link.ExternalObjectID) {
		deleted = removeExternalObject(&state, link.ExternalObjectID)
	}
	effects := []Effect{{Kind: "delete_external_link", ExternalLink: &link}}
	if deleted {
		effects = append(effects, Effect{Kind: "delete_external_object", ExternalObjectID: link.ExternalObjectID})
	}
	return Decision{State: state, Effects: effects}, nil
}

func PrepareExternalObjectDeletion(state DomainState, id int64) (ExternalObjectDeletionPreview, error) {
	var object *ExternalObject
	for i := range state.ExternalObjects {
		if state.ExternalObjects[i].ID == id {
			object = &state.ExternalObjects[i]
			break
		}
	}
	if id < 1 || object == nil {
		return ExternalObjectDeletionPreview{}, DomainError(fmt.Sprintf("External Object %d does not exist", id))
	}
	plan := ExternalObjectDeletionPlan{ExternalObjectID: id, Provider: object.Provider, Kind: object.Kind, ExternalKey: object.ExternalKey, CanonicalURL: object.CanonicalURL, LinkIDs: []int64{}}
	for _, link := range state.Links {
		if link.ExternalObjectID == id {
			plan.LinkIDs = append(plan.LinkIDs, link.ID)
		}
		if link.SpecExternalObjectID != nil && *link.SpecExternalObjectID == id && link.ExternalObjectID != id {
			plan.ClearedSpecLinkIDs = append(plan.ClearedSpecLinkIDs, link.ID)
		}
	}
	for _, snapshot := range state.Snapshots {
		if snapshot.ExternalObjectID == id {
			plan.SnapshotCount++
		}
	}
	for _, activity := range state.Activities {
		if activity.ExternalObjectID == id {
			plan.ActivityCount++
		}
	}
	sort.Slice(plan.LinkIDs, func(i, j int) bool { return plan.LinkIDs[i] < plan.LinkIDs[j] })
	sort.Slice(plan.ClearedSpecLinkIDs, func(i, j int) bool { return plan.ClearedSpecLinkIDs[i] < plan.ClearedSpecLinkIDs[j] })
	linkStates := []struct {
		Link      Link
		Attention LinkAttentionState
	}{}
	itemViews := []Item{}
	seenItems := map[int64]bool{}
	for _, link := range state.Links {
		if link.ExternalObjectID != id && (link.SpecExternalObjectID == nil || *link.SpecExternalObjectID != id) {
			continue
		}
		linkStates = append(linkStates, struct {
			Link      Link
			Attention LinkAttentionState
		}{link, LinkAttentionState{LinkAttentionOverrides: link.LinkAttentionOverrides, LinkID: link.ID, ReviewedActivityID: link.ReviewedActivityID, WatchUntil: link.WatchUntil, ReviewAt: link.ReviewAt, Provenance: link.Provenance}})
		if !seenItems[link.ItemID] {
			for _, item := range state.Items {
				if item.ID == link.ItemID {
					itemViews = append(itemViews, item)
					seenItems[item.ID] = true
					break
				}
			}
		}
	}
	raw, _ := json.Marshal(struct {
		Object ExternalObject
		Plan   ExternalObjectDeletionPlan
		Links  []struct {
			Link      Link
			Attention LinkAttentionState
		}
		Items      []Item
		Snapshots  []ExternalSnapshot
		Activities []Activity
	}{*object, plan, linkStates, itemViews, snapshotsForObject(state, id), activitiesForObject(state, id)})
	sum := sha256.Sum256(raw)
	return ExternalObjectDeletionPreview{StateFingerprint: hex.EncodeToString(sum[:]), Plan: plan}, nil
}

func decideDeleteExternalObject(state DomainState, event Event) (Decision, error) {
	if !event.Confirmed {
		return Decision{}, DomainError("deleting an External Object requires confirmation")
	}
	preview, err := PrepareExternalObjectDeletion(state, event.ExternalObjectID)
	if err != nil {
		return Decision{}, err
	}
	if event.StateFingerprint == "" || preview.StateFingerprint != event.StateFingerprint {
		return Decision{}, DomainError("External Object state changed after preview; prepare deletion again")
	}
	effects := []Effect{}
	for i := range state.Links {
		link := state.Links[i]
		if link.SpecExternalObjectID != nil && *link.SpecExternalObjectID == event.ExternalObjectID && link.ExternalObjectID != event.ExternalObjectID {
			link.SpecExternalObjectID = nil
			state.Links[i] = link
			effects = append(effects, Effect{Kind: "update_external_link", ExternalLink: &link})
		}
	}
	for _, linkID := range preview.Plan.LinkIDs {
		state.Links = removeLink(state.Links, linkID)
	}
	state.ExternalObjects = removeObject(state.ExternalObjects, event.ExternalObjectID)
	state.Snapshots = removeSnapshot(state.Snapshots, event.ExternalObjectID)
	state.Activities = removeActivities(state.Activities, event.ExternalObjectID)
	effects = append(effects, Effect{Kind: "delete_external_object", ExternalObjectID: event.ExternalObjectID})
	return Decision{State: state, Effects: effects}, nil
}

func linkIndex(s DomainState, id int64) int {
	for i, v := range s.Links {
		if v.ID == id {
			return i
		}
	}
	return -1
}
func linkEventStringPointer(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func hasLinksForObject(s DomainState, id int64) bool {
	for _, v := range s.Links {
		if v.ExternalObjectID == id {
			return true
		}
	}
	return false
}
func removeExternalObject(s *DomainState, id int64) bool {
	for i, v := range s.ExternalObjects {
		if v.ID == id {
			s.ExternalObjects = append(s.ExternalObjects[:i], s.ExternalObjects[i+1:]...)
			s.Snapshots = removeSnapshot(s.Snapshots, id)
			s.Activities = removeActivities(s.Activities, id)
			return true
		}
	}
	return false
}
func removeLink(v []Link, id int64) []Link {
	out := v[:0]
	for _, x := range v {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}
func removeObject(v []ExternalObject, id int64) []ExternalObject {
	out := v[:0]
	for _, x := range v {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}
func removeSnapshot(v []ExternalSnapshot, id int64) []ExternalSnapshot {
	out := v[:0]
	for _, x := range v {
		if x.ExternalObjectID != id {
			out = append(out, x)
		}
	}
	return out
}
func removeActivities(v []Activity, id int64) []Activity {
	out := v[:0]
	for _, x := range v {
		if x.ExternalObjectID != id {
			out = append(out, x)
		}
	}
	return out
}
func linksForObject(s DomainState, id int64) []Link {
	out := []Link{}
	for _, v := range s.Links {
		if v.ExternalObjectID == id {
			out = append(out, v)
		}
	}
	return out
}
func snapshotsForObject(s DomainState, id int64) []ExternalSnapshot {
	out := []ExternalSnapshot{}
	for _, v := range s.Snapshots {
		if v.ExternalObjectID == id {
			out = append(out, v)
		}
	}
	return out
}
func activitiesForObject(s DomainState, id int64) []Activity {
	out := []Activity{}
	for _, v := range s.Activities {
		if v.ExternalObjectID == id {
			out = append(out, v)
		}
	}
	return out
}
