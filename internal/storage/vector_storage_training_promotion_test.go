package storage

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func newTrainingPromotionTestEngine(t *testing.T, dir, indexDesc string, dimension int) *VectorEngineImpl {
	t.Helper()
	engine, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		dimension,
		indexDesc,
		faiss.MetricL2,
		false,
	)
	if err != nil {
		t.Fatalf("create %s engine: %v", indexDesc, err)
	}
	return engine
}

func waitForTrainingIndexMode(t *testing.T, engine *VectorEngineImpl, mode VectorIndexMode) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		engine.lock.RLock()
		got := engine.indexMeta.Mode
		engine.lock.RUnlock()
		if got == mode {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for vector index mode %q", mode)
}

func appendPromotionTestVector(t *testing.T, path string, id int64, vector []float32) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8+4*len(vector))
	binary.LittleEndian.PutUint64(buf[:8], uint64(id))
	for i, value := range vector {
		binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(value))
	}
	if _, err := file.Write(buf); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTrainingIndexPromotionIVFAndRestart(t *testing.T) {
	dir := t.TempDir()
	engine := newTrainingPromotionTestEngine(t, dir, "IVF2,Flat", 4)

	if err := engine.InsertVector(1, []float32{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	engine.flushData(true)
	engine.lock.RLock()
	if got := engine.indexMeta.Mode; got != VectorIndexModeFallback {
		engine.lock.RUnlock()
		t.Fatalf("below-threshold mode = %q, want fallback", got)
	}
	engine.lock.RUnlock()

	if err := engine.InsertVector(2, []float32{10, 10, 10, 10}); err != nil {
		t.Fatal(err)
	}
	engine.flushData(true)
	waitForTrainingIndexMode(t, engine, VectorIndexModeTrained)

	engine.lock.RLock()
	meta := engine.indexMeta
	engine.lock.RUnlock()
	if meta.IndexDataBytes == 0 {
		t.Fatal("trained index has no data watermark")
	}
	if _, err := os.Stat(filepath.Join(dir, "vector_index.faiss")); err != nil {
		t.Fatalf("promoted index was not persisted: %v", err)
	}
	persistedMeta, err := loadVectorIndexMeta(vectorIndexMetaPath(filepath.Join(dir, "vector_index.faiss")))
	if err != nil {
		t.Fatalf("read promoted index metadata: %v", err)
	}
	if persistedMeta.Mode != VectorIndexModeTrained || persistedMeta.IndexDataBytes != meta.IndexDataBytes {
		t.Fatalf("persisted promotion metadata = %+v, want trained watermark %d", persistedMeta, meta.IndexDataBytes)
	}
	if _, err := os.Stat(filepath.Join(dir, "vector_segments.manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("single-file vector engine unexpectedly created a segment manifest: %v", err)
	}

	if err := engine.InsertVector(1, []float32{1, 1, 1, 1}); err != nil {
		t.Fatalf("update trained IVF index: %v", err)
	}
	if err := engine.RemoveVector(2); err != nil {
		t.Fatalf("delete from trained IVF index: %v", err)
	}
	ids, distances, err := engine.SearchTopK([]float32{1, 1, 1, 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 1 || len(distances) != 1 || distances[0] != 0 {
		t.Fatalf("trained IVF search after update/delete = ids %v distances %v", ids, distances)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a durable data tail written after the last index snapshot.
	appendPromotionTestVector(t, filepath.Join(dir, "vector_data.db"), 3, []float32{20, 20, 20, 20})

	reopened := newTrainingPromotionTestEngine(t, dir, "IVF2,Flat", 4)
	waitForTrainingIndexMode(t, reopened, VectorIndexModeTrained)
	if _, err := reopened.GetVectorByID(1); err != nil {
		t.Fatalf("updated vector missing after restart: %v", err)
	}
	if _, err := reopened.GetVectorByID(2); err == nil {
		t.Fatal("deleted vector returned after restart")
	}
	if _, err := reopened.GetVectorByID(3); err != nil {
		t.Fatalf("watermark tail vector missing after restart: %v", err)
	}
	ids, _, err = reopened.SearchTopK([]float32{10, 10, 10, 10}, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id == 2 {
			t.Fatal("deleted vector returned by trained IVF search after restart")
		}
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "vector_index.faiss"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	recovered := newTrainingPromotionTestEngine(t, dir, "IVF2,Flat", 4)
	defer recovered.Close()
	waitForTrainingIndexMode(t, recovered, VectorIndexModeTrained)
	if _, err := recovered.GetVectorByID(3); err != nil {
		t.Fatalf("full rebuild after corrupt index lost data: %v", err)
	}
}

func TestTrainingIndexPromotionPQ(t *testing.T) {
	dir := t.TempDir()
	engine := newTrainingPromotionTestEngine(t, dir, "PQ2", 4)
	defer engine.Close()

	for i := 0; i < 256; i++ {
		value := float32(i) / 255
		if err := engine.InsertVector(int64(i+1), []float32{value, value * value, 1 - value, value / 2}); err != nil {
			t.Fatalf("insert PQ training vector %d: %v", i, err)
		}
	}
	engine.flushData(true)
	waitForTrainingIndexMode(t, engine, VectorIndexModeTrained)
	if err := engine.InsertVector(1, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("update trained PQ index: %v", err)
	}
	if err := engine.RemoveVector(2); err != nil {
		t.Fatalf("delete from trained PQ index: %v", err)
	}
}

func TestTrainingPromotionBlocksMutationsButNotSearch(t *testing.T) {
	dir := t.TempDir()
	engine := newTrainingPromotionTestEngine(t, dir, "IVF2,Flat", 4)
	defer engine.Close()

	if err := engine.InsertVector(1, []float32{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	engine.flushData(true)

	promotionStarted := make(chan struct{})
	releasePromotion := make(chan struct{})
	engine.promotionHook = func() {
		close(promotionStarted)
		<-releasePromotion
	}
	if err := engine.InsertVector(2, []float32{10, 10, 10, 10}); err != nil {
		t.Fatal(err)
	}
	engine.flushData(true)
	<-promotionStarted

	insertStarted := make(chan struct{})
	insertDone := make(chan error, 1)
	go func() {
		close(insertStarted)
		insertDone <- engine.InsertVector(3, []float32{20, 20, 20, 20})
	}()
	<-insertStarted
	select {
	case err := <-insertDone:
		t.Fatalf("mutation completed while promotion held the mutation gate: %v", err)
	default:
	}

	if _, _, err := engine.SearchTopK([]float32{0, 0, 0, 0}, 1); err != nil {
		t.Fatalf("search was unavailable during promotion: %v", err)
	}
	close(releasePromotion)
	if err := <-insertDone; err != nil {
		t.Fatalf("insert after promotion: %v", err)
	}
	waitForTrainingIndexMode(t, engine, VectorIndexModeTrained)
}

func TestTrainingPromotionFailureKeepsFallbackAndRetries(t *testing.T) {
	engine := newTrainingPromotionTestEngine(t, t.TempDir(), "IVF2,Flat", 4)
	defer engine.Close()

	engine.promotionError = func() error { return errors.New("injected promotion failure") }
	for id, vector := range map[int64][]float32{
		1: {0, 0, 0, 0},
		2: {10, 10, 10, 10},
	} {
		if err := engine.InsertVector(id, vector); err != nil {
			t.Fatal(err)
		}
	}
	engine.flushData(true)

	deadline := time.Now().Add(5 * time.Second)
	for {
		engine.lock.RLock()
		mode := engine.indexMeta.Mode
		queued := engine.promotionQueued
		engine.lock.RUnlock()
		if !queued {
			if mode != VectorIndexModeFallback {
				t.Fatalf("failed promotion mode = %q, want fallback", mode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for failed promotion")
		}
		time.Sleep(10 * time.Millisecond)
	}

	engine.promotionError = nil
	if err := engine.checkpoint(); err != nil {
		t.Fatal(err)
	}
	waitForTrainingIndexMode(t, engine, VectorIndexModeTrained)
}

func TestRequiredTrainCountForCompositeIndexes(t *testing.T) {
	tests := map[string]int{
		"Flat":         0,
		"HNSW32,Flat":  0,
		"IVF64,Flat":   64,
		"PQ8":          256,
		"IVF64,PQ16":   256,
		"HNSW128,PQ32": 256,
	}
	for descriptor, want := range tests {
		if got := requiredTrainCountForIndex(descriptor); got != want {
			t.Errorf("requiredTrainCountForIndex(%q) = %d, want %d", descriptor, got, want)
		}
	}
}

func TestVectorEngineRejectsInvalidConfiguredDescriptor(t *testing.T) {
	for _, test := range []struct {
		name       string
		descriptor string
		dimension  int
	}{
		{name: "bare Flat belongs to FlatMeta", descriptor: "Flat", dimension: 4},
		{name: "invalid grammar", descriptor: "PQ8,Flat", dimension: 128},
		{name: "PQ does not divide dimension", descriptor: "PQ8", dimension: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			engine, err := NewVectorEngine(
				filepath.Join(dir, "data.db"),
				filepath.Join(dir, "index.faiss"),
				filepath.Join(dir, "wal.db"),
				test.dimension,
				test.descriptor,
				faiss.MetricL2,
				false,
			)
			if err == nil {
				_ = engine.Close()
				t.Fatalf("NewVectorEngine accepted invalid descriptor %q for dimension %d", test.descriptor, test.dimension)
			}
		})
	}
}

func TestVectorEngineRejectsLegacySegmentManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vector_segments.manifest.json"), []byte(`{"segments":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		4,
		"HNSW8,Flat",
		faiss.MetricL2,
		false,
	)
	if err == nil {
		_ = engine.Close()
		t.Fatal("expected legacy segmented vector layout to be rejected")
	}
}

func TestHNSWUpdateRebuildsWithoutStaleVector(t *testing.T) {
	dir := t.TempDir()
	engine := newTrainingPromotionTestEngine(t, dir, " HNSW8 , Flat ", 4)
	defer engine.Close()

	if err := engine.InsertVector(1, []float32{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	engine.flushData(true)
	replacement := []float32{10, 10, 10, 10}
	if err := engine.InsertVector(1, replacement); err != nil {
		t.Fatalf("replace HNSW vector: %v", err)
	}
	ids, distances, err := engine.SearchTopK(replacement, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 1 || len(distances) != 1 || distances[0] != 0 {
		t.Fatalf("HNSW replacement search = ids %v distances %v, want id 1 at distance 0", ids, distances)
	}
}

func TestHNSWUsesSingleFileStorage(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewVectorEngine(
		filepath.Join(dir, "vector_data.db"),
		filepath.Join(dir, "vector_index.faiss"),
		filepath.Join(dir, "vector_wal.db"),
		4,
		"HNSW8,Flat",
		faiss.MetricL2,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	for id := int64(1); id <= 10; id++ {
		if err := engine.InsertVector(id, []float32{float32(id), 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
	}
	engine.flushData(true)

	engine.lock.RLock()
	defer engine.lock.RUnlock()
	if engine.dataFile == nil || engine.index == nil {
		t.Fatal("HNSW single-file data/index state was not initialized")
	}
}

func TestVectorDataScanRepairsPartialTail(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	engine := newTrainingPromotionTestEngine(t, dir, "HNSW8,Flat", 4)
	if err := engine.InsertVector(1, []float32{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(dataPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{1, 2, 3}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()

	reopened := newTrainingPromotionTestEngine(t, dir, "HNSW8,Flat", 4)
	defer reopened.Close()
	info, err := os.Stat(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(8 + 4*4); info.Size() != want {
		t.Fatalf("repaired data size = %d, want %d", info.Size(), want)
	}
}

func TestVectorEngineRejectsOperationsAfterClose(t *testing.T) {
	engine := newTrainingPromotionTestEngine(t, t.TempDir(), "HNSW8,Flat", 4)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := engine.InsertVector(1, []float32{0, 0, 0, 0}); !errors.Is(err, ErrVectorEngineClosed) {
		t.Fatalf("InsertVector after Close error = %v, want ErrVectorEngineClosed", err)
	}
	if _, _, err := engine.SearchTopK([]float32{0, 0, 0, 0}, 1); !errors.Is(err, ErrVectorEngineClosed) {
		t.Fatalf("SearchTopK after Close error = %v, want ErrVectorEngineClosed", err)
	}
	if err := engine.RemoveVector(1); !errors.Is(err, ErrVectorEngineClosed) {
		t.Fatalf("RemoveVector after Close error = %v, want ErrVectorEngineClosed", err)
	}
}
