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
	maxVectorSize int
	settings      SpaceSettings

	indexType string
	metric    int

	// vectorSegmentationEnabled is true for Flat and HNSW (no training batch).
	// When false (IVF, PQ, …): single vector data file, no hot rollover or cold
	// merge, and no sealed per-segment on-disk FAISS files. The active segment
	// still uses IDMap,Flat for incremental updates; a configured IVF/PQ index
	// is produced only for sealed segments when segmentation is enabled.
	// Legacy multi-segment manifests are compacted to the primary data file on open.
	vectorSegmentationEnabled bool

	layout           SegmentLayout
	primaryDataPath  string
	primaryIndexPath string
	manifest         *SegmentManifest
	segments         []*vectorSegment

	// Lifecycle / checkpointing
	closeOnce         sync.Once
	checkpointMu      sync.Mutex
	maintenanceMu     sync.RWMutex
	maintenanceClosed bool

	lock sync.RWMutex

	persistMu  sync.Mutex
	persistBuf []struct {
		id  int64
		vec []float32
	}
	indexBuildQueue chan int64
	mergeQueue      chan struct{}
	backgroundStop  chan struct{}
	backgroundWG    sync.WaitGroup
}

var _ VectorEngine = (*VectorEngineImpl)(nil)

type vectorSegment struct {
	meta        SegmentMeta
	dataFile    *os.File
	index       faiss.Index
	fileOffsets map[int64]int64
	deletedIDs  map[int64]bool
	// pendingUpsertIDs tracks IDs present in the FAISS index but not yet flushed to
	// the data file. RemoveIDs on some IVF variants aborts if the ID is missing;
	// we only remove when replacing an existing vector (disk or pending).
	pendingUpsertIDs map[int64]struct{}
}

// NewVectorEngine builds/loads the ID-mapped FAISS index and opens data + WAL files.
func NewVectorEngine(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int, enableWAL bool) (*VectorEngineImpl, error) {
	return NewVectorEngineWithSettings(dataPath, indexPath, walPath, maxVectorSize, indexDesc, metric, enableWAL, SpaceSettings{})
}

