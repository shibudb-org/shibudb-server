package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Podcopic-Labs/ShibuDb/internal/wal"

	"github.com/DataIntelligenceCrew/go-faiss"
)

type VectorEngineImpl struct {
	dataFile      *os.File
	indexFile     string
	wal           *wal.WAL
	maxVectorSize int

	faissIndex faiss.Index
	idMap      []int64 // maps FAISS internal index → your custom ID
	lock       sync.RWMutex

	indexType string
	metric    int

	pendingTrainVectors [][]float32 // buffer for training
	pendingTrainIDs     []int64     // buffer for IDs

	// Auto-flush mechanism similar to key-value storage
	quitChan     chan struct{}
	flushRunning int32
	closeOnce    sync.Once

	// Batching mechanism like key-value storage
	batchLock sync.Mutex
	batch     map[int64][]float32 // In-memory batch buffer
}

type vectorEntry struct {
	ID   int64
	Data []float32
}

var _ VectorEngine = (*VectorEngineImpl)(nil)

var _ VectorEngine = (*VectorEngineImpl)(nil)

func NewVectorEngine(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int) (*VectorEngineImpl, error) {
	dataFile, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}

	var faissIndex faiss.Index
	indexExists := false
	if _, err := os.Stat(indexPath); err == nil {
		faissIndex, err = faiss.ReadIndex(indexPath, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to read FAISS index from file: %w", err)
		}
		indexExists = true
	} else {
		faissIndex, err = faiss.IndexFactory(maxVectorSize, indexDesc, metric)
		if err != nil {
			return nil, fmt.Errorf("failed to create FAISS index: %w", err)
		}
	}

	w, err := wal.OpenWAL(walPath)
	if err != nil {
		return nil, err
	}

	e := &VectorEngineImpl{
		dataFile:      dataFile,
		indexFile:     indexPath,
		wal:           w,
		maxVectorSize: maxVectorSize,
		faissIndex:    faissIndex,
		idMap:         make([]int64, 0),
		indexType:     indexDesc,
		metric:        metric,
		quitChan:      make(chan struct{}),
		batch:         make(map[int64][]float32),
	}

	// If we loaded an existing index, rebuild the idMap from data file
	if indexExists {
		if err := e.rebuildIdMapFromDataFile(); err != nil {
			return nil, fmt.Errorf("failed to rebuild idMap: %w", err)
		}
	}

	if err := e.replayWAL(); err != nil {
		return nil, fmt.Errorf("WAL replay failed: %w", err)
	}

	// Start auto-checkpointing similar to key-value storage
	go e.autoCheckpoint()

	// Start auto-flush batch like key-value storage
	go e.autoFlushBatch()

	return e, nil
}

func (ve *VectorEngineImpl) replayWAL() error {
	records, err := ve.wal.Replay()
	if err != nil {
		return err
	}

	// Track successful replays to ensure we don't clear WAL if replay fails
	successfulReplays := 0

	for _, entry := range records {
		keyBytes := []byte(entry[0])
		if len(keyBytes) != 8 {
			return fmt.Errorf("invalid WAL key length: expected 8 bytes, got %d", len(keyBytes))
		}
		id := int64(binary.LittleEndian.Uint64(keyBytes))
		valBytes := []byte(entry[1])
		vector, err := bytesToFloat32Array(valBytes)
		if err != nil {
			return err
		}
		// During replay, we need to persist to data file to maintain consistency
		err = ve.insertInternal(id, vector, true)
		if err != nil {
			return fmt.Errorf("failed to replay vector with ID %d: %w", id, err)
		}
		successfulReplays++
	}

	// Only checkpoint and clear WAL if all replays were successful
	if successfulReplays > 0 {
		// Immediately checkpoint after successful replay (similar to key-value storage)
		if err := ve.checkpoint(); err != nil {
			return fmt.Errorf("failed to checkpoint after WAL replay: %w", err)
		}
		ve.wal.Clear()
	}
	return nil
}

func (ve *VectorEngineImpl) InsertVector(id int64, vector []float32) error {
	// Check if engine is closed
	select {
	case <-ve.quitChan:
		return fmt.Errorf("vector engine is closed")
	default:
	}

	if len(vector) != ve.maxVectorSize {
		return fmt.Errorf("vector length mismatch: expected %d", ve.maxVectorSize)
	}

	// Add to in-memory batch (like key-value storage)
	ve.batchLock.Lock()
	ve.batch[id] = vector
	ve.batchLock.Unlock()

	// Insert into FAISS index immediately for search functionality
	if err := ve.insertInternal(id, vector, false); err != nil {
		return err
	}

	return nil
}

