package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
	"github.com/Podcopic-Labs/ShibuDb/internal/wal"
)

type VectorEngineImpl struct {
	dataFile      *os.File
	indexFile     string
	wal           *wal.WAL
	maxVectorSize int

	// FAISS indices:
	// baseIndex needs Train (for IVF*/PQ*), and is wrapped by idMapIndex so we can use external IDs.
	baseIndex  faiss.Index
	idMapIndex faiss.Index // = faiss.NewIndexIDMap(baseIndex)

	indexType string
	metric    int

	// For training-aware ingestion
	trainPool  [][]float32         // vectors for training only
	pendingAdd map[int64][]float32 // id -> vector waiting to be AddWithIDs after training

	// For fast GetVectorByID from append-only data file
	fileOffsets map[int64]int64 // id -> byte offset in data file

	// Lifecycle / checkpointing
	quitChan     chan struct{}
	flushRunning int32
	closeOnce    sync.Once

	lock sync.RWMutex
}

var _ VectorEngine = (*VectorEngineImpl)(nil)

// NewVectorEngine builds/loads the ID-mapped FAISS index and opens data + WAL files.
// If an old non-IDMap index exists, delete it once so we can persist the ID-mapped index going forward.
func NewVectorEngine(dataPath, indexPath, walPath string, maxVectorSize int, indexDesc string, metric int) (*VectorEngineImpl, error) {
	df, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("open data file: %w", err)
	}

	// Create (or read) the base index
	var idmap faiss.Index
	if _, err := os.Stat(indexPath); err == nil {
		// load whatever was persisted (likely already IDMap-wrapped)
		idmap, err = faiss.ReadIndex(indexPath, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to read FAISS index from file: %w", err)
		}
	} else {
		// IMPORTANT: prefix your description with "IDMap," so the factory wraps the base index
		// e.g. indexDesc = "IVF1024,Flat" -> "IDMap,IVF1024,Flat"
		idmap, err = faiss.IndexFactory(maxVectorSize, "IDMap,"+indexDesc, metric)
		if err != nil {
			return nil, fmt.Errorf("failed to create FAISS index: %w", err)
		}
	}

	w, err := wal.OpenWAL(walPath)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	e := &VectorEngineImpl{
		dataFile:      df,
		indexFile:     indexPath,
		wal:           w,
		maxVectorSize: maxVectorSize,
		baseIndex:     idmap,
		idMapIndex:    idmap,
		indexType:     indexDesc,
		metric:        metric,
		trainPool:     make([][]float32, 0, 1024),
		pendingAdd:    make(map[int64][]float32),
		fileOffsets:   make(map[int64]int64),
		quitChan:      make(chan struct{}),
	}

	// Rebuild fileOffsets from data file (fast linear scan over fixed-size records).
	if err := e.rebuildOffsetsFromDataFile(); err != nil {
		return nil, fmt.Errorf("rebuildOffsetsFromDataFile: %w", err)
	}

	// Replay WAL (idempotent), which will train (if needed) and add pending vectors.
	if err := e.replayWAL(); err != nil {
		return nil, fmt.Errorf("WAL replay failed: %w", err)
	}

	// Auto-checkpoint FAISS index to disk
	go e.autoCheckpoint()

	return e, nil
}

// requiredTrainCount returns a conservative minimum to *allow* training.
// You can/should make this configurable; these are safe defaults.
func (ve *VectorEngineImpl) requiredTrainCount() int {
	// Flat/HNSW need no training
	if ve.indexType == "Flat" || strings.HasPrefix(ve.indexType, "HNSW") {
		return 0
	}

	// IVF*n* → need at least nlist (practically 4–10× nlist)
	nlist := 0
	if strings.HasPrefix(ve.indexType, "IVF") {
		fmt.Sscanf(ve.indexType, "IVF%d", &nlist)
	}

	// PQ present? require >= 256 samples minimally
	needsPQ := strings.Contains(ve.indexType, "PQ")

	minTrain := 0
	if nlist > 0 {
		minTrain = nlist
	}
	if needsPQ && minTrain < 256 {
		minTrain = 256
	}
	return minTrain
}

