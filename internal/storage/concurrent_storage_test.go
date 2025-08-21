package storage

import (
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestMultipleAutoFlushes(t *testing.T) {
	_ = os.Remove("auto_flush_storage.db")
	_ = os.Remove("auto_flush_wal.db")

	db, err := OpenDBWithPathsAndWAL("auto_flush_storage.db", "auto_flush_wal.db", "auto_flush_index.dat", true)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	var wg sync.WaitGroup
	concurrency := 5
	entriesPerThread := 20
	expected := sync.Map{}

	// Step 1: Concurrent writes spread out to span >3 seconds
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			for j := 0; j < entriesPerThread; j++ {
				key := "flushKey_" + strconv.Itoa(threadID) + "_" + strconv.Itoa(j)
				value := "flushVal_" + strconv.Itoa(threadID) + "_" + strconv.Itoa(j)
				db.PutBatch(key, value)
				expected.Store(key, value)

				// Add delay to allow auto flush to kick in
				time.Sleep(120 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// Step 2: Wait a bit more to let final flush finish
	time.Sleep(2 * time.Second)

	// Step 3: Verify all data
	var errCount int
	expected.Range(func(k, v interface{}) bool {
		key := k.(string)
		want := v.(string)

		got, err := db.Get(key)
		if err != nil {
			t.Errorf("Get failed for key=%s: %v", key, err)
			errCount++
			return true
		}
		if got != want {
			t.Errorf("Mismatch for key=%s: got=%s, want=%s", key, got, want)
			errCount++
		}
		return true
	})

	if errCount > 0 {
		t.Errorf("Test failed with %d incorrect entries", errCount)
	}
}
