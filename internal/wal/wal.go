package wal

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
)

type WAL struct {
	file                *os.File
	lock                sync.Mutex
	oldestPendingOffset int64
}

type WALEntry struct {
	Key   string
	Value string
	Flag  byte
}

const (
	EntryPending   byte = 'P'
	EntryCommitted byte = 'C'
	EntryDeleted   byte = 'D'
)

func OpenWAL(filename string) (*WAL, error) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666) // Remove O_APPEND
	if err != nil {
		return nil, err
	}
	return &WAL{file: file, oldestPendingOffset: -1}, nil
}

func (w *WAL) WriteEntry(key, value string) error {

	w.lock.Lock()
	defer w.lock.Unlock()
	offset, err := w.file.Seek(0, io.SeekEnd) // Ensure we start at the absolute end
	if err != nil {
		return err
	}

	keyBytes := []byte(key)
	valBytes := []byte(value)

	keySize := uint32(len(keyBytes))
	valSize := uint32(len(valBytes))

	buf := make([]byte, 9+len(keyBytes)+len(valBytes)) // Extra byte for commit flag
	binary.LittleEndian.PutUint32(buf[0:4], keySize)
	binary.LittleEndian.PutUint32(buf[4:8], valSize)
	buf[8] = EntryPending
	copy(buf[9:9+len(keyBytes)], keyBytes)
	copy(buf[9+len(keyBytes):], valBytes)

	_, err = w.file.Write(buf)
	if err != nil {
		return err
	}

	// Force sync to ensure data is written before unlocking
	err = w.file.Sync()
	if err != nil {
		return err
	}

	if w.oldestPendingOffset < 0 {
		w.oldestPendingOffset = offset
	}
	return nil
}

// Utility to read the WAL entry header starting at the current offset in the file.
// It is not thread-safe and must be called from a thread safe context.
func (w *WAL) readHeader() (keySize uint32, valSize uint32, commitFlag byte, err error) {
	header := make([]byte, 9)
	_, err = io.ReadFull(w.file, header)
	if err != nil {
		return
	}
	keySize = binary.LittleEndian.Uint32(header[0:4])
	valSize = binary.LittleEndian.Uint32(header[4:8])
	commitFlag = header[8]
	return
}

func (w *WAL) WriteDelete(key string) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	offset, err := w.file.Seek(0, io.SeekEnd) // Ensure we start at the absolute end
	if err != nil {
		return err
	}

	keyBytes := []byte(key)
	keySize := uint32(len(keyBytes))

	buf := make([]byte, 9+len(keyBytes))
	binary.LittleEndian.PutUint32(buf[0:4], keySize)
	binary.LittleEndian.PutUint32(buf[4:8], 0) // value size 0
	buf[8] = EntryDeleted
	copy(buf[9:], keyBytes)

	_, err = w.file.Write(buf)
	if err != nil {
		return err
	}

	err = w.file.Sync()
	if err != nil {
		return err
	}

	if w.oldestPendingOffset < 0 {
		w.oldestPendingOffset = offset
	}
	return nil
}

func (w *WAL) MarkCommitted() error {

	w.lock.Lock()
	defer w.lock.Unlock()

	if w.oldestPendingOffset < 0 {
		return nil
	}

	commitByte := []byte{EntryCommitted}
	offset := w.oldestPendingOffset
	for offset >= 0 {
		_, err := w.file.Seek(offset, io.SeekStart)
		if err != nil {
			return err
		}
		keySize, valSize, _, err := w.readHeader()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		_, err = w.file.Seek(-1, io.SeekCurrent)
		if err != nil {
			return err
		}
		_, err = w.file.Write(commitByte)
		offset += 9 + int64(keySize) + int64(valSize)
	}
	err := w.file.Sync()
	if err != nil {
		return err
	}
	// All pending entries have been commited
	w.oldestPendingOffset = -1
	return nil
}

func (w *WAL) Replay() ([]*WALEntry, error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	var entries []*WALEntry

	_, err := w.file.Seek(0, io.SeekStart) // Ensure we start at the absolute beginning
	if err != nil {
		return nil, err
	}

	for {
		keySize, valSize, commitFlag, err := w.readHeader()

		if err == io.EOF {
			break // Properly handle EOF
		} else if err != nil {
			return nil, err
		}

		if commitFlag == EntryCommitted {
			// Skip bytes for key and value since the transaction is commited
			_, err := w.file.Seek(int64(keySize)+int64(valSize), io.SeekCurrent)
			if err != nil {
				return nil, err
			}
			continue
		}

		keyBytes := make([]byte, keySize)
		_, err = io.ReadFull(w.file, keyBytes)
		if err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}

		valBytes := make([]byte, valSize)
		_, err = io.ReadFull(w.file, valBytes)
		if err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, &WALEntry{
			Key:   string(keyBytes),
			Value: string(valBytes),
			Flag:  commitFlag,
		})
	}
	return entries, nil
}

func (w *WAL) Clear() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.oldestPendingOffset = -1
	return os.Truncate(w.file.Name(), 0)
}

func (w *WAL) ShouldCheckpoint() bool {
	info, err := w.file.Stat()
	if err != nil {
		return false
	}
	return info.Size() > 1024*1024 // 1MB threshold for checkpointing
}

func (w *WAL) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.file.Close()
}
