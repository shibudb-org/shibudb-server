package index

import (
	"encoding/binary"
	"github.com/google/btree"
	"golang.org/x/sys/unix"
	"os"
	"sync"
	"syscall"
)

type BTreeIndex struct {
	lock        sync.RWMutex
	mmapLock    sync.Mutex
	btree       *btree.BTree
	file        *os.File
	mmapData    []byte
	writeOffset int // Track where to write next
}

type Item struct {
	Key   string
	Value int64
}

func (i Item) Less(other btree.Item) bool {
	return i.Key < other.(Item).Key
}

func NewBTreeIndex(filename string) (*BTreeIndex, error) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}

	size, err := file.Seek(0, 2)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = 4096
		file.Truncate(size)
	}

	mmapData, err := syscall.Mmap(int(file.Fd()), 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	idx := &BTreeIndex{
		btree:    btree.New(2),
		file:     file,
		mmapData: mmapData,
	}

	idx.writeOffset = idx.BatchLoadFromMmap()
	return idx, nil
}

func (idx *BTreeIndex) BatchLoadFromMmap() int {
	idx.lock.Lock()
	idx.mmapLock.Lock()
	defer idx.lock.Unlock()
	defer idx.mmapLock.Unlock()

	offset := 0
	for offset+8 <= len(idx.mmapData) {
		keySize := binary.LittleEndian.Uint32(idx.mmapData[offset : offset+4])
		pos := binary.LittleEndian.Uint32(idx.mmapData[offset+4 : offset+8])
		offset += 8

		if offset+int(keySize) > len(idx.mmapData) {
			break
		}

		key := string(idx.mmapData[offset : offset+int(keySize)])
		offset += int(keySize)

		idx.btree.ReplaceOrInsert(Item{Key: key, Value: int64(pos)})
	}
	return offset
}

func (idx *BTreeIndex) Add(key string, pos int64) error {
	idx.lock.Lock()
	defer idx.lock.Unlock()

	idx.btree.ReplaceOrInsert(Item{Key: key, Value: pos})
	return idx.appendIndexEntry(key, pos)
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

func (idx *BTreeIndex) Remove(key string) error {
	idx.lock.Lock()
	defer idx.lock.Unlock()

	item := idx.btree.Delete(Item{Key: key})
	if item == nil {
		return nil
	}
	return idx.persistIndex()
}

func (idx *BTreeIndex) persistIndex() error {
	if err := syscall.Munmap(idx.mmapData); err != nil {
		return err
	}
	if err := idx.file.Truncate(0); err != nil {
		return err
	}
	if err := idx.file.Truncate(4096); err != nil {
		return err
	}

	mmapData, err := syscall.Mmap(int(idx.file.Fd()), 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	idx.mmapData = mmapData
	idx.writeOffset = 0

	idx.btree.Ascend(func(i btree.Item) bool {
		item := i.(Item)
		_ = idx.appendIndexEntry(item.Key, item.Value)
		return true
	})
	return unix.Msync(idx.mmapData, unix.MS_SYNC)
}

func (idx *BTreeIndex) appendIndexEntry(key string, pos int64) error {
	keyBytes := []byte(key)
	keySize := uint32(len(keyBytes))
	entrySize := 8 + len(keyBytes)

	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()

	// Ensure enough space in mmap
	if idx.writeOffset+entrySize > len(idx.mmapData) {
		newSize := int64(len(idx.mmapData)*2 + entrySize + 4096)
		if err := syscall.Munmap(idx.mmapData); err != nil {
			return err
		}
		if err := idx.file.Truncate(newSize); err != nil {
			return err
		}
		mmapData, err := syscall.Mmap(int(idx.file.Fd()), 0, int(newSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			return err
		}
		idx.mmapData = mmapData
	}

	// Safe write
	offset := idx.writeOffset
	binary.LittleEndian.PutUint32(idx.mmapData[offset:offset+4], keySize)
	binary.LittleEndian.PutUint32(idx.mmapData[offset+4:offset+8], uint32(pos))
	copy(idx.mmapData[offset+8:offset+8+int(keySize)], keyBytes)

	idx.writeOffset += entrySize

	// Optional: sync to make data visible to all threads immediately
	if err := unix.Msync(idx.mmapData, unix.MS_SYNC); err != nil {
		return err
	}

	return nil
}

func (idx *BTreeIndex) Close() error {
	return syscall.Munmap(idx.mmapData)
}
