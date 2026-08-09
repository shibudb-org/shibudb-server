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

	"github.com/DataIntelligenceCrew/go-faiss"
	"github.com/shibudb.org/shibudb-server/internal/maintenance"
	"github.com/shibudb.org/shibudb-server/internal/wal"
)

// tombstoneMarker is the first float32 of a deleted vector record.
const tombstoneMarker = uint32(0x7FC00000)

var ErrDeletionNotSupported = errors.New("vector deletion not supported for HNSW index type")
var ErrVectorEngineClosed = errors.New("vector engine is closed")
var ErrVectorRecoveryRequired = errors.New("vector engine requires restart for WAL recovery")

type VectorEngineImpl struct {
	wal           *wal.WAL
	maxVectorSize int
	indexType     string
	metric        int

	dataPath      string
	indexPath     string
	indexMetaPath string
	dataFile      *os.File
	index         faiss.Index
	indexMeta     VectorIndexMeta
	fileOffsets   map[int64]int64
	deletedIDs    map[int64]bool
	// Some FAISS indexes fail hard when asked to remove an ID they do not
	// contain. Track buffered additions independently from durable offsets.
	pendingUpsertIDs map[int64]struct{}

	closeOnce         sync.Once
	checkpointMu      sync.Mutex
	mutationMu        sync.Mutex
	maintenanceMu     sync.RWMutex
	maintenanceClosed bool
	lock              sync.RWMutex
	closed            bool
	recoveryRequired  bool

	persistMu  sync.Mutex
	persistBuf []struct {
		id  int64
		vec []float32
	}

	promotionQueue  chan struct{}
	promotionQueued bool
	promotionHook   func()
	promotionError  func() error
	backgroundStop  chan struct{}
	backgroundWG    sync.WaitGroup
}

var _ VectorEngine = (*VectorEngineImpl)(nil)

func NewVectorEngine(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int, enableWAL bool) (*VectorEngineImpl, error) {
	indexDesc = normalizeVectorIndexDesc(indexDesc)
	if indexDesc == "Flat" {
		return nil, errors.New(`bare "Flat" is not supported by VectorEngineImpl; use FlatMeta storage`)
	}
	legacyManifestPath := filepath.Join(filepath.Dir(dataPath), "vector_segments.manifest.json")
	if _, err := os.Stat(legacyManifestPath); err == nil {
		return nil, fmt.Errorf("legacy segmented vector layout is not supported: remove or migrate %s", legacyManifestPath)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("check legacy vector layout: %w", err)
	}
	probe, err := newVectorFaissIndex(maxVectorSize, indexDesc, metric)
	if err != nil {
		return nil, fmt.Errorf("invalid FAISS index descriptor %q for dimension %d: %w", indexDesc, maxVectorSize, err)
	}
	probe.Delete()

	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return nil, err
	}
	dataFile, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}
	offsets, deletedIDs, err := scanVectorDataFile(dataPath, maxVectorSize)
	if err != nil {
		_ = dataFile.Close()
		return nil, err
	}
	metaPath := vectorIndexMetaPath(indexPath)
	meta, err := loadVectorIndexMeta(metaPath)
	if err != nil {
		_ = dataFile.Close()
		return nil, err
	}

	e := &VectorEngineImpl{
		maxVectorSize:    maxVectorSize,
		indexType:        indexDesc,
		metric:           metric,
		dataPath:         dataPath,
		indexPath:        indexPath,
		indexMetaPath:    metaPath,
		dataFile:         dataFile,
		indexMeta:        meta,
		fileOffsets:      offsets,
		deletedIDs:       deletedIDs,
		pendingUpsertIDs: make(map[int64]struct{}),
		promotionQueue:   make(chan struct{}, 1),
		backgroundStop:   make(chan struct{}),
	}
	e.index, e.indexMeta, err = e.loadOrBuildIndex()
	if err != nil {
		_ = dataFile.Close()
		return nil, err
	}

	if enableWAL {
		e.wal, err = wal.OpenWAL(walPath)
		if err != nil {
			e.index.Delete()
			_ = dataFile.Close()
			return nil, fmt.Errorf("open WAL: %w", err)
		}
		if err := e.replayWAL(); err != nil {
			e.index.Delete()
			_ = dataFile.Close()
			_ = e.wal.Close()
			return nil, fmt.Errorf("WAL replay failed: %w", err)
		}
		if err := e.flushDataLocked(true); err != nil {
			e.index.Delete()
			_ = dataFile.Close()
			_ = e.wal.Close()
			return nil, fmt.Errorf("persist WAL replay: %w", err)
		}
		if err := e.wal.Clear(); err != nil {
			e.index.Delete()
			_ = dataFile.Close()
			_ = e.wal.Close()
			return nil, fmt.Errorf("clear replayed WAL: %w", err)
		}
	}

	e.backgroundWG.Add(1)
	go e.promotionWorker()
	e.scheduleTrainingPromotion()
	return e, nil
}

