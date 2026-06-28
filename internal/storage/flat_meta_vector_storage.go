package storage

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/google/btree"
	"github.com/shibudb.org/shibudb-server/internal/maintenance"
	"github.com/shibudb.org/shibudb-server/internal/wal"
)

// FlatMetaVectorEngine is an exact (brute-force) vector engine with per-vector
// metadata and in-memory secondary indexes. A filtered query resolves the
// filter to a candidate ID set and scans distances over only those candidates;
// an unfiltered query scans all live vectors. Vectors and indexes live in
// memory; durability is provided by a set of append-only segment data files plus
// the WAL, and the in-memory indexes are rebuilt by scanning the segments on
// open.
//
// Persistence uses the same segmented layout as the key-value and Flat/HNSW
// vector engines: writes land in the active (hot) segment, which is sealed and
// rolled over to a fresh file once it exceeds SegmentRolloverBytes, and a
// background worker compacts the oldest cold segments once the segment count
// exceeds MaxSegmentsBeforeMerge. Because all live state is kept in memory,
// segments carry no per-segment on-disk index; reads never touch the files.
//
// Numeric metadata (int and float) is indexed and compared as float64, which is
// exact for integers up to 2^53.
type FlatMetaVectorEngine struct {
	dim       int
	metric    int
	specs     []MetadataFieldSpec
	specTypes map[string]string

	wal *wal.WAL

	lock sync.RWMutex

	settings SpaceSettings

	// In-memory state (guarded by lock).
	vectors  map[int64][]float32
	metadata map[int64]map[string]any
	liveIDs  *roaring64.Bitmap
	// stringIdx: field -> value -> set of ids (equality / IN).
	stringIdx map[string]map[string]*roaring64.Bitmap
	// numIdx: field -> ordered tree of value -> set of ids (equality / IN / range).
	numIdx map[string]*btree.BTreeG[flatMetaNumEntry]

	// Segmented append-only persistence (segments + manifest guarded by lock).
	layout          SegmentLayout
	primaryDataPath string
	manifest        *SegmentManifest
	segments        []*flatMetaSegment

	// Append-only persistence buffer (guarded by persistMu).
	persistMu  sync.Mutex
	persistBuf []flatMetaPersistItem

	mergeQueue     chan struct{}
	backgroundStop chan struct{}
	backgroundWG   sync.WaitGroup

	closeOnce         sync.Once
	maintenanceMu     sync.RWMutex
	maintenanceClosed bool
}

// flatMetaSegment is one append-only data file. It carries no on-disk index:
// the engine keeps the full live working set in memory, so reads never read
// segment files; the file handle is used only for appends (active segment) and
// for compaction reads during a merge.
type flatMetaSegment struct {
	meta     SegmentMeta
	dataFile *os.File
}

// flatMetaRecord is one decoded data-file record produced by streamFlatMetaDataFile.
type flatMetaRecord struct {
	id        int64
	tombstone bool
	raw       []byte
	meta      []byte
	vec       []float32
}

type flatMetaNumEntry struct {
	value float64
	ids   *roaring64.Bitmap
}

func flatMetaNumLess(a, b flatMetaNumEntry) bool { return a.value < b.value }

type flatMetaPersistItem struct {
	id        int64
	tombstone bool
	meta      []byte
	vec       []float32
}

const flatMetaDataFlagLive = 0
const flatMetaDataFlagTombstone = 1

var _ VectorEngine = (*FlatMetaVectorEngine)(nil)
var _ FilterableVectorEngine = (*FlatMetaVectorEngine)(nil)
var _ SpaceSettingsApplier = (*FlatMetaVectorEngine)(nil)

// NewFlatMetaVectorEngine opens (or creates) a filterable Flat vector space with
// default segment settings.
func NewFlatMetaVectorEngine(dataPath, walPath string, dim, metric int, specs []MetadataFieldSpec, enableWAL bool) (*FlatMetaVectorEngine, error) {
	return NewFlatMetaVectorEngineWithSettings(dataPath, walPath, dim, metric, specs, enableWAL, SpaceSettings{})
}

