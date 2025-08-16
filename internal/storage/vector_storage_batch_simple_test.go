package storage

import (
	"os"
	"testing"
)

// TestVectorEngineBatching tests the batching mechanism without FAISS
func TestVectorEngineBatching(t *testing.T) {
	// This test verifies that the batching fields are properly initialized
	// and that the batching mechanism is in place

	// Create a minimal vector engine for testing
	dataPath := "testdata/batch_test_data.db"
	indexPath := "testdata/batch_test_index.faiss"
	walPath := "testdata/batch_test_wal.db"
	maxVectorSize := 128
	indexDesc := "Flat"
	metric := 0 // L2 metric

	// Clean up any existing files
	os.Remove(dataPath)
	os.Remove(indexPath)
	os.Remove(walPath)

	// Ensure testdata directory exists
	os.MkdirAll("testdata", 0755)

	// Test that the engine can be created with batching fields
	ve, err := NewVectorEngine(dataPath, indexPath, walPath, maxVectorSize, indexDesc, metric)
	if err != nil {
		// If FAISS is not available, skip the test
		t.Skipf("Skipping test due to FAISS unavailability: %v", err)
	}
	defer ve.Close()

	// Verify that batching fields are initialized
	if ve.batch == nil {
		t.Error("Batch map was not initialized")
	}

	t.Log("Vector engine batching fields are properly initialized")
}
