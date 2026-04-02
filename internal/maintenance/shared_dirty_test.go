package maintenance

import (
	"sync"
	"testing"
	"time"
)

type testFlushTarget struct {
	mu    sync.Mutex
	calls int
	ch    chan struct{}
}

func (t *testFlushTarget) MaintenanceFlush() {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	t.ch <- struct{}{}
}

func TestDirtyLoopDeduplicatesTargetsPerTick(t *testing.T) {
	target := &testFlushTarget{ch: make(chan struct{}, 4)}
	loop := newDirtyLoop[FlushTarget](10*time.Millisecond, func(target FlushTarget) {
		target.MaintenanceFlush()
	})

	loop.mark(target)
	loop.mark(target)

	select {
	case <-target.ch:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected target to be flushed")
	}

	select {
	case <-target.ch:
		t.Fatal("expected target to be flushed only once for the tick")
	case <-time.After(30 * time.Millisecond):
	}

	target.mu.Lock()
	defer target.mu.Unlock()
	if target.calls != 1 {
		t.Fatalf("calls = %d, want 1", target.calls)
	}
}

func TestDirtyLoopRemovePreventsExecution(t *testing.T) {
	target := &testFlushTarget{ch: make(chan struct{}, 1)}
	loop := newDirtyLoop[FlushTarget](10*time.Millisecond, func(target FlushTarget) {
		target.MaintenanceFlush()
	})

	loop.mark(target)
	loop.remove(target)

	select {
	case <-target.ch:
		t.Fatal("expected removed target not to run")
	case <-time.After(50 * time.Millisecond):
	}
}