// NewFlatMetaVectorEngineWithSettings opens (or creates) a filterable Flat vector
// space using the provided segment rollover/merge settings.
func NewFlatMetaVectorEngineWithSettings(dataPath, walPath string, dim, metric int, specs []MetadataFieldSpec, enableWAL bool, settings SpaceSettings) (*FlatMetaVectorEngine, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("invalid vector dimension %d", dim)
	}
	if err := ValidateFieldSpecs(specs); err != nil {
		return nil, err
	}

	var w *wal.WAL
	if enableWAL {
		var err error
		w, err = wal.OpenWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("open WAL: %w", err)
		}
	}

	e := &FlatMetaVectorEngine{
		dim:             dim,
		metric:          metric,
		specs:           append([]MetadataFieldSpec(nil), specs...),
		specTypes:       fieldSpecTypes(specs),
		wal:             w,
		settings:        NormalizeSpaceSettings(settings),
		vectors:         make(map[int64][]float32),
		metadata:        make(map[int64]map[string]any),
		liveIDs:         roaring64.New(),
		stringIdx:       make(map[string]map[string]*roaring64.Bitmap),
		numIdx:          make(map[string]*btree.BTreeG[flatMetaNumEntry]),
		layout:          NewSegmentLayout(filepath.Dir(dataPath), "flat_meta", ".db", ".idx"),
		primaryDataPath: dataPath,
		mergeQueue:      make(chan struct{}, 1),
		backgroundStop:  make(chan struct{}),
	}
	for _, spec := range specs {
		if spec.Type == MetadataTypeString {
			e.stringIdx[spec.Name] = make(map[string]*roaring64.Bitmap)
		} else {
			e.numIdx[spec.Name] = btree.NewG[flatMetaNumEntry](32, flatMetaNumLess)
		}
	}

	manifest, err := e.loadOrCreateManifest()
	if err != nil {
		if w != nil {
			_ = w.Close()
		}
		return nil, err
	}
	e.manifest = manifest

	if err := e.loadSegments(); err != nil {
		if w != nil {
			_ = w.Close()
		}
		return nil, err
	}

	e.startBackgroundWorkers()

	if enableWAL {
		if err := e.replayWAL(); err != nil {
			return nil, fmt.Errorf("WAL replay failed: %w", err)
		}
	}
	return e, nil
}