func normalizeVectorIndexDesc(indexDesc string) string {
	parts := strings.Split(indexDesc, ",")
	for idx := range parts {
		parts[idx] = strings.TrimSpace(parts[idx])
	}
	return strings.Join(parts, ",")
}

func (ve *VectorEngineImpl) newIndexMeta(mode VectorIndexMode, dataBytes int64) (VectorIndexMeta, error) {
	meta := VectorIndexMeta{
		Version:        vectorIndexMetaVersion,
		Mode:           mode,
		IndexDataBytes: dataBytes,
		IndexType:      ve.indexType,
		Dimension:      ve.maxVectorSize,
		Metric:         ve.metric,
	}
	if mode == VectorIndexModeTrained && requiredTrainCountForIndex(ve.indexType) > 0 {
		checksum, err := fileSHA256(ve.indexPath)
		if err != nil {
			return VectorIndexMeta{}, fmt.Errorf("checksum trained vector index: %w", err)
		}
		meta.IndexSHA256 = checksum
		dataChecksum, err := filePrefixSHA256(ve.dataPath, dataBytes)
		if err != nil {
			return VectorIndexMeta{}, fmt.Errorf("checksum trained vector data: %w", err)
		}
		meta.DataSHA256 = dataChecksum
	}
	return meta, nil
}

func (ve *VectorEngineImpl) persistedIndexMatches(meta VectorIndexMeta) bool {
	if meta.IndexType != ve.indexType ||
		meta.Dimension != ve.maxVectorSize ||
		meta.Metric != ve.metric ||
		meta.IndexSHA256 == "" ||
		meta.DataSHA256 == "" {
		return false
	}
	indexChecksum, err := fileSHA256(ve.indexPath)
	if err != nil || indexChecksum != meta.IndexSHA256 {
		return false
	}
	dataChecksum, err := filePrefixSHA256(ve.dataPath, meta.IndexDataBytes)
	return err == nil && dataChecksum == meta.DataSHA256
}

func (ve *VectorEngineImpl) loadOrBuildIndex() (faiss.Index, VectorIndexMeta, error) {
	info, err := ve.dataFile.Stat()
	if err != nil {
		return nil, ve.indexMeta, err
	}
	dataBytes := info.Size()
	meta := ve.indexMeta
	required := requiredTrainCountForIndex(ve.indexType)

	if required > 0 &&
		meta.Mode == VectorIndexModeTrained &&
		meta.IndexDataBytes <= dataBytes &&
		ve.persistedIndexMatches(meta) {
		index, readErr := faiss.ReadIndex(ve.indexPath, 0)
		if readErr == nil && index.IsTrained() {
			if meta.IndexDataBytes == dataBytes {
				return index, meta, nil
			}
			if !strings.HasPrefix(ve.indexType, "HNSW") {
				if tailErr := applyVectorDataTail(index, ve.dataPath, meta.IndexDataBytes, ve.maxVectorSize); tailErr == nil {
					return index, meta, nil
				}
			}
			index.Delete()
		}
	}

	trained := required == 0 || liveVectorCount(ve.fileOffsets, ve.deletedIDs) >= required
	buildDesc := ve.indexType
	if !trained {
		buildDesc = "Flat"
	}
	index, err := buildVectorIndexFromDataFile(ve.dataPath, ve.maxVectorSize, buildDesc, ve.metric)
	if err != nil {
		return nil, meta, err
	}
	if required == 0 {
		meta, err = ve.newIndexMeta(VectorIndexModeTrained, 0)
		if err != nil {
			index.Delete()
			return nil, meta, err
		}
		return index, meta, nil
	}
	if !trained {
		meta, err = ve.newIndexMeta(VectorIndexModeFallback, 0)
		if err != nil {
			index.Delete()
			return nil, meta, err
		}
		_ = os.Remove(ve.indexPath)
		if err := writeVectorIndexMeta(ve.indexMetaPath, meta); err != nil {
			index.Delete()
			return nil, meta, err
		}
		return index, meta, nil
	}
	if err := writeFaissIndex(index, ve.indexPath); err != nil {
		index.Delete()
		return nil, meta, err
	}
	meta, err = ve.newIndexMeta(VectorIndexModeTrained, dataBytes)
	if err != nil {
		index.Delete()
		return nil, meta, err
	}
	if err := writeVectorIndexMeta(ve.indexMetaPath, meta); err != nil {
		index.Delete()
		return nil, meta, err
	}
	return index, meta, nil
}

