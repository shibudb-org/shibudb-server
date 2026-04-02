package maintenance

import (
	"sync"
	"time"
)

// FlushTarget is a storage engine that can participate in shared flush loops.
type FlushTarget interface {
	MaintenanceFlush()
}

// CheckpointTarget is a storage engine that can participate in shared checkpoint loops.
type CheckpointTarget interface {
	MaintenanceCheckpoint()
}

type dirtyLoop[T comparable] struct {
	interval time.Duration
	run      func(T)

	once  sync.Once
	mu    sync.Mutex
	dirty map[T]struct{}
}

func newDirtyLoop[T comparable](interval time.Duration, run func(T)) *dirtyLoop[T] {
	return &dirtyLoop[T]{
		interval: interval,
		run:      run,
		dirty:    make(map[T]struct{}),
	}
}

func (l *dirtyLoop[T]) mark(target T) {
	l.once.Do(func() {
		go l.loop()
	})

	l.mu.Lock()
	l.dirty[target] = struct{}{}
	l.mu.Unlock()
}

func (l *dirtyLoop[T]) remove(target T) {
	l.mu.Lock()
	delete(l.dirty, target)
	l.mu.Unlock()
}

func (l *dirtyLoop[T]) drain() []T {
	l.mu.Lock()
	if len(l.dirty) == 0 {
		l.mu.Unlock()
		return nil
	}

	items := make([]T, 0, len(l.dirty))
	for target := range l.dirty {
		items = append(items, target)
	}
	clear(l.dirty)
	l.mu.Unlock()
	return items
}

func (l *dirtyLoop[T]) loop() {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for range ticker.C {
		for _, target := range l.drain() {
			l.run(target)
		}
	}
}

var (
	vectorFlushLoop = newDirtyLoop[FlushTarget](50*time.Millisecond, func(target FlushTarget) {
		target.MaintenanceFlush()
	})
	kvFlushLoop = newDirtyLoop[FlushTarget](time.Second, func(target FlushTarget) {
		target.MaintenanceFlush()
	})
	vectorCheckpointLoop = newDirtyLoop[CheckpointTarget](30*time.Second, func(target CheckpointTarget) {
		target.MaintenanceCheckpoint()
	})
)

// MarkVectorFlushDirty schedules a vector engine for the shared 50ms flush cycle.
func MarkVectorFlushDirty(target FlushTarget) {
	vectorFlushLoop.mark(target)
}

// UnregisterVectorFlush removes a vector engine from the shared flush cycle.
func UnregisterVectorFlush(target FlushTarget) {
	vectorFlushLoop.remove(target)
}

// MarkKVFlushDirty schedules a KV engine for the shared 1s flush cycle.
func MarkKVFlushDirty(target FlushTarget) {
	kvFlushLoop.mark(target)
}

// UnregisterKVFlush removes a KV engine from the shared flush cycle.
func UnregisterKVFlush(target FlushTarget) {
	kvFlushLoop.remove(target)
}

// MarkVectorCheckpointDirty schedules a vector engine for the shared 30s checkpoint cycle.
func MarkVectorCheckpointDirty(target CheckpointTarget) {
	vectorCheckpointLoop.mark(target)
}

// UnregisterVectorCheckpoint removes a vector engine from the shared checkpoint cycle.
func UnregisterVectorCheckpoint(target CheckpointTarget) {
	vectorCheckpointLoop.remove(target)
}
