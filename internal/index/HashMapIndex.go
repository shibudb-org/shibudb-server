package index

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"os"
	"sync"
	"syscall"
)

type HashMapIndex struct {
	data        sync.Map
	mmapLock    sync.Mutex
	file        *os.File
	mmapData    []byte
	writeOffset int
}

func NewHashMapIndex(filename string) (*HashMapIndex, error) {
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

	idx := &HashMapIndex{
		file:     file,
		mmapData: mmapData,
	}

	idx.writeOffset = idx.BatchLoadFromMmap()
	return idx, nil
}

func (idx *HashMapIndex) BatchLoadFromMmap() int {
	idx.mmapLock.Lock()
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

		if key != "" {
			idx.data.Store(key, int64(pos))
		}
	}
	return offset
}

func (idx *HashMapIndex) Add(key string, pos int64) error {
	idx.data.Store(key, pos) // sync.Map handles locking intrinsically
	return idx.appendIndexEntry(key, pos)
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
	_, ok := idx.data.Load(key)
	if !ok {
		return nil
	}
	idx.data.Delete(key)

	return idx.persistIndex()
}

func (idx *HashMapIndex) persistIndex() error {
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

	idx.data.Range(func(key, value interface{}) bool {
		_ = idx.appendIndexEntry(key.(string), value.(int64))
		return true
	})
	return unix.Msync(idx.mmapData, unix.MS_SYNC)
}

func (idx *HashMapIndex) appendIndexEntry(key string, pos int64) error {
	keyBytes := []byte(key)
	keySize := uint32(len(keyBytes))
	entrySize := 8 + len(keyBytes)

	idx.mmapLock.Lock()
	defer idx.mmapLock.Unlock()

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

	offset := idx.writeOffset
	binary.LittleEndian.PutUint32(idx.mmapData[offset:offset+4], keySize)
	binary.LittleEndian.PutUint32(idx.mmapData[offset+4:offset+8], uint32(pos))
	copy(idx.mmapData[offset+8:offset+8+int(keySize)], keyBytes)

	idx.writeOffset += entrySize

	if err := unix.Msync(idx.mmapData, unix.MS_SYNC); err != nil {
		return err
	}

	return nil
}

func (idx *HashMapIndex) Close() error {
	return syscall.Munmap(idx.mmapData)
}
