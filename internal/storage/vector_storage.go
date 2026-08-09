package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
	"github.com/shibudb.org/shibudb-server/internal/maintenance"
	"github.com/shibudb.org/shibudb-server/internal/wal"
)

// tombstoneMarker is the first float32 of a vector record meaning "deleted" (same data file, no extra file).
// Quiet NaN 0x7FC00000 — reserved; vectors with NaN in first dimension are not supported.
const tombstoneMarker = uint32(0x7FC00000)

// ErrDeletionNotSupported is returned by RemoveVector when the index type (e.g. HNSW) does not support vector deletion.
var ErrDeletionNotSupported = errors.New("vector deletion not supported for HNSW index type")

type VectorEngineImpl struct {
	wal           *wal.WAL
	walMu         sync.Mutex
	maxVectorSize int

	indexType string
	metric    int

	layout           SegmentLayout
	primaryDataPath  string
	primaryIndexPath string
	manifest         *SegmentManifest
	store            *vectorStore

	// Lifecycle / checkpointing
	closeOnce         sync.Once
	checkpointMu      sync.Mutex
	maintenanceMu     sync.RWMutex
	maintenanceClosed bool

	lock sync.RWMutex

	batchMu sync.Mutex
	batch   map[int64]vectorBatchEntry
	pending atomic.Bool
}

var _ VectorEngine = (*VectorEngineImpl)(nil)

type vectorStore struct {
	dataFile    *os.File
	index       faiss.Index
	fileOffsets map[int64]int64
	deletedIDs  map[int64]bool
}

type vectorBatchEntry struct {
	vector  []float32
	deleted bool
}

// NewVectorEngine builds/loads the ID-mapped FAISS index and opens data + WAL files.
func NewVectorEngine(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int, enableWAL bool) (*VectorEngineImpl, error) {
	return NewVectorEngineWithSettings(dataPath, indexPath, walPath, maxVectorSize, indexDesc, metric, enableWAL, SpaceSettings{})
}

func NewVectorEngineWithSettings(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int, enableWAL bool, _ SpaceSettings) (*VectorEngineImpl, error) {
	var w *wal.WAL
	var err error
	if enableWAL {
		w, err = wal.OpenWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("open WAL: %w", err)
		}
	}

	e := &VectorEngineImpl{
		wal:              w,
		maxVectorSize:    maxVectorSize,
		indexType:        indexDesc,
		metric:           metric,
		batch:            make(map[int64]vectorBatchEntry),
		layout:           NewSegmentLayout(filepath.Dir(dataPath), "vector", ".db", ".faiss"),
		primaryDataPath:  dataPath,
		primaryIndexPath: indexPath,
	}

	manifest, err := e.loadOrCreateManifest()
	if err != nil {
		if e.wal != nil {
			_ = e.wal.Close()
		}
		return nil, err
	}
	e.manifest = manifest

	if err := e.compactLegacySegmentedLayoutIfNeeded(); err != nil {
		if e.wal != nil {
			_ = e.wal.Close()
		}
		return nil, err
	}

	if err := e.loadStore(); err != nil {
		if e.wal != nil {
			_ = e.wal.Close()
		}
		return nil, err
	}

	if enableWAL {
		if err := e.replayWAL(); err != nil {
			return nil, fmt.Errorf("WAL replay failed: %w", err)
		}
	}

	return e, nil
}

func (ve *VectorEngineImpl) UpdateSpaceSettings(_ SpaceSettings) error {
	return nil
}

