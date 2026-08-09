package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func TestVectorFlushBatchAddsQueuedVectors(t *testing.T) {
	dir := t.TempDir()
	ve, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		4, "HNSW32,Flat", faiss.MetricL2, false,
	)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	defer ve.Close()

	const count = 256
	ve.batchMu.Lock()
	for id := int64(1); id <= count; id++ {
		ve.batch[id] = vectorBatchEntry{vector: []float32{float32(id), 1, 2, 3}}
	}
	ve.pending.Store(true)
	ve.batchMu.Unlock()

	if got := ve.store.index.Ntotal(); got != 0 {
		t.Fatalf("FAISS index contains %d vectors before batch flush", got)
	}
	ids, _, err := ve.SearchTopK([]float32{count, 1, 2, 3}, 1)
	if err != nil || len(ids) != 1 || ids[0] != count {
		t.Fatalf("SearchTopK should include staged vector, ids=%v err=%v", ids, err)
	}
	if err := ve.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}
	if got := ve.store.index.Ntotal(); got != count {
		t.Fatalf("FAISS index contains %d vectors after batch flush, want %d", got, count)
	}
	if _, err := ve.GetVectorByID(count); err != nil {
		t.Fatalf("GetVectorByID after batch flush failed: %v", err)
	}
}

func TestVectorBatchIsLastWriteWinsAndCopiesInput(t *testing.T) {
	dir := t.TempDir()
	ve, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		4, "Flat", faiss.MetricL2, false,
	)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	defer ve.Close()

	first := []float32{1, 0, 0, 0}
	latest := []float32{0, 1, 0, 0}
	if err := ve.InsertVector(7, first); err != nil {
		t.Fatalf("first InsertVector failed: %v", err)
	}
	if err := ve.InsertVector(7, latest); err != nil {
		t.Fatalf("second InsertVector failed: %v", err)
	}
	latest[0] = 99
	if got, err := ve.GetVectorByID(7); err != nil || !reflect.DeepEqual(got, []float32{0, 1, 0, 0}) {
		t.Fatalf("GetVectorByID before flush = %v, err=%v", got, err)
	}

	if err := ve.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}
	got, err := ve.GetVectorByID(7)
	if err != nil {
		t.Fatalf("GetVectorByID failed: %v", err)
	}
	if want := []float32{0, 1, 0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetVectorByID = %v, want copied latest vector %v", got, want)
	}
	if got := ve.store.index.Ntotal(); got != 1 {
		t.Fatalf("FAISS index contains %d entries for one batched ID", got)
	}
}

func TestVectorBatchWALClearsOnlyAfterFlush(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "vector_wal.db")
	ve, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		walPath,
		4, "Flat", faiss.MetricL2, true,
	)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	defer ve.Close()

	vector := []float32{1, 2, 3, 4}
	key := make([]byte, 8)
	binary.LittleEndian.PutUint64(key, 1)
	if err := ve.wal.WriteEntry(string(key), string(float32ArrayToBytes(vector))); err != nil {
		t.Fatalf("write WAL entry: %v", err)
	}
	ve.batch[1] = vectorBatchEntry{vector: vector}
	info, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat WAL before flush: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("WAL was cleared before vector batch became durable")
	}

	if err := ve.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}
	info, err = os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat WAL after flush: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("WAL size after durable batch flush = %d, want 0", info.Size())
	}
}
