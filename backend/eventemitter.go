package backend

// EventEmitter is the application seam for events consumed by the frontend.
// Domain transitions stay pure; adapters publish only after their commit.
type EventEmitter interface {
	Emit(name string, payload any)
}

type EventEmitterFunc func(name string, payload any)

func (emit EventEmitterFunc) Emit(name string, payload any) { emit(name, payload) }

type discardEventEmitter struct{}

func (discardEventEmitter) Emit(string, any) {}