func (ve *VectorEngineImpl) loadOrCreateManifest() (*SegmentManifest, error) {
	path := ve.layout.ManifestPath()
	if _, err := os.Stat(path); err == nil {
		return LoadOrCreateSegmentManifest(ve.layout)
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
			DataFile:        filepath.Base(ve.primaryDataPath),
			IndexFile:       filepath.Base(ve.primaryIndexPath),
			CreatedAtUnixNs: now,
		}},
	}
	if info, err := os.Stat(ve.primaryDataPath); err == nil {
		manifest.Segments[0].SizeBytes = info.Size()
	}
	if err := WriteSegmentManifest(ve.layout, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// compactLegacySegmentedLayoutIfNeeded merges data created by older versions
// into the single-file layout now used by every FAISS index type.
func (ve *VectorEngineImpl) compactLegacySegmentedLayoutIfNeeded() error {
	if len(ve.manifest.Segments) <= 1 {
		return nil
	}
	log.Printf("storage: merging %d legacy vector segments into single-file layout for FAISS index %q",
		len(ve.manifest.Segments), ve.indexType)

	paths := make([]string, 0, len(ve.manifest.Segments))
	for _, meta := range ve.manifest.Segments {
		desc := ve.layout.Descriptor(meta)
		if _, err := os.Stat(desc.DataPath); err != nil {
			if os.IsNotExist(err) && filepath.Clean(desc.DataPath) == filepath.Clean(ve.primaryDataPath) {
				// The active segment file may legitimately be missing (e.g. after a rename
				// during migration/testing). Create it so compaction can proceed.
				f, createErr := os.OpenFile(desc.DataPath, os.O_RDWR|os.O_CREATE, 0666)
				if createErr != nil {
					return fmt.Errorf("create missing primary vector data file: %w", createErr)
				}
				_ = f.Close()
			} else {
				return fmt.Errorf("stat vector segment data %q: %w", desc.DataPath, err)
			}
		}
		paths = append(paths, desc.DataPath)
	}
	records, err := collectLatestVectorRecords(ve.maxVectorSize, paths...)
	if err != nil {
		return fmt.Errorf("collect vector records for compaction: %w", err)
	}
	if err := writeMergedVectorDataFile(ve.primaryDataPath, records); err != nil {
		return fmt.Errorf("write compacted vector data: %w", err)
	}
	_ = os.Remove(ve.primaryIndexPath)
	for _, meta := range ve.manifest.Segments {
		desc := ve.layout.Descriptor(meta)
		if filepath.Clean(desc.DataPath) != filepath.Clean(ve.primaryDataPath) {
			_ = os.Remove(desc.DataPath)
		}
		if desc.IndexPath != "" {
			_ = os.Remove(desc.IndexPath)
		}
	}
	now := time.Now().UnixNano()
	var size int64
	if info, err := os.Stat(ve.primaryDataPath); err == nil {
		size = info.Size()
	}
	ve.manifest.NextSegmentID = 2
	ve.manifest.ActiveSegmentID = 1
	ve.manifest.Segments = []SegmentMeta{{
		ID:              1,
		State:           SegmentStateHot,
		DataFile:        filepath.Base(ve.primaryDataPath),
		IndexFile:       filepath.Base(ve.primaryIndexPath),
		CreatedAtUnixNs: now,
		SizeBytes:       size,
	}}
	return WriteSegmentManifest(ve.layout, ve.manifest)
}

func (ve *VectorEngineImpl) loadStore() error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	ve.store = nil
	if len(ve.manifest.Segments) != 1 {
		return fmt.Errorf("FAISS manifest must contain one storage file, got %d", len(ve.manifest.Segments))
	}

	meta := ve.manifest.Segments[0]
	ve.manifest.ActiveSegmentID = meta.ID
	desc := ve.layout.Descriptor(meta)
	dataFile, err := os.OpenFile(desc.DataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}

	offsets, deletedIDs, err := scanVectorDataFile(desc.DataPath, ve.maxVectorSize)
	if err != nil {
		_ = dataFile.Close()
		return err
	}

	index, err := buildVectorIndexFromDataFile(desc.DataPath, ve.maxVectorSize, ve.activeIndexDesc(), ve.metric)
	if err != nil {
		_ = dataFile.Close()
		return err
	}
	meta.State = SegmentStateHot
	if info, err := dataFile.Stat(); err == nil {
		meta.SizeBytes = info.Size()
	}
	ve.manifest.Segments[0] = meta
	ve.store = &vectorStore{
		dataFile:    dataFile,
		index:       index,
		fileOffsets: offsets,
		deletedIDs:  deletedIDs,
	}
	return WriteSegmentManifest(ve.layout, ve.manifest)
}