func (ve *VectorEngineImpl) InsertVector(id int64, vector []float32) error {
	// sanity
	if len(vector) != ve.maxVectorSize {
		return fmt.Errorf("vector length mismatch: expected %d", ve.maxVectorSize)
	}

	// 1) WAL first
	key := make([]byte, 8)
	binary.LittleEndian.PutUint64(key, uint64(id))
	if err := ve.wal.WriteEntry(string(key), string(float32ArrayToBytes(vector))); err != nil {
		return err
	}

	// 2) Do the actual ingestion (train if needed, add to FAISS, persist), then mark committed.
	if err := ve.insertAfterWAL(id, vector); err != nil {
		return err
	}
	return ve.wal.MarkCommitted()
}

// insertAfterWAL performs the ingest without writing to WAL (used by InsertVector and WAL replay).
func (ve *VectorEngineImpl) insertAfterWAL(id int64, vector []float32) error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	nTrain := ve.requiredTrainCount()
	trained := (nTrain == 0) || ve.baseIndex.IsTrained()

	if trained {
		// Replace duplicate id if exists
		sel, _ := faiss.NewIDSelectorBatch([]int64{id})
		_, _ = ve.idMapIndex.RemoveIDs(sel)
		sel.Delete()
		if err := ve.idMapIndex.AddWithIDs(vector, []int64{id}); err != nil {
			return err
		}
		// Append to data file and update offset
		if err := ve.appendToDataFile(id, vector); err != nil {
			return err
		}
		return nil
	}

	// Not trained yet: stage for training + later AddWithIDs
	ve.pendingAdd[id] = vector
	ve.trainPool = append(ve.trainPool, vector)

	// If we crossed training threshold, train and flush pendingAdd in bulk
	if len(ve.trainPool) >= nTrain {
		train := make([]float32, 0, len(ve.trainPool)*ve.maxVectorSize)
		for _, v := range ve.trainPool {
			train = append(train, v...)
		}
		if err := ve.baseIndex.Train(train); err != nil {
			return fmt.Errorf("index training failed: %w", err)
		}

		// Bulk add IDs
		ids := make([]int64, 0, len(ve.pendingAdd))
		data := make([]float32, 0, len(ve.pendingAdd)*ve.maxVectorSize)
		for pid, pv := range ve.pendingAdd {
			ids = append(ids, pid)
			data = append(data, pv...)
		}
		if err := ve.idMapIndex.AddWithIDs(data, ids); err != nil {
			return err
		}

		// Persist all pending additions and clear buffers
		for pid, pv := range ve.pendingAdd {
			if err := ve.appendToDataFile(pid, pv); err != nil {
				return err
			}
		}
		if err := ve.dataFile.Sync(); err != nil {
			return err
		}
		ve.pendingAdd = make(map[int64][]float32)
		ve.trainPool = nil
	}

	return nil
}

func (ve *VectorEngineImpl) SearchTopK(query []float32, k int) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}
	ve.lock.RLock()
	defer ve.lock.RUnlock()

	dists, labels, err := ve.idMapIndex.Search(query, int64(k))
	if err != nil {
		return nil, nil, err
	}
	// labels already are your external IDs (thanks to IndexIDMap)
	return labels, dists, nil
}

func (ve *VectorEngineImpl) RangeSearch(query []float32, radius float32) ([]int64, []float32, error) {
	if len(query) != ve.maxVectorSize {
		return nil, nil, errors.New("invalid query size")
	}

	ve.lock.RLock()
	defer ve.lock.RUnlock()

	res, err := ve.idMapIndex.RangeSearch(query, radius)
	if err != nil {
		return nil, nil, err
	}
	defer res.Delete()

	labels, distances := res.Labels() // []int64
	lims := res.Lims()                // []int64 (len == nq+1)

	// enforce single query for this API
	if len(lims) != 2 {
		return nil, nil, fmt.Errorf("expected 1 query, got %d", len(lims)-1)
	}

	start := int(lims[0])
	end := int(lims[1])
	if start < 0 || end < start || end > len(labels) || end > len(distances) {
		return nil, nil, fmt.Errorf("invalid lims: [%d,%d) over labels=%d dists=%d",
			start, end, len(labels), len(distances))
	}

	n := end - start
	outIDs := make([]int64, n)
	outD := make([]float32, n)
	copy(outIDs, labels[start:end])
	copy(outD, distances[start:end])

	// OPTIONAL: sort by ascending distance (stable)
	// Comment out if you prefer FAISS's native order.
	type pair struct {
		id  int64
		dst float32
	}
	ps := make([]pair, n)
	for i := 0; i < n; i++ {
		ps[i] = pair{outIDs[i], outD[i]}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].dst < ps[j].dst })
	for i := 0; i < n; i++ {
		outIDs[i], outD[i] = ps[i].id, ps[i].dst
	}

	return outIDs, outD, nil
}

