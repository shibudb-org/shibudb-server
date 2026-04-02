package maintenance

import (
	"container/heap"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Job is a recurring maintenance operation registered with a Scheduler.
// Call Close() when the owning resource is torn down; it blocks until any
// in-flight execution of Fn finishes, then prevents all future runs.
type Job struct {
	Name     string
	Fn       func()
	Interval time.Duration

	closed atomic.Bool
	wg     sync.WaitGroup
}

// Close marks the job as closed and waits for any currently running Fn to
// finish before returning. After Close returns, Fn will never be called again.
func (j *Job) Close() {
	j.closed.Store(true)
	j.wg.Wait()
}

// IsClosed reports whether Close has been called.
func (j *Job) IsClosed() bool { return j.closed.Load() }

// run executes Fn once if the job is still open, tracked by the WaitGroup
// so Close() can block until in-flight execution completes.
func (j *Job) run() {
	j.wg.Add(1)
	defer j.wg.Done()
	if !j.closed.Load() {
		j.Fn()
	}
}

// ── heap internals ────────────────────────────────────────────────────────────

type scheduledItem struct {
	job     *Job
	nextRun time.Time
	index   int // position in the heap; kept up-to-date by Swap
}

type minHeap []*scheduledItem

func (h minHeap) Len() int            { return len(h) }
func (h minHeap) Less(i, j int) bool  { return h[i].nextRun.Before(h[j].nextRun) }
func (h minHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *minHeap) Push(x any)         { item := x.(*scheduledItem); item.index = len(*h); *h = append(*h, item) }
func (h *minHeap) Pop() any           { old := *h; n := len(old); item := old[n-1]; old[n-1] = nil; *h = old[:n-1]; return item }

// ── Scheduler ────────────────────────────────────────────────────────────────

// Scheduler runs one dispatcher goroutine that manages a min-heap of upcoming
// jobs and runtime.NumCPU() worker goroutines that execute them.
// Total goroutines: 1 + NumCPU(), regardless of how many jobs are registered.
type Scheduler struct {
	workCh chan *Job           // dispatcher → workers: jobs ready to execute
	addCh  chan *scheduledItem // Register/worker-re-enqueue → dispatcher
	done   chan struct{}
	wg     sync.WaitGroup
}

// New creates and starts a Scheduler. Call Stop() to shut it down cleanly.
func New() *Scheduler {
	n := runtime.NumCPU()
	s := &Scheduler{
		workCh: make(chan *Job, n*4),
		addCh:  make(chan *scheduledItem, 256),
		done:   make(chan struct{}),
	}
	s.wg.Add(1)
	go s.dispatch()
	for i := 0; i < n; i++ {
		s.wg.Add(1)
		go s.work()
	}
	return s
}

// Stop shuts down the scheduler and waits for all internal goroutines to exit.
// It does not call Close on registered jobs; callers must do that themselves
// before (or after) calling Stop.
func (s *Scheduler) Stop() {
	close(s.done)
	s.wg.Wait()
}

// Register adds job to the recurring schedule.
// The first execution is scheduled after one full interval.
// Closed jobs are silently ignored.
func (s *Scheduler) Register(job *Job) {
	if job.IsClosed() {
		return
	}
	item := &scheduledItem{job: job, nextRun: time.Now().Add(job.Interval)}
	select {
	case s.addCh <- item:
	case <-s.done:
	}
}

// dispatch is the single goroutine that owns the min-heap.
// It sleeps until the earliest job is due, then sends it to workCh.
func (s *Scheduler) dispatch() {
	defer s.wg.Done()

	h := &minHeap{}
	heap.Init(h)

	var timer *time.Timer
	stopAndDrainTimer := func() {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
		}
	}
	resetTimer := func() {
		stopAndDrainTimer()
		if h.Len() == 0 {
			return
		}
		d := time.Until((*h)[0].nextRun)
		if d < 0 {
			d = 0
		}
		timer = time.NewTimer(d)
	}

	for {
		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C
		}

		select {
		case <-s.done:
			stopAndDrainTimer()
			return

		case item := <-s.addCh:
			if !item.job.IsClosed() {
				heap.Push(h, item)
				resetTimer()
			}

		case <-timerC:
			now := time.Now()
			for h.Len() > 0 && !(*h)[0].nextRun.After(now) {
				item := heap.Pop(h).(*scheduledItem)
				if item.job.IsClosed() {
					continue
				}
				select {
				case s.workCh <- item.job:
					// worker will re-enqueue after running
				default:
					// all workers busy; push back with a short delay
					item.nextRun = now.Add(5 * time.Millisecond)
					heap.Push(h, item)
				}
			}
			resetTimer()
		}
	}
}

// work pulls jobs from workCh, executes them, then re-enqueues them.
func (s *Scheduler) work() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		case job := <-s.workCh:
			job.run()
			if job.IsClosed() {
				continue
			}
			item := &scheduledItem{
				job:     job,
				nextRun: time.Now().Add(job.Interval),
			}
			select {
			case s.addCh <- item:
			case <-s.done:
			}
		}
	}
}

// ── package-level default ─────────────────────────────────────────────────────

var (
	defaultOnce  sync.Once
	defaultSched *Scheduler
)

// Default returns the process-wide Scheduler, creating it on first call.
// Engine constructors use this so no signature changes are needed anywhere.
func Default() *Scheduler {
	defaultOnce.Do(func() {
		defaultSched = New()
		log.Printf("[maintenance] scheduler started with %d workers", runtime.NumCPU())
	})
	return defaultSched
}