func (ve *VectorEngineImpl) InsertVector(id int64, vector []float32) error {
	if len(vector) != ve.maxVectorSize {
		return fmt.Errorf("vector length mismatch: expected %d", ve.maxVectorSize)
	}
	ve.maintenanceMu.RLock()
	defer ve.maintenanceMu.RUnlock()
	if ve.maintenanceClosed {
		return errors.New("vector engine is closed")
	}

	entry := vectorBatchEntry{vector: append([]float32(nil), vector...)}
	if ve.wal != nil {
		ve.walMu.Lock()
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := ve.wal.WriteEntry(string(key), string(float32ArrayToBytes(vector))); err != nil {
			ve.walMu.Unlock()
			return err
		}
		ve.batchMu.Lock()
		ve.batch[id] = entry
		ve.pending.Store(true)
		ve.batchMu.Unlock()
		ve.walMu.Unlock()
	} else {
		ve.batchMu.Lock()
		ve.batch[id] = entry
		ve.pending.Store(true)
		ve.batchMu.Unlock()
	}
	maintenance.MarkVectorFlushDirty(ve)
	return nil
}

func (ve *VectorEngineImpl) SearchTopK(query []float32, k int) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}
	if k <= 0 {
		return []int64{}, []float32{}, nil
	}
	pending := ve.snapshotBatch()
	ve.lock.RLock()
	defer ve.lock.RUnlock()

	store := ve.store
	if store == nil {
		return nil, nil, errors.New("vector storage is not initialized")
	}

	searchK := int64(k)
	var dists []float32
	var labels []int64
	pendingLive := 0
	for _, entry := range pending {
		if !entry.deleted {
			pendingLive++
		}
	}
	for {
		var err error
		dists, labels, err = store.index.Search(query, searchK)
		if err != nil {
			return nil, nil, err
		}
		valid := 0
		for _, label := range labels {
			if label < 0 || store.deletedIDs[label] {
				continue
			}
			if _, staged := pending[label]; staged {
				continue
			}
			if _, exists := store.fileOffsets[label]; exists {
				valid++
			}
		}
		total := store.index.Ntotal()
		if valid+pendingLive >= k || searchK >= total {
			break
		}
		// Expand only when stale or unflushed entries were filtered. The normal
		// path asks FAISS for exactly k neighbors.
		searchK *= 2
		if searchK > total {
			searchK = total
		}
	}

	if len(pending) > 0 {
		type pair struct {
			id       int64
			distance float32
		}
		candidates := make([]pair, 0, len(labels)+pendingLive)
		for i, label := range labels {
			if label < 0 || store.deletedIDs[label] {
				continue
			}
			if _, staged := pending[label]; staged {
				continue
			}
			if _, exists := store.fileOffsets[label]; exists {
				candidates = append(candidates, pair{id: label, distance: dists[i]})
			}
		}
		for id, entry := range pending {
			if !entry.deleted {
				candidates = append(candidates, pair{id: id, distance: vectorDistance(ve.metric, query, entry.vector)})
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return betterDistanceForMetric(ve.metric, candidates[i].distance, candidates[j].distance)
		})
		if len(candidates) > k {
			candidates = candidates[:k]
		}
		ids := make([]int64, len(candidates))
		filteredDists := make([]float32, len(candidates))
		for i, candidate := range candidates {
			ids[i] = candidate.id
			filteredDists[i] = candidate.distance
		}
		return ids, filteredDists, nil
	}

	ids := make([]int64, 0, k)
	filteredDists := make([]float32, 0, k)
	for i, label := range labels {
		if label < 0 || store.deletedIDs[label] {
			continue
		}
		if _, exists := store.fileOffsets[label]; !exists {
			continue
		}
		ids = append(ids, label)
		filteredDists = append(filteredDists, dists[i])
		if len(ids) == k {
			break
		}
	}
	return ids, filteredDists, nil
}

