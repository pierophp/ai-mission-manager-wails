package backend

import "github.com/piero/ai-mission-manager-wails/backend/domain"

func (r *Runtime) home(contextID *int64, now string) domain.HomeView {
	r.mu.Lock()
	state := r.state
	r.mu.Unlock()
	return domain.HomeViewFor(state, contextID, now)
}
