package wal

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
)

type WAL struct {
	file *os.File
	lock sync.Mutex
}

func OpenWAL(filename string) (*WAL, error) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666) // Remove O_APPEND
	if err != nil {
		return nil, err
	}
	return &WAL{file: file}, nil
}

func (w *WAL) WriteEntry(key, value string) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	keyBytes := []byte(key)
	valBytes := []byte(value)

	keySize := uint32(len(keyBytes))
	valSize := uint32(len(valBytes))

	buf := make([]byte, 9+len(keyBytes)+len(valBytes)) // Extra byte for commit flag
	binary.LittleEndian.PutUint32(buf[0:4], keySize)
	binary.LittleEndian.PutUint32(buf[4:8], valSize)
	buf[8] = 'P' // 'P' means pending commit
	copy(buf[9:9+len(keyBytes)], keyBytes)
	copy(buf[9+len(keyBytes):], valBytes)

	_, err := w.file.Write(buf)
	if err != nil {
		return err
	}

	// Force sync to ensure data is written before unlocking
	return w.file.Sync()
}

func (w *WAL) WriteDelete(key string) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	keyBytes := []byte(key)
	keySize := uint32(len(keyBytes))

	buf := make([]byte, 9+len(keyBytes))
	binary.LittleEndian.PutUint32(buf[0:4], keySize)
	binary.LittleEndian.PutUint32(buf[4:8], 0) // value size 0
	buf[8] = 'D'                               // 'D' means delete
	copy(buf[9:], keyBytes)

	_, err := w.file.Write(buf)
	if err != nil {
		return err
	}

	return w.file.Sync()
}

func (w *WAL) MarkCommitted() error {
	w.lock.Lock()
	defer w.lock.Unlock()

	_, err := w.file.Seek(8, io.SeekStart)
	if err != nil {
		return err
	}

	commitByte := []byte{'C'}
	_, err = w.file.Write(commitByte)
	if err != nil {
		return err
	}

	return w.file.Sync() // Ensure changes are flushed
}

func (w *WAL) Replay() ([][2]string, error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	_, err := w.file.Seek(0, io.SeekStart) // Ensure we start at the absolute beginning
	if err != nil {
		return nil, err
	}

	var entries [][2]string
	for {
		header := make([]byte, 9)
		_, err := io.ReadFull(w.file, header)
		if err == io.EOF {
			break // Properly handle EOF
		} else if err != nil {
			return nil, err
		}

		keySize := binary.LittleEndian.Uint32(header[0:4])
		valSize := binary.LittleEndian.Uint32(header[4:8])
		commitFlag := header[8]

		if commitFlag == 'C' {
			continue // Skip already committed transactions
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

		entries = append(entries, [2]string{string(keyBytes), string(valBytes)})
	}
	return entries, nil
}

func (w *WAL) Clear() error {
	w.lock.Lock()
	defer w.lock.Unlock()
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