func (ve *VectorEngineImpl) RangeSearch(query []float32, radius float32) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}

	pending := ve.snapshotBatch()
	ve.lock.RLock()
	defer ve.lock.RUnlock()
	store := ve.store
	if store == nil {
		return nil, nil, errors.New("vector storage is not initialized")
	}

	type pair struct {
		id  int64
		dst float32
	}
	var ps []pair

	res, err := store.index.RangeSearch(query, radius)
	if err != nil {
		return nil, nil, err
	}
	labels, dists := res.Labels()
	lims := res.Lims()
	if len(lims) != 2 {
		res.Delete()
		return nil, nil, fmt.Errorf("expected 1 query, got %d", len(lims)-1)
	}
	for i := int(lims[0]); i < int(lims[1]); i++ {
		if store.deletedIDs[labels[i]] {
			continue
		}
		if _, staged := pending[labels[i]]; staged {
			continue
		}
		if _, exists := store.fileOffsets[labels[i]]; !exists {
			continue
		}
		ps = append(ps, pair{id: labels[i], dst: dists[i]})
	}
	res.Delete()
	for id, entry := range pending {
		if entry.deleted {
			continue
		}
		distance := vectorDistance(ve.metric, query, entry.vector)
		if distanceWithinRadius(ve.metric, distance, radius) {
			ps = append(ps, pair{id: id, dst: distance})
		}
	}

	sort.Slice(ps, func(i, j int) bool {
		return betterDistanceForMetric(ve.metric, ps[i].dst, ps[j].dst)
	})
	outIDs := make([]int64, len(ps))
	outD := make([]float32, len(ps))
	for i := 0; i < len(ps); i++ {
		outIDs[i], outD[i] = ps[i].id, ps[i].dst
	}

	return outIDs, outD, nil
}

func (ve *VectorEngineImpl) GetVectorByID(id int64) ([]float32, error) {
	if ve.pending.Load() {
		ve.batchMu.Lock()
		entry, exists := ve.batch[id]
		ve.batchMu.Unlock()
		if exists {
			if entry.deleted {
				return nil, fmt.Errorf("ID %d not found", id)
			}
			return append([]float32(nil), entry.vector...), nil
		}
	}

	ve.lock.RLock()
	defer ve.lock.RUnlock()
	store := ve.store
	if store == nil {
		return nil, errors.New("vector storage is not initialized")
	}
	offset, ok := store.fileOffsets[id]
	if !ok || store.deletedIDs[id] {
		return nil, fmt.Errorf("ID %d not found", id)
	}
	recordSize := 8 + 4*ve.maxVectorSize
	buf := make([]byte, recordSize)
	if _, err := store.dataFile.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("read vector at offset %d: %w", offset, err)
	}
	if len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker {
		return nil, fmt.Errorf("ID %d not found", id)
	}
	return bytesToFloat32Array(buf[8:])
}

func (ve *VectorEngineImpl) RemoveVector(id int64) error {
	// HNSW index type does not support remove_ids in FAISS; reject deletion up front.
	if strings.HasPrefix(ve.indexType, "HNSW") {
		return ErrDeletionNotSupported
	}
	ve.maintenanceMu.RLock()
	defer ve.maintenanceMu.RUnlock()
	if ve.maintenanceClosed {
		return errors.New("vector engine is closed")
	}

	entry := vectorBatchEntry{deleted: true}
	if ve.wal != nil {
		ve.walMu.Lock()
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := ve.wal.WriteDelete(string(key)); err != nil {
			ve.walMu.Unlock()
			return err
		}
		ve.batchMu.Lock()
		ve.batch[id] = entry
		ve.pending.Store(true)
		ve.batchMu.Unlock()
		ve.walMu.Unlock()
	} else {
		ve.batchMu.Lock()
		ve.batch[id] = entry
		ve.pending.Store(true)
		ve.batchMu.Unlock()
	}
	maintenance.MarkVectorFlushDirty(ve)
	return nil
}

func (ve *VectorEngineImpl) Close() error {
	ve.closeOnce.Do(func() {
		ve.maintenanceMu.Lock()
		ve.maintenanceClosed = true
		maintenance.UnregisterVectorFlush(ve)
		maintenance.UnregisterVectorCheckpoint(ve)
		ve.maintenanceMu.Unlock()

		if err := ve.FlushBatch(); err != nil {
			log.Printf("final vector batch flush failed: %v", err)
		}

		if ve.wal != nil {
			_ = ve.wal.Close()
		}
		ve.lock.Lock()
		if ve.store != nil {
			_ = ve.store.dataFile.Close()
			ve.store.index.Delete()
		}
		ve.lock.Unlock()
	})
	return nil
}