func liveVectorCount(offsets map[int64]int64, deletedIDs map[int64]bool) int {
	count := 0
	for id := range offsets {
		if !deletedIDs[id] {
			count++
		}
	}
	return count
}

func applyVectorDataTail(index faiss.Index, dataPath string, start int64, dimension int) error {
	recordSize := int64(8 + 4*dimension)
	if start < 0 || start%recordSize != 0 {
		return fmt.Errorf("invalid vector index watermark %d", start)
	}
	file, err := os.Open(dataPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, recordSize)
	for {
		_, err := io.ReadFull(file, buf)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read vector index tail: %w", err)
		}
		id := int64(binary.LittleEndian.Uint64(buf[:8]))
		selector, err := faiss.NewIDSelectorBatch([]int64{id})
		if err != nil {
			return err
		}
		_, removeErr := index.RemoveIDs(selector)
		selector.Delete()
		if removeErr != nil {
			return removeErr
		}
		if binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker {
			continue
		}
		vector, err := bytesToFloat32Array(buf[8:])
		if err != nil {
			return err
		}
		if err := index.AddWithIDs(vector, []int64{id}); err != nil {
			return err
		}
	}
}

func (ve *VectorEngineImpl) scheduleTrainingPromotion() {
	required := requiredTrainCountForIndex(ve.indexType)
	if required == 0 {
		return
	}
	ve.lock.Lock()
	if ve.closed ||
		ve.indexMeta.Mode == VectorIndexModeTrained ||
		ve.promotionQueued ||
		liveVectorCount(ve.fileOffsets, ve.deletedIDs) < required {
		ve.lock.Unlock()
		return
	}
	ve.promotionQueued = true
	ve.lock.Unlock()
	select {
	case ve.promotionQueue <- struct{}{}:
	case <-ve.backgroundStop:
		ve.finishTrainingPromotion()
	default:
	}
}

func (ve *VectorEngineImpl) promotionWorker() {
	defer ve.backgroundWG.Done()
	for {
		select {
		case <-ve.backgroundStop:
			return
		case <-ve.promotionQueue:
			ve.promoteTrainingIndex()
		}
	}
}

func (ve *VectorEngineImpl) promoteTrainingIndex() {
	ve.mutationMu.Lock()
	defer ve.mutationMu.Unlock()
	ve.lock.RLock()
	closed := ve.closed
	ve.lock.RUnlock()
	if closed {
		ve.finishTrainingPromotion()
		return
	}
	if err := ve.flushDataLocked(true); err != nil {
		log.Printf("flush vector data before promotion failed: %v", err)
		ve.finishTrainingPromotion()
		return
	}

	ve.lock.RLock()
	required := requiredTrainCountForIndex(ve.indexType)
	if ve.indexMeta.Mode == VectorIndexModeTrained ||
		liveVectorCount(ve.fileOffsets, ve.deletedIDs) < required {
		ve.lock.RUnlock()
		ve.finishTrainingPromotion()
		return
	}
	ve.lock.RUnlock()

	if ve.promotionHook != nil {
		ve.promotionHook()
	}
	if ve.promotionError != nil {
		if err := ve.promotionError(); err != nil {
			log.Printf("promote vector index %q failed: %v", ve.indexType, err)
			ve.finishTrainingPromotion()
			return
		}
	}
	candidate, err := buildVectorIndexFromDataFile(ve.dataPath, ve.maxVectorSize, ve.indexType, ve.metric)
	if err != nil {
		log.Printf("promote vector index %q failed: %v", ve.indexType, err)
		ve.finishTrainingPromotion()
		return
	}
	info, err := os.Stat(ve.dataPath)
	if err != nil {
		candidate.Delete()
		log.Printf("stat vector data before index promotion failed: %v", err)
		ve.finishTrainingPromotion()
		return
	}
	if err := writeFaissIndex(candidate, ve.indexPath); err != nil {
		candidate.Delete()
		log.Printf("persist promoted vector index %q failed: %v", ve.indexType, err)
		ve.finishTrainingPromotion()
		return
	}
	meta, err := ve.newIndexMeta(VectorIndexModeTrained, info.Size())
	if err != nil {
		candidate.Delete()
		log.Printf("identify promoted vector index failed: %v", err)
		ve.finishTrainingPromotion()
		return
	}
	if err := writeVectorIndexMeta(ve.indexMetaPath, meta); err != nil {
		candidate.Delete()
		log.Printf("persist vector promotion metadata failed: %v", err)
		ve.finishTrainingPromotion()
		return
	}

	ve.lock.Lock()
	oldIndex := ve.index
	ve.index = candidate
	ve.indexMeta = meta
	ve.promotionQueued = false
	ve.lock.Unlock()
	oldIndex.Delete()
}