func NewVectorEngineWithSettings(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int, enableWAL bool, settings SpaceSettings) (*VectorEngineImpl, error) {
	var w *wal.WAL
	var err error
	if enableWAL {
		w, err = wal.OpenWAL(walPath)
		if err != nil {
			return nil, fmt.Errorf("open WAL: %w", err)
		}
	}

	e := &VectorEngineImpl{
		wal:                       w,
		maxVectorSize:             maxVectorSize,
		settings:                  NormalizeVectorSpaceSettings(indexDesc, settings),
		indexType:                 indexDesc,
		metric:                    metric,
		vectorSegmentationEnabled: requiredTrainCountForIndex(indexDesc) == 0,
		layout:                    NewSegmentLayout(filepath.Dir(dataPath), "vector", ".db", ".faiss"),
		primaryDataPath:           dataPath,
		primaryIndexPath:          indexPath,
		indexBuildQueue:           make(chan int64, 32),
		mergeQueue:                make(chan struct{}, 1),
		backgroundStop:            make(chan struct{}),
	}

	manifest, err := e.loadOrCreateManifest()
	if err != nil {
		if e.wal != nil {
			_ = e.wal.Close()
		}
		return nil, err
	}
	e.manifest = manifest

	if !e.vectorSegmentationEnabled {
		if err := e.compactNonSegmentedTrainingLayoutIfNeeded(); err != nil {
			if e.wal != nil {
				_ = e.wal.Close()
			}
			return nil, err
		}
	}

	if err := e.loadSegments(); err != nil {
		if e.wal != nil {
			_ = e.wal.Close()
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

func (ve *VectorEngineImpl) UpdateSpaceSettings(settings SpaceSettings) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()
	ve.settings = NormalizeVectorSpaceSettings(ve.indexType, settings)
	ve.scheduleMergeCheckLocked()
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

// compactNonSegmentedTrainingLayoutIfNeeded merges a legacy multi-segment manifest
// into the primary vector data file. Training-based indexes never use per-segment
// rollover; older builds may still have left multiple segments on disk.
func (ve *VectorEngineImpl) compactNonSegmentedTrainingLayoutIfNeeded() error {
	if len(ve.manifest.Segments) <= 1 {
		return nil
	}
	log.Printf("storage: merging %d vector segments into single-file layout for training index %q",
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

func (ve *VectorEngineImpl) loadSegments() error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	ve.segments = nil
	if len(ve.manifest.Segments) == 0 {
		return errors.New("segment manifest has no segments")
	}

	ve.manifest.ActiveSegmentID = ve.manifest.Segments[len(ve.manifest.Segments)-1].ID
	for idx, meta := range ve.manifest.Segments {
		desc := ve.layout.Descriptor(meta)
		flags := os.O_RDWR
		// Only the active/hot segment is allowed to be created on open. Cold segment
		// files must exist; recreating them would hide data loss/corruption.
		if meta.ID == ve.manifest.ActiveSegmentID {
			flags |= os.O_CREATE
		}
		dataFile, err := os.OpenFile(desc.DataPath, flags, 0666)
		if err != nil {
			return err
		}

		offsets, deletedIDs, err := scanVectorDataFile(desc.DataPath, ve.maxVectorSize)
		if err != nil {
			_ = dataFile.Close()
			return err
		}

		var index faiss.Index
		if idx == len(ve.manifest.Segments)-1 {
			// Training indexes always use a Flat IDMap in the active segment so
			// incremental inserts and RemoveIDs stay well-defined; configured
			// IVF/PQ indexes exist only on sealed segments when segmentation is on.
			index, err = buildVectorIndexFromDataFile(desc.DataPath, ve.maxVectorSize, ve.hotSegmentIndexDesc(), ve.metric)
			meta.State = SegmentStateHot
		} else {
			index, err = loadOrBuildVectorSegmentIndex(desc, ve.maxVectorSize, ve.indexType, ve.metric)
			meta.State = SegmentStateCold
		}
		if err != nil {
			_ = dataFile.Close()
			return err
		}
		if info, err := dataFile.Stat(); err == nil {
			meta.SizeBytes = info.Size()
		}
		ve.manifest.Segments[idx] = meta
		ve.segments = append(ve.segments, &vectorSegment{
			meta:             meta,
			dataFile:         dataFile,
			index:            index,
			fileOffsets:      offsets,
			deletedIDs:       deletedIDs,
			pendingUpsertIDs: make(map[int64]struct{}),
		})
	}

	return WriteSegmentManifest(ve.layout, ve.manifest)
}

func (ve *VectorEngineImpl) startBackgroundWorkers() {
	ve.backgroundWG.Add(2)
	go ve.indexBuildWorker()
	go ve.mergeWorker()
}

func (ve *VectorEngineImpl) InsertVector(id int64, vector []float32) error {
	if len(vector) != ve.maxVectorSize {
		return fmt.Errorf("vector length mismatch: expected %d", ve.maxVectorSize)
	}

	// 1) WAL first (if enabled)
	if ve.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := ve.wal.WriteEntry(string(key), string(float32ArrayToBytes(vector))); err != nil {
			return err
		}
	}

	// 2) Ingest (train if needed, add to FAISS, enqueue persistence), then mark committed.
	if err := ve.insertAfterWAL(id, vector); err != nil {
		return err
	}

	if ve.wal != nil {
		return ve.wal.MarkCommitted()
	}
	return nil
}

// insertAfterWAL performs the ingest without writing to WAL (used by InsertVector and WAL replay).
func (ve *VectorEngineImpl) insertAfterWAL(id int64, vector []float32) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	active := ve.activeSegmentLocked()
	if active == nil {
		return errors.New("no active vector segment")
	}

	replace := false
	if _, ok := active.pendingUpsertIDs[id]; ok {
		replace = true
	} else if _, ok := active.fileOffsets[id]; ok && !active.deletedIDs[id] {
		replace = true
	}
	if replace {
		sel, _ := faiss.NewIDSelectorBatch([]int64{id})
		_, _ = active.index.RemoveIDs(sel)
		sel.Delete()
	}

	if err := active.index.AddWithIDs(vector, []int64{id}); err != nil {
		return err
	}
	delete(active.deletedIDs, id)
	active.pendingUpsertIDs[id] = struct{}{}
	ve.enqueuePersist(id, vector)
	return nil
}

func (ve *VectorEngineImpl) SearchTopK(query []float32, k int) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}
	ve.lock.RLock()
	defer ve.lock.RUnlock()

	type pair struct {
		id  int64
		dst float32
	}
	shadowed := make(map[int64]struct{})
	candidates := make([]pair, 0, k*len(ve.segments))
	searchK := int64(maxInt(k*8, 32))

	for segIdx := len(ve.segments) - 1; segIdx >= 0; segIdx-- {
		segment := ve.segments[segIdx]
		dists, labels, err := segment.index.Search(query, searchK)
		if err != nil {
			return nil, nil, err
		}
		for i, label := range labels {
			if label < 0 {
				continue
			}
			if _, seen := shadowed[label]; seen {
				continue
			}
			if segment.deletedIDs[label] {
				continue
			}
			if _, exists := segment.fileOffsets[label]; !exists {
				continue
			}
			candidates = append(candidates, pair{id: label, dst: dists[i]})
		}
		for id := range segment.fileOffsets {
			shadowed[id] = struct{}{}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return betterDistanceForMetric(ve.metric, candidates[i].dst, candidates[j].dst)
	})
	if len(candidates) > k {
		candidates = candidates[:k]
	}
	ids := make([]int64, len(candidates))
	dists := make([]float32, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.id
		dists[i] = candidate.dst
	}
	return ids, dists, nil
}

