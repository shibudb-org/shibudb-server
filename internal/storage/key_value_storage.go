package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	kvindex "github.com/shibudb.org/shibudb-server/internal/index"
	"github.com/shibudb.org/shibudb-server/internal/maintenance"
	"github.com/shibudb.org/shibudb-server/internal/wal"
)

type kvSegment struct {
	meta     SegmentMeta
	dataFile *os.File
	index    kvindex.KeyValueIndex
}

// kvBatchEntry is a single staged write in the in-memory batch. A deleted entry
// is a tombstone: it is persisted as a delete record by FlushBatch and shadows
// any value for the key in older segments.
type kvBatchEntry struct {
	value   string
	deleted bool
}

type ShibuDB struct {
	lock      sync.RWMutex
	wal       *wal.WAL
	walMu     sync.Mutex
	settings  SpaceSettings
	batchLock sync.Mutex
	batch     map[string]kvBatchEntry
	closeOnce sync.Once

	maintenanceMu     sync.RWMutex
	maintenanceClosed bool

	layout           SegmentLayout
	primaryDataPath  string
	primaryIndexPath string
	manifest         *SegmentManifest
	segments         []*kvSegment

	mergeQueue     chan struct{}
	backgroundStop chan struct{}
	backgroundWG   sync.WaitGroup
	indexType      string
}

func OpenDBWithPathsAndWAL(dataPath, walPath, indexPath string, enableWAL bool, indexType string) (*ShibuDB, error) {
	return OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath, enableWAL, indexType, SpaceSettings{})
}

func OpenDBWithPathsAndWALAndSettings(dataPath, walPath, indexPath string, enableWAL bool, indexType string, settings SpaceSettings) (*ShibuDB, error) {
	var dbWAL *wal.WAL
	var err error
	if enableWAL {
		dbWAL, err = wal.OpenWAL(walPath)
		if err != nil {
			return nil, err
		}
	}

	db := &ShibuDB{
		wal:              dbWAL,
		settings:         NormalizeSpaceSettings(settings),
		batch:            make(map[string]kvBatchEntry),
		layout:           NewSegmentLayout(filepath.Dir(dataPath), "data", ".db", ".idx"),
		primaryDataPath:  dataPath,
		primaryIndexPath: indexPath,
		mergeQueue:       make(chan struct{}, 1),
		backgroundStop:   make(chan struct{}),
		indexType:        indexType,
	}

	manifest, err := db.loadOrCreateManifest()
	if err != nil {
		if db.wal != nil {
			_ = db.wal.Close()
		}
		return nil, err
	}
	db.manifest = manifest

	if err := db.loadSegments(); err != nil {
		if db.wal != nil {
			_ = db.wal.Close()
		}
		return nil, err
	}

	db.startBackgroundWorkers()
	if enableWAL {
		db.replayWAL()
	}

	return db, nil
}

func OpenDB(filename string, walFilename string, indexType string) (*ShibuDB, error) {
	return OpenDBWithWAL(filename, walFilename, true, indexType)
}

func OpenDBWithWAL(filename string, walFilename string, enableWAL bool, indexType string) (*ShibuDB, error) {
	return OpenDBWithWALAndSettings(filename, walFilename, enableWAL, indexType, SpaceSettings{})
}

func OpenDBWithWALAndSettings(filename string, walFilename string, enableWAL bool, indexType string, settings SpaceSettings) (*ShibuDB, error) {
	return OpenDBWithPathsAndWALAndSettings(filename, walFilename, filepath.Join(filepath.Dir(filename), "index.dat"), enableWAL, indexType, settings)
}

