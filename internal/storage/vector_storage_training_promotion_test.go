package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func TestVectorTrainingIndexesPromoteFromFlat(t *testing.T) {
	for _, test := range []struct {
		name      string
		indexDesc string
	}{
		{name: "IVF", indexDesc: "IVF32,Flat"},
		{name: "PQ", indexDesc: "PQ4"},
		{name: "IVF_PQ", indexDesc: "IVF32,PQ4"},
		{name: "HNSW_PQ", indexDesc: "HNSW32,PQ4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			ve, err := NewVectorEngine(
				filepath.Join(dir, "vector_data.db"),
				filepath.Join(dir, "vector_index.faiss"),
				filepath.Join(dir, "vector_wal.db"),
				8, test.indexDesc, faiss.MetricL2, false,
			)
			if err != nil {
				t.Fatalf("NewVectorEngine failed: %v", err)
			}
			defer ve.Close()

			count := ve.indexPromotionThreshold()
			var query []float32
			for id := 1; id <= count; id++ {
				vector := deterministicTrainingVector(id, 8)
				if id == count {
					query = vector
				}
				if err := ve.InsertVector(int64(id), vector); err != nil {
					t.Fatalf("InsertVector %d failed: %v", id, err)
				}
			}
			if err := ve.FlushBatch(); err != nil {
				t.Fatalf("FlushBatch failed: %v", err)
			}
			if err := ve.promoteConfiguredIndex(); err != nil {
				t.Fatalf("promoteConfiguredIndex failed: %v", err)
			}

			ve.lock.RLock()
			configured := ve.store.configured
			trained := ve.store.index.IsTrained()
			total := ve.store.index.Ntotal()
			ve.lock.RUnlock()
			if !configured {
				t.Fatalf("%s index remained on Flat fallback", test.indexDesc)
			}
			if !trained {
				t.Fatalf("%s index is not trained", test.indexDesc)
			}
			if total != int64(count) {
				t.Fatalf("%s index contains %d vectors, want %d", test.indexDesc, total, count)
			}

			ids, _, err := ve.SearchTopK(query, 10)
			if err != nil || len(ids) == 0 {
				t.Fatalf("SearchTopK after promotion returned ids=%v err=%v", ids, err)
			}
		})
	}
}

func TestVectorTrainingIndexPromotesInBackground(t *testing.T) {
	dir := t.TempDir()
	ve, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		8, "IVF32,Flat", faiss.MetricL2, false,
	)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	defer ve.Close()

	for id := 1; id <= ve.indexPromotionThreshold(); id++ {
		if err := ve.InsertVector(int64(id), deterministicTrainingVector(id, 8)); err != nil {
			t.Fatalf("InsertVector %d failed: %v", id, err)
		}
	}
	if err := ve.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ve.lock.RLock()
		configured := ve.store.configured
		ve.lock.RUnlock()
		if configured {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("IVF index was not promoted in the background")
}

func TestVectorTrainingIndexStaysFlatBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	ve, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		8, "IVF32,Flat", faiss.MetricL2, false,
	)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	defer ve.Close()

	for id := 1; id <= 16; id++ {
		if err := ve.InsertVector(int64(id), deterministicTrainingVector(id, 8)); err != nil {
			t.Fatalf("InsertVector %d failed: %v", id, err)
		}
	}
	if err := ve.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}
	if err := ve.promoteConfiguredIndex(); err != nil {
		t.Fatalf("promoteConfiguredIndex failed: %v", err)
	}

	ve.lock.RLock()
	configured := ve.store.configured
	ve.lock.RUnlock()
	if configured {
		t.Fatal("IVF index promoted before reaching its training threshold")
	}
}

func deterministicTrainingVector(id, dimension int) []float32 {
	vector := make([]float32, dimension)
	for i := range vector {
		vector[i] = float32((id*(i+3))%97) / 97
	}
	return vector
}