func (e *FlatMetaVectorEngine) loadOrCreateManifest() (*SegmentManifest, error) {
	path := e.layout.ManifestPath()
	if _, err := os.Stat(path); err == nil {
		return LoadOrCreateSegmentManifest(e.layout)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	now := time.Now().UnixNano()
	manifest := &SegmentManifest{
		Version:         currentSegmentManifestVersion,
		NextSegmentID:   2,
		ActiveSegmentID: 1,
		Segments: []SegmentMeta{{
			ID:              1,
			State:           SegmentStateHot,
			DataFile:        filepath.Base(e.primaryDataPath),
			CreatedAtUnixNs: now,
		}},
	}
	if info, err := os.Stat(e.primaryDataPath); err == nil {
		manifest.Segments[0].SizeBytes = info.Size()
	}
	if err := WriteSegmentManifest(e.layout, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// loadSegments opens every segment data file and rebuilds the in-memory state by
// replaying the segments oldest-to-newest (last writer wins, tombstones drop).
func (e *FlatMetaVectorEngine) loadSegments() error {
	e.lock.Lock()
	defer e.lock.Unlock()

	e.segments = nil
	if len(e.manifest.Segments) == 0 {
		return errors.New("segment manifest has no segments")
	}

	e.manifest.ActiveSegmentID = e.manifest.Segments[len(e.manifest.Segments)-1].ID
	for idx, meta := range e.manifest.Segments {
		desc := e.layout.Descriptor(meta)
		flags := os.O_RDWR
		// Only the active/hot segment is allowed to be created on open. Cold
		// segment files must exist; recreating them would hide data loss.
		if meta.ID == e.manifest.ActiveSegmentID {
			flags |= os.O_CREATE
			meta.State = SegmentStateHot
		} else {
			meta.State = SegmentStateCold
		}
		dataFile, err := os.OpenFile(desc.DataPath, flags, 0666)
		if err != nil {
			return err
		}
		if info, err := dataFile.Stat(); err == nil {
			meta.SizeBytes = info.Size()
		}
		e.manifest.Segments[idx] = meta
		e.segments = append(e.segments, &flatMetaSegment{meta: meta, dataFile: dataFile})
	}

	if err := e.rebuildInMemoryLocked(); err != nil {
		return err
	}
	return WriteSegmentManifest(e.layout, e.manifest)
}

// rebuildInMemoryLocked replays all segment data files in order to reconstruct
// the in-memory vectors, metadata and secondary indexes. Caller holds e.lock.
func (e *FlatMetaVectorEngine) rebuildInMemoryLocked() error {
	for _, segment := range e.segments {
		desc := e.layout.Descriptor(segment.meta)
		err := streamFlatMetaDataFile(desc.DataPath, e.dim, func(rec flatMetaRecord) error {
			if rec.tombstone {
				e.indexRemoveLocked(rec.id)
				return nil
			}
			var raw map[string]any
			if len(rec.meta) > 0 {
				if err := json.Unmarshal(rec.meta, &raw); err != nil {
					return fmt.Errorf("decode metadata for id %d: %w", rec.id, err)
				}
			}
			norm, err := e.coerceMetadata(raw)
			if err != nil {
				return fmt.Errorf("metadata for id %d: %w", rec.id, err)
			}
			e.indexInsertLocked(rec.id, rec.vec, norm)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *FlatMetaVectorEngine) startBackgroundWorkers() {
	e.backgroundWG.Add(1)
	go e.mergeWorker()
}

func (e *FlatMetaVectorEngine) IndexedFields() []MetadataFieldSpec {
	return append([]MetadataFieldSpec(nil), e.specs...)
}

// === VectorEngine ===

func (e *FlatMetaVectorEngine) InsertVector(id int64, vector []float32) error {
	return e.InsertVectorWithMetadata(id, vector, nil)
}

func (e *FlatMetaVectorEngine) SearchTopK(query []float32, k int) ([]int64, []float32, error) {
	return e.SearchTopKFiltered(query, k, nil)
}

func (e *FlatMetaVectorEngine) RangeSearch(query []float32, radius float32) ([]int64, []float32, error) {
	return e.RangeSearchFiltered(query, radius, nil)
}

func (e *FlatMetaVectorEngine) GetVectorByID(id int64) ([]float32, error) {
	e.lock.RLock()
	defer e.lock.RUnlock()
	vec, ok := e.vectors[id]
	if !ok {
		return nil, fmt.Errorf("ID %d not found", id)
	}
	return append([]float32(nil), vec...), nil
}

func (e *FlatMetaVectorEngine) RemoveVector(id int64) error {
	if e.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := e.wal.WriteDelete(string(key)); err != nil {
			return err
		}
	}

	e.lock.Lock()
	_, existed := e.metadata[id]
	e.indexRemoveLocked(id)
	e.lock.Unlock()

	if existed {
		e.enqueuePersist(flatMetaPersistItem{id: id, tombstone: true})
	}
	if e.wal != nil {
		return e.wal.MarkCommitted()
	}
	return nil
}

func (e *FlatMetaVectorEngine) Close() error {
	e.closeOnce.Do(func() {
		e.maintenanceMu.Lock()
		e.maintenanceClosed = true
		maintenance.UnregisterVectorFlush(e)
		e.maintenanceMu.Unlock()

		e.flushData(true)

		close(e.backgroundStop)
		e.backgroundWG.Wait()

		if e.wal != nil {
			_ = e.wal.Close()
		}
		e.lock.Lock()
		for _, segment := range e.segments {
			_ = segment.dataFile.Close()
		}
		e.lock.Unlock()
	})
	return nil
}

func (e *FlatMetaVectorEngine) UpdateSpaceSettings(settings SpaceSettings) error {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.settings = NormalizeSpaceSettings(settings)
	e.scheduleMergeCheckLocked()
	return nil
}

// === FilterableVectorEngine ===

func (e *FlatMetaVectorEngine) InsertVectorWithMetadata(id int64, vector []float32, metadata map[string]any) error {
	if len(vector) != e.dim {
		return fmt.Errorf("vector length mismatch: expected %d, got %d", e.dim, len(vector))
	}
	norm, err := e.coerceMetadata(metadata)
	if err != nil {
		return err
	}
	metaBytes, err := json.Marshal(norm)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}

	if e.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := e.wal.WriteEntry(string(key), string(encodeFlatMetaWALValue(metaBytes, vector))); err != nil {
			return err
		}
	}

	vecCopy := append([]float32(nil), vector...)
	e.lock.Lock()
	e.indexInsertLocked(id, vecCopy, norm)
	e.lock.Unlock()

	e.enqueuePersist(flatMetaPersistItem{id: id, meta: metaBytes, vec: vecCopy})
	if e.wal != nil {
		return e.wal.MarkCommitted()
	}
	return nil
}

func (e *FlatMetaVectorEngine) SearchTopKFiltered(query []float32, k int, filter *MetadataFilter) ([]int64, []float32, error) {
	if len(query) != e.dim {
		return nil, nil, fmt.Errorf("invalid query size: expected %d, got %d", e.dim, len(query))
	}
	if k <= 0 {
		k = 1
	}
	e.lock.RLock()
	defer e.lock.RUnlock()

	candidates, err := e.candidateIDsLocked(filter)
	if err != nil {
		return nil, nil, err
	}
	pairs := e.scoreCandidatesLocked(query, candidates, false, 0)
	sort.Slice(pairs, func(i, j int) bool {
		return betterDistanceForMetric(e.metric, pairs[i].dist, pairs[j].dist)
	})
	if len(pairs) > k {
		pairs = pairs[:k]
	}
	return splitPairs(pairs)
}

func (e *FlatMetaVectorEngine) RangeSearchFiltered(query []float32, radius float32, filter *MetadataFilter) ([]int64, []float32, error) {
	if len(query) != e.dim {
		return nil, nil, fmt.Errorf("invalid query size: expected %d, got %d", e.dim, len(query))
	}
	e.lock.RLock()
	defer e.lock.RUnlock()

	candidates, err := e.candidateIDsLocked(filter)
	if err != nil {
		return nil, nil, err
	}
	pairs := e.scoreCandidatesLocked(query, candidates, true, radius)
	sort.Slice(pairs, func(i, j int) bool {
		return betterDistanceForMetric(e.metric, pairs[i].dist, pairs[j].dist)
	})
	return splitPairs(pairs)
}

// === filter evaluation ===

func (e *FlatMetaVectorEngine) candidateIDsLocked(filter *MetadataFilter) (*roaring64.Bitmap, error) {
	if filter == nil {
		return e.liveIDs.Clone(), nil
	}
	set, err := e.evalFilterLocked(filter)
	if err != nil {
		return nil, err
	}
	// Restrict to live IDs (defensive; posting lists already track live entries).
	set.And(e.liveIDs)
	return set, nil
}

func (e *FlatMetaVectorEngine) evalFilterLocked(filter *MetadataFilter) (*roaring64.Bitmap, error) {
	switch filter.Op {
	case FilterOpAnd:
		var acc *roaring64.Bitmap
		for _, sub := range filter.Filters {
			s, err := e.evalFilterLocked(sub)
			if err != nil {
				return nil, err
			}
			if acc == nil {
				acc = s
			} else {
				acc.And(s)
			}
		}
		if acc == nil {
			return roaring64.New(), nil
		}
		return acc, nil
	case FilterOpOr:
		acc := roaring64.New()
		for _, sub := range filter.Filters {
			s, err := e.evalFilterLocked(sub)
			if err != nil {
				return nil, err
			}
			acc.Or(s)
		}
		return acc, nil
	case FilterOpNot:
		if len(filter.Filters) != 1 {
			return nil, fmt.Errorf("%q requires exactly one sub-filter", filter.Op)
		}
		s, err := e.evalFilterLocked(filter.Filters[0])
		if err != nil {
			return nil, err
		}
		return roaring64.AndNot(e.liveIDs, s), nil
	case FilterOpEq:
		return e.eqSetLocked(filter.Field, filter.Value)
	case FilterOpIn:
		acc := roaring64.New()
		for _, v := range filter.Values {
			s, err := e.eqSetLocked(filter.Field, v)
			if err != nil {
				return nil, err
			}
			acc.Or(s)
		}
		return acc, nil
	case FilterOpGt, FilterOpGte, FilterOpLt, FilterOpLte:
		return e.rangeSetLocked(filter.Field, filter.Op, filter.Value)
	case FilterOpBetween:
		if len(filter.Values) != 2 {
			return nil, fmt.Errorf("%q requires two values", filter.Op)
		}
		lo, ok1 := toFloat64(filter.Values[0])
		hi, ok2 := toFloat64(filter.Values[1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%q on %q requires numeric bounds", filter.Op, filter.Field)
		}
		return e.numericRangeLocked(filter.Field, &lo, true, &hi, true), nil
	default:
		return nil, fmt.Errorf("unknown filter operator %q", filter.Op)
	}
}

func (e *FlatMetaVectorEngine) eqSetLocked(field string, raw any) (*roaring64.Bitmap, error) {
	switch e.specTypes[field] {
	case MetadataTypeString:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("eq on string field %q requires a string value", field)
		}
		if ids, ok := e.stringIdx[field][s]; ok {
			return ids.Clone(), nil
		}
		return roaring64.New(), nil
	case MetadataTypeInt, MetadataTypeFloat:
		v, ok := toFloat64(raw)
		if !ok {
			return nil, fmt.Errorf("eq on numeric field %q requires a number", field)
		}
		if tree := e.numIdx[field]; tree != nil {
			if entry, ok := tree.Get(flatMetaNumEntry{value: v}); ok {
				return entry.ids.Clone(), nil
			}
		}
		return roaring64.New(), nil
	default:
		return nil, fmt.Errorf("field %q is not indexed", field)
	}
}

func (e *FlatMetaVectorEngine) rangeSetLocked(field, op string, raw any) (*roaring64.Bitmap, error) {
	v, ok := toFloat64(raw)
	if !ok {
		return nil, fmt.Errorf("range op %q on %q requires a number", op, field)
	}
	switch op {
	case FilterOpGt:
		return e.numericRangeLocked(field, &v, false, nil, false), nil
	case FilterOpGte:
		return e.numericRangeLocked(field, &v, true, nil, false), nil
	case FilterOpLt:
		return e.numericRangeLocked(field, nil, false, &v, false), nil
	case FilterOpLte:
		return e.numericRangeLocked(field, nil, false, &v, true), nil
	default:
		return nil, fmt.Errorf("unsupported range op %q", op)
	}
}

func (e *FlatMetaVectorEngine) numericRangeLocked(field string, lo *float64, loInclusive bool, hi *float64, hiInclusive bool) *roaring64.Bitmap {
	out := roaring64.New()
	tree := e.numIdx[field]
	if tree == nil {
		return out
	}
	iter := func(entry flatMetaNumEntry) bool {
		if hi != nil {
			if hiInclusive {
				if entry.value > *hi {
					return false
				}
			} else if entry.value >= *hi {
				return false
			}
		}
		if lo != nil && !loInclusive && entry.value == *lo {
			return true // strict greater-than skips the lower bound itself
		}
		out.Or(entry.ids)
		return true
	}
	if lo != nil {
		tree.AscendGreaterOrEqual(flatMetaNumEntry{value: *lo}, iter)
	} else {
		tree.Ascend(iter)
	}
	return out
}

// === scoring ===

type flatMetaPair struct {
	id   int64
	dist float32
}

func (e *FlatMetaVectorEngine) scoreCandidatesLocked(query []float32, candidates *roaring64.Bitmap, useRadius bool, radius float32) []flatMetaPair {
	ids := candidates.ToArray()
	pairs := make([]flatMetaPair, 0, len(ids))
	for _, u := range ids {
		id := int64(u)
		vec, ok := e.vectors[id]
		if !ok {
			continue
		}
		dist := float32(metricDistance(e.metric, query, vec))
		if useRadius && !withinRadius(e.metric, dist, radius) {
			continue
		}
		pairs = append(pairs, flatMetaPair{id: id, dist: dist})
	}
	return pairs
}

func splitPairs(pairs []flatMetaPair) ([]int64, []float32, error) {
	ids := make([]int64, len(pairs))
	dists := make([]float32, len(pairs))
	for i, p := range pairs {
		ids[i] = p.id
		dists[i] = p.dist
	}
	return ids, dists, nil
}

// === in-memory index maintenance (lock held) ===

func (e *FlatMetaVectorEngine) indexInsertLocked(id int64, vec []float32, norm map[string]any) {
	if old, ok := e.metadata[id]; ok {
		e.removeFromIndexesLocked(id, old)
	}
	e.vectors[id] = vec
	e.metadata[id] = norm
	e.liveIDs.Add(uint64(id))
	e.addToIndexesLocked(id, norm)
}

func (e *FlatMetaVectorEngine) indexRemoveLocked(id int64) {
	if old, ok := e.metadata[id]; ok {
		e.removeFromIndexesLocked(id, old)
	}
	delete(e.vectors, id)
	delete(e.metadata, id)
	e.liveIDs.Remove(uint64(id))
}

func (e *FlatMetaVectorEngine) addToIndexesLocked(id int64, norm map[string]any) {
	uid := uint64(id)
	for field, raw := range norm {
		switch e.specTypes[field] {
		case MetadataTypeString:
			s, _ := raw.(string)
			values := e.stringIdx[field]
			bitmap, ok := values[s]
			if !ok {
				bitmap = roaring64.New()
				values[s] = bitmap
			}
			bitmap.Add(uid)
		case MetadataTypeInt, MetadataTypeFloat:
			v, _ := toFloat64(raw)
			tree := e.numIdx[field]
			if entry, ok := tree.Get(flatMetaNumEntry{value: v}); ok {
				entry.ids.Add(uid)
			} else {
				bitmap := roaring64.New()
				bitmap.Add(uid)
				tree.ReplaceOrInsert(flatMetaNumEntry{value: v, ids: bitmap})
			}
		}
	}
}

func (e *FlatMetaVectorEngine) removeFromIndexesLocked(id int64, norm map[string]any) {
	uid := uint64(id)
	for field, raw := range norm {
		switch e.specTypes[field] {
		case MetadataTypeString:
			s, _ := raw.(string)
			if values := e.stringIdx[field]; values != nil {
				if bitmap, ok := values[s]; ok {
					bitmap.Remove(uid)
					if bitmap.IsEmpty() {
						delete(values, s)
					}
				}
			}
		case MetadataTypeInt, MetadataTypeFloat:
			v, _ := toFloat64(raw)
			tree := e.numIdx[field]
			if tree == nil {
				continue
			}
			if entry, ok := tree.Get(flatMetaNumEntry{value: v}); ok {
				entry.ids.Remove(uid)
				if entry.ids.IsEmpty() {
					tree.Delete(flatMetaNumEntry{value: v})
				}
			}
		}
	}
}

// coerceMetadata validates raw metadata against the declared specs and returns a
// normalized map containing only declared fields (string -> string, numeric -> float64).
func (e *FlatMetaVectorEngine) coerceMetadata(raw map[string]any) (map[string]any, error) {
	norm := make(map[string]any, len(e.specs))
	for _, spec := range e.specs {
		val, ok := raw[spec.Name]
		if !ok || val == nil {
			continue
		}
		switch spec.Type {
		case MetadataTypeString:
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("metadata field %q must be a string", spec.Name)
			}
			norm[spec.Name] = s
		case MetadataTypeInt:
			f, ok := toFloat64(val)
			if !ok {
				return nil, fmt.Errorf("metadata field %q must be a number", spec.Name)
			}
			if f != math.Trunc(f) {
				return nil, fmt.Errorf("metadata field %q must be an integer", spec.Name)
			}
			norm[spec.Name] = f
		case MetadataTypeFloat:
			f, ok := toFloat64(val)
			if !ok {
				return nil, fmt.Errorf("metadata field %q must be a number", spec.Name)
			}
			norm[spec.Name] = f
		}
	}
	return norm, nil
}

// === persistence ===

func (e *FlatMetaVectorEngine) enqueuePersist(item flatMetaPersistItem) {
	e.persistMu.Lock()
	e.persistBuf = append(e.persistBuf, item)
	e.persistMu.Unlock()
	maintenance.MarkVectorFlushDirty(e)
}

// MaintenanceFlush participates in the shared 50ms vector flush loop.
func (e *FlatMetaVectorEngine) MaintenanceFlush() {
	e.maintenanceMu.RLock()
	defer e.maintenanceMu.RUnlock()
	if e.maintenanceClosed {
		return
	}
	e.flushData(false)
}

func (e *FlatMetaVectorEngine) flushData(force bool) {
	e.persistMu.Lock()
	buf := e.persistBuf
	if !force && len(buf) == 0 {
		e.persistMu.Unlock()
		return
	}
	e.persistBuf = nil
	e.persistMu.Unlock()
	if len(buf) == 0 {
		return
	}

	// Hold the write lock for the in-memory segment/manifest updates and the raw
	// file writes, then release it before fsync so searches are not blocked for
	// the full duration of the (potentially slow) syscall.
	e.lock.Lock()
	active := e.activeSegmentLocked()
	if active == nil {
		e.lock.Unlock()
		return
	}
	dataFile := active.dataFile
	for _, item := range buf {
		if err := appendFlatMetaRecord(dataFile, item); err != nil {
			log.Printf("flat-meta append failed for id=%d: %v", item.id, err)
		}
	}
	if info, err := dataFile.Stat(); err == nil {
		active.meta.SizeBytes = info.Size()
		e.updateSegmentMetaLocked(active.meta)
	}
	rotateErr := e.rotateHotSegmentLocked()
	e.lock.Unlock()

	if err := dataFile.Sync(); err != nil {
		log.Printf("flat-meta data file sync failed: %v", err)
	}
	if rotateErr != nil {
		log.Printf("flat-meta hot segment rotation failed: %v", rotateErr)
	}
}

// appendFlatMetaRecord appends one record to file.
// Layout: [id:8][flag:1][metaLen:4][meta][vector: dim*4 (live only)].
func appendFlatMetaRecord(file *os.File, item flatMetaPersistItem) error {
	header := make([]byte, 13)
	binary.LittleEndian.PutUint64(header[0:8], uint64(item.id))
	if item.tombstone {
		header[8] = flatMetaDataFlagTombstone
		binary.LittleEndian.PutUint32(header[9:13], 0)
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return err
		}
		_, err := file.Write(header)
		return err
	}

	header[8] = flatMetaDataFlagLive
	binary.LittleEndian.PutUint32(header[9:13], uint32(len(item.meta)))
	body := make([]byte, 0, 13+len(item.meta)+4*len(item.vec))
	body = append(body, header...)
	body = append(body, item.meta...)
	vecBytes := make([]byte, 4*len(item.vec))
	for i, v := range item.vec {
		binary.LittleEndian.PutUint32(vecBytes[i*4:], math.Float32bits(v))
	}
	body = append(body, vecBytes...)
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	_, err := file.Write(body)
	return err
}

// === segment management (lock held) ===

func (e *FlatMetaVectorEngine) activeSegmentLocked() *flatMetaSegment {
	for _, segment := range e.segments {
		if segment.meta.ID == e.manifest.ActiveSegmentID {
			return segment
		}
	}
	return nil
}

func (e *FlatMetaVectorEngine) updateSegmentMetaLocked(meta SegmentMeta) {
	for idx := range e.segments {
		if e.segments[idx].meta.ID == meta.ID {
			e.segments[idx].meta = meta
			break
		}
	}
	for idx := range e.manifest.Segments {
		if e.manifest.Segments[idx].ID == meta.ID {
			e.manifest.Segments[idx] = meta
			break
		}
	}
}

func (e *FlatMetaVectorEngine) segmentByIDLocked(id int64) *flatMetaSegment {
	for _, segment := range e.segments {
		if segment.meta.ID == id {
			return segment
		}
	}
	return nil
}

func (e *FlatMetaVectorEngine) segmentIndexByIDLocked(id int64) int {
	for idx, segment := range e.segments {
		if segment.meta.ID == id {
			return idx
		}
	}
	return -1
}

func (e *FlatMetaVectorEngine) scheduleMergeCheckLocked() {
	select {
	case e.mergeQueue <- struct{}{}:
	default:
	}
}

// rotateHotSegmentLocked seals the active segment and opens a fresh hot segment
// once the active file exceeds the rollover threshold. The sealed segment is
// marked cold directly: flat-meta keeps no per-segment on-disk index, so there
// is no separate indexing phase before it becomes eligible for merging.
func (e *FlatMetaVectorEngine) rotateHotSegmentLocked() error {
	active := e.activeSegmentLocked()
	if active == nil {
		return errors.New("no active segment")
	}
	if active.meta.SizeBytes < e.settings.SegmentRolloverBytes {
		return nil
	}

	active.meta.State = SegmentStateCold
	active.meta.SealedAtUnixNs = time.Now().UnixNano()
	e.updateSegmentMetaLocked(active.meta)

	newID := e.manifest.NextSegmentID
	e.manifest.NextSegmentID++
	newMeta := SegmentMeta{
		ID:              newID,
		State:           SegmentStateHot,
		DataFile:        e.layout.DataFileName(newID),
		CreatedAtUnixNs: time.Now().UnixNano(),
	}
	newDataFile, err := os.OpenFile(e.layout.DataPath(newID), os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}

	e.manifest.ActiveSegmentID = newID
	e.manifest.Segments = append(e.manifest.Segments, newMeta)
	e.segments = append(e.segments, &flatMetaSegment{meta: newMeta, dataFile: newDataFile})
	if err := WriteSegmentManifest(e.layout, e.manifest); err != nil {
		return err
	}

	e.scheduleMergeCheckLocked()
	return nil
}

// === background merge ===

func (e *FlatMetaVectorEngine) mergeWorker() {
	defer e.backgroundWG.Done()
	for {
		select {
		case <-e.backgroundStop:
			return
		case <-e.mergeQueue:
			for e.tryMergeOldestColdSegments() {
			}
		}
	}
}

func (e *FlatMetaVectorEngine) tryMergeOldestColdSegments() bool {
	e.lock.Lock()
	if len(e.segments) <= e.settings.MaxSegmentsBeforeMerge {
		e.lock.Unlock()
		return false
	}

	var first, second *flatMetaSegment
	for _, segment := range e.segments {
		if segment.meta.ID == e.manifest.ActiveSegmentID {
			continue
		}
		if segment.meta.State != SegmentStateCold {
			continue
		}
		if first == nil {
			first = segment
			continue
		}
		second = segment
		break
	}
	if first == nil || second == nil {
		e.lock.Unlock()
		return false
	}

	first.meta.State = SegmentStateMerging
	second.meta.State = SegmentStateMerging
	e.updateSegmentMetaLocked(first.meta)
	e.updateSegmentMetaLocked(second.meta)
	newID := second.meta.ID
	firstDesc := e.layout.Descriptor(first.meta)
	secondDesc := e.layout.Descriptor(second.meta)
	_ = WriteSegmentManifest(e.layout, e.manifest)
	e.lock.Unlock()

	mergedMeta, mergedFile, err := e.mergeSegments(newID, firstDesc, secondDesc)
	if err != nil {
		log.Printf("merge flat-meta segments %d and %d failed: %v", first.meta.ID, second.meta.ID, err)
		e.lock.Lock()
		if segment := e.segmentByIDLocked(first.meta.ID); segment != nil {
			segment.meta.State = SegmentStateCold
			e.updateSegmentMetaLocked(segment.meta)
		}
		if segment := e.segmentByIDLocked(second.meta.ID); segment != nil {
			segment.meta.State = SegmentStateCold
			e.updateSegmentMetaLocked(segment.meta)
		}
		_ = WriteSegmentManifest(e.layout, e.manifest)
		e.lock.Unlock()
		return false
	}

	e.lock.Lock()
	defer e.lock.Unlock()
	firstIdx := e.segmentIndexByIDLocked(first.meta.ID)
	secondIdx := e.segmentIndexByIDLocked(second.meta.ID)
	if firstIdx < 0 || secondIdx < 0 || secondIdx <= firstIdx {
		_ = mergedFile.Close()
		return false
	}

	_ = e.segments[firstIdx].dataFile.Close()
	_ = e.segments[secondIdx].dataFile.Close()
	_ = os.Remove(firstDesc.DataPath)

	mergedSegment := &flatMetaSegment{meta: mergedMeta, dataFile: mergedFile}
	e.segments = append(append([]*flatMetaSegment{}, mergedSegment), e.segments[secondIdx+1:]...)
	e.manifest.Segments = append(append([]SegmentMeta{}, mergedMeta), e.manifest.Segments[secondIdx+1:]...)
	_ = WriteSegmentManifest(e.layout, e.manifest)
	return len(e.segments) > e.settings.MaxSegmentsBeforeMerge
}

// mergeSegments compacts two segment data files into a new one keyed on newID,
// keeping the latest record per id (including tombstones). It returns the new
// segment meta and an open handle to the merged file.
func (e *FlatMetaVectorEngine) mergeSegments(newID int64, firstDesc, secondDesc SegmentDescriptor) (SegmentMeta, *os.File, error) {
	latestRecords, err := collectLatestFlatMetaRecords(e.dim, firstDesc.DataPath, secondDesc.DataPath)
	if err != nil {
		return SegmentMeta{}, nil, err
	}

	mergedDataPath := e.layout.DataPath(newID)
	if err := writeMergedFlatMetaDataFile(mergedDataPath, latestRecords); err != nil {
		return SegmentMeta{}, nil, err
	}
	dataFile, err := os.OpenFile(mergedDataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return SegmentMeta{}, nil, err
	}

	meta := SegmentMeta{
		ID:              newID,
		State:           SegmentStateCold,
		DataFile:        filepath.Base(mergedDataPath),
		CreatedAtUnixNs: time.Now().UnixNano(),
	}
	if info, err := dataFile.Stat(); err == nil {
		meta.SizeBytes = info.Size()
	}
	return meta, dataFile, nil
}

func collectLatestFlatMetaRecords(dim int, paths ...string) (map[int64][]byte, error) {
	records := make(map[int64][]byte)
	for _, path := range paths {
		err := streamFlatMetaDataFile(path, dim, func(rec flatMetaRecord) error {
			records[rec.id] = append([]byte(nil), rec.raw...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return records, nil
}

func writeMergedFlatMetaDataFile(dataPath string, records map[int64][]byte) error {
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return err
	}
	tmpPath := dataPath + ".merge.tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	for _, record := range records {
		if _, err := file.Write(record); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Remove(dataPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpPath, dataPath)
}

func (e *FlatMetaVectorEngine) replayWAL() error {
	if e.wal == nil {
		return nil
	}
	records, err := e.wal.Replay()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	for _, entry := range records {
		keyBytes := []byte(entry.Key)
		if len(keyBytes) != 8 {
			return fmt.Errorf("invalid WAL key length: expected 8, got %d", len(keyBytes))
		}
		id := int64(binary.LittleEndian.Uint64(keyBytes))
		if entry.Flag == wal.EntryDeleted {
			e.lock.Lock()
			_, existed := e.metadata[id]
			e.indexRemoveLocked(id)
			e.lock.Unlock()
			if existed {
				e.enqueuePersist(flatMetaPersistItem{id: id, tombstone: true})
			}
			continue
		}
		metaBytes, vec, err := decodeFlatMetaWALValue([]byte(entry.Value), e.dim)
		if err != nil {
			return fmt.Errorf("decode WAL value for id %d: %w", id, err)
		}
		var raw map[string]any
		if len(metaBytes) > 0 {
			if err := json.Unmarshal(metaBytes, &raw); err != nil {
				return fmt.Errorf("decode WAL metadata for id %d: %w", id, err)
			}
		}
		norm, err := e.coerceMetadata(raw)
		if err != nil {
			return err
		}
		e.lock.Lock()
		e.indexInsertLocked(id, vec, norm)
		e.lock.Unlock()
		e.enqueuePersist(flatMetaPersistItem{id: id, meta: metaBytes, vec: vec})
	}
	e.flushData(true)
	return e.wal.Clear()
}

// streamFlatMetaDataFile reads an append-only data file record by record (in file
// order) and invokes fn for each. A truncated trailing record (e.g. from a crash
// mid-append) is treated as end-of-file. A missing file is not an error.
func streamFlatMetaDataFile(path string, dim int, fn func(flatMetaRecord) error) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	header := make([]byte, 13)
	for {
		if _, err := io.ReadFull(file, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}
		id := int64(binary.LittleEndian.Uint64(header[0:8]))
		flag := header[8]
		metaLen := binary.LittleEndian.Uint32(header[9:13])

		metaBuf := make([]byte, metaLen)
		if _, err := io.ReadFull(file, metaBuf); err != nil {
			break
		}
		if flag == flatMetaDataFlagTombstone {
			raw := make([]byte, 0, 13+len(metaBuf))
			raw = append(raw, header...)
			raw = append(raw, metaBuf...)
			if err := fn(flatMetaRecord{id: id, tombstone: true, raw: raw, meta: metaBuf}); err != nil {
				return err
			}
			continue
		}
		vecBuf := make([]byte, 4*dim)
		if _, err := io.ReadFull(file, vecBuf); err != nil {
			break
		}
		vec := make([]float32, dim)
		for i := 0; i < dim; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(vecBuf[i*4:]))
		}
		raw := make([]byte, 0, 13+len(metaBuf)+len(vecBuf))
		raw = append(raw, header...)
		raw = append(raw, metaBuf...)
		raw = append(raw, vecBuf...)
		if err := fn(flatMetaRecord{id: id, tombstone: false, raw: raw, meta: metaBuf, vec: vec}); err != nil {
			return err
		}
	}
	return nil
}

func encodeFlatMetaWALValue(meta []byte, vec []float32) []byte {
	buf := make([]byte, 4+len(meta)+4*len(vec))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(meta)))
	copy(buf[4:], meta)
	off := 4 + len(meta)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[off+i*4:], math.Float32bits(v))
	}
	return buf
}