// === Internals ===

func (ve *VectorEngineImpl) checkpoint() error {
	ve.checkpointMu.Lock()
	defer ve.checkpointMu.Unlock()

	ve.lock.RLock()
	defer ve.lock.RUnlock()
	if ve.store == nil {
		return errors.New("vector storage is not initialized")
	}
	if err := ve.store.dataFile.Sync(); err != nil {
		return fmt.Errorf("sync data file: %w", err)
	}
	return nil
}

func (ve *VectorEngineImpl) replayWAL() error {
	if ve.wal == nil {
		return nil
	}

	records, err := ve.wal.Replay()
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
			ve.batch[id] = vectorBatchEntry{deleted: true}
			ve.pending.Store(true)
			continue
		}

		vec, err := bytesToFloat32Array([]byte(entry.Value))
		if err != nil {
			return fmt.Errorf("WAL decode: %w", err)
		}

		ve.batch[id] = vectorBatchEntry{vector: vec}
		ve.pending.Store(true)
	}
	return ve.FlushBatch()
}

// MaintenanceFlush participates in the shared vector flush loop.
func (ve *VectorEngineImpl) MaintenanceFlush() {
	ve.maintenanceMu.RLock()
	defer ve.maintenanceMu.RUnlock()
	if ve.maintenanceClosed {
		return
	}
	if err := ve.FlushBatch(); err != nil {
		log.Printf("vector batch flush failed: %v", err)
	}
}

// MaintenanceCheckpoint participates in the shared vector checkpoint loop.
func (ve *VectorEngineImpl) MaintenanceCheckpoint() {
	ve.maintenanceMu.RLock()
	defer ve.maintenanceMu.RUnlock()
	if ve.maintenanceClosed {
		return
	}
	if err := ve.checkpoint(); err != nil {
		log.Printf("checkpoint failed: %v", err)
	}
}

func (ve *VectorEngineImpl) FlushBatch() error {
	if ve.wal != nil {
		ve.walMu.Lock()
		defer ve.walMu.Unlock()
	}

	ve.batchMu.Lock()
	batch := ve.batch
	ve.batch = make(map[int64]vectorBatchEntry)
	ve.pending.Store(false)
	ve.batchMu.Unlock()

	if len(batch) == 0 {
		if ve.wal != nil {
			return ve.wal.Clear()
		}
		return nil
	}

	if err := ve.flushBatch(batch); err != nil {
		ve.batchMu.Lock()
		for id, entry := range batch {
			if _, newer := ve.batch[id]; !newer {
				ve.batch[id] = entry
			}
		}
		ve.pending.Store(len(ve.batch) > 0)
		ve.batchMu.Unlock()
		maintenance.MarkVectorFlushDirty(ve)
		return err
	}

	if ve.wal != nil {
		if err := ve.wal.Clear(); err != nil {
			maintenance.MarkVectorFlushDirty(ve)
			return err
		}
	}
	return nil
}