func (ve *VectorEngineImpl) requiredTrainCount() int {
	// For Flat indices, no training is required
	if ve.indexType == "Flat" {
		return 0
	}

	nlist := 1
	pqCodebook := 1
	n := 0 // for Sscanf

	// Parse IVF cluster count
	if len(ve.indexType) >= 3 && ve.indexType[:3] == "IVF" {
		n, _ = fmt.Sscanf(ve.indexType, "IVF%d", &nlist)
		if n != 1 || nlist <= 0 {
			nlist = 32 // fallback
		}
	}

	// Parse PQ codebook size (e.g., PQ4x4 means 4 centroids, PQ4 means 256 centroids)
	if idx := strings.Index(ve.indexType, "PQ"); idx != -1 {
		pqPart := ve.indexType[idx+2:]
		if xIdx := strings.Index(pqPart, "x"); xIdx != -1 {
			// e.g., PQ4x4
			var codebook int
			n, _ = fmt.Sscanf(pqPart[xIdx:], "x%d", &codebook)
			if n == 1 && codebook > 0 {
				pqCodebook = codebook
			}
		} else {
			pqCodebook = 256 // default for PQ4, PQ8, etc.
		}
	}

	if nlist > pqCodebook {
		return nlist
	}
	return pqCodebook
}

func (ve *VectorEngineImpl) insertInternal(id int64, vector []float32, persist bool) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	if vector == nil || len(vector) == 0 {
		return fmt.Errorf("empty vector for id=%d", id)
	}

	nTrain := ve.requiredTrainCount()
	needsTraining := (nTrain > 0) && !ve.faissIndex.IsTrained()

	if persist {
		ve.idMap = append(ve.idMap, id)

		if _, err := ve.dataFile.Seek(0, io.SeekEnd); err != nil {
			return fmt.Errorf("seek end: %w", err)
		}
		buf := make([]byte, 8+len(vector)*4)
		binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
		for i, v := range vector {
			binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(v))
		}
		if _, err := ve.dataFile.Write(buf); err != nil {
			return err
		}
		if err := ve.dataFile.Sync(); err != nil {
			return err
		}
	}

	if !needsTraining {
		if err := ve.faissIndex.Add(vector); err != nil {
			return err
		}
		return nil
	}

	ve.pendingTrainVectors = append(ve.pendingTrainVectors, vector)
	ve.pendingTrainIDs = append(ve.pendingTrainIDs, id)

	// Not enough to train yet — just buffer.
	if len(ve.pendingTrainVectors) < nTrain {
		return nil
	}

	// Enough buffered — train on all pending.
	trainData := make([]float32, 0, len(ve.pendingTrainVectors)*len(vector))
	for _, v := range ve.pendingTrainVectors {
		trainData = append(trainData, v...)
	}

	if err := ve.faissIndex.Train(trainData); err != nil {
		return fmt.Errorf("index training failed: %w", err)
	}

	for _, v := range ve.pendingTrainVectors {
		if err := ve.faissIndex.Add(v); err != nil {
			return err
		}
	}

	// Clear buffers now that they are in the index.
	ve.pendingTrainVectors = nil
	ve.pendingTrainIDs = nil
	return nil
}

func (ve *VectorEngineImpl) rebuildIdMapFromDataFile() error {
	// Seek to beginning of data file
	_, err := ve.dataFile.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("failed to seek to beginning of data file: %w", err)
	}

	recordSize := 8 + 4*ve.maxVectorSize // 8 bytes for ID + 4 bytes per float32
	ve.idMap = make([]int64, 0)

	for {
		buf := make([]byte, recordSize)
		_, err := ve.dataFile.Read(buf)
		if err != nil {
			if err.Error() == "EOF" {
				break // End of file
			}
			return fmt.Errorf("failed to read from data file: %w", err)
		}

		// Extract ID from the record
		id := int64(binary.LittleEndian.Uint64(buf[0:8]))
		ve.idMap = append(ve.idMap, id)
	}

	log.Printf("Rebuilt idMap with %d entries from data file", len(ve.idMap))
	return nil
}

func (ve *VectorEngineImpl) RangeSearch(query []float32, radius float32) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}

	ve.lock.RLock()
	defer ve.lock.RUnlock()

	// FAISS range search (single query)
	res, err := ve.faissIndex.RangeSearch(query, radius)
	if err != nil {
		return nil, nil, err
	}
	defer res.Delete()

	labels, distances := res.Labels()
	lims := res.Lims()
	nq := res.Nq()

	if nq != 1 {
		return nil, nil, fmt.Errorf("expected 1 query, got %d", nq)
	}

	if len(lims) < 2 {
		return []int64{}, []float32{}, nil
	}

	start, end := lims[0], lims[1]
	count := end - start
	ids := make([]int64, count)
	dists := make([]float32, count)

	for i := start; i < end; i++ {
		idx := labels[i]
		if int(idx) < len(ve.idMap) {
			ids[i-start] = ve.idMap[idx]
			dists[i-start] = distances[i]
		} else {
			ids[i-start] = -1
			dists[i-start] = distances[i]
		}
	}

	return ids, dists, nil
}

