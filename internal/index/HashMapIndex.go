package index

import (
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type HashMapIndex struct {
	data sync.Map

	// mmapLock guards mmapData, writeOffset, and any remap of the file
	// (grow, rewrite). Every access to the mapping must hold it so a
	// concurrent remap cannot unmap memory another goroutine is writing.
	mmapLock    sync.Mutex
	file        *os.File
	mmapData    []byte
	writeOffset int
}

func NewHashMapIndex(filename string) (*HashMapIndex, error) {
	file, mmapData, err := openIndexFile(filename)
	if err != nil {
		return nil, err
	}

	idx := &HashMapIndex{
		file:     file,
		mmapData: mmapData,
	}
	idx.writeOffset = scanIndexEntries(mmapData, func(key string, pos int64) {
		idx.data.Store(key, pos)
	})
	return idx, nil
}

func (idx *HashMapIndex) Add(key string, pos int64) error {
	idx.data.Store(key, pos)

	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()
	return idx.appendEntryLocked(key, pos)
}

func (idx *HashMapIndex) Get(key string) (int64, bool) {
	val, ok := idx.data.Load(key)
	if !ok {
		return 0, false
	}
	return val.(int64), true
}

func (idx *HashMapIndex) SnapshotEntries() map[string]int64 {
	entries := make(map[string]int64)
	idx.data.Range(func(key, value interface{}) bool {
		entries[key.(string)] = value.(int64)
		return true
	})
	return entries
}

func (idx *HashMapIndex) Remove(key string) error {
	if _, ok := idx.data.Load(key); !ok {
		return nil
	}
	idx.data.Delete(key)

	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()
	return idx.rewriteLocked()
}

// Sync flushes the mapped index to disk. Add intentionally does not sync;
// callers batch their writes and sync once.
func (idx *HashMapIndex) Sync() error {
	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()
	return unix.Msync(idx.mmapData, unix.MS_SYNC)
}

func (idx *HashMapIndex) Close() error {
	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()

	if idx.mmapData != nil {
		if err := unix.Msync(idx.mmapData, unix.MS_SYNC); err != nil {
			_ = idx.file.Close()
			return err
		}
		if err := syscall.Munmap(idx.mmapData); err != nil {
			_ = idx.file.Close()
			return err
		}
		idx.mmapData = nil
	}
	return idx.file.Close()
}

func (idx *HashMapIndex) appendEntryLocked(key string, pos int64) error {
	entrySize := indexEntryHeaderSize + len(key)
	if idx.writeOffset+entrySize > len(idx.mmapData) {
		mmapData, err := growIndexFile(idx.file, idx.mmapData, entrySize)
		if err != nil {
			return err
		}
		idx.mmapData = mmapData
	}
	idx.writeOffset = putIndexEntry(idx.mmapData, idx.writeOffset, key, pos)
	return nil
}

// rewriteLocked rebuilds the on-disk log from the current in-memory state.
func (idx *HashMapIndex) rewriteLocked() error {
	mmapData, err := resetIndexFile(idx.file, idx.mmapData)
	if err != nil {
		return err
	}
	idx.mmapData = mmapData
	idx.writeOffset = indexHeaderSize

	var appendErr error
	idx.data.Range(func(key, value interface{}) bool {
		if err := idx.appendEntryLocked(key.(string), value.(int64)); err != nil {
			appendErr = err
			return false
		}
		return true
	})
	if appendErr != nil {
		return appendErr
	}
	return unix.Msync(idx.mmapData, unix.MS_SYNC)
}
