package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestSegmentedKeyValueRestartRebuildsMissingColdIndex(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	walPath := filepath.Join(dir, "wal.db")
	indexPath := filepath.Join(dir, "index.dat")

	db, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, SpaceSettings{
		SegmentRolloverBytes:   80,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("OpenDBWithPathsAndWALAndSettings failed: %v", err)
	}

	if err := db.Put("alpha", strings.Repeat("a", 96)); err != nil {
		t.Fatalf("Put alpha failed: %v", err)
	}
	if err := db.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch alpha failed: %v", err)
	}
	if err := db.Put("beta", strings.Repeat("b", 96)); err != nil {
		t.Fatalf("Put beta failed: %v", err)
	}
	if err := db.FlushBatch(); err != nil {
		t.Fatalf("FlushBatch beta failed: %v", err)
	}

	waitForCondition(t, 3*time.Second, func() bool {
		db.lock.RLock()
		defer db.lock.RUnlock()
		return len(db.segments) >= 2 && db.segments[0].meta.State == SegmentStateCold
	}, "first key-value segment to become cold")

	firstIndexPath := db.layout.Descriptor(db.segments[0].meta).IndexPath
	if _, err := os.Stat(firstIndexPath); err != nil {
		t.Fatalf("expected cold index file to exist: %v", err)
	}

	if err := os.Remove(firstIndexPath); err != nil {
		t.Fatalf("remove cold index failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	reopened, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, SpaceSettings{
		SegmentRolloverBytes:   80,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer reopened.Close()

	if got, err := reopened.Get("alpha"); err != nil || got != strings.Repeat("a", 96) {
		t.Fatalf("Get alpha after restart = %q, err=%v", got, err)
	}
	if got, err := reopened.Get("beta"); err != nil || got != strings.Repeat("b", 96) {
		t.Fatalf("Get beta after restart = %q, err=%v", got, err)
	}
	if _, err := os.Stat(firstIndexPath); err != nil {
		t.Fatalf("expected missing cold index to be rebuilt on startup: %v", err)
	}
}

func TestSegmentedKeyValueMergesOldestColdSegments(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	walPath := filepath.Join(dir, "wal.db")
	indexPath := filepath.Join(dir, "index.dat")

	db, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, SpaceSettings{
		SegmentRolloverBytes:   80,
		MaxSegmentsBeforeMerge: 2,
	})
	if err != nil {
		t.Fatalf("OpenDBWithPathsAndWALAndSettings failed: %v", err)
	}
	defer db.Close()

	for _, item := range []struct {
		key   string
		value string
	}{
		{"alpha", strings.Repeat("a", 96)},
		{"beta", strings.Repeat("b", 96)},
		{"gamma", strings.Repeat("c", 96)},
	} {
		if err := db.Put(item.key, item.value); err != nil {
			t.Fatalf("Put %s failed: %v", item.key, err)
		}
		if err := db.FlushBatch(); err != nil {
			t.Fatalf("FlushBatch %s failed: %v", item.key, err)
		}
	}

	waitForCondition(t, 5*time.Second, func() bool {
		db.lock.RLock()
		defer db.lock.RUnlock()
		return len(db.segments) == 2
	}, "key-value cold segment merge")

	db.lock.RLock()
	mergedID := db.segments[0].meta.ID
	hotID := db.segments[len(db.segments)-1].meta.ID
	db.lock.RUnlock()
	if mergedID >= hotID {
		t.Fatalf("expected merged cold segment id %d to remain less than hot id %d", mergedID, hotID)
	}

	if got, err := db.Get("alpha"); err != nil || got != strings.Repeat("a", 96) {
		t.Fatalf("Get alpha after merge = %q, err=%v", got, err)
	}
	if got, err := db.Get("beta"); err != nil || got != strings.Repeat("b", 96) {
		t.Fatalf("Get beta after merge = %q, err=%v", got, err)
	}
	if got, err := db.Get("gamma"); err != nil || got != strings.Repeat("c", 96) {
		t.Fatalf("Get gamma after merge = %q, err=%v", got, err)
	}
}