func (db *ShibuDB) loadOrCreateManifest() (*SegmentManifest, error) {
	path := db.layout.ManifestPath()
	if _, err := os.Stat(path); err == nil {
		return LoadOrCreateSegmentManifest(db.layout)
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
			DataFile:        filepath.Base(db.primaryDataPath),
			IndexFile:       filepath.Base(db.primaryIndexPath),
			CreatedAtUnixNs: now,
		}},
	}
	if info, err := os.Stat(db.primaryDataPath); err == nil {
		manifest.Segments[0].SizeBytes = info.Size()
	}
	if err := WriteSegmentManifest(db.layout, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (db *ShibuDB) loadSegments() error {
	db.lock.Lock()
	defer db.lock.Unlock()

	db.segments = nil
	if len(db.manifest.Segments) == 0 {
		return errors.New("segment manifest has no segments")
	}

	db.manifest.ActiveSegmentID = db.manifest.Segments[len(db.manifest.Segments)-1].ID
	manifestChanged := false

	for idx, meta := range db.manifest.Segments {
		desc := db.layout.Descriptor(meta)
		dataFile, err := os.OpenFile(desc.DataPath, os.O_RDWR|os.O_CREATE, 0666)
		if err != nil {
			return err
		}

		var segIndex kvindex.KeyValueIndex
		if idx == len(db.manifest.Segments)-1 {
			// Hot segment: the index can lag the data file after a crash
			// (records are fsynced before index entries), so always rebuild
			// it from the data file. The hot segment is bounded by the
			// rollover size, so this scan is cheap.
			if _, err = RebuildKeyValueIndex(desc.DataPath, desc.IndexPath, db.indexType); err == nil {
				segIndex, err = kvindex.NewKeyValueIndex(desc.IndexPath, db.indexType)
			}
			meta.State = SegmentStateHot
		} else {
			segIndex, err = loadOrBuildKeyValueSegmentIndex(desc, db.indexType)
			meta.State = SegmentStateCold
		}
		if err != nil {
			_ = dataFile.Close()
			return err
		}

		if info, err := dataFile.Stat(); err == nil {
			meta.SizeBytes = info.Size()
		}
		db.manifest.Segments[idx] = meta
		db.segments = append(db.segments, &kvSegment{
			meta:     meta,
			dataFile: dataFile,
			index:    segIndex,
		})
		manifestChanged = true
	}

	if manifestChanged {
		if err := WriteSegmentManifest(db.layout, db.manifest); err != nil {
			return err
		}
	}
	return nil
}

func (db *ShibuDB) startBackgroundWorkers() {
	db.backgroundWG.Add(1)
	go db.mergeWorker()
}

func (db *ShibuDB) replayWAL() {
	if db.wal == nil {
		return
	}
	entries, err := db.wal.Replay()
	if err != nil {
		log.Printf("WAL replay failed: %v", err)
		return
	}
	for _, entry := range entries {
		if entry.Flag == wal.EntryDeleted {
			if err := db.Delete(entry.Key); err != nil && !errors.Is(err, errKeyDeleted) && !errors.Is(err, errKeyNotFound) {
				log.Printf("WAL replay delete failed for %q: %v", entry.Key, err)
			}
			continue
		}
		if err := db.PutBatch(entry.Key, entry.Value); err != nil {
			log.Printf("WAL replay put failed for %q: %v", entry.Key, err)
		}
	}
	if err := db.FlushBatch(); err != nil {
		log.Printf("WAL replay flush failed: %v", err)
	}
	_ = db.wal.Clear()
}

func (db *ShibuDB) PutBatch(key, value string) error {
	// Empty values are unrepresentable in the on-disk format (valSize == 0 is
	// the delete tombstone), so they are rejected at the entry points (query
	// engine, CLIs) and silently ignored here. This also drops any legacy
	// empty-value records during WAL replay.
	if value == "" {
		return nil
	}

	// With WAL enabled, the write must be durable before we acknowledge it:
	// persist (and fsync) the WAL record first, then stage it in the in-memory
	// batch for the asynchronous data-file flush. walMu serializes WAL appends
	// against the checkpoint clear in FlushBatch so a flush cannot drop a record
	// for a write that raced with it. The fsync happens before batchLock is
	// taken, so reads contending on batchLock are not blocked on the disk.
	if db.wal != nil {
		db.walMu.Lock()
		if err := db.wal.WriteEntry(key, value); err != nil {
			db.walMu.Unlock()
			return err
		}
		db.batchLock.Lock()
		db.batch[key] = kvBatchEntry{value: value}
		db.batchLock.Unlock()
		db.walMu.Unlock()
		maintenance.MarkKVFlushDirty(db)
		return nil
	}

	db.batchLock.Lock()
	db.batch[key] = kvBatchEntry{value: value}
	db.batchLock.Unlock()
	maintenance.MarkKVFlushDirty(db)
	return nil
}

func (db *ShibuDB) FlushBatch() error {
	// Hold walMu for the whole flush so no new WAL records are appended between
	// draining the batch and clearing the WAL below. This keeps the invariant
	// that the WAL contains exactly the records not yet persisted to the data
	// file, which makes the checkpoint clear safe (no acked write is lost).
	if db.wal != nil {
		db.walMu.Lock()
		defer db.walMu.Unlock()
	}

	db.batchLock.Lock()
	batchCopy := make(map[string]kvBatchEntry, len(db.batch))
	for k, v := range db.batch {
		batchCopy[k] = v
	}
	db.batch = make(map[string]kvBatchEntry)
	db.batchLock.Unlock()

	db.lock.Lock()
	defer db.lock.Unlock()

	if len(batchCopy) > 0 {
		active := db.activeSegmentLocked()
		if active == nil {
			return errors.New("no active segment")
		}

		for key, entry := range batchCopy {
			var pos int64
			var err error
			if entry.deleted {
				pos, err = appendDeleteRecord(active.dataFile, key)
			} else {
				pos, err = appendKeyValueRecord(active.dataFile, key, entry.value)
			}
			if err != nil {
				return err
			}
			// Deletes are indexed like puts: the entry points at the delete
			// record so the tombstone shadows the key in older segments.
			if err := active.index.Add(key, pos); err != nil {
				return err
			}
		}

		if err := active.dataFile.Sync(); err != nil {
			return err
		}
		if err := active.index.Sync(); err != nil {
			return err
		}
		if info, err := active.dataFile.Stat(); err == nil {
			active.meta.SizeBytes = info.Size()
			db.updateSegmentMetaLocked(active.meta)
		}
	}

	// Every batched PUT is now durable in the data file, and Delete persists its
	// tombstone synchronously, so all records currently in the WAL are redundant.
	// Clearing here (under walMu + db.lock) is the single checkpoint point, which
	// guarantees no acked write is dropped: walMu blocks new PUT appends and
	// db.lock blocks Delete appends while we clear.
	if db.wal != nil {
		if err := db.wal.Clear(); err != nil {
			return err
		}
	}

	if len(batchCopy) > 0 {
		return db.rotateHotSegmentLocked()
	}
	return nil
}

func (db *ShibuDB) Get(key string) (string, error) {
	db.batchLock.Lock()
	if entry, exists := db.batch[key]; exists {
		db.batchLock.Unlock()
		if entry.deleted {
			return "", errKeyDeleted
		}
		return entry.value, nil
	}
	db.batchLock.Unlock()

	db.lock.RLock()
	defer db.lock.RUnlock()

	for idx := len(db.segments) - 1; idx >= 0; idx-- {
		segment := db.segments[idx]
		pos, exists := segment.index.Get(key)
		if !exists {
			continue
		}
		foundKey, value, deleted, err := readKeyValueRecordAt(segment.dataFile, pos)
		if err != nil {
			return "", err
		}
		if foundKey != key {
			return "", fmt.Errorf("key mismatch at position %d: found %q expected %q", pos, foundKey, key)
		}
		if deleted {
			return "", errKeyDeleted
		}
		return value, nil
	}
	return "", errKeyNotFound
}

func (db *ShibuDB) Delete(key string) error {
	// Preserve the "key not found" contract, but check existence under a read
	// lock (no exclusive lock, no fsync) so deletes don't serialize behind every
	// other write the way the old synchronous path did.
	exists, err := db.keyExists(key)
	if err != nil {
		return err
	}
	if !exists {
		return errKeyNotFound
	}

	// Durable, batched delete: same model as PutBatch. Persist a delete record to
	// the WAL (fsync) before acking, then stage a tombstone in the batch for the
	// asynchronous data-file flush. This removes the per-delete data-file fsync
	// and the exclusive lock, and routes puts and deletes through the same ordered
	// batch so a put and delete of the same key resolve last-writer-wins instead
	// of racing on data-file offsets (which previously could resurrect a key).
	if db.wal != nil {
		db.walMu.Lock()
		if err := db.wal.WriteDelete(key); err != nil {
			db.walMu.Unlock()
			return err
		}
		db.batchLock.Lock()
		db.batch[key] = kvBatchEntry{deleted: true}
		db.batchLock.Unlock()
		db.walMu.Unlock()
		maintenance.MarkKVFlushDirty(db)
		return nil
	}

	db.batchLock.Lock()
	db.batch[key] = kvBatchEntry{deleted: true}
	db.batchLock.Unlock()
	maintenance.MarkKVFlushDirty(db)
	return nil
}

// keyExists reports whether key currently resolves to a live value, checking the
// in-memory batch first (most recent state) and then the persisted segments.
func (db *ShibuDB) keyExists(key string) (bool, error) {
	db.batchLock.Lock()
	if entry, ok := db.batch[key]; ok {
		db.batchLock.Unlock()
		return !entry.deleted, nil
	}
	db.batchLock.Unlock()

	db.lock.RLock()
	defer db.lock.RUnlock()
	return db.keyExistsLocked(key)
}

func (db *ShibuDB) Close() error {
	db.closeOnce.Do(func() {
		db.maintenanceMu.Lock()
		db.maintenanceClosed = true
		maintenance.UnregisterKVFlush(db)
		db.maintenanceMu.Unlock()

		_ = db.FlushBatch()
		close(db.backgroundStop)
		db.backgroundWG.Wait()

		db.lock.Lock()
		for _, segment := range db.segments {
			_ = segment.dataFile.Close()
			_ = segment.index.Close()
		}
		db.lock.Unlock()

		if db.wal != nil {
			_ = db.wal.Clear()
			_ = db.wal.Close()
		}
	})
	return nil
}

func (db *ShibuDB) Put(key, value string) error {
	return db.PutBatch(key, value)
}

func (db *ShibuDB) UpdateSpaceSettings(settings SpaceSettings) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	db.settings = NormalizeSpaceSettings(settings)
	db.scheduleMergeCheckLocked()
	return nil
}

