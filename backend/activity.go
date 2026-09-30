package backend

import "github.com/piero/ai-mission-manager-wails/backend/domain"

func (r *Runtime) listAuditHistory() []domain.AuditEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.AuditEntry{}, r.state.AuditEntries...)
}
func (r *Runtime) activityTab() domain.ActivityTabView {
	r.mu.Lock()
	defer r.mu.Unlock()
	view := domain.ActivityTabView{AuditEntries: append([]domain.AuditEntry{}, r.state.AuditEntries...), Activities: []domain.ObservedActivity{}}
	for i := len(r.state.Activities) - 1; i >= 0; i-- {
		activity := r.state.Activities[i]
		for _, object := range r.state.ExternalObjects {
			if object.ID == activity.ExternalObjectID {
				view.Activities = append(view.Activities, domain.ObservedActivity{Activity: activity, Object: object})
				break
			}
		}
	}
	return view
}
