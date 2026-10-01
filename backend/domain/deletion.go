package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Deletion previews are immutable snapshots. Keeping the serialized state in
// the plan makes confirmation sensitive to every modeled record.
type ItemDeletionWorkspace struct {
	ID     int64 `json:"id"`
	ItemID int64 `json:"itemId"`
}
type ItemDeletionPlan struct {
	ItemID                    int64                   `json:"itemId"`
	HumanIdentifier           string                  `json:"humanIdentifier"`
	Title                     string                  `json:"title"`
	ReminderCount             int                     `json:"reminderCount"`
	RelationshipCount         int                     `json:"relationshipCount"`
	Workspaces                []ItemDeletionWorkspace `json:"workspaces"`
	RunIDs                    []int64                 `json:"runIds"`
	ActiveRunIDs              []int64                 `json:"activeRunIds"`
	LinkIDs                   []int64                 `json:"linkIds"`
	OrphanedExternalObjectIDs []int64                 `json:"orphanedExternalObjectIds"`
	OrphanedSnapshotCount     int                     `json:"orphanedSnapshotCount"`
	OrphanedActivityCount     int                     `json:"orphanedActivityCount"`
	StateFingerprint          string                  `json:"stateFingerprint"`
}
type ItemDeletionSummary struct {
	ItemID              int64 `json:"itemId"`
	ReminderCount       int   `json:"reminderCount"`
	RelationshipCount   int   `json:"relationshipCount"`
	WorkspaceCount      int   `json:"workspaceCount"`
	RunCount            int   `json:"runCount"`
	LinkCount           int   `json:"linkCount"`
	ExternalObjectCount int   `json:"externalObjectCount"`
	SnapshotCount       int   `json:"snapshotCount"`
	ActivityCount       int   `json:"activityCount"`
}

func (p ItemDeletionPlan) Summary() ItemDeletionSummary {
	return ItemDeletionSummary{p.ItemID, p.ReminderCount, p.RelationshipCount, len(p.Workspaces), len(p.RunIDs), len(p.LinkIDs), len(p.OrphanedExternalObjectIDs), p.OrphanedSnapshotCount, p.OrphanedActivityCount}
}

