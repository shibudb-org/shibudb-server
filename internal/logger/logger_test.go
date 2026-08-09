package logger

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe bytes.Buffer for capturing log output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLevelFiltering(t *testing.T) {
	var out syncBuffer
	l := New(&out)

	l.SetLevel(LevelWarn)
	l.Debugf("test", "debug message")
	l.Infof("test", "info message")
	l.Warnf("test", "warn message")
	l.Errorf("test", "error message")

	got := out.String()
	if strings.Contains(got, "debug message") || strings.Contains(got, "info message") {
		t.Fatalf("expected debug/info to be filtered at WARN level, got:\n%s", got)
	}
	if !strings.Contains(got, "warn message") || !strings.Contains(got, "error message") {
		t.Fatalf("expected warn/error to be logged at WARN level, got:\n%s", got)
	}
}

func TestAsyncFlush(t *testing.T) {
	var out syncBuffer
	l := New(&out)
	l.Start()
	defer l.Stop()

	l.Infof("test", "buffered message %d", 42)

	// Nothing should be written synchronously in async mode.
	if got := out.String(); got != "" {
		t.Fatalf("expected empty output before flush tick, got: %q", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "buffered message 42") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("message was not flushed within deadline, output: %q", out.String())
}

func TestStopFlushesPending(t *testing.T) {
	var out syncBuffer
	l := New(&out)
	l.Start()
	l.Infof("test", "pending message")
	l.Stop()

	if !strings.Contains(out.String(), "pending message") {
		t.Fatalf("expected Stop to flush pending logs, got: %q", out.String())
	}
}

func TestLineFormat(t *testing.T) {
	var out syncBuffer
	l := New(&out)
	l.Infof("server", "hello %s", "world")

	got := out.String()
	if !strings.Contains(got, "[INFO ]") || !strings.Contains(got, "[server]") || !strings.Contains(got, "hello world") {
		t.Fatalf("unexpected line format: %q", got)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":   LevelDebug,
		"INFO":    LevelInfo,
		"Warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
	}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Fatalf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("bogus"); err == nil {
		t.Fatal("expected error for invalid level")
	}
}