func (ve *VectorEngineImpl) finishTrainingPromotion() {
	ve.lock.Lock()
	ve.promotionQueued = false
	ve.lock.Unlock()
}

func (ve *VectorEngineImpl) InsertVector(id int64, vector []float32) error {
	if len(vector) != ve.maxVectorSize {
		return fmt.Errorf("vector length mismatch: expected %d", ve.maxVectorSize)
	}
	ve.mutationMu.Lock()
	defer ve.mutationMu.Unlock()
	ve.lock.RLock()
	closed := ve.closed
	recoveryRequired := ve.recoveryRequired
	ve.lock.RUnlock()
	if closed {
		return ErrVectorEngineClosed
	}
	if recoveryRequired {
		return ErrVectorRecoveryRequired
	}
	if ve.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := ve.wal.WriteEntry(string(key), string(float32ArrayToBytes(vector))); err != nil {
			return ve.requireWALRecovery(err)
		}
	}
	if err := ve.insertAfterWAL(id, vector); err != nil {
		return ve.requireWALRecovery(err)
	}
	if ve.wal != nil {
		if err := ve.flushDataLocked(true); err != nil {
			return ve.requireWALRecovery(err)
		}
		if err := ve.wal.MarkCommitted(); err != nil {
			return ve.requireWALRecovery(err)
		}
	}
	return nil
}

func (ve *VectorEngineImpl) requireWALRecovery(err error) error {
	if ve.wal == nil {
		return err
	}
	ve.lock.Lock()
	ve.recoveryRequired = true
	ve.lock.Unlock()
	return fmt.Errorf("%w: %v", ErrVectorRecoveryRequired, err)
}

func (ve *VectorEngineImpl) insertAfterWAL(id int64, vector []float32) error {
	ve.lock.Lock()
	replace := false
	if _, ok := ve.pendingUpsertIDs[id]; ok {
		replace = true
	} else if _, ok := ve.fileOffsets[id]; ok && !ve.deletedIDs[id] {
		replace = true
	}
	if replace && strings.HasPrefix(ve.indexType, "HNSW") {
		ve.lock.Unlock()
		return ve.replaceHNSWVector(id, vector)
	}
	if replace {
		selector, err := faiss.NewIDSelectorBatch([]int64{id})
		if err != nil {
			ve.lock.Unlock()
			return err
		}
		_, err = ve.index.RemoveIDs(selector)
		selector.Delete()
		if err != nil {
			if rebuildErr := ve.rebuildIndexFromDataLocked(); rebuildErr != nil {
				err = fmt.Errorf("%w (rollback rebuild failed: %v)", err, rebuildErr)
			}
			ve.lock.Unlock()
			return fmt.Errorf("remove old vector %d before update: %w", id, err)
		}
	}
	if err := ve.index.AddWithIDs(vector, []int64{id}); err != nil {
		if replace {
			if rebuildErr := ve.rebuildIndexFromDataLocked(); rebuildErr != nil {
				err = fmt.Errorf("%w (rollback rebuild failed: %v)", err, rebuildErr)
			}
		}
		ve.lock.Unlock()
		return err
	}
	delete(ve.deletedIDs, id)
	ve.pendingUpsertIDs[id] = struct{}{}
	ve.enqueuePersist(id, vector)
	ve.lock.Unlock()
	return nil
}

