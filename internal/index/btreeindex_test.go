package index

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestBTreeIndex(t *testing.T) {
	// Remove any existing index file to start fresh
	os.Remove("test_index.dat")

	// Initialize BTreeIndex
	idx, err := NewBTreeIndex("test_index.dat")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}
	defer idx.Close()

	// Test inserting key-value pairs
	idx.Add("key1", 100)
	idx.Add("key2", 200)
	idx.Add("key3", 300)

	// Test retrieving keys
	pos, found := idx.Get("key1")
	if !found || pos != 100 {
		t.Errorf("Expected position 100 for key1, got %d", pos)
	}

	pos, found = idx.Get("key2")
	if !found || pos != 200 {
		t.Errorf("Expected position 200 for key2, got %d", pos)
	}

	pos, found = idx.Get("key3")
	if !found || pos != 300 {
		t.Errorf("Expected position 300 for key3, got %d", pos)
	}

	// Test retrieving a non-existent key
	pos, found = idx.Get("key4")
	if found {
		t.Errorf("Expected key4 to not be found, but got position %d", pos)
	}

	// Test updating an existing key
	idx.Add("key1", 500)
	pos, found = idx.Get("key1")
	if !found || pos != 500 {
		t.Errorf("Expected position 500 for key1 after update, got %d", pos)
	}

	// Test deleting a key
	idx.Remove("key2")
	pos, found = idx.Get("key2")
	if found {
		t.Errorf("Expected key2 to be deleted, but got position %d", pos)
	}

	// Test re-adding a deleted key
	idx.Add("key2", 600)
	pos, found = idx.Get("key2")
	if !found || pos != 600 {
		t.Errorf("Expected position 600 for re-added key2, got %d", pos)
	}

	// Test deleting a non-existent key (should not cause any issue)
	idx.Remove("key4") // Key4 was never added, so nothing should happen

	// Ensure all remaining keys are still accessible
	pos, found = idx.Get("key1")
	if !found || pos != 500 {
		t.Errorf("Expected position 500 for key1, got %d", pos)
	}

	pos, found = idx.Get("key3")
	if !found || pos != 300 {
		t.Errorf("Expected position 300 for key3, got %d", pos)
	}

	// Test index persistence after a crash
	t.Run("PersistenceTest", func(t *testing.T) {
		// Close current index to flush changes
		idx.Close()

		// Reload the index from file
		idx, err = NewBTreeIndex("test_index.dat")
		if err != nil {
			t.Fatalf("Failed to reload index: %v", err)
		}
		defer idx.Close()

		// Ensure all keys are still retrievable after reload
		pos, found := idx.Get("key1")
		if !found || pos != 500 {
			t.Errorf("Expected position 500 for key1 after reload, got %d", pos)
		}

		pos, found = idx.Get("key2")
		if !found || pos != 600 {
			t.Errorf("Expected position 600 for key2 after reload, got %d", pos)
		}

		pos, found = idx.Get("key3")
		if !found || pos != 300 {
			t.Errorf("Expected position 300 for key3 after reload, got %d", pos)
		}

		// Ensure deleted key is not present after reload
		pos, found = idx.Get("key4")
		if found {
			t.Errorf("Expected key4 to not be found after reload, but got position %d", pos)
		}
	})

	// New test: Ensure index is written to file after adding a key
	t.Run("IndexPersistenceToFile", func(t *testing.T) {
		// Remove any existing index file to start fresh
		os.Remove("test_index_persistence.dat")

		// Initialize BTreeIndex
		idx, err := NewBTreeIndex("test_index_persistence.dat")
		if err != nil {
			t.Fatalf("Failed to create index: %v", err)
		}

		// Add a key-value pair
		idx.Add("persistentKey", 12345)

		// Close the index to flush changes to disk
		idx.Close()

		// Verify the index file is not empty
		info, err := os.Stat("test_index_persistence.dat")
		if err != nil {
			t.Fatalf("Failed to stat index file: %v", err)
		}

		if info.Size() == 0 {
			t.Errorf("Index file is empty after adding key, expected non-zero size")
		}

		// Reopen the index
		idx, err = NewBTreeIndex("test_index_persistence.dat")
		if err != nil {
			t.Fatalf("Failed to reopen index: %v", err)
		}
		defer idx.Close()

		// Verify the key is still present
		pos, found := idx.Get("persistentKey")
		if !found || pos != 12345 {
			t.Errorf("Expected position 12345 for persistentKey after reload, got %d", pos)
		}
	})

	t.Run("ConcurrentUpdates", func(t *testing.T) {
		os.Remove("test_index_concurrent.dat")

		idx, err := NewBTreeIndex("test_index_concurrent.dat")
		if err != nil {
			t.Fatalf("Failed to create index: %v", err)
		}
		defer idx.Close()

		const numGoroutines = 10
		const updatesPerGoroutine = 100

		var wg sync.WaitGroup
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < updatesPerGoroutine; j++ {
					key := fmt.Sprintf("key-%d", j)
					pos := int64(id*1000 + j)
					if err := idx.Add(key, pos); err != nil {
						t.Errorf("Add failed in goroutine %d: %v", id, err)
					}
				}
			}(i)
		}
		wg.Wait()

		// Now verify that all keys exist and contain one of the expected values
		for j := 0; j < updatesPerGoroutine; j++ {
			key := fmt.Sprintf("key-%d", j)
			pos, found := idx.Get(key)
			if !found {
				t.Errorf("Expected to find key %s, but not found", key)
			}
			if pos < 0 {
				t.Errorf("Invalid position for key %s: %d", key, pos)
			}
		}
	})
}
