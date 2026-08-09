// Package logger provides leveled, buffered, asynchronous logging for the
// ShibuDB server. Log calls append formatted lines to an in-memory buffer;
// a background goroutine (started on server start via Init) flushes the
// buffer to the output every 100ms. The active level can be changed at
// runtime (e.g. from `shibudb manager log-level`).
package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level represents a log severity level. Setting a level enables it and
// everything above it: DEBUG enables all logs, ERROR enables only errors.
type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return fmt.Sprintf("LEVEL(%d)", int32(l))
	}
}

// ParseLevel converts a case-insensitive level name into a Level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("unknown log level %q (valid: debug, info, warn, error)", s)
	}
}

const (
	// flushInterval is how often the background goroutine flushes the buffer.
	flushInterval = 100 * time.Millisecond
	// maxBufferBytes forces an immediate flush if pending logs grow beyond
	// this size between ticks, so a burst of logging cannot grow memory
	// unboundedly.
	maxBufferBytes = 256 * 1024
)

// Logger is a leveled logger that buffers formatted lines in memory and
// flushes them to out on a fixed interval once Start is called. Before
// Start (e.g. in tests or CLI paths), writes are synchronous.
type Logger struct {
	level atomic.Int32

	mu      sync.Mutex
	out     io.Writer
	buf     []byte
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// New creates a Logger writing to out at LevelInfo, in synchronous mode.
// Call Start to enable buffered asynchronous flushing.
func New(out io.Writer) *Logger {
	l := &Logger{out: out}
	l.level.Store(int32(LevelInfo))
	return l
}

// SetLevel changes the minimum enabled level at runtime.
func (l *Logger) SetLevel(level Level) {
	l.level.Store(int32(level))
}

// GetLevel returns the current minimum enabled level.
func (l *Logger) GetLevel() Level {
	return Level(l.level.Load())
}

// SetOutput replaces the destination writer.
func (l *Logger) SetOutput(out io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = out
}

// Start launches the background flush goroutine. It is a no-op if the
// logger is already started.
func (l *Logger) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return
	}
	l.started = true
	l.stopCh = make(chan struct{})
	l.doneCh = make(chan struct{})
	go l.flushLoop(l.stopCh, l.doneCh)
}

// Stop terminates the background goroutine and flushes any pending lines.
// The logger falls back to synchronous mode afterwards.
func (l *Logger) Stop() {
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return
	}
	l.started = false
	stopCh, doneCh := l.stopCh, l.doneCh
	l.mu.Unlock()

	close(stopCh)
	<-doneCh
	l.Flush()
}

func (l *Logger) flushLoop(stopCh, doneCh chan struct{}) {
	defer close(doneCh)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.Flush()
		case <-stopCh:
			return
		}
	}
}

// Flush writes all buffered lines to the output.
func (l *Logger) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
}

func (l *Logger) flushLocked() {
	if len(l.buf) == 0 {
		return
	}
	_, _ = l.out.Write(l.buf)
	l.buf = l.buf[:0]
}

func (l *Logger) logf(level Level, component, format string, args ...interface{}) {
	if level < l.GetLevel() {
		return
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	line := fmt.Sprintf("%s [%-5s] [%s] %s\n", ts, level, component, fmt.Sprintf(format, args...))

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started {
		_, _ = l.out.Write([]byte(line))
		return
	}
	l.buf = append(l.buf, line...)
	if len(l.buf) >= maxBufferBytes {
		l.flushLocked()
	}
}

// Debugf logs at DEBUG level.
func (l *Logger) Debugf(component, format string, args ...interface{}) {
	l.logf(LevelDebug, component, format, args...)
}

// Infof logs at INFO level.
func (l *Logger) Infof(component, format string, args ...interface{}) {
	l.logf(LevelInfo, component, format, args...)
}

// Warnf logs at WARN level.
func (l *Logger) Warnf(component, format string, args ...interface{}) {
	l.logf(LevelWarn, component, format, args...)
}

// Errorf logs at ERROR level.
func (l *Logger) Errorf(component, format string, args ...interface{}) {
	l.logf(LevelError, component, format, args...)
}

// std is the process-wide logger used by the package-level functions.
// It writes synchronously to stdout until Init is called.
var std = New(os.Stdout)

// Init points the global logger at out and starts the background flush
// goroutine. Call this once on server start.
func Init(out io.Writer) {
	std.SetOutput(out)
	std.Start()
}

// Shutdown stops the background goroutine and flushes pending logs.
func Shutdown() {
	std.Stop()
}

// SetLevel changes the global logger's minimum enabled level.
func SetLevel(level Level) {
	std.SetLevel(level)
}

// GetLevel returns the global logger's minimum enabled level.
func GetLevel() Level {
	return std.GetLevel()
}

// Flush writes any buffered lines of the global logger to its output.
func Flush() {
	std.Flush()
}

// Debugf logs at DEBUG level to the global logger.
func Debugf(component, format string, args ...interface{}) {
	std.Debugf(component, format, args...)
}

// Infof logs at INFO level to the global logger.
func Infof(component, format string, args ...interface{}) {
	std.Infof(component, format, args...)
}

// Warnf logs at WARN level to the global logger.
func Warnf(component, format string, args ...interface{}) {
	std.Warnf(component, format, args...)
}

// Errorf logs at ERROR level to the global logger.
func Errorf(component, format string, args ...interface{}) {
	std.Errorf(component, format, args...)
}

// Fatalf logs at ERROR level, flushes all pending logs, and exits the
// process with status 1.
func Fatalf(component, format string, args ...interface{}) {
	std.Errorf(component, format, args...)
	std.Flush()
	os.Exit(1)
}
