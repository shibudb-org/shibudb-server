package storage

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
	"github.com/shibudb.org/shibudb-server/internal/wal"
)

func newTestFlatMetaEngine(t *testing.T, dim, metric int, specs []MetadataFieldSpec, enableWAL bool) *FlatMetaVectorEngine {
	t.Helper()
	dir := t.TempDir()
	e, err := NewFlatMetaVectorEngine(
		filepath.Join(dir, "flat_meta_data.db"),
		filepath.Join(dir, "flat_meta_wal.db"),
		dim, metric, specs, enableWAL,
	)
	if err != nil {
		t.Fatalf("NewFlatMetaVectorEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func idSetOf(ids []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func assertIDSet(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	gotSet := idSetOf(got)
	if len(gotSet) != len(want) {
		t.Fatalf("result set size mismatch: got %v, want %v", got, want)
	}
	for _, id := range want {
		if _, ok := gotSet[id]; !ok {
			t.Fatalf("expected id %d in results %v", id, got)
		}
	}
}

func TestFlatMetaInsertSearchNoFilter(t *testing.T) {
	e := newTestFlatMetaEngine(t, 3, faiss.MetricL2, nil, false)

	vectors := map[int64][]float32{
		1: {0, 0, 0},
		2: {1, 0, 0},
		3: {5, 5, 5},
	}
	for id, vec := range vectors {
		if err := e.InsertVector(id, vec); err != nil {
			t.Fatalf("InsertVector(%d): %v", id, err)
		}
	}

	ids, dists, err := e.SearchTopK([]float32{0, 0, 0}, 2)
	if err != nil {
		t.Fatalf("SearchTopK: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 results, got %d", len(ids))
	}
	if ids[0] != 1 {
		t.Fatalf("expected nearest id 1, got %d (dists=%v)", ids[0], dists)
	}
	if dists[0] != 0 {
		t.Fatalf("expected zero distance for exact match, got %f", dists[0])
	}
}

func TestFlatMetaStringFilter(t *testing.T) {
	specs := []MetadataFieldSpec{{Name: "user_id", Type: MetadataTypeString}}
	e := newTestFlatMetaEngine(t, 2, faiss.MetricL2, specs, false)

	mustInsert := func(id int64, vec []float32, user string) {
		if err := e.InsertVectorWithMetadata(id, vec, map[string]any{"user_id": user}); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
	}
	mustInsert(1, []float32{0, 0}, "alice")
	mustInsert(2, []float32{0.1, 0}, "bob")
	mustInsert(3, []float32{0.2, 0}, "alice")
	mustInsert(4, []float32{0.3, 0}, "carol")

	t.Run("eq", func(t *testing.T) {
		ids, _, err := e.SearchTopKFiltered([]float32{0, 0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "alice"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		assertIDSet(t, ids, 1, 3)
	})

	t.Run("in", func(t *testing.T) {
		ids, _, err := e.SearchTopKFiltered([]float32{0, 0}, 10, &MetadataFilter{Op: FilterOpIn, Field: "user_id", Values: []any{"bob", "carol"}})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		assertIDSet(t, ids, 2, 4)
	})

	t.Run("eq no match", func(t *testing.T) {
		ids, _, err := e.SearchTopKFiltered([]float32{0, 0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "dave"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("expected no results, got %v", ids)
		}
	})
}

func TestFlatMetaNumericRange(t *testing.T) {
	specs := []MetadataFieldSpec{{Name: "age", Type: MetadataTypeInt}}
	e := newTestFlatMetaEngine(t, 1, faiss.MetricL2, specs, false)

	for id := int64(1); id <= 5; id++ {
		if err := e.InsertVectorWithMetadata(id, []float32{float32(id)}, map[string]any{"age": float64(id * 10)}); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
	}
	// ages: 1->10, 2->20, 3->30, 4->40, 5->50
	q := []float32{0}

	cases := []struct {
		name   string
		filter *MetadataFilter
		want   []int64
	}{
		{"gt", &MetadataFilter{Op: FilterOpGt, Field: "age", Value: 30.0}, []int64{4, 5}},
		{"gte", &MetadataFilter{Op: FilterOpGte, Field: "age", Value: 30.0}, []int64{3, 4, 5}},
		{"lt", &MetadataFilter{Op: FilterOpLt, Field: "age", Value: 30.0}, []int64{1, 2}},
		{"lte", &MetadataFilter{Op: FilterOpLte, Field: "age", Value: 30.0}, []int64{1, 2, 3}},
		{"between", &MetadataFilter{Op: FilterOpBetween, Field: "age", Values: []any{20.0, 40.0}}, []int64{2, 3, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids, _, err := e.SearchTopKFiltered(q, 10, tc.filter)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			assertIDSet(t, ids, tc.want...)
		})
	}
}

func TestFlatMetaBooleanFilters(t *testing.T) {
	specs := []MetadataFieldSpec{
		{Name: "user_id", Type: MetadataTypeString},
		{Name: "score", Type: MetadataTypeFloat},
	}
	e := newTestFlatMetaEngine(t, 1, faiss.MetricL2, specs, false)

	insert := func(id int64, user string, score float64) {
		if err := e.InsertVectorWithMetadata(id, []float32{float32(id)}, map[string]any{"user_id": user, "score": score}); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
	}
	insert(1, "alice", 0.5)
	insert(2, "alice", 0.9)
	insert(3, "bob", 0.9)
	insert(4, "bob", 0.1)

	q := []float32{0}

	t.Run("and", func(t *testing.T) {
		filter := &MetadataFilter{Op: FilterOpAnd, Filters: []*MetadataFilter{
			{Op: FilterOpEq, Field: "user_id", Value: "alice"},
			{Op: FilterOpGte, Field: "score", Value: 0.8},
		}}
		ids, _, err := e.SearchTopKFiltered(q, 10, filter)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		assertIDSet(t, ids, 2)
	})

	t.Run("or", func(t *testing.T) {
		filter := &MetadataFilter{Op: FilterOpOr, Filters: []*MetadataFilter{
			{Op: FilterOpEq, Field: "user_id", Value: "bob"},
			{Op: FilterOpGte, Field: "score", Value: 0.85},
		}}
		ids, _, err := e.SearchTopKFiltered(q, 10, filter)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		assertIDSet(t, ids, 2, 3, 4)
	})

	t.Run("not", func(t *testing.T) {
		filter := &MetadataFilter{Op: FilterOpNot, Filters: []*MetadataFilter{
			{Op: FilterOpEq, Field: "user_id", Value: "alice"},
		}}
		ids, _, err := e.SearchTopKFiltered(q, 10, filter)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		assertIDSet(t, ids, 3, 4)
	})
}

func TestFlatMetaUpsertAndRemove(t *testing.T) {
	specs := []MetadataFieldSpec{{Name: "user_id", Type: MetadataTypeString}}
	e := newTestFlatMetaEngine(t, 1, faiss.MetricL2, specs, false)

	if err := e.InsertVectorWithMetadata(1, []float32{1}, map[string]any{"user_id": "alice"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Upsert id 1 to a different user; the old posting list entry must be removed.
	if err := e.InsertVectorWithMetadata(1, []float32{2}, map[string]any{"user_id": "bob"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ids, _, _ := e.SearchTopKFiltered([]float32{0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "alice"})
	if len(ids) != 0 {
		t.Fatalf("expected id 1 removed from 'alice', got %v", ids)
	}
	ids, _, _ = e.SearchTopKFiltered([]float32{0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "bob"})
	assertIDSet(t, ids, 1)

	vec, err := e.GetVectorByID(1)
	if err != nil || !reflect.DeepEqual(vec, []float32{2}) {
		t.Fatalf("expected upserted vector {2}, got %v err=%v", vec, err)
	}

	if err := e.RemoveVector(1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := e.GetVectorByID(1); err == nil {
		t.Fatalf("expected error after remove")
	}
	ids, _, _ = e.SearchTopKFiltered([]float32{0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "bob"})
	if len(ids) != 0 {
		t.Fatalf("expected no results after remove, got %v", ids)
	}
}

func TestFlatMetaPersistenceRebuild(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "flat_meta_data.db")
	walPath := filepath.Join(dir, "flat_meta_wal.db")
	specs := []MetadataFieldSpec{
		{Name: "user_id", Type: MetadataTypeString},
		{Name: "age", Type: MetadataTypeInt},
	}

	e1, err := NewFlatMetaVectorEngine(dataPath, walPath, 2, faiss.MetricL2, specs, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = e1.InsertVectorWithMetadata(1, []float32{0, 0}, map[string]any{"user_id": "alice", "age": 30.0})
	_ = e1.InsertVectorWithMetadata(2, []float32{1, 1}, map[string]any{"user_id": "bob", "age": 40.0})
	_ = e1.InsertVectorWithMetadata(3, []float32{2, 2}, map[string]any{"user_id": "alice", "age": 50.0})
	_ = e1.RemoveVector(2)
	if err := e1.Close(); err != nil { // Close flushes the append-only data file.
		t.Fatalf("close: %v", err)
	}

	e2, err := NewFlatMetaVectorEngine(dataPath, walPath, 2, faiss.MetricL2, specs, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = e2.Close() })

	if _, err := e2.GetVectorByID(2); err == nil {
		t.Fatalf("expected id 2 to remain removed after restart")
	}
	ids, _, err := e2.SearchTopKFiltered([]float32{0, 0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "alice"})
	if err != nil {
		t.Fatalf("search after restart: %v", err)
	}
	assertIDSet(t, ids, 1, 3)
}

func TestFlatMetaSegmentRolloverAndMerge(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "flat_meta_data.db")
	walPath := filepath.Join(dir, "flat_meta_wal.db")
	specs := []MetadataFieldSpec{{Name: "grp", Type: MetadataTypeString}}

	// Tiny rollover so each pair of inserts seals a segment, and a small merge
	// threshold so the background worker compacts aggressively.
	settings := SpaceSettings{SegmentRolloverBytes: 64, MaxSegmentsBeforeMerge: 3}
	e, err := NewFlatMetaVectorEngineWithSettings(dataPath, walPath, 2, faiss.MetricL2, specs, false, settings)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	const n = 40
	for i := 1; i <= n; i++ {
		if err := e.InsertVectorWithMetadata(int64(i), []float32{float32(i), float32(i)}, map[string]any{"grp": "g"}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		// Force the append + size-based rollover synchronously for determinism.
		e.flushData(true)
	}

	e.lock.RLock()
	segCount := len(e.segments)
	e.lock.RUnlock()
	if segCount < 2 {
		t.Fatalf("expected multiple segments after rollover, got %d", segCount)
	}

	// The background merge worker should compact down to <= MaxSegmentsBeforeMerge.
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.lock.RLock()
		segCount = len(e.segments)
		e.lock.RUnlock()
		if segCount <= settings.MaxSegmentsBeforeMerge {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("merge did not reduce segment count: still %d (> %d)", segCount, settings.MaxSegmentsBeforeMerge)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Overwrite one vector and delete another so last-writer-wins and tombstones
	// must be resolved across multiple segments on rebuild.
	if err := e.InsertVectorWithMetadata(1, []float32{100, 100}, map[string]any{"grp": "g"}); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	if err := e.RemoveVector(2); err != nil {
		t.Fatalf("remove 2: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: in-memory state is rebuilt by replaying every segment in order.
	e2, err := NewFlatMetaVectorEngineWithSettings(dataPath, walPath, 2, faiss.MetricL2, specs, false, settings)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = e2.Close() })

	if _, err := e2.GetVectorByID(2); err == nil {
		t.Fatalf("expected id 2 to remain removed after restart")
	}
	got, err := e2.GetVectorByID(1)
	if err != nil || !reflect.DeepEqual(got, []float32{100, 100}) {
		t.Fatalf("expected updated vector for id 1, got %v err=%v", got, err)
	}
	ids, _, err := e2.SearchTopKFiltered([]float32{0, 0}, n, &MetadataFilter{Op: FilterOpEq, Field: "grp", Value: "g"})
	if err != nil {
		t.Fatalf("search after restart: %v", err)
	}
	if len(ids) != n-1 {
		t.Fatalf("expected %d live vectors after restart, got %d (%v)", n-1, len(ids), ids)
	}
}

func TestFlatMetaWALReplay(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "flat_meta_data.db")
	walPath := filepath.Join(dir, "flat_meta_wal.db")
	specs := []MetadataFieldSpec{{Name: "user_id", Type: MetadataTypeString}}

	// This project's WAL is a single-slot design: each WriteEntry overwrites the
	// previous one and Replay returns only the pending (uncommitted) entry. Write
	// one pending entry, simulating an insert that never reached the data file.
	w, err := wal.OpenWAL(walPath)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	key := make([]byte, 8)
	binary.LittleEndian.PutUint64(key, uint64(7))
	meta, _ := json.Marshal(map[string]any{"user_id": "alice"})
	if err := w.WriteEntry(string(key), string(encodeFlatMetaWALValue(meta, []float32{1, 2}))); err != nil {
		t.Fatalf("wal write: %v", err)
	}
	_ = w.Close()

	e, err := NewFlatMetaVectorEngine(dataPath, walPath, 2, faiss.MetricL2, specs, true)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	vec, err := e.GetVectorByID(7)
	if err != nil || !reflect.DeepEqual(vec, []float32{1, 2}) {
		t.Fatalf("expected replayed vector for id 7, got %v err=%v", vec, err)
	}
	ids, _, err := e.SearchTopKFiltered([]float32{0, 0}, 10, &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "alice"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	assertIDSet(t, ids, 7)
}

func TestFlatMetaWALDeleteAndReplay(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "flat_meta_data.db")
	walPath := filepath.Join(dir, "flat_meta_wal.db")
	specs := []MetadataFieldSpec{
		{Name: "user_id", Type: MetadataTypeString},
		{Name: "age", Type: MetadataTypeInt},
	}

	e1, err := NewFlatMetaVectorEngine(dataPath, walPath, 2, faiss.MetricL2, specs, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	const id = 4
	if err := e1.InsertVectorWithMetadata(id, []float32{0, 0}, map[string]any{"user_id": "eve", "age": 35.0}); err != nil {
		t.Fatalf("insert %d: %v", id, err)
	}

	key := make([]byte, 8)
	binary.LittleEndian.PutUint64(key, uint64(id))
	if err := e1.wal.WriteDelete(string(key)); err != nil {
		t.Fatalf("write delete to wal %d: %v", id, err)
	}

	if err := e1.Close(); err != nil { // Close flushes the append-only data file.
		t.Fatalf("close: %v", err)
	}

	e2, err := NewFlatMetaVectorEngine(dataPath, walPath, 2, faiss.MetricL2, specs, true)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = e2.Close() })

	if _, err := e2.GetVectorByID(id); err == nil {
		t.Fatalf("expected id %d to be removed after restart due to pending delete entry in WAL", id)
	}
}

func TestFlatMetaValidation(t *testing.T) {
	specs := []MetadataFieldSpec{
		{Name: "user_id", Type: MetadataTypeString},
		{Name: "age", Type: MetadataTypeInt},
	}
	if err := ValidateFieldSpecs(specs); err != nil {
		t.Fatalf("valid specs rejected: %v", err)
	}
	if err := ValidateFieldSpecs([]MetadataFieldSpec{{Name: "x", Type: "bogus"}}); err == nil {
		t.Fatalf("expected invalid type rejection")
	}
	if err := ValidateFieldSpecs([]MetadataFieldSpec{{Name: "x", Type: MetadataTypeInt}, {Name: "x", Type: MetadataTypeInt}}); err == nil {
		t.Fatalf("expected duplicate field rejection")
	}

	// Range op on a string field is invalid.
	if err := ValidateFilter(&MetadataFilter{Op: FilterOpGt, Field: "user_id", Value: 1.0}, specs); err == nil {
		t.Fatalf("expected range-on-string rejection")
	}
	// Unknown field is invalid.
	if err := ValidateFilter(&MetadataFilter{Op: FilterOpEq, Field: "nope", Value: "x"}, specs); err == nil {
		t.Fatalf("expected unknown-field rejection")
	}
	// Valid nested filter.
	good := &MetadataFilter{Op: FilterOpAnd, Filters: []*MetadataFilter{
		{Op: FilterOpEq, Field: "user_id", Value: "alice"},
		{Op: FilterOpBetween, Field: "age", Values: []any{1.0, 2.0}},
	}}
	if err := ValidateFilter(good, specs); err != nil {
		t.Fatalf("valid nested filter rejected: %v", err)
	}

	// Engine rejects wrong-typed metadata on insert.
	e := newTestFlatMetaEngine(t, 1, faiss.MetricL2, specs, false)
	if err := e.InsertVectorWithMetadata(1, []float32{0}, map[string]any{"age": "not-a-number"}); err == nil {
		t.Fatalf("expected type error for non-numeric age")
	}
	if err := e.InsertVectorWithMetadata(1, []float32{0}, map[string]any{"age": 1.5}); err == nil {
		t.Fatalf("expected integer error for fractional int field")
	}
}

// === distance correctness ===

func refDistance(metric int, a, b []float32) float64 {
	switch metric {
	case faiss.MetricInnerProduct:
		var s float64
		for i := range a {
			s += float64(a[i]) * float64(b[i])
		}
		return s
	case faiss.MetricL1:
		var s float64
		for i := range a {
			s += math.Abs(float64(a[i]) - float64(b[i]))
		}
		return s
	case faiss.MetricLinf:
		var m float64
		for i := range a {
			d := math.Abs(float64(a[i]) - float64(b[i]))
			if d > m {
				m = d
			}
		}
		return m
	case faiss.MetricLp:
		var s float64
		for i := range a {
			d := math.Abs(float64(a[i]) - float64(b[i]))
			s += d * d
		}
		return s
	case faiss.MetricCanberra:
		var s float64
		for i := range a {
			num := math.Abs(float64(a[i]) - float64(b[i]))
			den := math.Abs(float64(a[i])) + math.Abs(float64(b[i]))
			if den != 0 {
				s += num / den
			}
		}
		return s
	case faiss.MetricBrayCurtis:
		var num, den float64
		for i := range a {
			num += math.Abs(float64(a[i]) - float64(b[i]))
			den += math.Abs(float64(a[i]) + float64(b[i]))
		}
		if den == 0 {
			return 0
		}
		return num / den
	case faiss.MetricJensenShannon:
		var js float64
		for i := range a {
			ai := float64(a[i])
			bi := float64(b[i])
			mi := 0.5 * (ai + bi)
			if ai > 0 && mi > 0 {
				js += ai * math.Log(ai/mi)
			}
			if bi > 0 && mi > 0 {
				js += bi * math.Log(bi/mi)
			}
		}
		return 0.5 * js
	default:
		var s float64
		for i := range a {
			d := float64(a[i]) - float64(b[i])
			s += d * d
		}
		return s
	}
}

func TestFlatMetaMetricDistanceReference(t *testing.T) {
	a := []float32{0.1, 0.4, 0.2, 0.3}
	b := []float32{0.3, 0.1, 0.25, 0.35}
	metrics := []int{
		faiss.MetricL2, faiss.MetricInnerProduct, faiss.MetricL1, faiss.MetricLinf,
		faiss.MetricLp, faiss.MetricCanberra, faiss.MetricBrayCurtis, faiss.MetricJensenShannon,
	}
	for _, m := range metrics {
		got := metricDistance(m, a, b)
		want := refDistance(m, a, b)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("metric %d: got %v want %v", m, got, want)
		}
	}
}

func TestFlatMetaFAISSParity(t *testing.T) {
	const dim = 8
	const n = 40
	metrics := map[string]int{
		"L2":            faiss.MetricL2,
		"InnerProduct":  faiss.MetricInnerProduct,
		"L1":            faiss.MetricL1,
		"Linf":          faiss.MetricLinf,
		"Canberra":      faiss.MetricCanberra,
		"BrayCurtis":    faiss.MetricBrayCurtis,
		"JensenShannon": faiss.MetricJensenShannon,
	}

	// Shared dataset and query (positive values so probability-style metrics behave).
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = randomVector(dim)
	}
	query := randomVector(dim)

	for name, metric := range metrics {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			faissEng, err := NewVectorEngine(
				filepath.Join(dir, "v.db"), filepath.Join(dir, "v.faiss"), filepath.Join(dir, "v.wal"),
				dim, "Flat", metric, false,
			)
			if err != nil {
				t.Skipf("FAISS Flat unsupported for metric %s: %v", name, err)
			}
			defer faissEng.Close()

			metaEng := newTestFlatMetaEngine(t, dim, metric, nil, false)

			for i, vec := range vectors {
				id := int64(i + 1)
				if err := faissEng.InsertVector(id, vec); err != nil {
					t.Fatalf("faiss insert: %v", err)
				}
				if err := metaEng.InsertVector(id, vec); err != nil {
					t.Fatalf("meta insert: %v", err)
				}
			}
			// FAISS only returns vectors whose data has been flushed to disk.
			time.Sleep(800 * time.Millisecond)

			faissIDs, faissDists, err := faissEng.SearchTopK(query, n)
			if err != nil {
				t.Fatalf("faiss search: %v", err)
			}
			if len(faissIDs) == 0 {
				t.Fatalf("faiss returned no results for metric %s", name)
			}
			metaIDs, metaDists, err := metaEng.SearchTopK(query, n)
			if err != nil {
				t.Fatalf("meta search: %v", err)
			}

			faissByID := make(map[int64]float32, len(faissIDs))
			for i, id := range faissIDs {
				faissByID[id] = faissDists[i]
			}
			for i, id := range metaIDs {
				fd, ok := faissByID[id]
				if !ok {
					continue
				}
				if diff := math.Abs(float64(fd - metaDists[i])); diff > 1e-2*math.Max(1, math.Abs(float64(fd))) {
					t.Errorf("metric %s id %d: faiss=%f meta=%f", name, id, fd, metaDists[i])
				}
			}
			// Nearest neighbour must agree.
			if faissIDs[0] != metaIDs[0] {
				t.Errorf("metric %s: nearest id mismatch faiss=%d meta=%d", name, faissIDs[0], metaIDs[0])
			}
		})
	}
}
