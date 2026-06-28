package wal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestWAL(t *testing.T) {
	// Clean up test files before starting
	os.Remove("test_wal.db")

	// Initialize WAL
	w, err := OpenWAL("test_wal.db")
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	defer w.Close()

	// Helper function to print WAL state
	printWALState := func(stage string) {
		entries, err := w.Replay()
		if err != nil && err != io.EOF {
			fmt.Printf("[DEBUG] WAL State after %s: ERROR - %v\n", stage, err)
		} else {
			fmt.Printf("[DEBUG] WAL State after %s: %v\n", stage, entries)
		}
	}

	// Test WriteEntry()
	t.Run("WriteEntry", func(t *testing.T) {
		printWALState("Before WriteEntry")
		err := w.WriteEntry("key1", "value1")
		if err != nil {
			t.Errorf("WriteEntry failed: %v", err)
		}
		printWALState("After WriteEntry")
	})

	// Test Replay before commit
	t.Run("ReplayBeforeCommit", func(t *testing.T) {
		printWALState("Before ReplayBeforeCommit")
		entries, err := w.Replay()
		if err != nil {
			t.Errorf("Replay failed: %v", err)
		}
		if len(entries) != 1 || entries[0].Key != "key1" || entries[0].Value != "value1" {
			t.Errorf("Unexpected replay data: %v", entries)
		}
		printWALState("After ReplayBeforeCommit")
	})

	// Test MarkCommitted()
	t.Run("MarkCommitted", func(t *testing.T) {
		printWALState("Before MarkCommitted")
		err := w.MarkCommitted()
		if err != nil {
			t.Errorf("MarkCommitted failed: %v", err)
		}
		printWALState("After MarkCommitted")
	})

	// Test Replay after commit
	t.Run("ReplayAfterCommit", func(t *testing.T) {
		printWALState("Before ReplayAfterCommit")
		entries, err := w.Replay()
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("Replay failed: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("Expected no entries after commit, but got: %v", entries)
		}
		printWALState("After ReplayAfterCommit")
	})

	// Test WriteEntry, MarkCommited, WriteEntry and Replay
	t.Run("WriteEntryMarkCommitedWriteEntryReplay", func(t *testing.T) {
		printWALState("Before WriteEntryMarkCommitWriteEntryAndReplay")
		err := w.WriteEntry("key2", "value2")
		if err != nil {
			t.Errorf("WriteEntry failed: %v", err)
		}
		err = w.MarkCommitted()
		if err != nil {
			t.Errorf("MarkCommitted failed: %v", err)
		}
		err = w.WriteEntry("longKey3", "longValue3")
		if err != nil {
			t.Errorf("WriteEntry failed: %v", err)
		}
		entries, err := w.Replay()
		if err != nil {
			t.Errorf("Replay failed: %v", err)
		}
		if len(entries) != 1 || entries[0].Key != "longKey3" || entries[0].Value != "longValue3" {
			t.Errorf("Unexpected replay data: %v", entries)
		}

		printWALState("After WriteEntryAndReplayAfterCommit")
	})

	// Test MarkCommitted() on the third entry
	t.Run("MarkCommitted2", func(t *testing.T) {
		printWALState("Before MarkCommitted")
		err := w.MarkCommitted()
		if err != nil {
			t.Errorf("MarkCommitted failed: %v", err)
		}
		printWALState("After MarkCommitted")
	})

	// Test Replay after committing the third entry
	t.Run("ReplayAfterCommit2", func(t *testing.T) {
		printWALState("Before ReplayAfterCommit2")
		entries, err := w.Replay()
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("Replay failed: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("Expected no entries after commit, but got: %v", entries)
		}
		printWALState("After ReplayAfterCommit2")
	})

	t.Run("WriteDelete", func(t *testing.T) {
		printWALState("Before WriteDelete")
		err := w.WriteDelete("deletedKey")
		if err != nil {
			t.Errorf("WriteDelete failed: %v", err)
		}
		entries, err := w.Replay()
		if err != nil {
			t.Errorf("Replay failed after WriteDelete: %v", err)
		}
		if len(entries) != 1 || entries[0].Key != "deletedKey" || entries[0].Value != "" || entries[0].Flag != EntryDeleted {
			t.Errorf("Expected a single DELETE entry for 'deletedKey', but got: %v", entries)
		}
		printWALState("After WriteDelete")
	})

	t.Run("CommitAndReplayAfterDelete", func(t *testing.T) {
		printWALState("Before CommitAndReplayAfterDelete")
		err := w.MarkCommitted()
		if err != nil {
			t.Errorf("MarkCommitted failed: %v", err)
		}
		entries, err := w.Replay()
		if len(entries) != 0 {
			t.Errorf("Expected no entries after commit, but got: %v", entries)
		}
		printWALState("After CommitAndReplayAfterDelete")
	})
	// Test Clear()
	t.Run("Clear", func(t *testing.T) {
		printWALState("Before Clear")
		err := w.Clear()
		if err != nil {
			t.Errorf("Clear failed: %v", err)
		}
		entries, err := w.Replay()
		if err != nil && err != io.EOF {
			t.Errorf("Replay failed after Clear: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("Expected no entries after Clear, but got: %v", entries)
		}
		printWALState("After Clear")
	})
}