func (ve *VectorEngineImpl) flushBatch(batch map[int64]vectorBatchEntry) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	store := ve.store
	if store == nil {
		return errors.New("vector storage is not initialized")
	}

	type appliedEntry struct {
		id      int64
		offset  int64
		deleted bool
	}
	applied := make([]appliedEntry, 0, len(batch))
	ids := make([]int64, 0, len(batch))
	vectors := make([]float32, 0, len(batch)*ve.maxVectorSize)
	removeIDs := make([]int64, 0, len(batch))
	rebuildIndex := false

	for id, entry := range batch {
		offset, err := appendVectorBatchRecord(store.dataFile, id, entry, ve.maxVectorSize)
		if err != nil {
			return fmt.Errorf("append vector batch record %d: %w", id, err)
		}
		applied = append(applied, appliedEntry{id: id, offset: offset, deleted: entry.deleted})

		if _, exists := store.fileOffsets[id]; exists && !store.deletedIDs[id] {
			removeIDs = append(removeIDs, id)
			if strings.HasPrefix(ve.indexType, "HNSW") {
				rebuildIndex = true
			}
		}
		if !entry.deleted {
			ids = append(ids, id)
			vectors = append(vectors, entry.vector...)
		}
	}

	if err := store.dataFile.Sync(); err != nil {
		return fmt.Errorf("sync vector data batch: %w", err)
	}

	if rebuildIndex {
		return ve.rebuildStoreIndexLocked()
	}

	if len(removeIDs) > 0 {
		selector, err := faiss.NewIDSelectorBatch(removeIDs)
		if err != nil {
			return fmt.Errorf("create batch ID selector: %w", err)
		}
		_, removeErr := store.index.RemoveIDs(selector)
		selector.Delete()
		if removeErr != nil {
			if err := ve.rebuildStoreIndexLocked(); err != nil {
				return fmt.Errorf("remove batch IDs: %v; rebuild index: %w", removeErr, err)
			}
			return nil
		}
	}

	if len(ids) > 0 {
		if err := store.index.AddWithIDs(vectors, ids); err != nil {
			if rebuildErr := ve.rebuildStoreIndexLocked(); rebuildErr != nil {
				return fmt.Errorf("add vector batch: %v; rebuild index: %w", err, rebuildErr)
			}
			return nil
		}
	}

	for _, entry := range applied {
		store.fileOffsets[entry.id] = entry.offset
		if entry.deleted {
			store.deletedIDs[entry.id] = true
		} else {
			delete(store.deletedIDs, entry.id)
		}
	}
	return nil
}

func (ve *VectorEngineImpl) rebuildStoreIndexLocked() error {
	index, err := buildVectorIndexFromDataFile(ve.primaryDataPath, ve.maxVectorSize, ve.activeIndexDesc(), ve.metric)
	if err != nil {
		return fmt.Errorf("rebuild vector index after batch update: %w", err)
	}
	offsets, deletedIDs, err := scanVectorDataFile(ve.primaryDataPath, ve.maxVectorSize)
	if err != nil {
		index.Delete()
		return fmt.Errorf("rescan vector data after batch update: %w", err)
	}
	ve.store.index.Delete()
	ve.store.index = index
	ve.store.fileOffsets = offsets
	ve.store.deletedIDs = deletedIDs
	return nil
}

func appendVectorBatchRecord(file *os.File, id int64, entry vectorBatchEntry, dimension int) (int64, error) {
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 8+4*dimension)
	binary.LittleEndian.PutUint64(buf[:8], uint64(id))
	if entry.deleted {
		binary.LittleEndian.PutUint32(buf[8:12], tombstoneMarker)
	} else {
		for i, value := range entry.vector {
			binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(value))
		}
	}
	if _, err := file.Write(buf); err != nil {
		return 0, err
	}
	return offset, nil
}

func (ve *VectorEngineImpl) snapshotBatch() map[int64]vectorBatchEntry {
	if !ve.pending.Load() {
		return nil
	}
	ve.batchMu.Lock()
	defer ve.batchMu.Unlock()
	if len(ve.batch) == 0 {
		return nil
	}
	batch := make(map[int64]vectorBatchEntry, len(ve.batch))
	for id, entry := range ve.batch {
		batch[id] = entry
	}
	return batch
}

