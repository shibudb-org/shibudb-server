package storage

import (
	"encoding/binary"
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

	db, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, "btree", SpaceSettings{
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

	reopened, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, "btree", SpaceSettings{
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

	db, err := OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, true, "btree", SpaceSettings{
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

	for key, want := range map[string]string{
		"alpha": strings.Repeat("a", 96),
		"beta":  strings.Repeat("b", 96),
		"gamma": strings.Repeat("c", 96),
	} {
		if got, err := db.Get(key); err != nil || got != want {
			t.Fatalf("Get %s after merge = %q, err=%v", key, got, err)
		}
	}
}

func TestFAISSVectorIndexesNeverRollOver(t *testing.T) {
	for _, indexDesc := range []string{"Flat", "HNSW32,Flat", "IVF32,Flat"} {
		t.Run(indexDesc, func(t *testing.T) {
			dir := t.TempDir()
			dataPath := filepath.Join(dir, "vector_data.db")
			indexPath := filepath.Join(dir, "vector_index.faiss")
			walPath := filepath.Join(dir, "vector_wal.db")

			ve, err := NewVectorEngineWithSettings(dataPath, indexPath, walPath, 4, indexDesc, faiss.MetricL2, true, SpaceSettings{
				SegmentRolloverBytes:   1,
				MaxSegmentsBeforeMerge: 1,
			})
			if err != nil {
				t.Fatalf("NewVectorEngineWithSettings failed: %v", err)
			}
			defer ve.Close()

			for id := int64(1); id <= 64; id++ {
				if err := ve.InsertVector(id, []float32{float32(id), 1, 2, 3}); err != nil {
					t.Fatalf("InsertVector %d failed: %v", id, err)
				}
			}
			ve.flushData(true)

			if len(ve.segments) != 1 || len(ve.manifest.Segments) != 1 {
				t.Fatalf("FAISS index %q rolled over: segments=%d manifest=%d", indexDesc, len(ve.segments), len(ve.manifest.Segments))
			}
			if matches, err := filepath.Glob(filepath.Join(dir, "vector_segment_*")); err != nil || len(matches) != 0 {
				t.Fatalf("unexpected FAISS segment files %v, err=%v", matches, err)
			}
		})
	}
}

func TestFAISSVectorCompactsLegacySegmentedLayout(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "vector_data.db")
	indexPath := filepath.Join(dir, "vector_index.faiss")
	walPath := filepath.Join(dir, "vector_wal.db")

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("NewVectorEngine failed: %v", err)
	}
	if err := ve.InsertVector(1, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("InsertVector failed: %v", err)
	}
	ve.flushData(true)
	if err := ve.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	sealedDataPath := filepath.Join(dir, "vector_segment_000001.db")
	sealedIndexPath := filepath.Join(dir, "vector_segment_000001.faiss")
	if err := os.Rename(dataPath, sealedDataPath); err != nil {
		t.Fatalf("rename primary data: %v", err)
	}
	record := make([]byte, 8+4*4)
	binary.LittleEndian.PutUint64(record[:8], 2)
	copy(record[8:], float32ArrayToBytes([]float32{0, 1, 0, 0}))
	if err := os.WriteFile(dataPath, record, 0666); err != nil {
		t.Fatalf("write active data: %v", err)
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

	reopened, err := NewVectorEngine(dataPath, indexPath, walPath, 4, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer reopened.Close()

	for id, query := range map[int64][]float32{
		1: {1, 0, 0, 0},
		2: {0, 1, 0, 0},
	} {
		ids, _, err := reopened.SearchTopK(query, 1)
		if err != nil || len(ids) != 1 || ids[0] != id {
			t.Fatalf("SearchTopK id=%d returned ids=%v err=%v", id, ids, err)
		}
	}
	if len(reopened.manifest.Segments) != 1 {
		t.Fatalf("expected compacted single-file manifest, got %d segments", len(reopened.manifest.Segments))
	}
	if _, err := os.Stat(sealedDataPath); !os.IsNotExist(err) {
		t.Fatalf("legacy segment data still exists, err=%v", err)
	}
}