func TestSegmentedVectorRestartAndSearchAcrossSegments(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true, SpaceSettings{
		SegmentRolloverBytes:   48,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("NewVectorEngineWithSettings failed: %v", err)
	}

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0, 1, 0, 0}
	vec3 := []float32{0, 0, 1, 0}

	if err := ve.InsertVector(1, vec1); err != nil {
		t.Fatalf("InsertVector 1 failed: %v", err)
	}
	if err := ve.InsertVector(2, vec2); err != nil {
		t.Fatalf("InsertVector 2 failed: %v", err)
	}
	ve.flushData(true)

	if err := ve.InsertVector(3, vec3); err != nil {
		t.Fatalf("InsertVector 3 failed: %v", err)
	}
	ve.flushData(true)

	waitForCondition(t, 3*time.Second, func() bool {
		ve.lock.RLock()
		defer ve.lock.RUnlock()
		return len(ve.segments) >= 2 && ve.segments[0].meta.State == SegmentStateCold
	}, "first vector segment to become cold")

	if ids, _, err := ve.SearchTopK(vec1, 3); err != nil || len(ids) == 0 || ids[0] != 1 {
		t.Fatalf("SearchTopK for vec1 returned ids=%v err=%v", ids, err)
	}
	if ids, _, err := ve.SearchTopK(vec3, 3); err != nil || len(ids) == 0 || ids[0] != 3 {
		t.Fatalf("SearchTopK for vec3 returned ids=%v err=%v", ids, err)
	}

	firstIndexPath := ve.layout.Descriptor(ve.segments[0].meta).IndexPath
	if err := os.Remove(firstIndexPath); err != nil {
		t.Fatalf("remove vector cold index failed: %v", err)
	}
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	reopened, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true, SpaceSettings{
		SegmentRolloverBytes:   48,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("reopen vector engine failed: %v", err)
	}
	defer reopened.Close()

	if ids, _, err := reopened.SearchTopK(vec1, 3); err != nil || len(ids) == 0 || ids[0] != 1 {
		t.Fatalf("SearchTopK vec1 after restart returned ids=%v err=%v", ids, err)
	}
	if ids, _, err := reopened.SearchTopK(vec3, 3); err != nil || len(ids) == 0 || ids[0] != 3 {
		t.Fatalf("SearchTopK vec3 after restart returned ids=%v err=%v", ids, err)
	}
	if _, err := os.Stat(firstIndexPath); err != nil {
		t.Fatalf("expected vector cold index to be rebuilt on startup: %v", err)
	}
}

func TestSegmentedVectorTrainingFallbackOnMissingColdIndex(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 8, "IVF32,Flat", faiss.MetricL2, true, SpaceSettings{})
	if err != nil {
		t.Fatalf("NewVectorEngineWithSettings failed: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := ve.InsertVector(int64(300+i), randomVector(8)); err != nil {
			t.Fatalf("InsertVector %d failed: %v", i, err)
		}
	}
	ve.flushData(true)
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	sealedDataPath := filepath.Join(dir, "vector_segment_000001.db")
	sealedIndexPath := filepath.Join(dir, "vector_segment_000001.faiss")
	if err := os.Rename(dataPath, sealedDataPath); err != nil {
		t.Fatalf("rename primary data to sealed segment failed: %v", err)
	}
	manifest := &SegmentManifest{
		Version:         currentSegmentManifestVersion,
		NextSegmentID:   3,
		ActiveSegmentID: 2,
		Segments: []SegmentMeta{
			{ID: 1, State: SegmentStateCold, DataFile: filepath.Base(sealedDataPath), IndexFile: filepath.Base(sealedIndexPath)},
			{ID: 2, State: SegmentStateHot, DataFile: filepath.Base(dataPath), IndexFile: filepath.Base(indexPath)},
		},
	}
	if err := WriteSegmentManifest(NewSegmentLayout(dir, "vector", ".db", ".faiss"), manifest); err != nil {
		t.Fatalf("WriteSegmentManifest failed: %v", err)
	}

	reopened, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 8, "IVF32,Flat", faiss.MetricL2, true, SpaceSettings{})
	if err != nil {
		t.Fatalf("expected reopen with training fallback to Flat, got err=%v", err)
	}
	defer reopened.Close()
	// Training-based indexes compact legacy multi-segment layouts to the primary
	// data file on open; sealed per-segment FAISS files are not expected.
	if _, err := os.Stat(sealedIndexPath); err == nil {
		t.Fatalf("did not expect sealed index file %q for training index layout", sealedIndexPath)
	}
}