type RepositoryDeletionWorkspace struct {
	ID     int64 `json:"id"`
	ItemID int64 `json:"itemId"`
}
type RepositoryDeletionPlan struct {
	RepositoryID     int64                         `json:"repositoryId"`
	Name             string                        `json:"name"`
	RemoteURL        string                        `json:"remoteUrl"`
	Workspaces       []RepositoryDeletionWorkspace `json:"workspaces"`
	StateFingerprint string                        `json:"stateFingerprint"`
}
type MachineDeletionRun struct {
	ID             int64         `json:"id"`
	ItemID         int64         `json:"itemId"`
	ItemIdentifier string        `json:"itemIdentifier"`
	ItemTitle      string        `json:"itemTitle"`
	WorkspaceID    *int64        `json:"workspaceId"`
	WorktreeID     *int64        `json:"worktreeId"`
	State          RunState      `json:"state"`
	PaneStatus     RunPaneStatus `json:"paneStatus"`
}
type MachineDeletionPlan struct {
	MachineID                       int64                `json:"machineId"`
	Name                            string               `json:"name"`
	Runs                            []MachineDeletionRun `json:"runs"`
	ActiveRunIDs                    []int64              `json:"activeRunIds"`
	WorktreeIDs                     []int64              `json:"worktreeIds"`
	RepositoryLocationRepositoryIDs []int64              `json:"repositoryLocationRepositoryIds"`
	StateFingerprint                string               `json:"stateFingerprint"`
}
type ParentDeletionProject struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type ParentDeletionItem struct {
	ID              int64  `json:"id"`
	HumanIdentifier string `json:"humanIdentifier"`
	Title           string `json:"title"`
	ProjectID       int64  `json:"projectId"`
}
type ParentDeletionRepository struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	RemoteURL string `json:"remoteUrl"`
	ProjectID int64  `json:"projectId"`
}
type ParentDeletionMachine struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type ParentDeletionRun struct {
	ID             int64         `json:"id"`
	ItemID         int64         `json:"itemId"`
	ItemIdentifier string        `json:"itemIdentifier"`
	ItemTitle      string        `json:"itemTitle"`
	WorkspaceID    *int64        `json:"workspaceId"`
	WorktreeID     *int64        `json:"worktreeId"`
	MachineID      int64         `json:"machineId"`
	State          RunState      `json:"state"`
	PaneStatus     RunPaneStatus `json:"paneStatus"`
}
type ParentDeletionPlan struct {
	ContextID                 *int64                     `json:"contextId"`
	ProjectID                 *int64                     `json:"projectId"`
	Name                      string                     `json:"name"`
	Projects                  []ParentDeletionProject    `json:"projects"`
	Items                     []ParentDeletionItem       `json:"items"`
	Repositories              []ParentDeletionRepository `json:"repositories"`
	Machines                  []ParentDeletionMachine    `json:"machines"`
	Workspaces                []ItemDeletionWorkspace    `json:"workspaces"`
	Runs                      []ParentDeletionRun        `json:"runs"`
	ActiveRunIDs              []int64                    `json:"activeRunIds"`
	ReminderCount             int                        `json:"reminderCount"`
	RelationshipCount         int                        `json:"relationshipCount"`
	LinkIDs                   []int64                    `json:"linkIds"`
	AttentionDefaults         []ContextAttentionDefault  `json:"attentionDefaults"`
	OrphanedExternalObjectIDs []int64                    `json:"orphanedExternalObjectIds"`
	OrphanedSnapshotCount     int                        `json:"orphanedSnapshotCount"`
	OrphanedActivityCount     int                        `json:"orphanedActivityCount"`
	StateFingerprint          string                     `json:"stateFingerprint"`
}
type ParentDeletionSummary struct {
	ContextID             *int64 `json:"contextId"`
	ProjectID             *int64 `json:"projectId"`
	ProjectCount          int    `json:"projectCount"`
	ItemCount             int    `json:"itemCount"`
	RepositoryCount       int    `json:"repositoryCount"`
	MachineCount          int    `json:"machineCount"`
	WorkspaceCount        int    `json:"workspaceCount"`
	RunCount              int    `json:"runCount"`
	ReminderCount         int    `json:"reminderCount"`
	RelationshipCount     int    `json:"relationshipCount"`
	LinkCount             int    `json:"linkCount"`
	AttentionDefaultCount int    `json:"attentionDefaultCount"`
	ExternalObjectCount   int    `json:"externalObjectCount"`
	SnapshotCount         int    `json:"snapshotCount"`
	ActivityCount         int    `json:"activityCount"`
}

func (p ParentDeletionPlan) Summary() ParentDeletionSummary {
	return ParentDeletionSummary{p.ContextID, p.ProjectID, len(p.Projects), len(p.Items), len(p.Repositories), len(p.Machines), len(p.Workspaces), len(p.Runs), p.ReminderCount, p.RelationshipCount, len(p.LinkIDs), len(p.AttentionDefaults), len(p.OrphanedExternalObjectIDs), p.OrphanedSnapshotCount, p.OrphanedActivityCount}
}

type ResetLocalDataRecord struct {
	Kind  string `json:"kind"`
	ID    int64  `json:"id"`
	Label string `json:"label"`
}
type ResetLocalDataSummary struct {
	ContextCount          int `json:"contextCount"`
	ProjectCount          int `json:"projectCount"`
	RepositoryCount       int `json:"repositoryCount"`
	ItemCount             int `json:"itemCount"`
	WorkspaceCount        int `json:"workspaceCount"`
	MachineCount          int `json:"machineCount"`
	RunCount              int `json:"runCount"`
	ReminderCount         int `json:"reminderCount"`
	RelationshipCount     int `json:"relationshipCount"`
	LinkCount             int `json:"linkCount"`
	ExternalObjectCount   int `json:"externalObjectCount"`
	SnapshotCount         int `json:"snapshotCount"`
	ActivityCount         int `json:"activityCount"`
	AttentionDefaultCount int `json:"attentionDefaultCount"`
}
type ResetLocalDataPlan struct {
	Summary          ResetLocalDataSummary   `json:"summary"`
	AffectedRecords  []ResetLocalDataRecord  `json:"affectedRecords"`
	Workspaces       []ItemDeletionWorkspace `json:"workspaces"`
	StateFingerprint string                  `json:"stateFingerprint"`
}

