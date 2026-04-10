package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func TestRebuildKeyValueIndex(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	walPath := filepath.Join(dir, "wal.db")
	indexPath := filepath.Join(dir, "index.dat")

	db, err := OpenDBWithPathsAndWAL(dataPath, walPath, indexPath, true)
	if err != nil {
		t.Fatalf("OpenDBWithPathsAndWAL failed: %v", err)
	}

	if err := db.PutBatch("alpha", "one"); err != nil {
		t.Fatalf("PutBatch alpha failed: %v", err)
	}
	if err := db.PutBatch("beta", "two"); err != nil {
		t.Fatalf("PutBatch beta failed: %v", err)
	}
	if err := db.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}

	if err := db.PutBatch("alpha", "updated"); err != nil {
		t.Fatalf("PutBatch alpha update failed: %v", err)
	}
	if err := db.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch update failed: %v", err)
	}
	if err := db.Delete("beta"); err != nil {
		t.Fatalf("Delete beta failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := os.WriteFile(indexPath, []byte("corrupt"), 0644); err != nil {
		t.Fatalf("corrupt index write failed: %v", err)
	}

	stats, err := RebuildKeyValueIndex(dataPath, indexPath)
	if err != nil {
		t.Fatalf("RebuildKeyValueIndex failed: %v", err)
	}
	if stats.RecordsScanned != 4 {
		t.Fatalf("expected 4 records scanned, got %d", stats.RecordsScanned)
	}
	if stats.LiveKeys != 1 {
		t.Fatalf("expected 1 live key, got %d", stats.LiveKeys)
	}

	rebuilt, err := OpenDBWithPathsAndWAL(dataPath, walPath, indexPath, true)
	if err != nil {
		t.Fatalf("reopen rebuilt DB failed: %v", err)
	}
	defer rebuilt.Close()

	val, err := rebuilt.Get("alpha")
	if err != nil {
		t.Fatalf("Get alpha failed: %v", err)
	}
	if val != "updated" {
		t.Fatalf("expected updated value, got %q", val)
	}
	if _, err := rebuilt.Get("beta"); err == nil {
		t.Fatal("expected deleted key beta to remain absent after rebuild")
	}
}

func TestRebuildVectorIndexFlat(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0, 1, 0, 0}

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	if err := ve.InsertVector(11, vec1); err != nil {
		t.Fatalf("InsertVector 11 failed: %v", err)
	}
	if err := ve.InsertVector(22, vec2); err != nil {
		t.Fatalf("InsertVector 22 failed: %v", err)
	}
	ve.flushData(true)
	if err := ve.RemoveVector(22); err != nil {
		t.Fatalf("RemoveVector 22 failed: %v", err)
	}
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := os.WriteFile(indexPath, []byte("corrupt"), 0644); err != nil {
		t.Fatalf("corrupt vector index write failed: %v", err)
	}

	stats, err := RebuildVectorIndex(dataPath, indexPath, 4, "Flat", faiss.MetricL2)
	if err != nil {
		t.Fatalf("RebuildVectorIndex failed: %v", err)
	}
	if stats.LiveVectors != 1 {
		t.Fatalf("expected 1 live vector, got %d", stats.LiveVectors)
	}
	if stats.TombstonedIDs != 1 {
		t.Fatalf("expected 1 tombstoned ID, got %d", stats.TombstonedIDs)
	}

	rebuilt, err := NewVectorEngine(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("reopen rebuilt vector engine failed: %v", err)
	}
	defer rebuilt.Close()

	gotVec, err := rebuilt.GetVectorByID(11)
	if err != nil {
		t.Fatalf("GetVectorByID 11 failed: %v", err)
	}
	if len(gotVec) != len(vec1) {
		t.Fatalf("expected vector length %d, got %d", len(vec1), len(gotVec))
	}

	ids, _, err := rebuilt.SearchTopK(vec1, 5)
	if err != nil {
		t.Fatalf("SearchTopK failed: %v", err)
	}
	if len(ids) == 0 || ids[0] != 11 {
		t.Fatalf("expected top hit 11 after rebuild, got %v", ids)
	}
}

func TestRebuildVectorIndexIVFFlat(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, 8, "IVF32,Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}

	queryID := int64(1032)
	var queryVec []float32
	for i := 0; i < 64; i++ {
		vec := randomVector(8)
		id := int64(1000 + i)
		if err := ve.InsertVector(id, vec); err != nil {
			t.Fatalf("InsertVector %d failed: %v", id, err)
		}
		if id == queryID {
			queryVec = append([]float32(nil), vec...)
		}
	}
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := os.WriteFile(indexPath, []byte("corrupt"), 0644); err != nil {
		t.Fatalf("corrupt IVF index write failed: %v", err)
	}

	stats, err := RebuildVectorIndex(dataPath, indexPath, 8, "IVF32,Flat", faiss.MetricL2)
	if err != nil {
		t.Fatalf("RebuildVectorIndex IVF failed: %v", err)
	}
	if stats.LiveVectors != 64 {
		t.Fatalf("expected 64 live vectors, got %d", stats.LiveVectors)
	}
	if stats.TrainingSamples < 32 {
		t.Fatalf("expected at least 32 training samples, got %d", stats.TrainingSamples)
	}

	rebuilt, err := NewVectorEngine(dataPath, indexPath, walPath, 8, "IVF32,Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("reopen rebuilt IVF engine failed: %v", err)
	}
	defer rebuilt.Close()

	ids, _, err := rebuilt.SearchTopK(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchTopK failed: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == queryID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rebuilt IVF index search hits to include %d, got %v", queryID, ids)
	}
}

func TestRebuildVectorIndexTrainingErrorClassification(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, 8, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := ve.InsertVector(int64(100+i), randomVector(8)); err != nil {
			t.Fatalf("InsertVector %d failed: %v", i, err)
		}
	}
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = RebuildVectorIndex(dataPath, indexPath, 8, "IVF32,Flat", faiss.MetricL2)
	if err == nil {
		t.Fatal("expected rebuild error for insufficient training data")
	}
	if !isVectorRebuildTrainingError(err) {
		t.Fatalf("expected training-classified rebuild error, got %v", err)
	}
}

func TestRebuildVectorIndexNonTrainingErrorClassification(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, 8, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := ve.InsertVector(int64(200+i), randomVector(8)); err != nil {
			t.Fatalf("InsertVector %d failed: %v", i, err)
		}
	}
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = RebuildVectorIndex(dataPath, indexPath, 7, "Flat", faiss.MetricL2)
	if err == nil {
		t.Fatal("expected rebuild error for wrong dimension")
	}
	if isVectorRebuildTrainingError(err) {
		t.Fatalf("expected non-training rebuild error, got %v", err)
	}
	if errors.Is(err, errVectorRebuildTraining) {
		t.Fatalf("expected wrong-dimension error not to wrap training sentinel, got %v", err)
	}
}