func TestSegmentedVectorNoFallbackOnNonTrainingColdIndexFailure(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true, SpaceSettings{
		SegmentRolloverBytes:   48,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("NewVectorEngineWithSettings failed: %v", err)
	}
	if err := ve.InsertVector(1, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("InsertVector 1 failed: %v", err)
	}
	if err := ve.InsertVector(2, []float32{0, 1, 0, 0}); err != nil {
		t.Fatalf("InsertVector 2 failed: %v", err)
	}
	ve.flushData(true)
	waitForCondition(t, 3*time.Second, func() bool {
		ve.lock.RLock()
		defer ve.lock.RUnlock()
		return len(ve.segments) >= 2 && ve.segments[0].meta.State == SegmentStateCold
	}, "first vector segment to become cold")

	firstIndexPath := ve.layout.Descriptor(ve.segments[0].meta).IndexPath
	firstDataPath := ve.layout.Descriptor(ve.segments[0].meta).DataPath
	if err := os.Remove(firstIndexPath); err != nil {
		t.Fatalf("remove vector cold index failed: %v", err)
	}
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := os.Remove(firstDataPath); err != nil {
		t.Fatalf("remove vector cold data failed: %v", err)
	}

	_, err = NewVectorEngineWithSettings(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true, SpaceSettings{
		SegmentRolloverBytes:   48,
		MaxSegmentsBeforeMerge: 10,
	})
	if err == nil {
		t.Fatal("expected reopen to fail without Flat fallback for non-training rebuild errors")
	}
}

func TestSegmentedVectorMergesOldestColdSegments(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true, SpaceSettings{
		SegmentRolloverBytes:   48,
		MaxSegmentsBeforeMerge: 2,
	})
	if err != nil {
		t.Fatalf("NewVectorEngineWithSettings failed: %v", err)
	}
	defer ve.Close()

	vectors := []struct {
		id  int64
		vec []float32
	}{
		{1, []float32{1, 0, 0, 0}},
		{2, []float32{0, 1, 0, 0}},
		{3, []float32{0, 0, 1, 0}},
		{4, []float32{0, 0, 0, 1}},
	}

	for _, item := range vectors {
		if err := ve.InsertVector(item.id, item.vec); err != nil {
			t.Fatalf("InsertVector %d failed: %v", item.id, err)
		}
		ve.flushData(true)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		ve.lock.RLock()
		defer ve.lock.RUnlock()
		return len(ve.segments) == 2
	}, "vector cold segment merge")

	ve.lock.RLock()
	mergedID := ve.segments[0].meta.ID
	hotID := ve.segments[len(ve.segments)-1].meta.ID
	ve.lock.RUnlock()
	if mergedID >= hotID {
		t.Fatalf("expected merged cold segment id %d to remain less than hot id %d", mergedID, hotID)
	}

	if ids, _, err := ve.SearchTopK(vectors[0].vec, 4); err != nil || len(ids) == 0 || ids[0] != 1 {
		t.Fatalf("SearchTopK merged vec1 returned ids=%v err=%v", ids, err)
	}
	if ids, _, err := ve.SearchTopK(vectors[3].vec, 4); err != nil || len(ids) == 0 || ids[0] != 4 {
		t.Fatalf("SearchTopK hot vec4 returned ids=%v err=%v", ids, err)
	}
}

func TestSegmentedVectorSearchTopKInnerProductOrdering(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 2, "Flat", faiss.MetricInnerProduct, true, SpaceSettings{
		SegmentRolloverBytes:   24,
		MaxSegmentsBeforeMerge: 10,
	})
	if err != nil {
		t.Fatalf("NewVectorEngineWithSettings failed: %v", err)
	}
	defer ve.Close()

	if err := ve.InsertVector(1, []float32{1, 0}); err != nil {
		t.Fatalf("InsertVector 1 failed: %v", err)
	}
	ve.flushData(true)
	if err := ve.InsertVector(2, []float32{0.5, 0}); err != nil {
		t.Fatalf("InsertVector 2 failed: %v", err)
	}
	ve.flushData(true)

	waitForCondition(t, 3*time.Second, func() bool {
		ve.lock.RLock()
		defer ve.lock.RUnlock()
		return len(ve.segments) >= 2
	}, "multiple inner-product vector segments")

	ids, _, err := ve.SearchTopK([]float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("SearchTopK failed: %v", err)
	}
	if len(ids) < 2 {
		t.Fatalf("expected 2 ids, got %v", ids)
	}
	if ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("expected higher inner-product hit first, got %v", ids)
	}
}