func (ve *VectorEngineImpl) replaceHNSWVector(id int64, vector []float32) error {
	if err := ve.flushDataLocked(true); err != nil {
		return err
	}
	ve.lock.Lock()
	info, err := ve.dataFile.Stat()
	if err != nil {
		ve.lock.Unlock()
		return err
	}
	originalSize := info.Size()
	originalOffset, hadOffset := ve.fileOffsets[id]
	wasDeleted := ve.deletedIDs[id]
	if err := ve.appendToDataFile(id, vector); err != nil {
		if rollbackErr := ve.rollbackHNSWAppendLocked(id, originalOffset, hadOffset, wasDeleted, originalSize); rollbackErr != nil {
			err = fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		ve.lock.Unlock()
		return err
	}
	if err := ve.dataFile.Sync(); err != nil {
		if rollbackErr := ve.rollbackHNSWAppendLocked(id, originalOffset, hadOffset, wasDeleted, originalSize); rollbackErr != nil {
			err = fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		ve.lock.Unlock()
		return err
	}
	buildDesc := ve.indexType
	if ve.indexMeta.Mode != VectorIndexModeTrained {
		buildDesc = "Flat"
	}
	ve.lock.Unlock()

	replacement, err := buildVectorIndexFromDataFile(ve.dataPath, ve.maxVectorSize, buildDesc, ve.metric)
	if err != nil {
		ve.lock.Lock()
		if rollbackErr := ve.rollbackHNSWAppendLocked(id, originalOffset, hadOffset, wasDeleted, originalSize); rollbackErr != nil {
			err = fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		ve.lock.Unlock()
		return fmt.Errorf("rebuild HNSW index for vector update: %w", err)
	}
	ve.lock.Lock()
	oldIndex := ve.index
	ve.index = replacement
	delete(ve.deletedIDs, id)
	delete(ve.pendingUpsertIDs, id)
	ve.lock.Unlock()
	oldIndex.Delete()
	maintenance.MarkVectorCheckpointDirty(ve)
	return nil
}

func (ve *VectorEngineImpl) rollbackHNSWAppendLocked(id int64, originalOffset int64, hadOffset, wasDeleted bool, originalSize int64) error {
	if err := ve.dataFile.Truncate(originalSize); err != nil {
		ve.closed = true
		return err
	}
	if _, err := ve.dataFile.Seek(0, io.SeekEnd); err != nil {
		ve.closed = true
		return err
	}
	if err := ve.dataFile.Sync(); err != nil {
		ve.closed = true
		return err
	}
	if hadOffset {
		ve.fileOffsets[id] = originalOffset
	} else {
		delete(ve.fileOffsets, id)
	}
	if wasDeleted {
		ve.deletedIDs[id] = true
	} else {
		delete(ve.deletedIDs, id)
	}
	return nil
}

func (ve *VectorEngineImpl) SearchTopK(query []float32, k int) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}
	ve.lock.RLock()
	defer ve.lock.RUnlock()
	if ve.closed {
		return nil, nil, ErrVectorEngineClosed
	}
	if ve.recoveryRequired {
		return nil, nil, ErrVectorRecoveryRequired
	}
	searchK := int64(maxInt(k*8, 32))
	dists, labels, err := ve.index.Search(query, searchK)
	if err != nil {
		return nil, nil, err
	}
	type pair struct {
		id  int64
		dst float32
	}
	candidates := make([]pair, 0, k)
	for idx, id := range labels {
		if id < 0 || ve.deletedIDs[id] {
			continue
		}
		if _, exists := ve.fileOffsets[id]; !exists {
			continue
		}
		candidates = append(candidates, pair{id: id, dst: dists[idx]})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return betterDistanceForMetric(ve.metric, candidates[i].dst, candidates[j].dst)
	})
	if len(candidates) > k {
		candidates = candidates[:k]
	}
	ids := make([]int64, len(candidates))
	outDists := make([]float32, len(candidates))
	for idx, candidate := range candidates {
		ids[idx], outDists[idx] = candidate.id, candidate.dst
	}
	return ids, outDists, nil
}

func (ve *VectorEngineImpl) RangeSearch(query []float32, radius float32) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}
	ve.lock.RLock()
	defer ve.lock.RUnlock()
	if ve.closed {
		return nil, nil, ErrVectorEngineClosed
	}
	if ve.recoveryRequired {
		return nil, nil, ErrVectorRecoveryRequired
	}
	result, err := ve.index.RangeSearch(query, radius)
	if err != nil {
		return nil, nil, err
	}
	defer result.Delete()
	labels, dists := result.Labels()
	lims := result.Lims()
	if len(lims) != 2 {
		return nil, nil, fmt.Errorf("expected 1 query, got %d", len(lims)-1)
	}
	type pair struct {
		id  int64
		dst float32
	}
	pairs := make([]pair, 0, lims[1]-lims[0])
	for idx := int(lims[0]); idx < int(lims[1]); idx++ {
		id := labels[idx]
		if ve.deletedIDs[id] {
			continue
		}
		if _, exists := ve.fileOffsets[id]; !exists {
			continue
		}
		pairs = append(pairs, pair{id: id, dst: dists[idx]})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return betterDistanceForMetric(ve.metric, pairs[i].dst, pairs[j].dst)
	})
	ids := make([]int64, len(pairs))
	outDists := make([]float32, len(pairs))
	for idx, pair := range pairs {
		ids[idx], outDists[idx] = pair.id, pair.dst
	}
	return ids, outDists, nil
}

