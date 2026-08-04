package spaces

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RestoreStats tracks progress counters for restore.
type RestoreStats struct {
	SpacesRestored int
	KVRecords      int
	VectorRecords  int
}

// RestoreAll reads a JSON-lines dump from r and restores spaces into baseDir.
// mode is either "overwrite" or "merge".
func RestoreAll(baseDir string, r io.Reader, mode string) (RestoreStats, error) {
	var stats RestoreStats

	if mode != "overwrite" && mode != "merge" {
		return stats, fmt.Errorf("invalid restore mode %q: must be \"overwrite\" or \"merge\"", mode)
	}

	dec := json.NewDecoder(r)

	// Read header.
	var header DumpHeader
	if err := dec.Decode(&header); err != nil {
		return stats, fmt.Errorf("read dump header: %w", err)
	}
	if header.Version != DumpFormatVersion {
		return stats, fmt.Errorf("unsupported dump format version %d (expected %d)", header.Version, DumpFormatVersion)
	}

	// Process records line by line.
	var currentMeta *spaceMeta
	var kvBuf map[string]string   // accumulated KV records for current space
	var vecBuf []DumpVectorRecord // accumulated vector records for current space

	flush := func() error {
		if currentMeta == nil {
			return nil
		}
		nKV, nVec, err := restoreSpace(baseDir, *currentMeta, kvBuf, vecBuf, mode)
		if err != nil {
			return err
		}
		stats.SpacesRestored++
		stats.KVRecords += nKV
		stats.VectorRecords += nVec
		currentMeta = nil
		kvBuf = nil
		vecBuf = nil
		return nil
	}

	for dec.More() {
		// Peek at record_type to determine the type.
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return stats, fmt.Errorf("read dump record: %w", err)
		}

		var peek struct {
			RecordType string `json:"record_type"`
		}
		if err := json.Unmarshal(raw, &peek); err != nil {
			return stats, fmt.Errorf("peek record type: %w", err)
		}

		switch peek.RecordType {
		case "space_meta":
			// Flush previous space.
			if err := flush(); err != nil {
				return stats, err
			}
			var sm DumpSpaceMeta
			if err := json.Unmarshal(raw, &sm); err != nil {
				return stats, fmt.Errorf("dump appears corrupted or malformed: invalid space_meta record: %w", err)
			}
			if sm.Name == "" || sm.EngineType == "" {
				return stats, fmt.Errorf("dump appears corrupted or malformed: space_meta missing required fields")
			}
			currentMeta = &sm.spaceMeta
			kvBuf = make(map[string]string)
			vecBuf = nil

		case "kv":
			if currentMeta == nil || kvBuf == nil {
				return stats, fmt.Errorf("dump appears corrupted or malformed: found kv record before space_meta")
			}
			var rec DumpKVRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return stats, fmt.Errorf("dump appears corrupted or malformed: invalid kv record: %w", err)
			}
			kvBuf[rec.Key] = rec.Value

		case "vector":
			if currentMeta == nil {
				return stats, fmt.Errorf("dump appears corrupted or malformed: found vector record before space_meta")
			}
			var rec DumpVectorRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return stats, fmt.Errorf("dump appears corrupted or malformed: invalid vector record: %w", err)
			}
			vecBuf = append(vecBuf, rec)

		default:
			return stats, fmt.Errorf("dump appears corrupted or malformed: unknown record type %q", peek.RecordType)
		}
	}

	// Flush last space.
	if err := flush(); err != nil {
		return stats, err
	}

	return stats, nil
}