func decodeFlatMetaWALValue(b []byte, dim int) ([]byte, []float32, error) {
	if len(b) < 4 {
		return nil, nil, fmt.Errorf("short WAL value")
	}
	metaLen := binary.LittleEndian.Uint32(b[0:4])
	if len(b) < int(4+metaLen) {
		return nil, nil, fmt.Errorf("truncated WAL metadata")
	}
	meta := b[4 : 4+metaLen]
	vecBytes := b[4+metaLen:]
	if len(vecBytes) != 4*dim {
		return nil, nil, fmt.Errorf("WAL vector length mismatch: expected %d bytes, got %d", 4*dim, len(vecBytes))
	}
	vec := make([]float32, dim)
	for i := 0; i < dim; i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(vecBytes[i*4:]))
	}
	return append([]byte(nil), meta...), vec, nil
}

// === distances ===

func metricDistance(metric int, a, b []float32) float64 {
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
		// metric_arg (p) is not plumbed through the space model; default p=2.
		const p = 2.0
		var s float64
		for i := range a {
			s += math.Pow(math.Abs(float64(a[i])-float64(b[i])), p)
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
	default: // faiss.MetricL2 (squared L2)
		var s float64
		for i := range a {
			d := float64(a[i]) - float64(b[i])
			s += d * d
		}
		return s
	}
}

func withinRadius(metric int, dist, radius float32) bool {
	if metric == faiss.MetricInnerProduct {
		return dist >= radius
	}
	return dist <= radius
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