func (ve *VectorEngineImpl) GetVectorByID(id int64) ([]float32, error) {
	ve.lock.RLock()
	defer ve.lock.RUnlock()
	if ve.closed {
		return nil, ErrVectorEngineClosed
	}
	if ve.recoveryRequired {
		return nil, ErrVectorRecoveryRequired
	}
	offset, ok := ve.fileOffsets[id]
	if !ok || ve.deletedIDs[id] {
		return nil, fmt.Errorf("ID %d not found", id)
	}
	_, vector, deleted, err := readVectorRecordAt(ve.dataFile, offset, ve.maxVectorSize)
	if err != nil {
		return nil, err
	}
	if deleted {
		return nil, fmt.Errorf("ID %d not found", id)
	}
	return vector, nil
}

func (ve *VectorEngineImpl) RemoveVector(id int64) error {
	ve.mutationMu.Lock()
	defer ve.mutationMu.Unlock()
	ve.lock.RLock()
	closed := ve.closed
	recoveryRequired := ve.recoveryRequired
	ve.lock.RUnlock()
	if closed {
		return ErrVectorEngineClosed
	}
	if recoveryRequired {
		return ErrVectorRecoveryRequired
	}
	if strings.HasPrefix(ve.indexType, "HNSW") {
		return ErrDeletionNotSupported
	}
	if err := ve.flushDataLocked(true); err != nil {
		return err
	}
	if ve.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		if err := ve.wal.WriteDelete(string(key)); err != nil {
			return ve.requireWALRecovery(err)
		}
	}
	if err := ve.removeAfterWAL(id); err != nil {
		return ve.requireWALRecovery(err)
	}
	if ve.wal != nil {
		if err := ve.wal.MarkCommitted(); err != nil {
			return ve.requireWALRecovery(err)
		}
	}
	return nil
}

func (ve *VectorEngineImpl) removeAfterWAL(id int64) error {
	ve.lock.RLock()
	alreadyDeleted := ve.deletedIDs[id]
	ve.lock.RUnlock()
	if alreadyDeleted {
		ve.lock.Lock()
		err := ve.rebuildIndexFromDataLocked()
		ve.lock.Unlock()
		return err
	}
	if err := ve.appendTombstoneToDataFile(id); err != nil {
		return err
	}

	ve.lock.Lock()
	defer ve.lock.Unlock()
	selector, err := faiss.NewIDSelectorBatch([]int64{id})
	if err != nil {
		return fmt.Errorf("create ID selector: %w", err)
	}
	defer selector.Delete()
	if _, err := ve.index.RemoveIDs(selector); err != nil {
		if rebuildErr := ve.rebuildIndexFromDataLocked(); rebuildErr != nil {
			return fmt.Errorf("remove vector %d: %w (rebuild failed: %v)", id, err, rebuildErr)
		}
	}
	ve.deletedIDs[id] = true
	delete(ve.pendingUpsertIDs, id)
	return nil
}

func (ve *VectorEngineImpl) rebuildIndexFromDataLocked() error {
	indexDesc := ve.indexType
	if requiredTrainCountForIndex(ve.indexType) > 0 && ve.indexMeta.Mode != VectorIndexModeTrained {
		indexDesc = "Flat"
	}
	index, err := buildVectorIndexFromDataFile(ve.dataPath, ve.maxVectorSize, indexDesc, ve.metric)
	if err != nil {
		return err
	}
	oldIndex := ve.index
	ve.index = index
	oldIndex.Delete()
	return nil
}

func (ve *VectorEngineImpl) appendTombstoneToDataFile(id int64) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()
	pos, err := ve.dataFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+4*ve.maxVectorSize)
	binary.LittleEndian.PutUint64(buf[:8], uint64(id))
	binary.LittleEndian.PutUint32(buf[8:12], tombstoneMarker)
	if _, err := ve.dataFile.Write(buf); err != nil {
		return err
	}
	if err := ve.dataFile.Sync(); err != nil {
		return err
	}
	ve.fileOffsets[id] = pos
	ve.deletedIDs[id] = true
	delete(ve.pendingUpsertIDs, id)
	return nil
}

func (ve *VectorEngineImpl) Close() error {
	var closeErr error
	ve.closeOnce.Do(func() {
		ve.maintenanceMu.Lock()
		ve.maintenanceClosed = true
		maintenance.UnregisterVectorFlush(ve)
		maintenance.UnregisterVectorCheckpoint(ve)
		ve.maintenanceMu.Unlock()

		ve.mutationMu.Lock()
		ve.lock.Lock()
		ve.closed = true
		ve.lock.Unlock()
		ve.mutationMu.Unlock()
		close(ve.backgroundStop)
		ve.backgroundWG.Wait()

		ve.mutationMu.Lock()
		if err := ve.flushDataLocked(true); err != nil {
			closeErr = err
		} else if err := ve.persistTrainingIndex(); err != nil {
			closeErr = err
		}
		ve.mutationMu.Unlock()
		if ve.wal != nil {
			if err := ve.wal.Close(); closeErr == nil && err != nil {
				closeErr = err
			}
		}
		ve.lock.Lock()
		if err := ve.dataFile.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
		ve.index.Delete()
		ve.lock.Unlock()
	})
	return closeErr
}

