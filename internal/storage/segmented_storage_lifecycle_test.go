package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestSegmentedKeyValueRestartRebuildsMissingColdIndex(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	walPath := filepath.Join(dir, "wal.db")
	indexPath := filepath.Join(dir, "index.dat")

	db, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, "btree", SpaceSettings{
		SegmentRolloverBytes:   80,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("OpenDBWithPathsAndWALAndSettings failed: %v", err)
	}

	if err := db.Put("alpha", strings.Repeat("a", 96)); err != nil {
		t.Fatalf("Put alpha failed: %v", err)
	}
	if err := db.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch alpha failed: %v", err)
	}
	if err := db.Put("beta", strings.Repeat("b", 96)); err != nil {
		t.Fatalf("Put beta failed: %v", err)
	}
	if err := db.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch beta failed: %v", err)
	}

	waitForCondition(t, 3*time.Second, func() bool {
		db.lock.RLock()
		defer db.lock.RUnlock()
		return len(db.segments) >= 2 && db.segments[0].meta.State == SegmentStateCold
	}, "first key-value segment to become cold")

	firstIndexPath := db.layout.Descriptor(db.segments[0].meta).IndexPath
	if _, err := os.Stat(firstIndexPath); err != nil {
		t.Fatalf("expected cold index file to exist: %v", err)
	}
	if err := os.Remove(firstIndexPath); err != nil {
		t.Fatalf("remove cold index failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	reopened, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, "btree", SpaceSettings{
		SegmentRolloverBytes:   80,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer reopened.Close()
	if got, err := reopened.Get("alpha"); err != nil || got != strings.Repeat("a", 96) {
		t.Fatalf("Get alpha after restart = %q, err=%v", got, err)
	}
	if got, err := reopened.Get("beta"); err != nil || got != strings.Repeat("b", 96) {
		t.Fatalf("Get beta after restart = %q, err=%v", got, err)
	}
	if _, err := os.Stat(firstIndexPath); err != nil {
		t.Fatalf("expected missing cold index to be rebuilt on startup: %v", err)
	}
}

func TestSegmentedKeyValueMergesOldestColdSegments(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	walPath := filepath.Join(dir, "wal.db")
	indexPath := filepath.Join(dir, "index.dat")

	db, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, "btree", SpaceSettings{
		SegmentRolloverBytes:   80,
		MaxSegmentsBeforeMerge: 2,
	})
	if err != nil {
		t.Fatalf("OpenDBWithPathsAndWALAndSettings failed: %v", err)
	}
	defer db.Close()
	for _, item := range []struct {
		key   string
		value string
	}{
		{"alpha", strings.Repeat("a", 96)},
		{"beta", strings.Repeat("b", 96)},
		{"gamma", strings.Repeat("c", 96)},
	} {
		if err := db.Put(item.key, item.value); err != nil {
			t.Fatalf("Put %s failed: %v", item.key, err)
		}
		if err := db.FlushBatch(); err != nil {
			t.Fatalf("FlushBatch %s failed: %v", item.key, err)
		}
	}
	waitForCondition(t, 5*time.Second, func() bool {
		db.lock.RLock()
		defer db.lock.RUnlock()
		return len(db.segments) == 2
	}, "key-value cold segment merge")
	db.lock.RLock()
	mergedID := db.segments[0].meta.ID
	hotID := db.segments[len(db.segments)-1].meta.ID
	db.lock.RUnlock()
	if mergedID >= hotID {
		t.Fatalf("expected merged cold segment id %d to remain less than hot id %d", mergedID, hotID)
	}
	if got, err := db.Get("alpha"); err != nil || got != strings.Repeat("a", 96) {
		t.Fatalf("Get alpha after merge = %q, err=%v", got, err)
	}
	if got, err := db.Get("beta"); err != nil || got != strings.Repeat("b", 96) {
		t.Fatalf("Get beta after merge = %q, err=%v", got, err)
	}
	if got, err := db.Get("gamma"); err != nil || got != strings.Repeat("c", 96) {
		t.Fatalf("Get gamma after merge = %q, err=%v", got, err)
	}
}
