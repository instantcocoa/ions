package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConcurrencyTracker_AcquireAndRelease(t *testing.T) {
	ct := NewConcurrencyTracker()
	ctx := context.Background()

	derived, release := ct.Acquire(ctx, "group-1", false)
	assert.NotNil(t, derived)
	assert.NoError(t, derived.Err())

	release()

	// After release, the group should be free.
	ct.mu.Lock()
	_, exists := ct.groups["group-1"]
	ct.mu.Unlock()
	assert.False(t, exists)
}

func TestConcurrencyTracker_CancelInProgress(t *testing.T) {
	ct := NewConcurrencyTracker()
	ctx := context.Background()

	// First holder.
	derived1, release1 := ct.Acquire(ctx, "deploy", false)
	defer release1()

	// Second holder with cancel-in-progress: should cancel the first.
	derived2, release2 := ct.Acquire(ctx, "deploy", true)
	defer release2()

	// First context should be cancelled.
	select {
	case <-derived1.Done():
		// Expected.
	case <-time.After(time.Second):
		t.Fatal("first context should have been cancelled")
	}

	// Second context should still be active.
	assert.NoError(t, derived2.Err())
}

func TestConcurrencyTracker_NoCancelInProgress(t *testing.T) {
	ct := NewConcurrencyTracker()
	ctx := context.Background()

	// First holder.
	derived1, release1 := ct.Acquire(ctx, "deploy", false)
	defer release1()

	// Second holder without cancel-in-progress: first should NOT be cancelled.
	derived2, release2 := ct.Acquire(ctx, "deploy", false)
	defer release2()

	// Both contexts should be active.
	assert.NoError(t, derived1.Err())
	assert.NoError(t, derived2.Err())
}

func TestConcurrencyTracker_DifferentGroups(t *testing.T) {
	ct := NewConcurrencyTracker()
	ctx := context.Background()

	derived1, release1 := ct.Acquire(ctx, "group-a", true)
	defer release1()

	// Different group should not cancel the first.
	derived2, release2 := ct.Acquire(ctx, "group-b", true)
	defer release2()

	assert.NoError(t, derived1.Err())
	assert.NoError(t, derived2.Err())
}

func TestConcurrencyTracker_ReleaseSuperseded(t *testing.T) {
	ct := NewConcurrencyTracker()
	ctx := context.Background()

	// First holder.
	_, release1 := ct.Acquire(ctx, "deploy", false)

	// Second holder supersedes.
	_, release2 := ct.Acquire(ctx, "deploy", true)

	// Release from first holder should not remove the group
	// (it was superseded by the second).
	release1()

	ct.mu.Lock()
	_, exists := ct.groups["deploy"]
	ct.mu.Unlock()
	assert.True(t, exists, "group should still exist because second holder is active")

	// Release from second holder should remove it.
	release2()

	ct.mu.Lock()
	_, exists = ct.groups["deploy"]
	ct.mu.Unlock()
	assert.False(t, exists)
}

func TestConcurrencyTracker_ParentCancellation(t *testing.T) {
	ct := NewConcurrencyTracker()
	ctx, cancel := context.WithCancel(context.Background())

	derived, release := ct.Acquire(ctx, "group-1", false)
	defer release()

	// Cancel the parent.
	cancel()

	// Derived should also be cancelled.
	select {
	case <-derived.Done():
		// Expected.
	case <-time.After(time.Second):
		t.Fatal("derived context should be cancelled when parent is cancelled")
	}
}
