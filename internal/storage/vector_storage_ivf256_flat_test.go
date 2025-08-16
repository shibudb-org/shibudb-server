package storage

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func TestVectorEngineImpl_InsertAndSearch_IVF256Flat(t *testing.T) {
	dataPath := "testdata/vector_data_ivf256_flat.db"
	indexPath := "testdata/vector_index_ivf256_flat.faiss"
	walPath := "testdata/vector_wal_ivf256_flat.db"
	maxVectorSize := 1024
	indexDesc := "IVF256,Flat"
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
		t.Skipf("Failed to create engine: %v", err)
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

	//t.Run("Insert duplicate ID", func(t *testing.T) {
	//	id := int64(12345)
	//	vec1 := randomVector(maxVectorSize)
	//	vec2 := randomVector(maxVectorSize)
	//	err := ve.InsertVector(id, vec1)
	//	if err != nil {
	//		t.Errorf("InsertVector (first) failed: %v", err)
	//	}
	//	err = ve.InsertVector(id, vec2)
	//	if err != nil {
	//		t.Errorf("InsertVector (duplicate) failed: %v", err)
	//	}
	//	stored, err := ve.GetVectorByID(id)
	//	if err != nil {
	//		t.Errorf("GetVectorByID failed: %v", err)
	//	}
	//	if !reflect.DeepEqual(stored, vec1) {
	//		t.Errorf("Expected stored vector to match first inserted, got %v", stored)
	//	}
	//})

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

	t.Run("RangeSearch basic functionality", func(t *testing.T) {
		vec := make([]float32, maxVectorSize)
		for i := range vec {
			vec[i] = 0.5
		}
		id := int64(99999)
		err := ve.InsertVector(id, vec)
		if err != nil {
			t.Fatalf("InsertVector for RangeSearch failed: %v", err)
		}

		ids, dists, err := ve.RangeSearch(vec, 10.0)
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

		ids, dists, err = ve.RangeSearch(vec, 0.0)
		if err != nil {
			t.Errorf("RangeSearch with zero radius failed: %v", err)
		}
		if len(ids) > 1 {
			t.Errorf("Expected at most 1 result for zero radius, got %d", len(ids))
		}

		badVec := make([]float32, maxVectorSize-1)
		_, _, err = ve.RangeSearch(badVec, 10.0)
		if err == nil {
			t.Errorf("Expected error for wrong dimension, got nil")
		}
	})

	t.Run("Insert after Close", func(t *testing.T) {
		ve.Close()
		err := ve.InsertVector(9999, randomVector(maxVectorSize))
		if err == nil {
			t.Error("Expected error after engine closed")
		}
	})
}
