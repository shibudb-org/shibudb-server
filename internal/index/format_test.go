package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newIndexForTest(t *testing.T, indexType, filename string) KeyValueIndex {
	t.Helper()
	idx, err := NewKeyValueIndex(filename, indexType)
	if err != nil {
		t.Fatalf("open %s index: %v", indexType, err)
	}
	return idx
}

// Regression: positions used to be persisted as uint32, silently truncating
// offsets in data files larger than 4 GiB.
func TestIndex_LargePositionRoundTrip(t *testing.T) {
	for _, indexType := range []string{"hashmap", "btree"} {
		t.Run(indexType, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "large_pos.idx")
			bigPos := int64(5) << 30 // > 4 GiB

			idx := newIndexForTest(t, indexType, filename)
			if err := idx.Add("big", bigPos); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if err := idx.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			idx = newIndexForTest(t, indexType, filename)
			defer idx.Close()
			pos, ok := idx.Get("big")
			if !ok || pos != bigPos {
				t.Fatalf("expected %d after reload, got %d (ok=%v)", bigPos, pos, ok)
			}
		})
	}
}

// Regression: reloading used to scan the zero-filled tail of the mmap and set
// the write offset near the end of the file, so every reopen+write cycle grew
// the file even with a constant number of entries.
func TestIndex_ReopenDoesNotGrowFile(t *testing.T) {
	for _, indexType := range []string{"hashmap", "btree"} {
		t.Run(indexType, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "regrow.idx")

			for cycle := 0; cycle < 5; cycle++ {
				idx := newIndexForTest(t, indexType, filename)
				for i := 0; i < 10; i++ {
					if err := idx.Add(fmt.Sprintf("key%d", i), int64(cycle*100+i)); err != nil {
						t.Fatalf("Add: %v", err)
					}
				}
				if err := idx.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
			}

			info, err := os.Stat(filename)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Size() != indexInitialFileSize {
				t.Fatalf("file grew to %d bytes across reopen cycles, expected %d", info.Size(), indexInitialFileSize)
			}

			// Note: rewriting the same keys appends duplicate log entries;
			// last write wins on reload.
			idx := newIndexForTest(t, indexType, filename)
			defer idx.Close()
			for i := 0; i < 10; i++ {
				pos, ok := idx.Get(fmt.Sprintf("key%d", i))
				if !ok || pos != int64(400+i) {
					t.Fatalf("key%d: expected %d, got %d (ok=%v)", i, 400+i, pos, ok)
				}
			}
		})
	}
}

// Old-format files (no header) must fail loading so callers rebuild them from
// the data file instead of silently misparsing entries.
func TestIndex_RejectsUnrecognizedFormat(t *testing.T) {
	for _, indexType := range []string{"hashmap", "btree"} {
		t.Run(indexType, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "old_format.idx")

			// Simulate an old-format file: entries with no header, 4-byte
			// positions, zero-padded to the initial mmap size.
			raw := make([]byte, indexInitialFileSize)
			copy(raw, []byte{4, 0, 0, 0, 100, 0, 0, 0, 'k', 'e', 'y', '1'})
			if err := os.WriteFile(filename, raw, 0666); err != nil {
				t.Fatalf("write old-format file: %v", err)
			}

			if _, err := NewKeyValueIndex(filename, indexType); err == nil {
				t.Fatal("expected error opening old-format index file, got nil")
			}
		})
	}
}

func TestIndex_SyncPersistsWithoutClose(t *testing.T) {
	for _, indexType := range []string{"hashmap", "btree"} {
		t.Run(indexType, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "sync.idx")

			idx := newIndexForTest(t, indexType, filename)
			defer idx.Close()
			if err := idx.Add("key1", 42); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if err := idx.Sync(); err != nil {
				t.Fatalf("Sync: %v", err)
			}

			// The mapping is MAP_SHARED, so a second reader through the file
			// must observe the synced entry.
			other := newIndexForTest(t, indexType, filename)
			defer other.Close()
			pos, ok := other.Get("key1")
			if !ok || pos != 42 {
				t.Fatalf("expected 42 after Sync, got %d (ok=%v)", pos, ok)
			}
		})
	}
}

// Regression: Remove used to munmap and remap the file without holding the
// mmap lock, racing with concurrent Adds writing through the old mapping.
// Run with -race to exercise this.
func TestIndex_ConcurrentAddRemove(t *testing.T) {
	for _, indexType := range []string{"hashmap", "btree"} {
		t.Run(indexType, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "concurrent.idx")
			idx := newIndexForTest(t, indexType, filename)
			defer idx.Close()

			const workers = 8
			const opsPerWorker = 200

			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < opsPerWorker; i++ {
						key := fmt.Sprintf("key-%d-%d", w, i)
						if err := idx.Add(key, int64(i)); err != nil {
							t.Errorf("Add: %v", err)
							return
						}
						if i%10 == 0 {
							if err := idx.Remove(key); err != nil {
								t.Errorf("Remove: %v", err)
								return
							}
						}
					}
				}(w)
			}
			wg.Wait()
		})
	}
}