// restoreSpace recreates a single space from dump data.
func restoreSpace(baseDir string, meta spaceMeta, kvData map[string]string, vecData []DumpVectorRecord, mode string) (int, int, error) {
	spacePath := filepath.Join(baseDir, meta.Name)

	if mode == "overwrite" {
		// Remove existing space directory if present.
		if _, err := os.Stat(spacePath); err == nil {
			if err := os.RemoveAll(spacePath); err != nil {
				return 0, 0, fmt.Errorf("remove existing space %q: %w", meta.Name, err)
			}
		}
	}

	if err := os.MkdirAll(spacePath, 0755); err != nil {
		return 0, 0, fmt.Errorf("create space dir %q: %w", meta.Name, err)
	}

	// Write space metadata file.
	metaData, err := json.MarshalIndent(normalizeSpaceMeta(meta), "", "  ")
	if err != nil {
		return 0, 0, fmt.Errorf("marshal space meta: %w", err)
	}
	metaData = append(metaData, '\n')
	metaPath := filepath.Join(spacePath, spaceMetaFileName)
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		return 0, 0, fmt.Errorf("write space meta: %w", err)
	}

	switch meta.EngineType {
	case "key-value":
		n, err := restoreKeyValueData(spacePath, meta, kvData, mode)
		return n, 0, err
	case "vector":
		if meta.IndexType == "Flat" && len(meta.IndexedMetadataFields) > 0 {
			n, err := restoreFlatMetaVectorData(spacePath, meta, vecData, mode)
			return 0, n, err
		}
		n, err := restoreVectorData(spacePath, meta, vecData, mode)
		return 0, n, err
	default:
		return 0, 0, fmt.Errorf("unknown engine type %q", meta.EngineType)
	}
}

// restoreKeyValueData writes KV data into the primary data file.
func restoreKeyValueData(spacePath string, meta spaceMeta, data map[string]string, mode string) (int, error) {
	dataPath := filepath.Join(spacePath, "data.db")

	if mode == "merge" {
		// Read existing data from all segments first.
		existing := make(map[string]string)
		existingDel := make(map[string]bool)
		paths, err := collectKVDataPaths(spacePath)
		if err == nil {
			for _, path := range paths {
				_ = scanKVDataFile(path, existing, existingDel)
			}
		}
		// Merge: dump data wins for conflicts.
		for k, v := range data {
			existing[k] = v
			existingDel[k] = false
		}
		data = make(map[string]string)
		for k, v := range existing {
			if !existingDel[k] {
				data[k] = v
			}
		}
	}

	file, err := os.OpenFile(dataPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := data[key]
		keyBytes := []byte(key)
		valBytes := []byte(value)
		buf := make([]byte, 8+len(keyBytes)+len(valBytes))
		binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keyBytes)))
		binary.LittleEndian.PutUint32(buf[4:8], uint32(len(valBytes)))
		copy(buf[8:], keyBytes)
		copy(buf[8+len(keyBytes):], valBytes)
		if _, err := file.Write(buf); err != nil {
			return 0, err
		}
	}

	if err := file.Sync(); err != nil {
		return 0, err
	}

	// Remove stale index/manifest/WAL files; the engine rebuilds them on open.
	cleanupAuxFiles(spacePath, []string{
		"index.dat",
		"wal.db",
		"data_segments.manifest.json",
	})

	return len(data), nil
}

// restoreVectorData writes vector data into the primary data file.
func restoreVectorData(spacePath string, meta spaceMeta, data []DumpVectorRecord, mode string) (int, error) {
	dataPath := filepath.Join(spacePath, "vector_data.db")
	dimension := meta.Dimension

	if mode == "merge" {
		// Read existing live vectors.
		existingVecs := make(map[int64][]float32)
		if paths, err := collectVectorDataPaths(spacePath); err == nil {
			for _, path := range paths {
				readExistingVectors(path, dimension, existingVecs)
			}
		}
		// Dump data wins.
		for _, rec := range data {
			existingVecs[rec.ID] = rec.Vector
		}
		data = nil
		for id, vec := range existingVecs {
			data = append(data, DumpVectorRecord{ID: id, Vector: vec})
		}
		sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	}

	file, err := os.OpenFile(dataPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	for _, rec := range data {
		if len(rec.Vector) != dimension {
			return 0, fmt.Errorf("vector ID %d has dimension mismatch: expected %d, got %d", rec.ID, dimension, len(rec.Vector))
		}
		recordSize := 8 + 4*dimension
		buf := make([]byte, recordSize)
		binary.LittleEndian.PutUint64(buf[0:8], uint64(rec.ID))
		for i, v := range rec.Vector {
			binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(v))
		}
		if _, err := file.Write(buf); err != nil {
			return 0, err
		}
	}

	if err := file.Sync(); err != nil {
		return 0, err
	}

	cleanupAuxFiles(spacePath, []string{
		"vector_index.faiss",
		"vector_wal.db",
		"vector_segments.manifest.json",
	})

	return len(data), nil
}