func (ve *VectorEngineImpl) GetVectorByID(id int64) ([]float32, error) {
	ve.lock.RLock()
	offset, ok := ve.fileOffsets[id]
	ve.lock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("ID %d not found", id)
	}

	recordSize := 8 + 4*ve.maxVectorSize
	buf := make([]byte, recordSize)
	if _, err := ve.dataFile.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("read vector at offset %d: %w", offset, err)
	}
	return bytesToFloat32Array(buf[8:])
}

func (ve *VectorEngineImpl) Close() error {
	ve.closeOnce.Do(func() {
		log.Println("Closing vector engine...")
		close(ve.quitChan)

		// Final checkpoint
		if err := ve.checkpoint(); err != nil {
			log.Printf("Final checkpoint failed: %v", err)
		}

		ve.wal.Close()
		ve.dataFile.Close()
		ve.idMapIndex.Delete()
	})
	return nil
}

// === Internals ===

func (ve *VectorEngineImpl) autoCheckpoint() {
	ticker := time.NewTicker(30 * time.Second)
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
			atomic.StoreInt32(&ve.flushRunning, 0)
		case <-ve.quitChan:
			return
		}
	}
}

func (ve *VectorEngineImpl) checkpoint() error {
	ve.lock.Lock()
	defer ve.lock.Unlock()

	// Persist the (ID-mapped) index
	if err := faiss.WriteIndex(ve.idMapIndex, ve.indexFile); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	// Ensure data file flushed
	if err := ve.dataFile.Sync(); err != nil {
		return fmt.Errorf("sync data file: %w", err)
	}
	return nil
}

func (ve *VectorEngineImpl) replayWAL() error {
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
		vec, err := bytesToFloat32Array([]byte(entry[1]))
		if err != nil {
			return fmt.Errorf("WAL decode: %w", err)
		}

		// IMPORTANT: do not write to WAL here again — just ingest.
		if err := ve.insertAfterWAL(id, vec); err != nil {
			return fmt.Errorf("replay insert id=%d: %w", id, err)
		}
	}

	// After successful replay, checkpoint and clear WAL
	if err := ve.checkpoint(); err != nil {
		return fmt.Errorf("checkpoint after replay: %w", err)
	}
	ve.wal.Clear()
	return nil
}

func (ve *VectorEngineImpl) appendToDataFile(id int64, vector []float32) error {
	// Seek end, remember offset for GetVectorByID
	pos, err := ve.dataFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+len(vector)*4)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
	for i, v := range vector {
		binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(v))
	}
	if _, err := ve.dataFile.Write(buf); err != nil {
		return err
	}
	ve.fileOffsets[id] = pos
	return nil
}

func (ve *VectorEngineImpl) rebuildOffsetsFromDataFile() error {
	// Walk the file and record the last offset for each ID (latest write wins).
	if _, err := ve.dataFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	recordSize := 8 + 4*ve.maxVectorSize
	offset := int64(0)

	for {
		buf := make([]byte, recordSize)
		n, err := ve.dataFile.Read(buf)
		if err != nil {
			if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
				break
			}
			if err == io.ErrUnexpectedEOF && n > 0 {
				// Truncated tail — ignore the last partial record
				break
			}
			return fmt.Errorf("read data file: %w", err)
		}
		if n < recordSize {
			// Partial/truncated record — ignore
			break
		}
		id := int64(binary.LittleEndian.Uint64(buf[0:8]))
		ve.fileOffsets[id] = offset
		offset += int64(recordSize)
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
