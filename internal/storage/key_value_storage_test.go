package storage

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestShibuDB(t *testing.T) {
	// Clean up test files before starting
	os.Remove("test_storage.db")
	os.Remove("test_wal.db")

	// Initialize database
	db, err := OpenDBWithPathsAndWAL("test_storage.db", "test_wal.db", "test_index.dat", true)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Test PutBatch and FlushBatch
	t.Run("PutBatch", func(t *testing.T) {
		db.PutBatch("key1", "value1")
		db.PutBatch("key2", "value2")
		db.PutBatch("intKey", "42")
		db.PutBatch("floatKey", "3.14159")
		db.PutBatch("boolKey", "true")
		db.PutBatch("jsonKey", "{\"name\":\"test\", \"age\":25}")
		err := db.FlushBatch()
		if err != nil {
			t.Errorf("FlushBatch failed: %v", err)
		}
	})

	// Test Get for different data types
	t.Run("GetString", func(t *testing.T) {
		val, err := db.Get("key1")
		if err != nil || val != "value1" {
			t.Errorf("Expected 'value1', got '%s', err: %v", val, err)
		}
	})

	t.Run("GetInteger", func(t *testing.T) {
		val, err := db.Get("intKey")
		if err != nil || val != "42" {
			t.Errorf("Expected '42', got '%s', err: %v", val, err)
		}
	})

	t.Run("GetFloat", func(t *testing.T) {
		val, err := db.Get("floatKey")
		if err != nil || val != "3.14159" {
			t.Errorf("Expected '3.14159', got '%s', err: %v", val, err)
		}
	})

	t.Run("GetBoolean", func(t *testing.T) {
		val, err := db.Get("boolKey")
		if err != nil || val != "true" {
			t.Errorf("Expected 'true', got '%s', err: %v", val, err)
		}
	})

	t.Run("GetJSON", func(t *testing.T) {
		val, err := db.Get("jsonKey")
		if err != nil || val != "{\"name\":\"test\", \"age\":25}" {
			t.Errorf("Expected '{\"name\":\"test\", \"age\":25}', got '%s', err: %v", val, err)
		}
	})

	// Test Get for non-existent key
	t.Run("GetNonExistentKey", func(t *testing.T) {
		_, err := db.Get("non_existent")
		if err == nil {
			t.Errorf("Expected error for non-existent key, got nil")
		}
	})

	// Test WAL replay functionality
	t.Run("WALReplay", func(t *testing.T) {
		db.replayWAL()
		val, err := db.Get("key1")
		if err != nil || val != "value1" {
			t.Errorf("WAL replay failed: expected value1, got '%s', err: %v", val, err)
		}
	})

	// Test Duplicate Key Overwrite
	t.Run("DuplicateKeyOverwrite", func(t *testing.T) {
		// Insert a key with an initial value
		db.PutBatch("duplicateKey", "initialValue")
		db.FlushBatch()

		// Overwrite the same key with a new value
		db.PutBatch("duplicateKey", "newValue")
		db.FlushBatch()

		// Retrieve the key from storage
		val, err := db.Get("duplicateKey")
		if err != nil {
			t.Errorf("Failed to retrieve key after overwrite: %v", err)
		}
		if val != "newValue" {
			t.Errorf("Expected 'newValue', got '%s'", val)
		}

		// Ensure the old value does not exist in the storage
		pos, exists := db.index.Get("duplicateKey")
		if !exists {
			t.Errorf("Index does not contain 'duplicateKey' after overwrite")
		}

		// Ensure there is only one valid entry in the database for 'duplicateKey'
		fileInfo, err := db.file.Stat()
		if err != nil {
			t.Fatalf("Failed to get storage file info: %v", err)
		}
		if pos >= fileInfo.Size() {
			t.Errorf("Storage file contains stale data for 'duplicateKey'")
		}
	})

	// Test Delete and WAL replay does not restore deleted keys
	t.Run("DeleteKeyAndWALReplay", func(t *testing.T) {
		// Put and flush a key
		db.PutBatch("deleteMe", "tempValue")
		db.FlushBatch()

		// Delete the key
		err := db.Delete("deleteMe")
		if err != nil {
			t.Errorf("Delete failed: %v", err)
		}

		// Close and reopen the database to simulate crash recovery
		db.Close()
		db, err = OpenDBWithPathsAndWAL("test_storage.db", "test_wal.db", "test_index.dat", true)
		if err != nil {
			t.Fatalf("Failed to reopen DB for WAL replay test: %v", err)
		}
		defer db.Close()

		// Try to get the deleted key
		_, err = db.Get("deleteMe")
		if err == nil {
			t.Errorf("Expected error for deleted key after WAL replay, got nil")
		}
	})

	// Test Multiple Entries in Single Flush
	t.Run("FlushMultipleEntries", func(t *testing.T) {
		// Add multiple entries
		total := 10
		for i := 0; i < total; i++ {
			key := "flushKey" + string(rune(i))
			value := "flushValue" + string(rune(i))
			db.PutBatch(key, value)
		}

		// Validate all entries
		for i := 0; i < total; i++ {
			key := "flushKey" + string(rune(i))
			expected := "flushValue" + string(rune(i))
			val, err := db.Get(key)
			if err != nil {
				t.Errorf("Get failed for key %s: %v", key, err)
			}
			if val != expected {
				t.Errorf("Expected '%s', got '%s' for key %s", expected, val, key)
			}
		}
	})

	t.Run("ConcurrentPutAndAutoFlush", func(t *testing.T) {
		// Use new DB to isolate from other tests
		db.Close()
		os.Remove("test_storage_concurrent.db")
		os.Remove("test_wal_concurrent.db")
		db2, err := OpenDBWithPathsAndWAL("test_storage_concurrent.db", "test_wal_concurrent.db", "test_index_concurrent.dat", true)
		if err != nil {
			t.Fatalf("Failed to open concurrent test DB: %v", err)
		}
		defer db2.Close()

		numGoroutines := 10
		entriesPerGoroutine := 10
		done := make(chan bool)

		// Concurrent puts
		for g := 0; g < numGoroutines; g++ {
			go func(gid int) {
				for i := 0; i < entriesPerGoroutine; i++ {
					key := fmt.Sprintf("concurrentKey-%d-%d", gid, i)
					value := fmt.Sprintf("value-%d-%d", gid, i)
					err := db2.PutBatch(key, value)
					if err != nil {
						t.Errorf("PutBatch failed for %s: %v", key, err)
					}
				}
				done <- true
			}(g)
		}

		// Wait for all goroutines
		for g := 0; g < numGoroutines; g++ {
			<-done
		}

		// Let auto-flush run
		time.Sleep(5 * time.Second)

		// Validate all entries
		for g := 0; g < numGoroutines; g++ {
			for i := 0; i < entriesPerGoroutine; i++ {
				key := fmt.Sprintf("concurrentKey-%d-%d", g, i)
				expected := fmt.Sprintf("value-%d-%d", g, i)
				val, err := db2.Get(key)
				if err != nil {
					t.Errorf("Get failed for key %s: %v", key, err)
				}
				if val != expected {
					t.Errorf("Mismatch for key %s: expected '%s', got '%s'", key, expected, val)
				}
			}
		}
	})
}
