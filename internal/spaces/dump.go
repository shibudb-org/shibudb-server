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
	"time"
)

// DumpFormatVersion identifies the dump file format so restores can detect
// incompatible layouts. Bump this when the on-disk schema changes.
const DumpFormatVersion = 1

// DumpHeader is the first JSON line in every dump file.
type DumpHeader struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	// SpaceFilter is non-empty when --space was used; empty means "all spaces".
	SpaceFilter string `json:"space_filter,omitempty"`
}

// DumpSpaceMeta is emitted once per space, before its records.
type DumpSpaceMeta struct {
	RecordType string `json:"record_type"` // always "space_meta"
	spaceMeta
}

// DumpKVRecord is one live key-value pair.
type DumpKVRecord struct {
	RecordType string `json:"record_type"` // "kv"
	Space      string `json:"space"`
	Key        string `json:"key"`
	Value      string `json:"value"`
}

// DumpVectorRecord is one live vector.
type DumpVectorRecord struct {
	RecordType string           `json:"record_type"` // "vector"
	Space      string           `json:"space"`
	ID         int64            `json:"id"`
	Vector     []float32        `json:"vector"`
	Metadata   *json.RawMessage `json:"metadata,omitempty"`
}

// DumpStats tracks progress counters.
type DumpStats struct {
	SpacesDumped  int
	KVRecords     int
	VectorRecords int
}

// tombstoneMarkerUint32 matches the quiet-NaN sentinel used by the vector storage
// for delete tombstones.
const tombstoneMarkerUint32 = uint32(0x7FC00000)

// flatMetaFlagTombstone matches flatMetaDataFlagTombstone in flat_meta_vector_storage.go.
const flatMetaFlagTombstone = 1