func (ve *VectorEngineImpl) checkpoint() error {
	ve.checkpointMu.Lock()
	defer ve.checkpointMu.Unlock()
	ve.mutationMu.Lock()
	defer ve.mutationMu.Unlock()
	if err := ve.flushDataLocked(true); err != nil {
		return err
	}
	if err := ve.persistTrainingIndex(); err != nil {
		return err
	}
	ve.lock.RLock()
	defer ve.lock.RUnlock()
	if err := ve.dataFile.Sync(); err != nil {
		return fmt.Errorf("sync data file: %w", err)
	}
	return nil
}

func (ve *VectorEngineImpl) persistTrainingIndex() error {
	if requiredTrainCountForIndex(ve.indexType) == 0 {
		return nil
	}
	ve.lock.Lock()
	defer ve.lock.Unlock()
	if ve.indexMeta.Mode != VectorIndexModeTrained {
		return nil
	}
	if err := ve.dataFile.Sync(); err != nil {
		return fmt.Errorf("sync trained vector data: %w", err)
	}
	info, err := ve.dataFile.Stat()
	if err != nil {
		return fmt.Errorf("stat trained vector data: %w", err)
	}
	if err := writeFaissIndex(ve.index, ve.indexPath); err != nil {
		return err
	}
	meta, err := ve.newIndexMeta(VectorIndexModeTrained, info.Size())
	if err != nil {
		return err
	}
	if err := writeVectorIndexMeta(ve.indexMetaPath, meta); err != nil {
		return err
	}
	ve.indexMeta = meta
	return nil
}

func (ve *VectorEngineImpl) replayWAL() error {
	records, err := ve.wal.Replay()
	if err != nil {
		return err
	}
	for _, entry := range records {
		keyBytes := []byte(entry.Key)
		if len(keyBytes) != 8 {
			return fmt.Errorf("invalid WAL key length: expected 8, got %d", len(keyBytes))
		}
		id := int64(binary.LittleEndian.Uint64(keyBytes))
		if entry.Flag == wal.EntryDeleted {
			if err := ve.removeAfterWAL(id); err != nil {
				return fmt.Errorf("replay remove id=%d: %w", id, err)
			}
			continue
		}
		vector, err := bytesToFloat32Array([]byte(entry.Value))
		if err != nil {
			return fmt.Errorf("WAL decode: %w", err)
		}
		if err := ve.insertAfterWAL(id, vector); err != nil {
			return fmt.Errorf("replay insert id=%d: %w", id, err)
		}
		if err := ve.flushDataLocked(true); err != nil {
			return fmt.Errorf("persist replayed insert id=%d: %w", id, err)
		}
	}
	return nil
}

func (ve *VectorEngineImpl) appendToDataFile(id int64, vector []float32) error {
	pos, err := ve.dataFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+len(vector)*4)
	binary.LittleEndian.PutUint64(buf[:8], uint64(id))
	for idx, value := range vector {
		binary.LittleEndian.PutUint32(buf[8+idx*4:], math.Float32bits(value))
	}
	if _, err := ve.dataFile.Write(buf); err != nil {
		return err
	}
	ve.fileOffsets[id] = pos
	delete(ve.deletedIDs, id)
	return nil
}

func (ve *VectorEngineImpl) enqueuePersist(id int64, vector []float32) {
	vectorCopy := append([]float32(nil), vector...)
	ve.persistMu.Lock()
	ve.persistBuf = append(ve.persistBuf, struct {
		id  int64
		vec []float32
	}{id: id, vec: vectorCopy})
	ve.persistMu.Unlock()
	maintenance.MarkVectorFlushDirty(ve)
	maintenance.MarkVectorCheckpointDirty(ve)
}

func (ve *VectorEngineImpl) MaintenanceFlush() {
	ve.maintenanceMu.RLock()
	defer ve.maintenanceMu.RUnlock()
	if !ve.maintenanceClosed {
		if err := ve.flushData(false); err != nil {
			log.Printf("flush vector data failed: %v", err)
		}
	}
}

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

func (ve *VectorEngineImpl) flushData(force bool) error {
	ve.mutationMu.Lock()
	defer ve.mutationMu.Unlock()
	return ve.flushDataLocked(force)
}