func (ve *VectorEngineImpl) RangeSearch(query []float32, radius float32) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}

	ve.lock.RLock()
	defer ve.lock.RUnlock()

	type pair struct {
		id  int64
		dst float32
	}
	shadowed := make(map[int64]struct{})
	var ps []pair

	for segIdx := len(ve.segments) - 1; segIdx >= 0; segIdx-- {
		segment := ve.segments[segIdx]
		res, err := segment.index.RangeSearch(query, radius)
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
			if _, seen := shadowed[labels[i]]; seen {
				continue
			}
			if segment.deletedIDs[labels[i]] {
				continue
			}
			if _, exists := segment.fileOffsets[labels[i]]; !exists {
				continue
			}
			ps = append(ps, pair{id: labels[i], dst: dists[i]})
		}
		res.Delete()
		for id := range segment.fileOffsets {
			shadowed[id] = struct{}{}
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
	ve.lock.RLock()
	defer ve.lock.RUnlock()
	for idx := len(ve.segments) - 1; idx >= 0; idx-- {
		segment := ve.segments[idx]
		offset, ok := segment.fileOffsets[id]
		if !ok {
			continue
		}
		if segment.deletedIDs[id] {
			return nil, fmt.Errorf("ID %d not found", id)
		}
		recordSize := 8 + 4*ve.maxVectorSize
		buf := make([]byte, recordSize)
		if _, err := segment.dataFile.ReadAt(buf, offset); err != nil {
			return nil, fmt.Errorf("read vector at offset %d: %w", offset, err)
		}
		if len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker {
			return nil, fmt.Errorf("ID %d not found", id)
		}
		return bytesToFloat32Array(buf[8:])
	}
	return nil, fmt.Errorf("ID %d not found", id)
}

func (ve *VectorEngineImpl) RemoveVector(id int64) error {
	// HNSW index type does not support remove_ids in FAISS; reject deletion up front.
	if strings.HasPrefix(ve.indexType, "HNSW") {
		return ErrDeletionNotSupported
	}

	// 1) Persist tombstone in the same data file (like key_value_storage Delete: no extra file).
	if err := ve.appendTombstoneToDataFile(id); err != nil {
		return err
	}

	// 2) WAL - log the deletion (if enabled) for crash recovery
	if ve.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := ve.wal.WriteEntry(string(key), ""); err != nil {
			return err
		}
	}

	// 3) Remove from FAISS index and pendingAdd; fileOffsets[id] already points to tombstone
	if err := ve.removeAfterWAL(id); err != nil {
		return err
	}

	if ve.wal != nil {
		return ve.wal.MarkCommitted()
	}
	return nil
}

// removeAfterWAL performs the removal without writing to WAL (used by RemoveVector and WAL replay).
func (ve *VectorEngineImpl) removeAfterWAL(id int64) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	active := ve.activeSegmentLocked()
	if active == nil {
		return errors.New("no active vector segment")
	}

	sel, err := faiss.NewIDSelectorBatch([]int64{id})
	if err != nil {
		return fmt.Errorf("create ID selector: %w", err)
	}
	defer sel.Delete()

	_, err = active.index.RemoveIDs(sel)
	if err != nil {
		log.Printf("warning: RemoveIDs failed for index type %s: %v", ve.indexType, err)
	}
	active.deletedIDs[id] = true
	delete(active.pendingUpsertIDs, id)

	return nil
}

