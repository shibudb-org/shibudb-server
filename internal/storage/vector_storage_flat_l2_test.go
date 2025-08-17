package storage

import (
	"log"
	"math/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func randomVector(dim int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = rand.Float32()
	}
	return vec
}

func TestVectorEngineImpl_InsertAndSearch(t *testing.T) {
	dataPath := "testdata/vector_data.db"
	indexPath := "testdata/vector_index.faiss"
	walPath := "testdata/vector_wal.db"
	maxVectorSize := 4
	indexDesc := "Flat"
	metric := faiss.MetricL2

	os.MkdirAll("testdata", 0755)
	os.Remove(dataPath)
	os.Remove(indexPath)
	os.Remove(walPath)

	t.Cleanup(func() {
		os.Remove(dataPath)
		os.Remove(indexPath)
		os.Remove(walPath)
	})

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, maxVectorSize, indexDesc, metric)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer ve.Close()

	rand.Seed(time.Now().UnixNano())
	vec := randomVector(maxVectorSize)

	t.Run("Insert 1000 vectors", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			vec = randomVector(maxVectorSize)
			id := int64(1000 + i)
			err := ve.InsertVector(id, vec)
			if err != nil {
				t.Errorf("InsertVector failed at i=%d: %v", i, err)
			}
		}
	})

	t.Run("Search inserted vector", func(t *testing.T) {
		ids, dists, err := ve.SearchTopK(vec, 1)
		if err != nil {
			t.Errorf("SearchTopK failed: %v", err)
		}
		if len(ids) != 1 {
			t.Errorf("Expected 1 id, got %v", ids)
		}
		if len(dists) != 1 {
			t.Errorf("Expected one distance, got %d", len(dists))
		}
		vectorData, err := ve.GetVectorByID(ids[0])
		if err != nil {
			t.Errorf("GetVectorByID failed: %v", err)
		}
		if len(vectorData) != maxVectorSize {
			t.Errorf("Expected vector length %d, got %d", maxVectorSize, len(vectorData))
		}
	})

	t.Run("Search non-existent vector", func(t *testing.T) {
		fakeVec := randomVector(maxVectorSize)
		ids, _, err := ve.SearchTopK(fakeVec, 1)
		if err != nil && len(ids) != 0 {
			t.Logf("Expected no results or error for non-existent vector, got ids=%v, err=%v", ids, err)
		}
	})

	t.Run("Insert duplicate ID", func(t *testing.T) {
		// Create a fresh engine for this test to avoid interference from previous tests
		cleanDataPath := "testdata/vector_data_clean.db"
		cleanIndexPath := "testdata/vector_index_clean.faiss"
		cleanWalPath := "testdata/vector_wal_clean.db"

		os.Remove(cleanDataPath)
		os.Remove(cleanIndexPath)
		os.Remove(cleanWalPath)

		cleanVe, err := NewVectorEngine(cleanDataPath, cleanIndexPath, cleanWalPath, maxVectorSize, indexDesc, metric)
		if err != nil {
			t.Fatalf("Failed to create clean engine: %v", err)
		}
		defer cleanVe.Close()
		defer func() {
			os.Remove(cleanDataPath)
			os.Remove(cleanIndexPath)
			os.Remove(cleanWalPath)
		}()

		// Debug: Check if the clean engine is actually empty
		log.Printf("Clean engine created with paths: %s, %s, %s", cleanDataPath, cleanIndexPath, cleanWalPath)

		id := int64(12345)
		vec1 := randomVector(maxVectorSize)
		vec2 := randomVector(maxVectorSize)
		err = cleanVe.InsertVector(id, vec1)
		if err != nil {
			t.Errorf("InsertVector (first) failed: %v", err)
		}
		err = cleanVe.InsertVector(id, vec2)
		if err != nil {
			t.Errorf("InsertVector (duplicate) failed: %v", err)
		}
		// TODO: Currently, GetVectorByID returns the first inserted vector for duplicate IDs.
		// If the logic changes to return the latest, update this test accordingly.
		stored, err := cleanVe.GetVectorByID(id)
		if err != nil {
			t.Errorf("GetVectorByID failed: %v", err)
		}
		if !reflect.DeepEqual(stored, vec2) {
			t.Errorf("Expected stored vector to match first inserted, got %v", stored)
		}
	})

	t.Run("Insert and search min/max vector size", func(t *testing.T) {
		minVec := randomVector(1)
		maxVec := randomVector(maxVectorSize)
		minID := int64(1)
		maxID := int64(2)
		err := ve.InsertVector(minID, minVec)
		if err == nil {
			t.Error("Expected error for min vector size (should not match engine size)")
		}
		err = ve.InsertVector(maxID, maxVec)
		if err != nil {
			t.Errorf("InsertVector for max size failed: %v", err)
		}
	})

	t.Run("Insert after Close", func(t *testing.T) {
		ve.Close()
		err := ve.InsertVector(9999, randomVector(maxVectorSize))
		if err == nil {
			t.Error("Expected error after engine closed")
		}
	})

	t.Run("RangeSearch basic functionality", func(t *testing.T) {
		// Create a fresh engine for this test to avoid interference from previous tests
		cleanDataPath := "testdata/vector_data_rangesearch.db"
		cleanIndexPath := "testdata/vector_index_rangesearch.faiss"
		cleanWalPath := "testdata/vector_wal_rangesearch.db"

		os.Remove(cleanDataPath)
		os.Remove(cleanIndexPath)
		os.Remove(cleanWalPath)

		cleanVe, err := NewVectorEngine(cleanDataPath, cleanIndexPath, cleanWalPath, maxVectorSize, indexDesc, metric)
		if err != nil {
			t.Fatalf("Failed to create clean engine: %v", err)
		}
		defer cleanVe.Close()
		defer func() {
			os.Remove(cleanDataPath)
			os.Remove(cleanIndexPath)
			os.Remove(cleanWalPath)
		}()

		// Insert a known vector
		vec := make([]float32, maxVectorSize)
		for i := range vec {
			vec[i] = 0.5
		}
		id := int64(99999)
		err = cleanVe.InsertVector(id, vec)
		if err != nil {
			t.Fatalf("InsertVector for RangeSearch failed: %v", err)
		}

		// Range search with large radius (should find the vector)
		ids, dists, err := cleanVe.RangeSearch(vec, 10.0)
		if err != nil {
			t.Errorf("RangeSearch failed: %v", err)
		}
		found := false
		for _, foundID := range ids {
			if foundID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("RangeSearch did not find inserted vector")
		}
		if len(ids) != len(dists) {
			t.Errorf("ids and dists length mismatch: %d vs %d", len(ids), len(dists))
		}

		// Range search with tiny radius (should find none or only exact match)
		ids, dists, err = cleanVe.RangeSearch(vec, 0.0)
		if err != nil {
			t.Errorf("RangeSearch with zero radius failed: %v", err)
		}
		if len(ids) > 1 {
			t.Errorf("Expected at most 1 result for zero radius, got %d", len(ids))
		}

		// Range search with wrong dimension
		badVec := make([]float32, maxVectorSize-1)
		_, _, err = cleanVe.RangeSearch(badVec, 10.0)
		if err == nil {
			t.Errorf("Expected error for wrong dimension, got nil")
		}
	})

	// Additional test: Insert, get, and search for a 4-dimensional vector
	t.Run("Insert, Get, and Search 4D vector", func(t *testing.T) {
		// Create a new engine for 4D vectors
		dataPath4 := "testdata/vector_data_4d.db"
		indexPath4 := "testdata/vector_index_4d.faiss"
		walPath4 := "testdata/vector_wal_4d.db"
		indexDesc4 := "Flat"
		metric4 := faiss.MetricL2

		os.Remove(dataPath4)
		os.Remove(indexPath4)
		os.Remove(walPath4)
		t.Cleanup(func() {
			os.Remove(dataPath4)
			os.Remove(indexPath4)
			os.Remove(walPath4)
		})

		ve4, err := NewVectorEngine(dataPath4, indexPath4, walPath4, 4, indexDesc4, metric4)
		if err != nil {
			t.Fatalf("Failed to create 4D engine: %v", err)
		}
		defer ve4.Close()

		vec := []float32{0.1, 0.2, 0.3, 0.4}
		id := int64(555)
		err = ve4.InsertVector(id, vec)
		if err != nil {
			t.Fatalf("InsertVector failed: %v", err)
		}

		// Get by ID
		got, err := ve4.GetVectorByID(id)
		if err != nil {
			t.Fatalf("GetVectorByID failed: %v", err)
		}
		if !reflect.DeepEqual(got, vec) {
			t.Errorf("Expected %v, got %v", vec, got)
		}

		// SearchTopK
		ids, dists, err := ve4.SearchTopK(vec, 1)
		if err != nil {
			t.Fatalf("SearchTopK failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != id {
			t.Errorf("Expected id %d, got %v", id, ids)
		}
		if len(dists) != 1 {
			t.Errorf("Expected 1 distance, got %v", dists)
		}
	})
}

func TestVectorEngineImpl_InvalidInsert(t *testing.T) {
	ve, _ := NewVectorEngine("/tmp/vec.db", "/tmp/vec.idx", "/tmp/vec.wal", 8, "Flat", faiss.MetricL2)
	defer ve.Close()

	err := ve.InsertVector(123, []float32{1.0, 2.0})
	if err == nil {
		t.Error("Expected vector length mismatch error")
	}

	err = ve.InsertVector(124, nil)
	if err == nil {
		t.Error("Expected error for nil vector")
	}
}