// restoreFlatMetaVectorData writes flat-meta vector data into the primary data file.
func restoreFlatMetaVectorData(spacePath string, meta spaceMeta, data []DumpVectorRecord, mode string) (int, error) {
	dataPath := filepath.Join(spacePath, "flat_meta_data.db")
	dimension := meta.Dimension

	if mode == "merge" {
		// Read existing live records.
		type existEntry struct {
			vec      []float32
			metaJSON []byte
		}
		existing := make(map[int64]*existEntry)
		if paths, err := collectFlatMetaDataPaths(spacePath); err == nil {
			for _, path := range paths {
				_ = streamFlatMetaFile(path, dimension, func(id int64, tombstone bool, vec []float32, metaBytes []byte) error {
					if tombstone {
						existing[id] = nil
					} else {
						existing[id] = &existEntry{vec: vec, metaJSON: metaBytes}
					}
					return nil
				})
			}
		}

		// Dump data wins.
		for _, rec := range data {
			var metaBytes []byte
			if rec.Metadata != nil {
				metaBytes = []byte(*rec.Metadata)
			} else {
				metaBytes = []byte("{}")
			}
			existing[rec.ID] = &existEntry{vec: rec.Vector, metaJSON: metaBytes}
		}

		data = nil
		for id, entry := range existing {
			if entry != nil {
				raw := json.RawMessage(entry.metaJSON)
				data = append(data, DumpVectorRecord{
					ID:       id,
					Vector:   entry.vec,
					Metadata: &raw,
				})
			}
		}
		sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	}

	file, err := os.OpenFile(dataPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	for _, rec := range data {
		if len(rec.Vector) != dimension {
			return 0, fmt.Errorf("flat-meta vector ID %d has dimension mismatch: expected %d, got %d", rec.ID, dimension, len(rec.Vector))
		}
		var metaBytes []byte
		if rec.Metadata != nil {
			metaBytes = []byte(*rec.Metadata)
		} else {
			metaBytes = []byte("{}")
		}

		// Record format: [8B id][1B flag=0][4B metaLen][metaBytes][vecBytes]
		header := make([]byte, 13)
		binary.LittleEndian.PutUint64(header[0:8], uint64(rec.ID))
		header[8] = 0 // live
		binary.LittleEndian.PutUint32(header[9:13], uint32(len(metaBytes)))

		if _, err := file.Write(header); err != nil {
			return 0, err
		}
		if _, err := file.Write(metaBytes); err != nil {
			return 0, err
		}

		vecBuf := make([]byte, 4*dimension)
		for i, v := range rec.Vector {
			binary.LittleEndian.PutUint32(vecBuf[i*4:], math.Float32bits(v))
		}
		if _, err := file.Write(vecBuf); err != nil {
			return 0, err
		}
	}

	if err := file.Sync(); err != nil {
		return 0, err
	}

	cleanupAuxFiles(spacePath, []string{
		"flat_meta_wal.db",
		"flat_meta_segments.manifest.json",
	})

	return len(data), nil
}

// readExistingVectors scans a vector data file and populates live vectors into
// the map (last-writer-wins, skipping tombstones).
func readExistingVectors(path string, dimension int, out map[int64][]float32) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	recordSize := 8 + 4*dimension
	buf := make([]byte, recordSize)
	for {
		n, err := file.Read(buf)
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) || n < recordSize {
			break
		}
		if err != nil {
			break
		}
		id := int64(binary.LittleEndian.Uint64(buf[0:8]))
		isTombstone := len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarkerUint32
		if isTombstone {
			delete(out, id)
			continue
		}
		vec := make([]float32, dimension)
		for i := 0; i < dimension; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[8+i*4:]))
		}
		out[id] = vec
	}
}

// cleanupAuxFiles removes auxiliary files (indexes, manifests, WALs) that will
// be rebuilt by the engine on next open.
func cleanupAuxFiles(spacePath string, names []string) {
	for _, name := range names {
		path := filepath.Join(spacePath, name)
		_ = os.Remove(path)
	}
	// Also remove numbered segment files.
	entries, err := os.ReadDir(spacePath)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.Contains(name, "_segment_") {
			_ = os.Remove(filepath.Join(spacePath, name))
		}
	}
}

// UpdateManifestAfterRestore rewrites the spaces.manifest file to include all
// restored spaces. Called after all spaces are restored.
func UpdateManifestAfterRestore(baseDir string) error {
	names, err := discoverSpaceDirs(baseDir)
	if err != nil {
		return err
	}
	sort.Strings(names)
	return rewriteManifest(filepath.Join(baseDir, spacesManifestFileName), names)
}
