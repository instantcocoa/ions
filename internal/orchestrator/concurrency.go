package orchestrator

import (
	"context"
	"sync"
)

// ConcurrencyTracker tracks active concurrency groups and supports
// cancel-in-progress behavior. When a new run claims a concurrency group
// that already has an active run, the old run is cancelled if
// cancel-in-progress is true.
type ConcurrencyTracker struct {
	mu     sync.Mutex
	groups map[string]*concurrencyEntry
	nextID uint64
}

type concurrencyEntry struct {
	id     uint64
	cancel context.CancelFunc
}

// NewConcurrencyTracker creates a new tracker.
func NewConcurrencyTracker() *ConcurrencyTracker {
	return &ConcurrencyTracker{
		groups: make(map[string]*concurrencyEntry),
	}
}

// Acquire claims a concurrency group. If cancelInProgress is true and the
// group is already held, the previous holder's context is cancelled.
// Returns a context derived from the parent and a release function that
// MUST be called when the run/job finishes.
func (ct *ConcurrencyTracker) Acquire(ctx context.Context, group string, cancelInProgress bool) (context.Context, func()) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	// Cancel existing holder if cancel-in-progress is enabled.
	if existing, ok := ct.groups[group]; ok && cancelInProgress {
		existing.cancel()
	}

	ct.nextID++
	id := ct.nextID

	derived, cancel := context.WithCancel(ctx)
	ct.groups[group] = &concurrencyEntry{id: id, cancel: cancel}

	release := func() {
		ct.mu.Lock()
		defer ct.mu.Unlock()
		// Only remove if we're still the current holder (not superseded).
		if entry, ok := ct.groups[group]; ok && entry.id == id {
			delete(ct.groups, group)
		}
	}

	return derived, release
}
