package crawler

import (
	"context"
	"sync"
)

// FakeSandbox returns scripted observations for tests (no containers).
type FakeSandbox struct {
	mu    sync.Mutex
	Calls []Target
	Hook  func(Target) Observation
}

func (f *FakeSandbox) Observe(ctx context.Context, t Target) Observation {
	f.mu.Lock()
	f.Calls = append(f.Calls, t)
	hook := f.Hook
	f.mu.Unlock()
	if hook != nil {
		return hook(t)
	}
	return Observation{ServerID: t.ServerID, EventType: EventOK, Tools: nil}
}