func (ve *VectorEngineImpl) flushDataLocked(force bool) error {
	ve.persistMu.Lock()
	buf := ve.persistBuf
	if !force && len(buf) == 0 {
		ve.persistMu.Unlock()
		return nil
	}
	ve.persistMu.Unlock()
	if len(buf) == 0 {
		if force {
			ve.scheduleTrainingPromotion()
		}
		return nil
	}

	ve.lock.Lock()
	info, err := ve.dataFile.Stat()
	if err != nil {
		ve.lock.Unlock()
		return fmt.Errorf("stat vector data before flush: %w", err)
	}
	originalSize := info.Size()
	for _, item := range buf {
		if err := ve.appendToDataFile(item.id, item.vec); err != nil {
			rollbackErr := ve.rollbackDataFileLocked(originalSize)
			ve.lock.Unlock()
			if rollbackErr != nil {
				return fmt.Errorf("append vector %d: %w (rollback failed: %v)", item.id, err, rollbackErr)
			}
			return fmt.Errorf("append vector %d: %w", item.id, err)
		}
	}
	if err := ve.dataFile.Sync(); err != nil {
		ve.lock.Unlock()
		return fmt.Errorf("sync vector data: %w", err)
	}
	for _, item := range buf {
		delete(ve.pendingUpsertIDs, item.id)
	}
	ve.lock.Unlock()

	ve.persistMu.Lock()
	ve.persistBuf = ve.persistBuf[len(buf):]
	ve.persistMu.Unlock()
	ve.scheduleTrainingPromotion()
	return nil
}

func (ve *VectorEngineImpl) rollbackDataFileLocked(size int64) error {
	if err := ve.dataFile.Truncate(size); err != nil {
		return err
	}
	if _, err := ve.dataFile.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	offsets, deletedIDs, err := scanVectorDataFile(ve.dataPath, ve.maxVectorSize)
	if err != nil {
		return err
	}
	ve.fileOffsets = offsets
	ve.deletedIDs = deletedIDs
	return nil
}

func float32ArrayToBytes(values []float32) []byte {
	buf := make([]byte, len(values)*4)
	for idx, value := range values {
		binary.LittleEndian.PutUint32(buf[idx*4:], math.Float32bits(value))
	}
	return buf
}

func bytesToFloat32Array(buf []byte) ([]float32, error) {
	if len(buf)%4 != 0 {
		return nil, errors.New("buffer size must be multiple of 4")
	}
	vector := make([]float32, len(buf)/4)
	for idx := range vector {
		vector[idx] = math.Float32frombits(binary.LittleEndian.Uint32(buf[idx*4:]))
	}
	return vector, nil
}

func buildVectorIndexFromDataFile(dataPath string, dimension int, indexDesc string, metric int) (faiss.Index, error) {
	offsets, deletedIDs, err := scanVectorDataFile(dataPath, dimension)
	if err != nil {
		return nil, err
	}
	index, err := newVectorFaissIndex(dimension, indexDesc, metric)
	if err != nil {
		return nil, err
	}
	dataFile, err := os.Open(dataPath)
	if err != nil {
		index.Delete()
		return nil, err
	}
	defer dataFile.Close()

	ids := make([]int64, 0, len(offsets))
	data := make([]float32, 0, len(offsets)*dimension)
	for id, offset := range offsets {
		if deletedIDs[id] {
			continue
		}
		_, vector, deleted, err := readVectorRecordAt(dataFile, offset, dimension)
		if err != nil {
			index.Delete()
			return nil, err
		}
		if !deleted {
			ids = append(ids, id)
			data = append(data, vector...)
		}
	}
	required := requiredTrainCountForIndex(indexDesc)
	if required > 0 && len(ids) < required {
		index.Delete()
		return buildVectorIndexFromDataFile(dataPath, dimension, "Flat", metric)
	}
	if required > 0 {
		trainCount := trainingSampleCount(required, len(ids))
		if err := index.Train(data[:trainCount*dimension]); err != nil {
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
		_, err := io.ReadFull(file, buf)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			if err := file.Truncate(offset); err != nil {
				return nil, nil, fmt.Errorf("truncate partial vector record: %w", err)
			}
			if err := file.Sync(); err != nil {
				return nil, nil, fmt.Errorf("sync repaired vector data: %w", err)
			}
			break
		}
		if err != nil {
			return nil, nil, err
		}
		id := int64(binary.LittleEndian.Uint64(buf[:8]))
		offsets[id] = offset
		deletedIDs[id] = binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker
		offset += int64(recordSize)
	}
	return offsets, deletedIDs, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func betterDistanceForMetric(metric int, left, right float32) bool {
	if metric == faiss.MetricInnerProduct {
		return left > right
	}
	return left < right
}
