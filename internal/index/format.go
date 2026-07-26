package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// On-disk key-value index format:
//
//	header:  magic (u32) | version (u32)
//	entry:   keySize (u32) | pos (u64) | key bytes
//
// A keySize of 0 marks the end of the log; the zero-filled tail of the
// mmap-extended file provides it implicitly. Positions are 8 bytes so data
// files larger than 4 GiB index correctly.
const (
	indexMagic           uint32 = 0x53424958 // "SBIX"
	indexFormatVersion   uint32 = 1
	indexHeaderSize             = 8
	indexEntryHeaderSize        = 12
	indexInitialFileSize        = int64(4096)
)

var errBadIndexHeader = errors.New("unrecognized key-value index file format")

// openIndexFile opens (creating if needed) an index file, validates or writes
// the format header, and memory-maps the whole file.
func openIndexFile(filename string) (*os.File, []byte, error) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, nil, err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}

	size := info.Size()
	newFile := size == 0
	if size < indexInitialFileSize {
		size = indexInitialFileSize
		if err := file.Truncate(size); err != nil {
			_ = file.Close()
			return nil, nil, err
		}
	}

	mmapData, err := syscall.Mmap(int(file.Fd()), 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}

	if newFile {
		writeIndexHeader(mmapData)
	} else if err := checkIndexHeader(mmapData); err != nil {
		_ = syscall.Munmap(mmapData)
		_ = file.Close()
		return nil, nil, fmt.Errorf("%s: %w", filename, err)
	}
	return file, mmapData, nil
}

func writeIndexHeader(mmapData []byte) {
	binary.LittleEndian.PutUint32(mmapData[0:4], indexMagic)
	binary.LittleEndian.PutUint32(mmapData[4:8], indexFormatVersion)
}

func checkIndexHeader(mmapData []byte) error {
	if len(mmapData) < indexHeaderSize {
		return errBadIndexHeader
	}
	if binary.LittleEndian.Uint32(mmapData[0:4]) != indexMagic {
		return errBadIndexHeader
	}
	if version := binary.LittleEndian.Uint32(mmapData[4:8]); version != indexFormatVersion {
		return fmt.Errorf("%w: unsupported version %d", errBadIndexHeader, version)
	}
	return nil
}

// scanIndexEntries walks entries from the header to the end-of-log marker
// (or a truncated tail) and returns the offset where the next entry should
// be appended.
func scanIndexEntries(mmapData []byte, visit func(key string, pos int64)) int {
	offset := indexHeaderSize
	for offset+indexEntryHeaderSize <= len(mmapData) {
		keySize := binary.LittleEndian.Uint32(mmapData[offset : offset+4])
		if keySize == 0 {
			break
		}
		if int(keySize) > len(mmapData)-offset-indexEntryHeaderSize {
			break
		}
		pos := binary.LittleEndian.Uint64(mmapData[offset+4 : offset+12])
		entryEnd := offset + indexEntryHeaderSize + int(keySize)
		visit(string(mmapData[offset+indexEntryHeaderSize:entryEnd]), int64(pos))
		offset = entryEnd
	}
	return offset
}

// putIndexEntry writes one entry at offset and returns the next write offset.
// The caller must have ensured capacity.
func putIndexEntry(mmapData []byte, offset int, key string, pos int64) int {
	binary.LittleEndian.PutUint32(mmapData[offset:offset+4], uint32(len(key)))
	binary.LittleEndian.PutUint64(mmapData[offset+4:offset+12], uint64(pos))
	copy(mmapData[offset+indexEntryHeaderSize:], key)
	return offset + indexEntryHeaderSize + len(key)
}

// growIndexFile remaps the file with enough room for at least `needed` more
// bytes. The previous mapping is unmapped; the caller must replace its
// reference with the returned mapping.
func growIndexFile(file *os.File, mmapData []byte, needed int) ([]byte, error) {
	newSize := int64(len(mmapData)*2 + needed + int(indexInitialFileSize))
	if err := syscall.Munmap(mmapData); err != nil {
		return nil, err
	}
	if err := file.Truncate(newSize); err != nil {
		return nil, err
	}
	return syscall.Mmap(int(file.Fd()), 0, int(newSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
}

// resetIndexFile truncates the file back to its initial size, remaps it, and
// writes a fresh header. Used when rewriting the log after a removal.
func resetIndexFile(file *os.File, mmapData []byte) ([]byte, error) {
	if err := syscall.Munmap(mmapData); err != nil {
		return nil, err
	}
	if err := file.Truncate(0); err != nil {
		return nil, err
	}
	if err := file.Truncate(indexInitialFileSize); err != nil {
		return nil, err
	}
	newData, err := syscall.Mmap(int(file.Fd()), 0, int(indexInitialFileSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	writeIndexHeader(newData)
	return newData, nil
}