func (db *ShibuDB) MaintenanceFlush() {
	db.maintenanceMu.RLock()
	defer db.maintenanceMu.RUnlock()
	if db.maintenanceClosed {
		return
	}
	if err := db.FlushBatch(); err != nil {
		log.Printf("FlushBatch failed: %v", err)
	}
}

func (db *ShibuDB) mergeWorker() {
	defer db.backgroundWG.Done()
	for {
		select {
		case <-db.backgroundStop:
			return
		case <-db.mergeQueue:
			for db.tryMergeOldestColdSegments() {
			}
		}
	}
}

func (db *ShibuDB) tryMergeOldestColdSegments() bool {
	db.lock.Lock()
	if len(db.segments) <= db.settings.MaxSegmentsBeforeMerge {
		db.lock.Unlock()
		return false
	}

	var first, second *kvSegment
	for _, segment := range db.segments {
		if segment.meta.ID == db.manifest.ActiveSegmentID {
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
		db.lock.Unlock()
		return false
	}

	first.meta.State = SegmentStateMerging
	second.meta.State = SegmentStateMerging
	db.updateSegmentMetaLocked(first.meta)
	db.updateSegmentMetaLocked(second.meta)

	newID := second.meta.ID
	firstDesc := db.layout.Descriptor(first.meta)
	secondDesc := db.layout.Descriptor(second.meta)
	_ = WriteSegmentManifest(db.layout, db.manifest)
	db.lock.Unlock()

	mergedMeta, mergedIndex, mergedFile, err := db.mergeSegments(newID, firstDesc, secondDesc)
	if err != nil {
		log.Printf("merge key-value segments %d and %d failed: %v", first.meta.ID, second.meta.ID, err)
		db.lock.Lock()
		if segment := db.segmentByIDLocked(first.meta.ID); segment != nil {
			segment.meta.State = SegmentStateCold
			db.updateSegmentMetaLocked(segment.meta)
		}
		if segment := db.segmentByIDLocked(second.meta.ID); segment != nil {
			segment.meta.State = SegmentStateCold
			db.updateSegmentMetaLocked(segment.meta)
		}
		_ = WriteSegmentManifest(db.layout, db.manifest)
		db.lock.Unlock()
		return false
	}

	db.lock.Lock()
	defer db.lock.Unlock()

	firstIdx := db.segmentIndexByIDLocked(first.meta.ID)
	secondIdx := db.segmentIndexByIDLocked(second.meta.ID)
	if firstIdx < 0 || secondIdx < 0 || secondIdx <= firstIdx {
		_ = mergedFile.Close()
		_ = mergedIndex.Close()
		return false
	}

	_ = db.segments[firstIdx].dataFile.Close()
	_ = db.segments[firstIdx].index.Close()
	_ = db.segments[secondIdx].dataFile.Close()
	_ = db.segments[secondIdx].index.Close()
	_ = os.Remove(firstDesc.DataPath)
	_ = os.Remove(firstDesc.IndexPath)

	mergedSegment := &kvSegment{
		meta:     mergedMeta,
		dataFile: mergedFile,
		index:    mergedIndex,
	}
	db.segments = append(append([]*kvSegment{}, mergedSegment), db.segments[secondIdx+1:]...)
	db.manifest.Segments = append(append([]SegmentMeta{}, mergedMeta), db.manifest.Segments[secondIdx+1:]...)
	_ = WriteSegmentManifest(db.layout, db.manifest)
	return len(db.segments) > db.settings.MaxSegmentsBeforeMerge
}

func (db *ShibuDB) mergeSegments(newID int64, firstDesc, secondDesc SegmentDescriptor) (SegmentMeta, kvindex.KeyValueIndex, *os.File, error) {
	latestRecords, err := collectLatestKeyValueRecords(firstDesc.DataPath, secondDesc.DataPath)
	if err != nil {
		return SegmentMeta{}, nil, nil, err
	}

	mergedDataPath := db.layout.DataPath(newID)
	mergedIndexPath := db.layout.IndexPath(newID)
	if err := writeMergedKeyValueDataFile(mergedDataPath, latestRecords); err != nil {
		return SegmentMeta{}, nil, nil, err
	}
	if _, err := RebuildKeyValueIndex(mergedDataPath, mergedIndexPath, db.indexType); err != nil {
		return SegmentMeta{}, nil, nil, err
	}

	mergedIndex, err := kvindex.NewKeyValueIndex(mergedIndexPath, db.indexType)
	if err != nil {
		return SegmentMeta{}, nil, nil, err
	}
	dataFile, err := os.OpenFile(mergedDataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		_ = mergedIndex.Close()
		return SegmentMeta{}, nil, nil, err
	}

	meta := SegmentMeta{
		ID:              newID,
		State:           SegmentStateCold,
		DataFile:        filepath.Base(mergedDataPath),
		IndexFile:       filepath.Base(mergedIndexPath),
		CreatedAtUnixNs: time.Now().UnixNano(),
	}
	if info, err := dataFile.Stat(); err == nil {
		meta.SizeBytes = info.Size()
	}
	return meta, mergedIndex, dataFile, nil
}

func (db *ShibuDB) rotateHotSegmentLocked() error {
	active := db.activeSegmentLocked()
	if active == nil {
		return errors.New("no active segment")
	}
	if active.meta.SizeBytes < db.settings.SegmentRolloverBytes {
		return nil
	}

	// The hot segment's index is kept current by FlushBatch and was synced
	// just before rotation, so the sealed segment is immediately cold — no
	// background index build is needed.
	active.meta.State = SegmentStateCold
	active.meta.SealedAtUnixNs = time.Now().UnixNano()
	db.updateSegmentMetaLocked(active.meta)

	newID := db.manifest.NextSegmentID
	db.manifest.NextSegmentID++
	newMeta := SegmentMeta{
		ID:              newID,
		State:           SegmentStateHot,
		DataFile:        db.layout.DataFileName(newID),
		IndexFile:       db.layout.IndexFileName(newID),
		CreatedAtUnixNs: time.Now().UnixNano(),
	}
	newDataFile, err := os.OpenFile(db.layout.DataPath(newID), os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	newIndex, err := kvindex.NewKeyValueIndex(db.layout.IndexPath(newID), db.indexType)
	if err != nil {
		_ = newDataFile.Close()
		return err
	}

	db.manifest.ActiveSegmentID = newID
	db.manifest.Segments = append(db.manifest.Segments, newMeta)
	db.segments = append(db.segments, &kvSegment{
		meta:     newMeta,
		dataFile: newDataFile,
		index:    newIndex,
	})
	if err := WriteSegmentManifest(db.layout, db.manifest); err != nil {
		return err
	}

	db.scheduleMergeCheckLocked()
	return nil
}

func (db *ShibuDB) activeSegmentLocked() *kvSegment {
	for _, segment := range db.segments {
		if segment.meta.ID == db.manifest.ActiveSegmentID {
			return segment
		}
	}
	return nil
}

func (db *ShibuDB) keyExistsLocked(key string) (bool, error) {
	for idx := len(db.segments) - 1; idx >= 0; idx-- {
		segment := db.segments[idx]
		pos, exists := segment.index.Get(key)
		if !exists {
			continue
		}
		_, _, deleted, err := readKeyValueRecordAt(segment.dataFile, pos)
		if err != nil {
			return false, err
		}
		return !deleted, nil
	}
	return false, nil
}

func (db *ShibuDB) updateSegmentMetaLocked(meta SegmentMeta) {
	for idx := range db.segments {
		if db.segments[idx].meta.ID == meta.ID {
			db.segments[idx].meta = meta
			break
		}
	}
	for idx := range db.manifest.Segments {
		if db.manifest.Segments[idx].ID == meta.ID {
			db.manifest.Segments[idx] = meta
			break
		}
	}
}

func (db *ShibuDB) segmentByIDLocked(id int64) *kvSegment {
	for _, segment := range db.segments {
		if segment.meta.ID == id {
			return segment
		}
	}
	return nil
}

func (db *ShibuDB) segmentIndexByIDLocked(id int64) int {
	for idx, segment := range db.segments {
		if segment.meta.ID == id {
			return idx
		}
	}
	return -1
}

func (db *ShibuDB) scheduleMergeCheckLocked() {
	select {
	case db.mergeQueue <- struct{}{}:
	default:
	}
}

var (
	errKeyNotFound = errors.New("key not found")
	errKeyDeleted  = errors.New("key is deleted")
)

func appendKeyValueRecord(file *os.File, key, value string) (int64, error) {
	keyBytes := []byte(key)
	valBytes := []byte(value)
	buf := make([]byte, 8+len(keyBytes)+len(valBytes))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keyBytes)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(valBytes)))
	copy(buf[8:], keyBytes)
	copy(buf[8+len(keyBytes):], valBytes)

	pos, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := file.Write(buf); err != nil {
		return 0, err
	}
	return pos, nil
}