func stateFingerprint(s DomainState) string { b, _ := json.Marshal(s); return string(b) }
func PlanItemDeletion(s DomainState, id int64) (ItemDeletionPlan, error) {
	var item *Item
	for i := range s.Items {
		if s.Items[i].ID == id {
			item = &s.Items[i]
			break
		}
	}
	if item == nil {
		return ItemDeletionPlan{}, fmt.Errorf("Item %d does not exist", id)
	}
	p := ItemDeletionPlan{ItemID: id, HumanIdentifier: item.HumanIdentifier, Title: item.Title, ReminderCount: len(item.Reminders), Workspaces: []ItemDeletionWorkspace{}, RunIDs: []int64{}, ActiveRunIDs: []int64{}, LinkIDs: []int64{}, OrphanedExternalObjectIDs: []int64{}, StateFingerprint: stateFingerprint(s)}
	objs := map[int64]bool{}
	for _, l := range s.Links {
		if l.ItemID == id {
			p.LinkIDs = append(p.LinkIDs, l.ID)
			objs[l.ExternalObjectID] = true
		}
	}
	for _, w := range s.Workspaces {
		if w.ItemID == id {
			p.Workspaces = append(p.Workspaces, ItemDeletionWorkspace{w.ID, id})
		}
	}
	for _, r := range s.Runs {
		if r.ItemID == id {
			p.RunIDs = append(p.RunIDs, r.ID)
			if runActive(r) {
				p.ActiveRunIDs = append(p.ActiveRunIDs, r.ID)
			}
		}
	}
	for _, rel := range s.Relationships {
		if rel.FromItemID == id || rel.ToItemID == id {
			p.RelationshipCount++
		}
	}
	for _, object := range s.ExternalObjects {
		if !objs[object.ID] {
			continue
		}
		shared := false
		for _, l := range s.Links {
			if l.ExternalObjectID == object.ID && l.ItemID != id {
				shared = true
			}
		}
		if !shared {
			p.OrphanedExternalObjectIDs = append(p.OrphanedExternalObjectIDs, object.ID)
		}
	}
	for _, v := range s.Snapshots {
		for _, oid := range p.OrphanedExternalObjectIDs {
			if v.ExternalObjectID == oid {
				p.OrphanedSnapshotCount++
			}
		}
	}
	for _, v := range s.Activities {
		for _, oid := range p.OrphanedExternalObjectIDs {
			if v.ExternalObjectID == oid {
				p.OrphanedActivityCount++
			}
		}
	}
	return p, nil
}
func PlanRepositoryDeletion(s DomainState, id int64) (RepositoryDeletionPlan, error) {
	p := RepositoryDeletionPlan{RepositoryID: id, Workspaces: []RepositoryDeletionWorkspace{}, StateFingerprint: stateFingerprint(s)}
	for _, r := range s.Repositories {
		if r.ID == id {
			p.Name = r.Name
			p.RemoteURL = r.RemoteURL
			goto found
		}
	}
	return p, fmt.Errorf("Repository %d does not exist", id)
found:
	for _, w := range s.Workspaces {
		for _, r := range w.Repositories {
			if r.RepositoryID == id {
				p.Workspaces = append(p.Workspaces, RepositoryDeletionWorkspace{w.ID, w.ItemID})
				break
			}
		}
	}
	return p, nil
}
func PlanMachineDeletion(s DomainState, id int64) (MachineDeletionPlan, error) {
	p := MachineDeletionPlan{MachineID: id, Runs: []MachineDeletionRun{}, ActiveRunIDs: []int64{}, WorktreeIDs: []int64{}, RepositoryLocationRepositoryIDs: []int64{}, StateFingerprint: stateFingerprint(s)}
	for _, m := range s.Machines {
		if m.ID == id {
			p.Name = m.Name
			goto found
		}
	}
	return p, fmt.Errorf("Machine %d does not exist", id)
found:
	for _, r := range s.Runs {
		if r.MachineID != id {
			continue
		}
		mr := MachineDeletionRun{ID: r.ID, ItemID: r.ItemID, WorkspaceID: r.WorkspaceID, WorktreeID: r.WorktreeID, State: r.State, PaneStatus: r.PaneStatus}
		for _, i := range s.Items {
			if i.ID == r.ItemID {
				mr.ItemIdentifier = i.HumanIdentifier
				mr.ItemTitle = i.Title
			}
		}
		p.Runs = append(p.Runs, mr)
		if runActive(r) {
			p.ActiveRunIDs = append(p.ActiveRunIDs, r.ID)
		}
	}
	for _, w := range s.Worktrees {
		if w.MachineID == id {
			p.WorktreeIDs = append(p.WorktreeIDs, w.ID)
		}
	}
	for _, l := range s.RepositoryLocations {
		if l.MachineID == id {
			p.RepositoryLocationRepositoryIDs = append(p.RepositoryLocationRepositoryIDs, l.RepositoryID)
		}
	}
	return p, nil
}
func PlanProjectDeletion(s DomainState, id int64) (ParentDeletionPlan, error) {
	for _, p := range s.Projects {
		if p.ID == id {
			return planParent(s, nil, &id, p.Name)
		}
	}
	return ParentDeletionPlan{}, fmt.Errorf("Project %d does not exist", id)
}
func PlanContextDeletion(s DomainState, id int64) (ParentDeletionPlan, error) {
	for _, c := range s.Contexts {
		if c.ID == id {
			return planParent(s, &id, nil, c.Name)
		}
	}
	return ParentDeletionPlan{}, fmt.Errorf("Context %d does not exist", id)
}
func planParent(s DomainState, cid, pid *int64, name string) (ParentDeletionPlan, error) {
	p := ParentDeletionPlan{ContextID: cid, ProjectID: pid, Name: name, Projects: []ParentDeletionProject{}, Items: []ParentDeletionItem{}, Repositories: []ParentDeletionRepository{}, Machines: []ParentDeletionMachine{}, Workspaces: []ItemDeletionWorkspace{}, Runs: []ParentDeletionRun{}, ActiveRunIDs: []int64{}, LinkIDs: []int64{}, AttentionDefaults: []ContextAttentionDefault{}, OrphanedExternalObjectIDs: []int64{}, StateFingerprint: stateFingerprint(s)}
	projects := map[int64]bool{}
	items := map[int64]bool{}
	machines := map[int64]bool{}
	for _, v := range s.Projects {
		if (pid != nil && v.ID == *pid) || (cid != nil && v.ContextID == *cid) {
			projects[v.ID] = true
			p.Projects = append(p.Projects, ParentDeletionProject{v.ID, v.Name})
		}
	}
	for _, v := range s.Items {
		if projects[v.ProjectID] {
			items[v.ID] = true
			p.Items = append(p.Items, ParentDeletionItem{v.ID, v.HumanIdentifier, v.Title, v.ProjectID})
			p.ReminderCount += len(v.Reminders)
		}
	}
	for _, v := range s.Repositories {
		if projects[v.ProjectID] {
			p.Repositories = append(p.Repositories, ParentDeletionRepository{v.ID, v.Name, v.RemoteURL, v.ProjectID})
		}
	}
	if cid != nil {
		for _, v := range s.Machines {
			if v.ContextID == *cid {
				machines[v.ID] = true
				p.Machines = append(p.Machines, ParentDeletionMachine{v.ID, v.Name})
			}
		}
	}
	for _, w := range s.Workspaces {
		if items[w.ItemID] {
			p.Workspaces = append(p.Workspaces, ItemDeletionWorkspace{w.ID, w.ItemID})
		}
	}
	for _, r := range s.Runs {
		if items[r.ItemID] || machines[r.MachineID] {
			item := Item{}
			for _, i := range s.Items {
				if i.ID == r.ItemID {
					item = i
				}
			}
			p.Runs = append(p.Runs, ParentDeletionRun{r.ID, r.ItemID, item.HumanIdentifier, item.Title, r.WorkspaceID, r.WorktreeID, r.MachineID, r.State, r.PaneStatus})
			if runActive(r) {
				p.ActiveRunIDs = append(p.ActiveRunIDs, r.ID)
			}
		}
	}
	for _, r := range s.Relationships {
		if items[r.FromItemID] || items[r.ToItemID] {
			p.RelationshipCount++
		}
	}
	objs := map[int64]bool{}
	for _, l := range s.Links {
		if items[l.ItemID] {
			p.LinkIDs = append(p.LinkIDs, l.ID)
			objs[l.ExternalObjectID] = true
		}
	}
	for _, object := range s.ExternalObjects {
		if !objs[object.ID] {
			continue
		}
		shared := false
		for _, l := range s.Links {
			if l.ExternalObjectID == object.ID && !items[l.ItemID] {
				shared = true
			}
		}
		if !shared {
			p.OrphanedExternalObjectIDs = append(p.OrphanedExternalObjectIDs, object.ID)
		}
	}
	for _, a := range s.AttentionDefaults {
		if cid != nil && a.ContextID == *cid {
			p.AttentionDefaults = append(p.AttentionDefaults, a)
		}
	}
	for _, v := range s.Snapshots {
		for _, oid := range p.OrphanedExternalObjectIDs {
			if v.ExternalObjectID == oid {
				p.OrphanedSnapshotCount++
			}
		}
	}
	for _, v := range s.Activities {
		for _, oid := range p.OrphanedExternalObjectIDs {
			if v.ExternalObjectID == oid {
				p.OrphanedActivityCount++
			}
		}
	}
	return p, nil
}
func PlanResetLocalData(s DomainState) ResetLocalDataPlan {
	p := ResetLocalDataPlan{AffectedRecords: []ResetLocalDataRecord{}, Workspaces: []ItemDeletionWorkspace{}, StateFingerprint: stateFingerprint(s)}
	p.Summary = ResetLocalDataSummary{len(s.Contexts), len(s.Projects), len(s.Repositories), len(s.Items), len(s.Workspaces), len(s.Machines), len(s.Runs), 0, len(s.Relationships), len(s.Links), len(s.ExternalObjects), len(s.Snapshots), len(s.Activities), len(s.AttentionDefaults)}
	for _, c := range s.Contexts {
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Context", c.ID, c.Name})
	}
	for _, v := range s.Projects {
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Project", v.ID, v.Name})
	}
	for _, v := range s.Repositories {
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Repository", v.ID, v.Name})
	}
	for _, v := range s.Items {
		p.Summary.ReminderCount += len(v.Reminders)
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Item", v.ID, v.HumanIdentifier + " · " + v.Title})
	}
	for _, v := range s.Workspaces {
		p.Workspaces = append(p.Workspaces, ItemDeletionWorkspace{v.ID, v.ItemID})
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Workspace", v.ID, fmt.Sprintf("Item %d", v.ItemID)})
	}
	for _, v := range s.Machines {
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Machine", v.ID, v.Name})
	}
	for _, run := range s.Runs {
		itemName, machineName := "unknown", "unknown"
		for _, item := range s.Items {
			if item.ID == run.ItemID {
				itemName = item.HumanIdentifier
			}
		}
		for _, machine := range s.Machines {
			if machine.ID == run.MachineID {
				machineName = machine.Name
			}
		}
		state := string(run.State)
		if state != "" {
			state = strings.ToUpper(state[:1]) + state[1:]
		}
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Run", run.ID, fmt.Sprintf("%s · Item %s · Machine %s", state, itemName, machineName)})
	}
	for _, link := range s.Links {
		itemName, objectName := "unknown", "unknown"
		for _, item := range s.Items {
			if item.ID == link.ItemID {
				itemName = item.HumanIdentifier
			}
		}
		for _, object := range s.ExternalObjects {
			if object.ID == link.ExternalObjectID {
				objectName = object.ExternalKey
			}
		}
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"Link", link.ID, fmt.Sprintf("Item %s → %s", itemName, objectName)})
	}
	for _, object := range s.ExternalObjects {
		p.AffectedRecords = append(p.AffectedRecords, ResetLocalDataRecord{"External Object", object.ID, object.ExternalKey})
	}
	return p
}
