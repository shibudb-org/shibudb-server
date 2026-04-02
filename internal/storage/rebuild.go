package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DataIntelligenceCrew/go-faiss"
	kvindex "github.com/shibudb.org/shibudb-server/internal/index"
)

const (
	vectorRebuildBatchSize   = 1024
	vectorRebuildTrainSample = 10000
)

type KeyValueRebuildStats struct {
	RecordsScanned int
	LiveKeys       int
	IndexPath      string
}

type VectorRebuildStats struct {
	RecordsScanned  int
	LiveVectors     int
	TombstonedIDs   int
	TrainingSamples int
	IndexPath       string
}

type vectorRecordLocation struct {
	id     int64
	offset int64
}

// RebuildKeyValueIndex reconstructs a key-value index file from the append-only
// data file. The latest record wins; records with zero-length values are
// treated as deletions.
func RebuildKeyValueIndex(dataPath, indexPath string) (KeyValueRebuildStats, error) {
	stats := KeyValueRebuildStats{IndexPath: indexPath}

	dataFile, err := os.Open(dataPath)
	if err != nil {
		return stats, fmt.Errorf("open data file: %w", err)
	}
	defer dataFile.Close()

	latestOffsets := make(map[string]int64)
	var offset int64

	for {
		recordOffset := offset
		header := make([]byte, 8)
		n, err := io.ReadFull(dataFile, header)
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
			break
		}
		if err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("read record header at offset %d: %w", recordOffset, err)
		}

		keySize := binary.LittleEndian.Uint32(header[0:4])
		valSize := binary.LittleEndian.Uint32(header[4:8])

		keyBytes := make([]byte, keySize)
		if _, err := io.ReadFull(dataFile, keyBytes); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return stats, fmt.Errorf("read key at offset %d: %w", recordOffset, err)
		}

		if _, err := io.CopyN(io.Discard, dataFile, int64(valSize)); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return stats, fmt.Errorf("skip value at offset %d: %w", recordOffset, err)
		}

		stats.RecordsScanned++
		offset += int64(8) + int64(keySize) + int64(valSize)

		key := string(keyBytes)
		if valSize == 0 {
			delete(latestOffsets, key)
			continue
		}
		latestOffsets[key] = recordOffset
	}

	stats.LiveKeys = len(latestOffsets)
	if err := writeKeyValueIndex(indexPath, latestOffsets); err != nil {
		return stats, err
	}
	return stats, nil
}

// RebuildVectorIndex reconstructs a FAISS index file from the append-only
// vector data file. The latest record wins; tombstones are excluded.
func RebuildVectorIndex(dataPath, indexPath string, dimension int, indexDesc string, metric int) (VectorRebuildStats, error) {
	stats := VectorRebuildStats{IndexPath: indexPath}
	if dimension <= 0 {
		return stats, fmt.Errorf("invalid vector dimension %d", dimension)
	}

	dataFile, err := os.Open(dataPath)
	if err != nil {
		return stats, fmt.Errorf("open vector data file: %w", err)
	}
	defer dataFile.Close()

	recordSize := 8 + 4*dimension
	latestOffsets := make(map[int64]int64)
	var offset int64

	for {
		buf := make([]byte, recordSize)
		n, err := dataFile.Read(buf)
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
			break
		}
		if err == io.ErrUnexpectedEOF || n < recordSize {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("read vector data at offset %d: %w", offset, err)
		}

		id := int64(binary.LittleEndian.Uint64(buf[0:8]))
		latestOffsets[id] = offset
		stats.RecordsScanned++
		offset += int64(recordSize)
	}

	locations := make([]vectorRecordLocation, 0, len(latestOffsets))
	for id, off := range latestOffsets {
		locations = append(locations, vectorRecordLocation{id: id, offset: off})
	}
	sort.Slice(locations, func(i, j int) bool { return locations[i].offset < locations[j].offset })

	requiredTrainCount := requiredTrainCountForIndex(indexDesc)
	targetTrainingSamples := trainingSampleCount(requiredTrainCount, len(locations))

	live := make([]vectorRecordLocation, 0, len(locations))
	trainData := make([]float32, 0, targetTrainingSamples*dimension)
	for _, loc := range locations {
		_, vec, deleted, err := readVectorRecordAt(dataFile, loc.offset, dimension)
		if err != nil {
			return stats, err
		}
		if deleted {
			stats.TombstonedIDs++
			continue
		}
		live = append(live, loc)
		if len(trainData)/dimension < targetTrainingSamples {
			trainData = append(trainData, vec...)
		}
	}

	stats.LiveVectors = len(live)
	stats.TrainingSamples = len(trainData) / dimension
	if requiredTrainCount > 0 && stats.LiveVectors > 0 && stats.LiveVectors < requiredTrainCount {
		return stats, fmt.Errorf(
			"cannot rebuild %q index: need at least %d live vectors for training, found %d",
			indexDesc, requiredTrainCount, stats.LiveVectors,
		)
	}

	index, err := faiss.IndexFactory(dimension, "IDMap,"+indexDesc, metric)
	if err != nil {
		return stats, fmt.Errorf("create FAISS index: %w", err)
	}

	if requiredTrainCount > 0 && stats.LiveVectors > 0 {
		if err := index.Train(trainData); err != nil {
			return stats, fmt.Errorf("train FAISS index: %w", err)
		}
	}

	for start := 0; start < len(live); start += vectorRebuildBatchSize {
		end := start + vectorRebuildBatchSize
		if end > len(live) {
			end = len(live)
		}

		ids := make([]int64, 0, end-start)
		data := make([]float32, 0, (end-start)*dimension)
		for _, loc := range live[start:end] {
			id, vec, deleted, err := readVectorRecordAt(dataFile, loc.offset, dimension)
			if err != nil {
				return stats, err
			}
			if deleted {
				continue
			}
			ids = append(ids, id)
			data = append(data, vec...)
		}
		if len(ids) == 0 {
			continue
		}
		if err := index.AddWithIDs(data, ids); err != nil {
			return stats, fmt.Errorf("add vectors to FAISS index: %w", err)
		}
	}

	if err := writeFaissIndex(index, indexPath); err != nil {
		return stats, err
	}
	return stats, nil
}