func appendDeleteRecord(file *os.File, key string) (int64, error) {
	keyBytes := []byte(key)
	buf := make([]byte, 8+len(keyBytes))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keyBytes)))
	binary.LittleEndian.PutUint32(buf[4:8], 0)
	copy(buf[8:], keyBytes)

	pos, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := file.Write(buf); err != nil {
		return 0, err
	}
	return pos, nil
}

func readKeyValueRecordAt(file *os.File, pos int64) (string, string, bool, error) {
	header := make([]byte, 8)
	if _, err := file.ReadAt(header, pos); err != nil {
		return "", "", false, err
	}
	keySize := binary.LittleEndian.Uint32(header[0:4])
	valSize := binary.LittleEndian.Uint32(header[4:8])

	keyBytes := make([]byte, keySize)
	if _, err := file.ReadAt(keyBytes, pos+8); err != nil {
		return "", "", false, err
	}
	if valSize == 0 {
		return string(keyBytes), "", true, nil
	}
	valBytes := make([]byte, valSize)
	if _, err := file.ReadAt(valBytes, pos+8+int64(keySize)); err != nil {
		return "", "", false, err
	}
	return string(keyBytes), string(valBytes), false, nil
}

// loadOrBuildKeyValueSegmentIndex opens a cold segment's index for serving
// reads, rebuilding it from the data file when it is missing or unreadable
// (e.g. an old-format file that fails the header check).
func loadOrBuildKeyValueSegmentIndex(desc SegmentDescriptor, indexType string) (kvindex.KeyValueIndex, error) {
	if _, err := os.Stat(desc.IndexPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if _, rebuildErr := RebuildKeyValueIndex(desc.DataPath, desc.IndexPath, indexType); rebuildErr != nil {
			return nil, rebuildErr
		}
		return kvindex.NewKeyValueIndex(desc.IndexPath, indexType)
	}

	idx, err := kvindex.NewKeyValueIndex(desc.IndexPath, indexType)
	if err == nil {
		return idx, nil
	}
	if _, rebuildErr := RebuildKeyValueIndex(desc.DataPath, desc.IndexPath, indexType); rebuildErr != nil {
		return nil, rebuildErr
	}
	return kvindex.NewKeyValueIndex(desc.IndexPath, indexType)
}

func collectLatestKeyValueRecords(paths ...string) (map[string][]byte, error) {
	records := make(map[string][]byte)
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		var readErr error
		for {
			header := make([]byte, 8)
			n, err := io.ReadFull(file, header)
			if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
				break
			}
			if err == io.ErrUnexpectedEOF {
				break
			}
			if err != nil {
				readErr = err
				break
			}

			keySize := binary.LittleEndian.Uint32(header[0:4])
			valSize := binary.LittleEndian.Uint32(header[4:8])
			record := make([]byte, 8+int(keySize)+int(valSize))
			copy(record[:8], header)
			if _, err := io.ReadFull(file, record[8:]); err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					break
				}
				readErr = err
				break
			}
			key := string(record[8 : 8+keySize])
			records[key] = record
		}
		_ = file.Close()
		if readErr != nil {
			return nil, readErr
		}
	}
	return records, nil
}

func writeMergedKeyValueDataFile(dataPath string, records map[string][]byte) error {
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
