package index

import (
	"os"
	"sync"
	"syscall"

	"github.com/google/btree"
	"golang.org/x/sys/unix"
)

type BTreeIndex struct {
	// lock guards the btree. Lock ordering: lock before mmapLock.
	lock  sync.RWMutex
	btree *btree.BTree

	// mmapLock guards mmapData, writeOffset, and any remap of the file
	// (grow, rewrite). Every access to the mapping must hold it so a
	// concurrent remap cannot unmap memory another goroutine is writing.
	mmapLock    sync.Mutex
	file        *os.File
	mmapData    []byte
	writeOffset int
}

type Item struct {
	Key   string
	Value int64
}

func (i Item) Less(other btree.Item) bool {
	return i.Key < other.(Item).Key
}

func NewBTreeIndex(filename string) (*BTreeIndex, error) {
	file, mmapData, err := openIndexFile(filename)
	if err != nil {
		return nil, err
	}

	idx := &BTreeIndex{
		btree:    btree.New(2),
		file:     file,
		mmapData: mmapData,
	}
	idx.writeOffset = scanIndexEntries(mmapData, func(key string, pos int64) {
		idx.btree.ReplaceOrInsert(Item{Key: key, Value: pos})
	})
	return idx, nil
}

func (idx *BTreeIndex) Add(key string, pos int64) error {
	idx.lock.Lock()
	defer idx.lock.Unlock()

	idx.btree.ReplaceOrInsert(Item{Key: key, Value: pos})

	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()
	return idx.appendEntryLocked(key, pos)
}

func (idx *BTreeIndex) Get(key string) (int64, bool) {
	idx.lock.RLock()
	defer idx.lock.RUnlock()

	item := idx.btree.Get(Item{Key: key})
	if item == nil {
		return 0, false
	}
	return item.(Item).Value, true
}

func (idx *BTreeIndex) SnapshotEntries() map[string]int64 {
	idx.lock.RLock()
	defer idx.lock.RUnlock()

	entries := make(map[string]int64)
	idx.btree.Ascend(func(i btree.Item) bool {
		item := i.(Item)
		entries[item.Key] = item.Value
		return true
	})
	return entries
}

func (idx *BTreeIndex) Remove(key string) error {
	idx.lock.Lock()
	defer idx.lock.Unlock()

	if item := idx.btree.Delete(Item{Key: key}); item == nil {
		return nil
	}

	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()
	return idx.rewriteLocked()
}

// Sync flushes the mapped index to disk. Add intentionally does not sync;
// callers batch their writes and sync once.
func (idx *BTreeIndex) Sync() error {
	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()
	return unix.Msync(idx.mmapData, unix.MS_SYNC)
}

func (idx *BTreeIndex) Close() error {
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

func (idx *BTreeIndex) appendEntryLocked(key string, pos int64) error {
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
// Caller must hold both lock (for btree iteration) and mmapLock.
func (idx *BTreeIndex) rewriteLocked() error {
	mmapData, err := resetIndexFile(idx.file, idx.mmapData)
	if err != nil {
		return err
	}
	idx.mmapData = mmapData
	idx.writeOffset = indexHeaderSize

	var appendErr error
	idx.btree.Ascend(func(i btree.Item) bool {
		item := i.(Item)
		if err := idx.appendEntryLocked(item.Key, item.Value); err != nil {
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