func float32ArrayToBytes(arr []float32) []byte {
	buf := make([]byte, len(arr)*4)
	for i, v := range arr {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func bytesToFloat32Array(buf []byte) ([]float32, error) {
	if len(buf)%4 != 0 {
		return nil, fmt.Errorf("buffer size must be multiple of 4")
	}
	vec := make([]float32, len(buf)/4)
	for i := 0; i < len(vec); i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return vec, nil
}

func buildVectorIndexFromDataFile(dataPath string, dimension int, indexDesc string, metric int) (faiss.Index, error) {
	offsets, deletedIDs, err := scanVectorDataFile(dataPath, dimension)
	if err != nil {
		return nil, err
	}
	index, err := faiss.IndexFactory(dimension, "IDMap,"+indexDesc, metric)
	if err != nil {
		return nil, err
	}
	if len(offsets) == 0 {
		if requiredTrainCountForIndex(indexDesc) > 0 {
			index.Delete()
			return faiss.IndexFactory(dimension, "IDMap,Flat", metric)
		}
		return index, nil
	}

	dataFile, err := os.Open(dataPath)
	if err != nil {
		index.Delete()
		return nil, err
	}
	defer dataFile.Close()

	requiredTrainCount := requiredTrainCountForIndex(indexDesc)
	ids := make([]int64, 0, len(offsets))
	data := make([]float32, 0, len(offsets)*dimension)
	for id, offset := range offsets {
		if deletedIDs[id] {
			continue
		}
		_, vec, deleted, err := readVectorRecordAt(dataFile, offset, dimension)
		if err != nil {
			index.Delete()
			return nil, err
		}
		if deleted {
			continue
		}
		ids = append(ids, id)
		data = append(data, vec...)
	}
	if len(ids) == 0 {
		if requiredTrainCountForIndex(indexDesc) > 0 {
			index.Delete()
			return faiss.IndexFactory(dimension, "IDMap,Flat", metric)
		}
		return index, nil
	}
	if requiredTrainCount > 0 && len(ids) > 0 {
		if len(ids) < requiredTrainCount {
			index.Delete()
			return buildVectorIndexFromDataFile(dataPath, dimension, "Flat", metric)
		}
		trainCount := trainingSampleCount(requiredTrainCount, len(ids))
		if trainCount > len(ids) {
			trainCount = len(ids)
		}
		trainData := data[:trainCount*dimension]
		if err := index.Train(trainData); err != nil {
			index.Delete()
			return nil, err
		}
	}
	if len(ids) > 0 {
		if err := index.AddWithIDs(data, ids); err != nil {
			index.Delete()
			return nil, err
		}
	}
	return index, nil
}

func scanVectorDataFile(dataPath string, dimension int) (map[int64]int64, map[int64]bool, error) {
	file, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	recordSize := 8 + 4*dimension
	offsets := make(map[int64]int64)
	deletedIDs := make(map[int64]bool)
	var offset int64
	for {
		buf := make([]byte, recordSize)
		n, err := file.Read(buf)
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
			break
		}
		if err == io.ErrUnexpectedEOF || n < recordSize {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		id := int64(binary.LittleEndian.Uint64(buf[0:8]))
		offsets[id] = offset
		if len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker {
			deletedIDs[id] = true
		} else {
			delete(deletedIDs, id)
		}
		offset += int64(recordSize)
	}
	return offsets, deletedIDs, nil
}

func collectLatestVectorRecords(dimension int, paths ...string) (map[int64][]byte, error) {
	recordSize := 8 + 4*dimension
	records := make(map[int64][]byte)
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		for {
			buf := make([]byte, recordSize)
			n, err := file.Read(buf)
			if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
				break
			}
			if err == io.ErrUnexpectedEOF || n < recordSize {
				break
			}
			if err != nil {
				_ = file.Close()
				return nil, err
			}
			id := int64(binary.LittleEndian.Uint64(buf[0:8]))
			records[id] = append([]byte(nil), buf...)
		}
		_ = file.Close()
	}
	return records, nil
}

func writeMergedVectorDataFile(dataPath string, records map[int64][]byte) error {
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

func (ve *VectorEngineImpl) activeIndexDesc() string {
	if requiredTrainCountForIndex(ve.indexType) > 0 {
		return "Flat"
	}
	return ve.indexType
}

func betterDistanceForMetric(metric int, left, right float32) bool {
	if metric == faiss.MetricInnerProduct {
		return left > right
	}
	return left < right
}

func vectorDistance(metric int, left, right []float32) float32 {
	var distance float32
	if metric == faiss.MetricInnerProduct {
		for i := range left {
			distance += left[i] * right[i]
		}
		return distance
	}
	for i := range left {
		delta := left[i] - right[i]
		distance += delta * delta
	}
	return distance
}

func distanceWithinRadius(metric int, distance, radius float32) bool {
	if metric == faiss.MetricInnerProduct {
		return distance > radius
	}
	return distance < radius
}
