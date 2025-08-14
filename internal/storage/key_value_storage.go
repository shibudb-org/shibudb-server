package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Podcopic-Labs/ShibuDb/internal/index"
	"github.com/Podcopic-Labs/ShibuDb/internal/wal"
)

type ShibuDB struct {
	file         *os.File
	lock         sync.RWMutex
	index        *index.BTreeIndex
	wal          *wal.WAL
	batchLock    sync.Mutex
	batch        map[string]string
	quitChan     chan struct{}
	flushRunning int32
	closeOnce    sync.Once
}

func OpenDBWithPaths(dataPath, walPath, indexPath string) (*ShibuDB, error) {
	file, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}

	dbIndex, err := index.NewBTreeIndex(indexPath)
	if err != nil {
		return nil, err
	}

	dbWAL, err := wal.OpenWAL(walPath)
	if err != nil {
		return nil, err
	}

	db := &ShibuDB{
		file:     file,
		index:    dbIndex,
		wal:      dbWAL,
		quitChan: make(chan struct{}),
		batch:    make(map[string]string),
	}

	db.index.BatchLoadFromMmap()
	db.replayWAL()

	go db.autoFlushBatch()

	return db, nil
}

func OpenDB(filename string, walFilename string) (*ShibuDB, error) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}
	dbIndex, err := index.NewBTreeIndex("index.dat")
	if err != nil {
		return nil, err
	}
	dbWAL, err := wal.OpenWAL(walFilename)
	if err != nil {
		return nil, err
	}
	db := &ShibuDB{
		file:     file,
		index:    dbIndex,
		wal:      dbWAL,
		quitChan: make(chan struct{}),
		batch:    make(map[string]string),
	}
	db.index.BatchLoadFromMmap()
	db.replayWAL()

	go db.autoFlushBatch()

	return db, nil
}

func (db *ShibuDB) replayWAL() {
	entries, err := db.wal.Replay()
	if err != nil {
		log.Printf("WAL replay failed: %v", err)
		return
	}
	for _, entry := range entries {
		if entry[1] != "" {
			db.PutBatch(entry[0], entry[1])
		}
	}
	db.FlushBatch()
	db.wal.Clear()
}

func (db *ShibuDB) autoFlushBatch() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !atomic.CompareAndSwapInt32(&db.flushRunning, 0, 1) {
				continue
			}
			if err := db.FlushBatch(); err != nil {
				log.Printf("FlushBatch failed: %v", err)
			}

			// Reset flag AFTER flush fully completes
			atomic.StoreInt32(&db.flushRunning, 0)

		case <-db.quitChan:
			return
		}
	}
}

func (db *ShibuDB) PutBatch(key, value string) error {
	db.batchLock.Lock()
	db.batch[key] = value
	db.batchLock.Unlock()
	return nil
}

func (db *ShibuDB) FlushBatch() error {
	db.batchLock.Lock()
	batchCopy := make(map[string]string, len(db.batch))
	for k, v := range db.batch {
		batchCopy[k] = v
	}
	db.batch = make(map[string]string)
	db.batchLock.Unlock()

	if len(batchCopy) == 0 {
		return nil
	}

	db.lock.Lock()
	defer db.lock.Unlock()

	for key, value := range batchCopy {
		err := db.wal.WriteEntry(key, value)
		if err != nil {
			return err
		}
	}

	for key, value := range batchCopy {
		keyBytes := []byte(key)
		valBytes := []byte(value)

		keySize := uint32(len(keyBytes))
		valSize := uint32(len(valBytes))

		buf := make([]byte, 8+len(keyBytes)+len(valBytes))
		binary.LittleEndian.PutUint32(buf[0:4], keySize)
		binary.LittleEndian.PutUint32(buf[4:8], valSize)
		copy(buf[8:], keyBytes)
		copy(buf[8+len(keyBytes):], valBytes)

		// Use Seek once to get atomic write offset
		pos, err := db.file.Seek(0, 2)
		if err != nil {
			return err
		}

		written, err := db.file.WriteAt(buf, pos)
		if err != nil {
			return err
		}

		if written != len(buf) {
			return fmt.Errorf("short write: wrote %d of %d bytes", written, len(buf))
		}

		// Only add to index after a confirmed successful write
		err = db.index.Add(key, pos)
		if err != nil {
			return err
		}
	}

	// Sync to flush data to disk
	if err := db.file.Sync(); err != nil {
		return err
	}

	db.wal.MarkCommitted()
	if db.wal.ShouldCheckpoint() {
		db.wal.Clear()
	}

	return nil
}

func (db *ShibuDB) Get(key string) (string, error) {
	// Check batch first for read-your-own-writes
	db.batchLock.Lock()
	if val, exists := db.batch[key]; exists {
		db.batchLock.Unlock()
		return val, nil
	}
	db.batchLock.Unlock()

	db.lock.RLock()
	defer db.lock.RUnlock()

	log.Println("Checking index position")
	pos, exists := db.index.Get(key)
	if !exists {
		return "", errors.New("key not found")
	}
	log.Println("Found pos for index: " + strconv.FormatInt(pos, 10))
	fmt.Printf("Found pos for index: " + strconv.FormatInt(pos, 10))

	header := make([]byte, 8)
	_, err := db.file.ReadAt(header, pos)
	if err != nil {
		return "", err
	}

	keySize := binary.LittleEndian.Uint32(header[0:4])
	valSize := binary.LittleEndian.Uint32(header[4:8])

	log.Println("Key size found", keySize)
	log.Println("Val size found", valSize)

	keyBytes := make([]byte, keySize)
	_, err = db.file.ReadAt(keyBytes, pos+8)
	if err != nil {
		log.Println("Error reading key bytes")
		return "", err
	}

	valBytes := make([]byte, valSize)
	_, err = db.file.ReadAt(valBytes, pos+8+int64(keySize))
	if err != nil {
		log.Println("Error reading value bytes")
		return "", err
	}

	log.Println("Key found", string(keyBytes))
	log.Println("Value found", string(valBytes))

	if string(keyBytes) == key {
		value := string(valBytes)
		if value == "__deleted__" {
			return "", errors.New("key is deleted")
		}
		return value, nil
	}
	return "", errors.New("key mismatch at position: " + strconv.FormatInt(pos, 10) + ". Found: " + string(keyBytes) + ". Expected: " + key)
}

func (db *ShibuDB) Delete(key string) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	_, exists := db.index.Get(key)
	if !exists {
		return errors.New("key not found")
	}

	db.index.Remove(key)
	err := db.wal.WriteDelete(key)
	if err != nil {
		return err
	}

	keyBytes := []byte(key)
	keySize := uint32(len(keyBytes))
	valSize := uint32(0)

	buf := make([]byte, 8+len(keyBytes))
	binary.LittleEndian.PutUint32(buf[0:4], keySize)
	binary.LittleEndian.PutUint32(buf[4:8], valSize)
	copy(buf[8:], keyBytes)

	pos, err := db.file.Seek(0, 2)
	if err != nil {
		return err
	}
	_, err = db.file.WriteAt(buf, pos)
	return err
}

func (db *ShibuDB) Close() error {
	db.closeOnce.Do(func() {
		log.Println("Closed.............")
		close(db.quitChan)
		db.FlushBatch()
		db.wal.Clear()
		db.wal.Close()
		db.file.Close()
	})
	return nil
}

func (db *ShibuDB) Put(key, value string) error {
	return db.PutBatch(key, value)
}