func writeKeyValueIndex(indexPath string, latestOffsets map[string]int64) error {
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}

	tmpPath := indexPath + ".rebuild.tmp"
	if err := os.RemoveAll(tmpPath); err != nil {
		return fmt.Errorf("remove old temp index: %w", err)
	}

	idx, err := kvindex.NewBTreeIndex(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp index: %w", err)
	}

	for key, pos := range latestOffsets {
		if err := idx.Add(key, pos); err != nil {
			return fmt.Errorf("write key %q to temp index: %w", key, err)
		}
	}
	if err := idx.Close(); err != nil {
		return fmt.Errorf("close temp index: %w", err)
	}

	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old index: %w", err)
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		return fmt.Errorf("install rebuilt index: %w", err)
	}
	return nil
}

func writeFaissIndex(index faiss.Index, indexPath string) error {
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return fmt.Errorf("create vector index dir: %w", err)
	}

	tmpPath := indexPath + ".rebuild.tmp"
	if err := os.RemoveAll(tmpPath); err != nil {
		return fmt.Errorf("remove old temp FAISS index: %w", err)
	}
	if err := faiss.WriteIndex(index, tmpPath); err != nil {
		return fmt.Errorf("write rebuilt FAISS index: %w", err)
	}
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old FAISS index: %w", err)
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		return fmt.Errorf("install rebuilt FAISS index: %w", err)
	}
	return nil
}

func readVectorRecordAt(file *os.File, offset int64, dimension int) (int64, []float32, bool, error) {
	recordSize := 8 + 4*dimension
	buf := make([]byte, recordSize)
	if _, err := file.ReadAt(buf, offset); err != nil {
		return 0, nil, false, fmt.Errorf("read vector at offset %d: %w", offset, err)
	}

	id := int64(binary.LittleEndian.Uint64(buf[0:8]))
	if len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarker {
		return id, nil, true, nil
	}

	vec, err := bytesToFloat32Array(buf[8:])
	if err != nil {
		return 0, nil, false, fmt.Errorf("decode vector at offset %d: %w", offset, err)
	}
	return id, vec, false, nil
}

func requiredTrainCountForIndex(indexDesc string) int {
	if indexDesc == "Flat" || strings.HasPrefix(indexDesc, "HNSW") {
		return 0
	}

	nlist := 0
	if strings.HasPrefix(indexDesc, "IVF") {
		fmt.Sscanf(indexDesc, "IVF%d", &nlist)
	}

	required := nlist
	if strings.Contains(indexDesc, "PQ") && required < 256 {
		required = 256
	}
	return required
}

func trainingSampleCount(requiredTrainCount, totalVectors int) int {
	if requiredTrainCount == 0 || totalVectors == 0 {
		return 0
	}

	target := vectorRebuildTrainSample
	if target < requiredTrainCount {
		target = requiredTrainCount
	}
	if totalVectors < target {
		return totalVectors
	}
	return target
}