func (ve *VectorEngineImpl) SearchTopK(query []float32, k int) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}

	ve.lock.RLock()
	defer ve.lock.RUnlock()

	distances, indexes, err := ve.faissIndex.Search(query, int64(k))
	if err != nil {
		return nil, nil, err
	}

	ids := make([]int64, len(indexes))
	for i, idx := range indexes {
		// Handle negative or very large indices that FAISS might return
		if idx < 0 || idx >= int64(len(ve.idMap)) {
			ids[i] = -1
		} else {
			ids[i] = ve.idMap[idx]
		}
	}

	// Log the search results (only for small k to avoid excessive output)
	if k <= 10 {
		log.Printf("SearchTopK: ids=%v, dists=%v", ids, distances)
	}

	return ids, distances, nil
}

func (ve *VectorEngineImpl) GetVectorByID(id int64) ([]float32, error) {
	ve.lock.RLock()
	defer ve.lock.RUnlock()

	index := -1
	// TODO: looks very inefficient, can we do better?
	for i, storedID := range ve.idMap {
		if storedID == id {
			index = i
			break
		}
	}

	log.Println("Checking index: ", index)
	log.Println("Current Batch: ", ve.batch)
	log.Println("Current idMap: ", ve.idMap)

	if index == -1 {
		return nil, fmt.Errorf("ID %d not found", id)
	}

	// Calculate offset: each record = 8 (id) + 4 * vector size
	recordSize := 8 + 4*ve.maxVectorSize
	offset := int64(index * recordSize)

	buf := make([]byte, recordSize)
	_, err := ve.dataFile.ReadAt(buf, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read vector at offset %d: %w", offset, err)
	}

	return bytesToFloat32Array(buf[8:])
}

func (ve *VectorEngineImpl) Close() error {
	ve.closeOnce.Do(func() {
		log.Println("Closing vector engine...")
		close(ve.quitChan)

		// Flush any pending batch before closing
		if err := ve.flushBatch(); err != nil {
			log.Printf("Final batch flush failed: %v", err)
		}

		// Final checkpoint before closing
		if err := ve.checkpoint(); err != nil {
			log.Printf("Final checkpoint failed: %v", err)
		}

		ve.wal.Close()
		ve.dataFile.Close()
		ve.faissIndex.Delete()
	})
	return nil
}

func (ve *VectorEngineImpl) autoCheckpoint() {
	ticker := time.NewTicker(30 * time.Second) // Checkpoint every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !atomic.CompareAndSwapInt32(&ve.flushRunning, 0, 1) {
				continue
			}
			if err := ve.checkpoint(); err != nil {
				log.Printf("Checkpoint failed: %v", err)
			}

			// Reset flag AFTER checkpoint fully completes
			atomic.StoreInt32(&ve.flushRunning, 0)

		case <-ve.quitChan:
			return
		}
	}
}

func (ve *VectorEngineImpl) autoFlushBatch() {
	ticker := time.NewTicker(1 * time.Second) // Flush every 1 second like key-value storage
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !atomic.CompareAndSwapInt32(&ve.flushRunning, 0, 1) {
				continue
			}
			if err := ve.flushBatch(); err != nil {
				log.Printf("FlushBatch failed: %v", err)
			}

			// Reset flag AFTER flush fully completes
			atomic.StoreInt32(&ve.flushRunning, 0)

		case <-ve.quitChan:
			return
		}
	}
}

func (ve *VectorEngineImpl) flushBatch() error {
	ve.batchLock.Lock()
	batchCopy := make(map[int64][]float32, len(ve.batch))
	for k, v := range ve.batch {
		batchCopy[k] = v
	}
	ve.batch = make(map[int64][]float32)
	ve.batchLock.Unlock()

	if len(batchCopy) == 0 {
		return nil
	}

	ve.lock.Lock()
	defer ve.lock.Unlock()

	log.Println("Flushing batch: ", batchCopy)

	// Write all vectors to WAL in batch
	for id, vector := range batchCopy {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))
		val := float32ArrayToBytes(vector)
		if err := ve.wal.WriteEntry(string(key), string(val)); err != nil {
			return err
		}
	}

	// Write all vectors to data file in batch
	for id, vector := range batchCopy {
		ve.idMap = append(ve.idMap, id)
		buf := make([]byte, 8+len(vector)*4)
		binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
		for i, v := range vector {
			binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(v))
		}
		_, err := ve.dataFile.Write(buf)
		if err != nil {
			return err
		}
	}

	// Single sync for entire batch
	if err := ve.dataFile.Sync(); err != nil {
		return err
	}

	// Single commit for entire batch
	if err := ve.wal.MarkCommitted(); err != nil {
		return err
	}

	return nil
}

func (ve *VectorEngineImpl) checkpoint() error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	// Write index to disk
	if err := faiss.WriteIndex(ve.faissIndex, ve.indexFile); err != nil {
		return fmt.Errorf("failed to write index during checkpoint: %w", err)
	}

	// Sync data file to ensure all data is persisted
	if err := ve.dataFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync data file during checkpoint: %w", err)
	}

	return nil
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