// appendTombstoneToDataFile appends a tombstone record for id to the data file (same format as
// normal records; first float32 of payload = tombstoneMarker) and updates fileOffsets[id].
// Same pattern as key_value_storage.Delete: persist delete on disk in the main file.
func (ve *VectorEngineImpl) appendTombstoneToDataFile(id int64) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	active := ve.activeSegmentLocked()
	if active == nil {
		return errors.New("no active vector segment")
	}

	pos, err := active.dataFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	recordSize := 8 + 4*ve.maxVectorSize
	buf := make([]byte, recordSize)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
	binary.LittleEndian.PutUint32(buf[8:12], tombstoneMarker)
	// rest is zero; same size as a normal vector record
	if _, err := active.dataFile.Write(buf); err != nil {
		return err
	}
	if err := active.dataFile.Sync(); err != nil {
		return err
	}
	active.fileOffsets[id] = pos
	active.deletedIDs[id] = true
	delete(active.pendingUpsertIDs, id)
	if info, err := active.dataFile.Stat(); err == nil {
		active.meta.SizeBytes = info.Size()
		ve.updateSegmentMetaLocked(active.meta)
	}
	return ve.rotateHotSegmentLocked()
}

func (ve *VectorEngineImpl) Close() error {
	ve.closeOnce.Do(func() {
		ve.maintenanceMu.Lock()
		ve.maintenanceClosed = true
		maintenance.UnregisterVectorFlush(ve)
		maintenance.UnregisterVectorCheckpoint(ve)
		ve.maintenanceMu.Unlock()

		// Final flush of any pending data-file writes
		ve.flushData(true)

		close(ve.backgroundStop)
		ve.backgroundWG.Wait()

		if ve.wal != nil {
			_ = ve.wal.Close()
		}
		ve.lock.Lock()
		for _, segment := range ve.segments {
			_ = segment.dataFile.Close()
			segment.index.Delete()
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
	for _, segment := range ve.segments {
		if err := segment.dataFile.Sync(); err != nil {
			return fmt.Errorf("sync data file: %w", err)
		}
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
		if len(entry) != 2 {
			continue
		}
		keyBytes := []byte(entry[0])
		if len(keyBytes) != 8 {
			return fmt.Errorf("invalid WAL key length: expected 8, got %d", len(keyBytes))
		}
		id := int64(binary.LittleEndian.Uint64(keyBytes))

		// Check if this is a deletion (empty value)
		if len(entry[1]) == 0 {
			// IMPORTANT: do not write to WAL here again — just remove.
			if err := ve.removeAfterWAL(id); err != nil {
				return fmt.Errorf("replay remove id=%d: %w", id, err)
			}
			continue
		}

		vec, err := bytesToFloat32Array([]byte(entry[1]))
		if err != nil {
			return fmt.Errorf("WAL decode: %w", err)
		}

		if err := ve.insertAfterWAL(id, vec); err != nil {
			return fmt.Errorf("replay insert id=%d: %w", id, err)
		}
	}

	ve.wal.Clear()
	return nil
}

func (ve *VectorEngineImpl) appendToDataFile(id int64, vector []float32) error {
	active := ve.activeSegmentLocked()
	if active == nil {
		return errors.New("no active vector segment")
	}

	pos, err := active.dataFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+len(vector)*4)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
	for i, v := range vector {
		binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(v))
	}
	if _, err := active.dataFile.Write(buf); err != nil {
		return err
	}
	active.fileOffsets[id] = pos
	delete(active.deletedIDs, id)
	return nil
}

func (ve *VectorEngineImpl) enqueuePersist(id int64, vec []float32) {
	ve.persistMu.Lock()
	ve.persistBuf = append(ve.persistBuf, struct {
		id  int64
		vec []float32
	}{id: id, vec: vec})
	ve.persistMu.Unlock()
	maintenance.MarkVectorFlushDirty(ve)
}

// MaintenanceFlush participates in the shared vector flush loop.
func (ve *VectorEngineImpl) MaintenanceFlush() {
	ve.maintenanceMu.RLock()
	defer ve.maintenanceMu.RUnlock()
	if ve.maintenanceClosed {
		return
	}
	ve.flushData(false)
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

func (ve *VectorEngineImpl) flushData(force bool) {
	ve.persistMu.Lock()
	buf := ve.persistBuf
	if !force && len(buf) == 0 {
		ve.persistMu.Unlock()
		return
	}
	ve.persistBuf = nil
	ve.persistMu.Unlock()

	if len(buf) == 0 {
		return
	}

	// Hold the write lock only for the in-memory fileOffsets updates and
	// the raw file writes. Release before fsync so searches are not blocked
	// for the full duration of the (potentially slow) syscall.
	ve.lock.Lock()
	active := ve.activeSegmentLocked()
	if active == nil {
		ve.lock.Unlock()
		return
	}
	for _, it := range buf {
		if err := ve.appendToDataFile(it.id, it.vec); err != nil {
			log.Printf("appendToDataFile failed for id=%d: %v", it.id, err)
		} else {
			delete(active.pendingUpsertIDs, it.id)
		}
	}
	if info, err := active.dataFile.Stat(); err == nil {
		active.meta.SizeBytes = info.Size()
		ve.updateSegmentMetaLocked(active.meta)
	}
	rotateErr := ve.rotateHotSegmentLocked()
	ve.lock.Unlock()

	if err := active.dataFile.Sync(); err != nil {
		log.Printf("data file sync failed: %v", err)
	}
	if rotateErr != nil {
		log.Printf("hot segment rotation failed: %v", rotateErr)
	}
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

func (ve *VectorEngineImpl) activeSegmentLocked() *vectorSegment {
	for _, segment := range ve.segments {
		if segment.meta.ID == ve.manifest.ActiveSegmentID {
			return segment
		}
	}
	return nil
}

func (ve *VectorEngineImpl) updateSegmentMetaLocked(meta SegmentMeta) {
	for idx := range ve.segments {
		if ve.segments[idx].meta.ID == meta.ID {
			ve.segments[idx].meta = meta
			break
		}
	}
	for idx := range ve.manifest.Segments {
		if ve.manifest.Segments[idx].ID == meta.ID {
			ve.manifest.Segments[idx] = meta
			break
		}
	}
}

func (ve *VectorEngineImpl) rotateHotSegmentLocked() error {
	if !ve.vectorSegmentationEnabled {
		return nil
	}
	active := ve.activeSegmentLocked()
	if active == nil || active.meta.SizeBytes < ve.settings.SegmentRolloverBytes {
		return nil
	}

	active.meta.State = SegmentStateSealed
	active.meta.SealedAtUnixNs = time.Now().UnixNano()
	ve.updateSegmentMetaLocked(active.meta)

	newID := ve.manifest.NextSegmentID
	ve.manifest.NextSegmentID++
	newMeta := SegmentMeta{
		ID:              newID,
		State:           SegmentStateHot,
		DataFile:        ve.layout.DataFileName(newID),
		IndexFile:       ve.layout.IndexFileName(newID),
		CreatedAtUnixNs: time.Now().UnixNano(),
	}
	index, err := faiss.IndexFactory(ve.maxVectorSize, "IDMap,"+ve.hotSegmentIndexDesc(), ve.metric)
	if err != nil {
		return err
	}
	dataFile, err := os.OpenFile(ve.layout.DataPath(newID), os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		index.Delete()
		return err
	}

	ve.manifest.ActiveSegmentID = newID
	ve.manifest.Segments = append(ve.manifest.Segments, newMeta)
	ve.segments = append(ve.segments, &vectorSegment{
		meta:             newMeta,
		dataFile:         dataFile,
		index:            index,
		fileOffsets:      make(map[int64]int64),
		deletedIDs:       make(map[int64]bool),
		pendingUpsertIDs: make(map[int64]struct{}),
	})
	if err := WriteSegmentManifest(ve.layout, ve.manifest); err != nil {
		return err
	}

	ve.enqueueIndexBuildLocked(active.meta.ID)
	ve.scheduleMergeCheckLocked()
	return nil
}

func (ve *VectorEngineImpl) indexBuildWorker() {
	defer ve.backgroundWG.Done()
	for {
		select {
		case <-ve.backgroundStop:
			return
		case id := <-ve.indexBuildQueue:
			ve.buildIndexForSegment(id)
		}
	}
}

func (ve *VectorEngineImpl) mergeWorker() {
	defer ve.backgroundWG.Done()
	for {
		select {
		case <-ve.backgroundStop:
			return
		case <-ve.mergeQueue:
			for ve.tryMergeOldestColdSegments() {
			}
		}
	}
}

func (ve *VectorEngineImpl) buildIndexForSegment(id int64) {
	ve.lock.Lock()
	segment := ve.segmentByIDLocked(id)
	if segment == nil || id == ve.manifest.ActiveSegmentID {
		ve.lock.Unlock()
		return
	}
	segment.meta.State = SegmentStateIndexing
	ve.updateSegmentMetaLocked(segment.meta)
	desc := ve.layout.Descriptor(segment.meta)
	_ = WriteSegmentManifest(ve.layout, ve.manifest)
	ve.lock.Unlock()

	if err := buildSealedVectorIndex(desc.DataPath, desc.IndexPath, ve.maxVectorSize, ve.indexType, ve.metric); err != nil {
		log.Printf("build vector segment index failed for segment %d: %v", id, err)
		ve.lock.Lock()
		if segment := ve.segmentByIDLocked(id); segment != nil {
			segment.meta.State = SegmentStateSealed
			ve.updateSegmentMetaLocked(segment.meta)
			_ = WriteSegmentManifest(ve.layout, ve.manifest)
		}
		ve.lock.Unlock()
		return
	}

	ve.lock.Lock()
	if segment := ve.segmentByIDLocked(id); segment != nil {
		segment.meta.State = SegmentStateCold
		if info, err := os.Stat(desc.DataPath); err == nil {
			segment.meta.SizeBytes = info.Size()
		}
		ve.updateSegmentMetaLocked(segment.meta)
		_ = WriteSegmentManifest(ve.layout, ve.manifest)
		ve.scheduleMergeCheckLocked()
	}
	ve.lock.Unlock()
}

func (ve *VectorEngineImpl) tryMergeOldestColdSegments() bool {
	ve.lock.Lock()
	if !ve.vectorSegmentationEnabled {
		ve.lock.Unlock()
		return false
	}
	if len(ve.segments) <= ve.settings.MaxSegmentsBeforeMerge {
		ve.lock.Unlock()
		return false
	}

	var first, second *vectorSegment
	for _, segment := range ve.segments {
		if segment.meta.ID == ve.manifest.ActiveSegmentID {
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
		ve.lock.Unlock()
		return false
	}

	first.meta.State = SegmentStateMerging
	second.meta.State = SegmentStateMerging
	ve.updateSegmentMetaLocked(first.meta)
	ve.updateSegmentMetaLocked(second.meta)
	newID := second.meta.ID
	firstDesc := ve.layout.Descriptor(first.meta)
	secondDesc := ve.layout.Descriptor(second.meta)
	_ = WriteSegmentManifest(ve.layout, ve.manifest)
	ve.lock.Unlock()

	meta, mergedIndex, dataFile, offsets, deletedIDs, err := ve.mergeSegments(newID, firstDesc, secondDesc)
	if err != nil {
		log.Printf("merge vector segments %d and %d failed: %v", first.meta.ID, second.meta.ID, err)
		ve.lock.Lock()
		if segment := ve.segmentByIDLocked(first.meta.ID); segment != nil {
			segment.meta.State = SegmentStateCold
			ve.updateSegmentMetaLocked(segment.meta)
		}
		if segment := ve.segmentByIDLocked(second.meta.ID); segment != nil {
			segment.meta.State = SegmentStateCold
			ve.updateSegmentMetaLocked(segment.meta)
		}
		_ = WriteSegmentManifest(ve.layout, ve.manifest)
		ve.lock.Unlock()
		return false
	}

	ve.lock.Lock()
	defer ve.lock.Unlock()
	firstIdx := ve.segmentIndexByIDLocked(first.meta.ID)
	secondIdx := ve.segmentIndexByIDLocked(second.meta.ID)
	if firstIdx < 0 || secondIdx <= firstIdx {
		_ = dataFile.Close()
		mergedIndex.Delete()
		return false
	}

	ve.segments[firstIdx].index.Delete()
	ve.segments[secondIdx].index.Delete()
	_ = ve.segments[firstIdx].dataFile.Close()
	_ = ve.segments[secondIdx].dataFile.Close()
	_ = os.Remove(firstDesc.DataPath)
	_ = os.Remove(firstDesc.IndexPath)

	mergedSegment := &vectorSegment{
		meta:             meta,
		dataFile:         dataFile,
		index:            mergedIndex,
		fileOffsets:      offsets,
		deletedIDs:       deletedIDs,
		pendingUpsertIDs: make(map[int64]struct{}),
	}
	ve.segments = append(append([]*vectorSegment{}, mergedSegment), ve.segments[secondIdx+1:]...)
	ve.manifest.Segments = append(append([]SegmentMeta{}, meta), ve.manifest.Segments[secondIdx+1:]...)
	_ = WriteSegmentManifest(ve.layout, ve.manifest)
	return len(ve.segments) > ve.settings.MaxSegmentsBeforeMerge
}

func (ve *VectorEngineImpl) mergeSegments(newID int64, firstDesc, secondDesc SegmentDescriptor) (SegmentMeta, faiss.Index, *os.File, map[int64]int64, map[int64]bool, error) {
	records, err := collectLatestVectorRecords(ve.maxVectorSize, firstDesc.DataPath, secondDesc.DataPath)
	if err != nil {
		return SegmentMeta{}, nil, nil, nil, nil, err
	}

	dataPath := ve.layout.DataPath(newID)
	indexPath := ve.layout.IndexPath(newID)
	if err := writeMergedVectorDataFile(dataPath, records); err != nil {
		return SegmentMeta{}, nil, nil, nil, nil, err
	}
	if err := buildSealedVectorIndex(dataPath, indexPath, ve.maxVectorSize, ve.indexType, ve.metric); err != nil {
		return SegmentMeta{}, nil, nil, nil, nil, err
	}
	index, err := faiss.ReadIndex(indexPath, 0)
	if err != nil {
		return SegmentMeta{}, nil, nil, nil, nil, err
	}
	dataFile, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		index.Delete()
		return SegmentMeta{}, nil, nil, nil, nil, err
	}
	offsets, deletedIDs, err := scanVectorDataFile(dataPath, ve.maxVectorSize)
	if err != nil {
		index.Delete()
		_ = dataFile.Close()
		return SegmentMeta{}, nil, nil, nil, nil, err
	}

	meta := SegmentMeta{
		ID:              newID,
		State:           SegmentStateCold,
		DataFile:        filepath.Base(dataPath),
		IndexFile:       filepath.Base(indexPath),
		CreatedAtUnixNs: time.Now().UnixNano(),
	}
	if info, err := dataFile.Stat(); err == nil {
		meta.SizeBytes = info.Size()
	}
	return meta, index, dataFile, offsets, deletedIDs, nil
}

func (ve *VectorEngineImpl) enqueueIndexBuildLocked(id int64) {
	select {
	case ve.indexBuildQueue <- id:
	default:
		go func() {
			select {
			case ve.indexBuildQueue <- id:
			case <-ve.backgroundStop:
			}
		}()
	}
}

func (ve *VectorEngineImpl) scheduleMergeCheckLocked() {
	select {
	case ve.mergeQueue <- struct{}{}:
	default:
	}
}

func (ve *VectorEngineImpl) segmentByIDLocked(id int64) *vectorSegment {
	for _, segment := range ve.segments {
		if segment.meta.ID == id {
			return segment
		}
	}
	return nil
}

func (ve *VectorEngineImpl) segmentIndexByIDLocked(id int64) int {
	for idx, segment := range ve.segments {
		if segment.meta.ID == id {
			return idx
		}
	}
	return -1
}

func loadOrBuildVectorSegmentIndex(desc SegmentDescriptor, dimension int, indexDesc string, metric int) (faiss.Index, error) {
	index, err := faiss.ReadIndex(desc.IndexPath, 0)
	if err == nil {
		return index, nil
	}
	if err := buildSealedVectorIndex(desc.DataPath, desc.IndexPath, dimension, indexDesc, metric); err != nil {
		return nil, err
	}
	return faiss.ReadIndex(desc.IndexPath, 0)
}

func buildSealedVectorIndex(dataPath, indexPath string, dimension int, indexDesc string, metric int) error {
	if _, err := RebuildVectorIndex(dataPath, indexPath, dimension, indexDesc, metric); err == nil {
		return nil
	} else if !isVectorRebuildTrainingError(err) {
		return err
	}
	return func() error {
		_, err := RebuildVectorIndex(dataPath, indexPath, dimension, "Flat", metric)
		return err
	}()
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
		deletedIDs[id] = len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (ve *VectorEngineImpl) hotSegmentIndexDesc() string {
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