// DumpAll writes a complete JSON-lines dump of every space (or only `spaceFilter`
// when non-empty) under baseDir to the given writer. The server must be stopped.
func DumpAll(baseDir string, w io.Writer, spaceFilter string) (DumpStats, error) {
	var stats DumpStats

	// Discover spaces.
	sm := &SpaceManager{}
	discovered, err := discoverSpaceDirs(baseDir)
	if err != nil {
		return stats, fmt.Errorf("discover spaces: %w", err)
	}

	names := discovered
	sort.Strings(names)

	if spaceFilter != "" {
		found := false
		for _, n := range names {
			if n == spaceFilter {
				found = true
				break
			}
		}
		if !found {
			return stats, fmt.Errorf("space %q not found", spaceFilter)
		}
		names = []string{spaceFilter}
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	// Write header.
	if err := enc.Encode(DumpHeader{
		Version:     DumpFormatVersion,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		SpaceFilter: spaceFilter,
	}); err != nil {
		return stats, fmt.Errorf("write header: %w", err)
	}

	for _, name := range names {
		metaPath := filepath.Join(baseDir, name, spaceMetaFileName)
		meta, err := sm.readSpaceMetaFile(metaPath)
		if err != nil {
			return stats, fmt.Errorf("read space %q metadata: %w", name, err)
		}

		// Emit space metadata.
		if err := enc.Encode(DumpSpaceMeta{
			RecordType: "space_meta",
			spaceMeta:  meta,
		}); err != nil {
			return stats, fmt.Errorf("write space meta for %q: %w", name, err)
		}
		stats.SpacesDumped++

		spacePath := filepath.Join(baseDir, name)
		switch meta.EngineType {
		case "key-value":
			n, err := dumpKeyValueSpace(enc, spacePath, meta)
			if err != nil {
				return stats, fmt.Errorf("dump KV space %q: %w", name, err)
			}
			stats.KVRecords += n
		case "vector":
			if meta.IndexType == "Flat" && len(meta.IndexedMetadataFields) > 0 {
				n, err := dumpFlatMetaVectorSpace(enc, spacePath, meta)
				if err != nil {
					return stats, fmt.Errorf("dump flat-meta vector space %q: %w", name, err)
				}
				stats.VectorRecords += n
			} else {
				n, err := dumpVectorSpace(enc, spacePath, meta)
				if err != nil {
					return stats, fmt.Errorf("dump vector space %q: %w", name, err)
				}
				stats.VectorRecords += n
			}
		default:
			return stats, fmt.Errorf("unknown engine type %q for space %q", meta.EngineType, name)
		}
	}
	return stats, nil
}

// discoverSpaceDirs returns subdirectory names that contain a space.meta.json.
func discoverSpaceDirs(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(baseDir, entry.Name(), spaceMetaFileName)
		info, err := os.Stat(metaPath)
		if err != nil || info.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

// dumpKeyValueSpace scans all segments of a key-value space, collects the latest
// live value per key, and emits them as DumpKVRecord lines.
func dumpKeyValueSpace(enc *json.Encoder, spacePath string, meta spaceMeta) (int, error) {
	// Collect all data file paths from the segment manifest.
	dataPaths, err := collectKVDataPaths(spacePath)
	if err != nil {
		return 0, err
	}

	// Scan all files in segment order, last-writer-wins.
	latest := make(map[string]string) // key → value
	deleted := make(map[string]bool)
	for _, path := range dataPaths {
		if err := scanKVDataFile(path, latest, deleted); err != nil {
			return 0, fmt.Errorf("scan %s: %w", path, err)
		}
	}

	// Emit live records.
	count := 0
	keys := make([]string, 0, len(latest))
	for k := range latest {
		if !deleted[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		if err := enc.Encode(DumpKVRecord{
			RecordType: "kv",
			Space:      meta.Name,
			Key:        k,
			Value:      latest[k],
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// collectKVDataPaths finds data files for a KV space. It checks the segment
// manifest first, falling back to the single data.db file.
func collectKVDataPaths(spacePath string) ([]string, error) {
	manifestPath := filepath.Join(spacePath, "data_segments.manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Segments []struct {
				DataFile string `json:"data_file"`
			} `json:"segments"`
		}
		if err := json.Unmarshal(data, &manifest); err == nil && len(manifest.Segments) > 0 {
			var paths []string
			for _, seg := range manifest.Segments {
				p := filepath.Join(spacePath, seg.DataFile)
				if _, err := os.Stat(p); err == nil {
					paths = append(paths, p)
				}
			}
			if len(paths) > 0 {
				return paths, nil
			}
		}
	}
	// Fallback: single data.db
	singlePath := filepath.Join(spacePath, "data.db")
	if _, err := os.Stat(singlePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return []string{singlePath}, nil
}

// scanKVDataFile reads all records from an append-only KV data file.
// Format: [4B keySize LE][4B valSize LE][keyBytes][valBytes]
// valSize == 0 means tombstone (delete).
func scanKVDataFile(path string, latest map[string]string, deletedMap map[string]bool) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	header := make([]byte, 8)
	for {
		n, err := io.ReadFull(file, header)
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
			break
		}
		if err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}

		keySize := binary.LittleEndian.Uint32(header[0:4])
		valSize := binary.LittleEndian.Uint32(header[4:8])

		keyBytes := make([]byte, keySize)
		if _, err := io.ReadFull(file, keyBytes); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}

		key := string(keyBytes)

		if valSize == 0 {
			// Tombstone
			deletedMap[key] = true
			latest[key] = ""
			continue
		}

		valBytes := make([]byte, valSize)
		if _, err := io.ReadFull(file, valBytes); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}

		latest[key] = string(valBytes)
		deletedMap[key] = false
	}
	return nil
}

// dumpVectorSpace scans all vector data segments and emits live vectors.
func dumpVectorSpace(enc *json.Encoder, spacePath string, meta spaceMeta) (int, error) {
	dataPaths, err := collectVectorDataPaths(spacePath)
	if err != nil {
		return 0, err
	}

	dimension := meta.Dimension
	recordSize := 8 + 4*dimension

	// Collect latest offset per ID.
	type vecEntry struct {
		path   string
		offset int64
	}
	latest := make(map[int64]vecEntry)
	tombstoned := make(map[int64]bool)

	for _, path := range dataPaths {
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}

		var offset int64
		buf := make([]byte, recordSize)
		for {
			n, err := file.Read(buf)
			if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
				break
			}
			if err == io.ErrUnexpectedEOF || n < recordSize {
				break
			}
			if err != nil {
				file.Close()
				return 0, err
			}

			id := int64(binary.LittleEndian.Uint64(buf[0:8]))
			isTombstone := len(buf) >= 12 && binary.LittleEndian.Uint32(buf[8:12]) == tombstoneMarkerUint32
			latest[id] = vecEntry{path: path, offset: offset}
			tombstoned[id] = isTombstone
			offset += int64(recordSize)
		}
		file.Close()
	}

	// Emit live vectors in sorted ID order.
	ids := make([]int64, 0, len(latest))
	for id := range latest {
		if !tombstoned[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	count := 0
	for _, id := range ids {
		entry := latest[id]
		vec, err := readVectorAt(entry.path, entry.offset, dimension)
		if err != nil {
			return count, fmt.Errorf("read vector %d: %w", id, err)
		}
		if err := enc.Encode(DumpVectorRecord{
			RecordType: "vector",
			Space:      meta.Name,
			ID:         id,
			Vector:     vec,
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// collectVectorDataPaths finds vector data files from the segment manifest.
func collectVectorDataPaths(spacePath string) ([]string, error) {
	manifestPath := filepath.Join(spacePath, "vector_segments.manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Segments []struct {
				DataFile string `json:"data_file"`
			} `json:"segments"`
		}
		if err := json.Unmarshal(data, &manifest); err == nil && len(manifest.Segments) > 0 {
			var paths []string
			for _, seg := range manifest.Segments {
				p := filepath.Join(spacePath, seg.DataFile)
				if _, err := os.Stat(p); err == nil {
					paths = append(paths, p)
				}
			}
			if len(paths) > 0 {
				return paths, nil
			}
		}
	}
	// Fallback: single vector_data.db
	singlePath := filepath.Join(spacePath, "vector_data.db")
	if _, err := os.Stat(singlePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return []string{singlePath}, nil
}

// readVectorAt reads a vector (without the ID prefix) at a given offset from a
// data file.
func readVectorAt(path string, offset int64, dimension int) ([]float32, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	recordSize := 8 + 4*dimension
	buf := make([]byte, recordSize)
	if _, err := file.ReadAt(buf, offset); err != nil {
		return nil, err
	}
	vec := make([]float32, dimension)
	for i := 0; i < dimension; i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[8+i*4:]))
	}
	return vec, nil
}

// dumpFlatMetaVectorSpace scans all flat-meta vector data segments and emits
// live vectors with their metadata.
func dumpFlatMetaVectorSpace(enc *json.Encoder, spacePath string, meta spaceMeta) (int, error) {
	dataPaths, err := collectFlatMetaDataPaths(spacePath)
	if err != nil {
		return 0, err
	}

	dimension := meta.Dimension

	// Collect latest record per ID (last-writer-wins).
	type flatMetaEntry struct {
		vec       []float32
		metaJSON  []byte
		tombstone bool
	}
	latest := make(map[int64]*flatMetaEntry)

	for _, path := range dataPaths {
		if err := streamFlatMetaFile(path, dimension, func(id int64, tombstone bool, vec []float32, metaBytes []byte) error {
			latest[id] = &flatMetaEntry{
				vec:       vec,
				metaJSON:  metaBytes,
				tombstone: tombstone,
			}
			return nil
		}); err != nil {
			return 0, fmt.Errorf("stream %s: %w", path, err)
		}
	}

	// Emit live vectors in sorted ID order.
	ids := make([]int64, 0, len(latest))
	for id, entry := range latest {
		if !entry.tombstone {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	count := 0
	for _, id := range ids {
		entry := latest[id]
		rec := DumpVectorRecord{
			RecordType: "vector",
			Space:      meta.Name,
			ID:         id,
			Vector:     entry.vec,
		}
		if len(entry.metaJSON) > 0 && string(entry.metaJSON) != "{}" && string(entry.metaJSON) != "null" {
			raw := json.RawMessage(entry.metaJSON)
			rec.Metadata = &raw
		}
		if err := enc.Encode(rec); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// collectFlatMetaDataPaths finds flat-meta vector data files from the manifest.
func collectFlatMetaDataPaths(spacePath string) ([]string, error) {
	manifestPath := filepath.Join(spacePath, "flat_meta_segments.manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Segments []struct {
				DataFile string `json:"data_file"`
			} `json:"segments"`
		}
		if err := json.Unmarshal(data, &manifest); err == nil && len(manifest.Segments) > 0 {
			var paths []string
			for _, seg := range manifest.Segments {
				p := filepath.Join(spacePath, seg.DataFile)
				if _, err := os.Stat(p); err == nil {
					paths = append(paths, p)
				}
			}
			if len(paths) > 0 {
				return paths, nil
			}
		}
	}
	// Fallback: single flat_meta_data.db
	singlePath := filepath.Join(spacePath, "flat_meta_data.db")
	if _, err := os.Stat(singlePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return []string{singlePath}, nil
}

// streamFlatMetaFile reads a flat-meta vector data file record by record.
// Record format: [8B id LE][1B flag][4B metaLen LE][metaBytes][vecBytes if live]
func streamFlatMetaFile(path string, dim int, fn func(id int64, tombstone bool, vec []float32, metaBytes []byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	header := make([]byte, 13)
	for {
		if _, err := io.ReadFull(file, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}
		id := int64(binary.LittleEndian.Uint64(header[0:8]))
		flag := header[8]
		metaLen := binary.LittleEndian.Uint32(header[9:13])

		metaBuf := make([]byte, metaLen)
		if _, err := io.ReadFull(file, metaBuf); err != nil {
			break
		}

		if flag == flatMetaFlagTombstone {
			if err := fn(id, true, nil, metaBuf); err != nil {
				return err
			}
			continue
		}

		vecBuf := make([]byte, 4*dim)
		if _, err := io.ReadFull(file, vecBuf); err != nil {
			break
		}
		vec := make([]float32, dim)
		for i := 0; i < dim; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(vecBuf[i*4:]))
		}
		if err := fn(id, false, vec, metaBuf); err != nil {
			return err
		}
	}
	return nil
}
