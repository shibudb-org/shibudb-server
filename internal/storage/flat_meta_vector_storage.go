package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"sync"

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
// memory; durability is provided by an append-only data file plus the WAL, and
// the in-memory indexes are rebuilt by scanning the data file on open.
//
// Numeric metadata (int and float) is indexed and compared as float64, which is
// exact for integers up to 2^53.
type FlatMetaVectorEngine struct {
	dataPath  string
	dim       int
	metric    int
	specs     []MetadataFieldSpec
	specTypes map[string]string

	wal *wal.WAL

	lock sync.RWMutex

	// In-memory state (guarded by lock).
	vectors  map[int64][]float32
	metadata map[int64]map[string]any
	liveIDs  *roaring64.Bitmap
	// stringIdx: field -> value -> set of ids (equality / IN).
	stringIdx map[string]map[string]*roaring64.Bitmap
	// numIdx: field -> ordered tree of value -> set of ids (equality / IN / range).
	numIdx map[string]*btree.BTreeG[flatMetaNumEntry]

	// Append-only persistence buffer (guarded by persistMu); file writes by fileMu.
	persistMu  sync.Mutex
	persistBuf []flatMetaPersistItem
	fileMu     sync.Mutex
	dataFile   *os.File

	closeOnce         sync.Once
	maintenanceMu     sync.RWMutex
	maintenanceClosed bool
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

// NewFlatMetaVectorEngine opens (or creates) a filterable Flat vector space.
func NewFlatMetaVectorEngine(dataPath, walPath string, dim, metric int, specs []MetadataFieldSpec, enableWAL bool) (*FlatMetaVectorEngine, error) {
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
		dataPath:  dataPath,
		dim:       dim,
		metric:    metric,
		specs:     append([]MetadataFieldSpec(nil), specs...),
		specTypes: fieldSpecTypes(specs),
		wal:       w,
		vectors:   make(map[int64][]float32),
		metadata:  make(map[int64]map[string]any),
		liveIDs:   roaring64.New(),
		stringIdx: make(map[string]map[string]*roaring64.Bitmap),
		numIdx:    make(map[string]*btree.BTreeG[flatMetaNumEntry]),
	}
	for _, spec := range specs {
		if spec.Type == MetadataTypeString {
			e.stringIdx[spec.Name] = make(map[string]*roaring64.Bitmap)
		} else {
			e.numIdx[spec.Name] = btree.NewG[flatMetaNumEntry](32, flatMetaNumLess)
		}
	}

	dataFile, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		if w != nil {
			_ = w.Close()
		}
		return nil, err
	}
	e.dataFile = dataFile

	if err := e.rebuildFromDataFile(); err != nil {
		_ = dataFile.Close()
		if w != nil {
			_ = w.Close()
		}
		return nil, err
	}

	if enableWAL {
		if err := e.replayWAL(); err != nil {
			_ = dataFile.Close()
			return nil, fmt.Errorf("WAL replay failed: %w", err)
		}
	}
	return e, nil
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
		if err := e.wal.WriteEntry(string(key), ""); err != nil {
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

		if e.wal != nil {
			_ = e.wal.Close()
		}
		e.fileMu.Lock()
		_ = e.dataFile.Close()
		e.fileMu.Unlock()
	})
	return nil
}

func (e *FlatMetaVectorEngine) UpdateSpaceSettings(_ SpaceSettings) error {
	// Segment settings do not apply to the in-memory flat engine.
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

	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	for _, item := range buf {
		if err := e.appendRecordLocked(item); err != nil {
			log.Printf("flat-meta append failed for id=%d: %v", item.id, err)
		}
	}
	if err := e.dataFile.Sync(); err != nil {
		log.Printf("flat-meta data file sync failed: %v", err)
	}
}

// appendRecordLocked writes one record; caller holds fileMu.
// Layout: [id:8][flag:1][metaLen:4][meta][vector: dim*4 (live only)].
func (e *FlatMetaVectorEngine) appendRecordLocked(item flatMetaPersistItem) error {
	header := make([]byte, 13)
	binary.LittleEndian.PutUint64(header[0:8], uint64(item.id))
	if item.tombstone {
		header[8] = flatMetaDataFlagTombstone
		binary.LittleEndian.PutUint32(header[9:13], 0)
		if _, err := e.dataFile.Seek(0, io.SeekEnd); err != nil {
			return err
		}
		_, err := e.dataFile.Write(header)
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
	if _, err := e.dataFile.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	_, err := e.dataFile.Write(body)
	return err
}

func (e *FlatMetaVectorEngine) rebuildFromDataFile() error {
	liveVecs, liveMeta, err := scanFlatMetaDataFile(e.dataPath, e.dim)
	if err != nil {
		return err
	}
	e.lock.Lock()
	defer e.lock.Unlock()
	for id, vec := range liveVecs {
		var raw map[string]any
		if len(liveMeta[id]) > 0 {
			if err := json.Unmarshal(liveMeta[id], &raw); err != nil {
				return fmt.Errorf("decode metadata for id %d: %w", id, err)
			}
		}
		norm, err := e.coerceMetadata(raw)
		if err != nil {
			return fmt.Errorf("metadata for id %d: %w", id, err)
		}
		e.indexInsertLocked(id, vec, norm)
	}
	return nil
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
		keyBytes := []byte(entry[0])
		if len(keyBytes) != 8 {
			return fmt.Errorf("invalid WAL key length: expected 8, got %d", len(keyBytes))
		}
		id := int64(binary.LittleEndian.Uint64(keyBytes))
		if len(entry[1]) == 0 {
			e.lock.Lock()
			_, existed := e.metadata[id]
			e.indexRemoveLocked(id)
			e.lock.Unlock()
			if existed {
				e.enqueuePersist(flatMetaPersistItem{id: id, tombstone: true})
			}
			continue
		}
		metaBytes, vec, err := decodeFlatMetaWALValue([]byte(entry[1]), e.dim)
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

// scanFlatMetaDataFile reads the append-only data file and returns the latest
// live vector and metadata bytes per id (tombstones drop the id).
func scanFlatMetaDataFile(path string, dim int) (map[int64][]float32, map[int64][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int64][]float32{}, map[int64][]byte{}, nil
		}
		return nil, nil, err
	}
	defer file.Close()

	reader := io.Reader(file)
	vectors := make(map[int64][]float32)
	metas := make(map[int64][]byte)
	header := make([]byte, 13)
	for {
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, nil, err
		}
		id := int64(binary.LittleEndian.Uint64(header[0:8]))
		flag := header[8]
		metaLen := binary.LittleEndian.Uint32(header[9:13])

		metaBuf := make([]byte, metaLen)
		if _, err := io.ReadFull(reader, metaBuf); err != nil {
			break
		}
		if flag == flatMetaDataFlagTombstone {
			delete(vectors, id)
			delete(metas, id)
			continue
		}
		vecBuf := make([]byte, 4*dim)
		if _, err := io.ReadFull(reader, vecBuf); err != nil {
			break
		}
		vec := make([]float32, dim)
		for i := 0; i < dim; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(vecBuf[i*4:]))
		}
		vectors[id] = vec
		metas[id] = metaBuf
	}
	return vectors, metas, nil
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
