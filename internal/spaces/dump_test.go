package spaces

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/storage"
)

// TestDumpRestoreKV tests a full round-trip: create a KV space manually, dump it,
// restore it into a fresh directory, and verify the data matches.
func TestDumpRestoreKV(t *testing.T) {
	baseDir := t.TempDir()

	// Create a KV space manually (same on-disk layout as the engine).
	spaceName := "test_kv"
	spacePath := filepath.Join(baseDir, spaceName)
	if err := os.MkdirAll(spacePath, 0755); err != nil {
		t.Fatal(err)
	}

	// Write space.meta.json.
	meta := spaceMeta{
		LayoutVersion: currentSpaceLayoutVersion,
		Name:          spaceName,
		EngineType:    "key-value",
		IndexType:     "btree",
	}
	meta = normalizeSpaceMeta(meta)
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(spacePath, spaceMetaFileName), append(metaBytes, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Write data.db with some records (append-only format).
	dataPath := filepath.Join(spacePath, "data.db")
	dataFile, err := os.Create(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	writeKVRecord(t, dataFile, "key1", "value1")
	writeKVRecord(t, dataFile, "key2", "value2")
	writeKVRecord(t, dataFile, "key1", "value1_updated") // overwrite key1
	writeKVDeleteRecord(t, dataFile, "key2")             // delete key2
	writeKVRecord(t, dataFile, "key3", "value3")
	dataFile.Close()

	// Write spaces manifest.
	manifestPath := filepath.Join(baseDir, spacesManifestFileName)
	record := manifestRecord{Version: currentSpaceLayoutVersion, Op: manifestOpCreate, Space: spaceName}
	line, _ := json.Marshal(record)
	if err := os.WriteFile(manifestPath, append(line, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Dump.
	var buf bytes.Buffer
	stats, err := DumpAll(baseDir, &buf, "")
	if err != nil {
		t.Fatalf("DumpAll failed: %v", err)
	}
	if stats.SpacesDumped != 1 {
		t.Errorf("expected 1 space dumped, got %d", stats.SpacesDumped)
	}
	if stats.KVRecords != 2 { // key1 (updated) + key3 (key2 deleted)
		t.Errorf("expected 2 KV records dumped, got %d", stats.KVRecords)
	}

	// Verify dump content.
	lines := splitJSONLines(t, buf.String())
	if len(lines) < 3 { // header + space_meta + 2 kv records = 4 lines
		t.Fatalf("expected at least 4 dump lines, got %d", len(lines))
	}

	// Verify header.
	var header DumpHeader
	if err := json.Unmarshal(lines[0], &header); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if header.Version != DumpFormatVersion {
		t.Errorf("header version: expected %d, got %d", DumpFormatVersion, header.Version)
	}

	// Restore into a new directory.
	restoreDir := t.TempDir()
	restoreStats, err := RestoreAll(restoreDir, bytes.NewReader(buf.Bytes()), "overwrite")
	if err != nil {
		t.Fatalf("RestoreAll failed: %v", err)
	}
	if restoreStats.SpacesRestored != 1 {
		t.Errorf("expected 1 space restored, got %d", restoreStats.SpacesRestored)
	}
	if restoreStats.KVRecords != 2 {
		t.Errorf("expected 2 KV records restored, got %d", restoreStats.KVRecords)
	}

	// Verify restored data by scanning the data file.
	restoredDataPath := filepath.Join(restoreDir, spaceName, "data.db")
	restored := make(map[string]string)
	restoredDel := make(map[string]bool)
	if err := scanKVDataFile(restoredDataPath, restored, restoredDel); err != nil {
		t.Fatalf("scan restored data: %v", err)
	}

	// Should have key1=value1_updated and key3=value3; key2 should not exist.
	if v, ok := restored["key1"]; !ok || v != "value1_updated" {
		t.Errorf("key1: expected 'value1_updated', got %q (exists=%v)", v, ok)
	}
	if v, ok := restored["key3"]; !ok || v != "value3" {
		t.Errorf("key3: expected 'value3', got %q (exists=%v)", v, ok)
	}
	if _, ok := restored["key2"]; ok && !restoredDel["key2"] {
		t.Error("key2 should not exist in restored data (was deleted)")
	}

	// Verify space.meta.json exists.
	restoredMetaPath := filepath.Join(restoreDir, spaceName, spaceMetaFileName)
	if _, err := os.Stat(restoredMetaPath); err != nil {
		t.Errorf("restored space.meta.json missing: %v", err)
	}
}

// TestDumpRestoreVector tests a full round-trip for a vector space.
func TestDumpRestoreVector(t *testing.T) {
	baseDir := t.TempDir()

	spaceName := "test_vec"
	spacePath := filepath.Join(baseDir, spaceName)
	if err := os.MkdirAll(spacePath, 0755); err != nil {
		t.Fatal(err)
	}

	dimension := 4
	meta := spaceMeta{
		LayoutVersion: currentSpaceLayoutVersion,
		Name:          spaceName,
		EngineType:    "vector",
		IndexType:     "Flat",
		Metric:        "L2",
		Dimension:     dimension,
	}
	meta = normalizeSpaceMeta(meta)
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(spacePath, spaceMetaFileName), append(metaBytes, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Write vector_data.db.
	dataPath := filepath.Join(spacePath, "vector_data.db")
	dataFile, err := os.Create(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	vec1 := []float32{1.0, 2.0, 3.0, 4.0}
	vec2 := []float32{5.0, 6.0, 7.0, 8.0}
	vec3 := []float32{9.0, 10.0, 11.0, 12.0}
	writeVectorRecord(t, dataFile, 1, vec1, dimension)
	writeVectorRecord(t, dataFile, 2, vec2, dimension)
	writeVectorTombstone(t, dataFile, 1, dimension) // delete vector 1
	writeVectorRecord(t, dataFile, 3, vec3, dimension)
	dataFile.Close()

	// Write spaces manifest.
	manifestPath := filepath.Join(baseDir, spacesManifestFileName)
	record := manifestRecord{Version: currentSpaceLayoutVersion, Op: manifestOpCreate, Space: spaceName}
	line, _ := json.Marshal(record)
	if err := os.WriteFile(manifestPath, append(line, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Dump.
	var buf bytes.Buffer
	stats, err := DumpAll(baseDir, &buf, "")
	if err != nil {
		t.Fatalf("DumpAll failed: %v", err)
	}
	if stats.VectorRecords != 2 { // vec2 + vec3 (vec1 deleted)
		t.Errorf("expected 2 vectors dumped, got %d", stats.VectorRecords)
	}

	// Restore.
	restoreDir := t.TempDir()
	restoreStats, err := RestoreAll(restoreDir, bytes.NewReader(buf.Bytes()), "overwrite")
	if err != nil {
		t.Fatalf("RestoreAll failed: %v", err)
	}
	if restoreStats.VectorRecords != 2 {
		t.Errorf("expected 2 vectors restored, got %d", restoreStats.VectorRecords)
	}

	// Verify restored data.
	restoredDataPath := filepath.Join(restoreDir, spaceName, "vector_data.db")
	existingVecs := make(map[int64][]float32)
	readExistingVectors(restoredDataPath, dimension, existingVecs)

	if len(existingVecs) != 2 {
		t.Fatalf("expected 2 restored vectors, got %d", len(existingVecs))
	}
	assertFloat32SliceEqual(t, existingVecs[2], vec2, "vector 2")
	assertFloat32SliceEqual(t, existingVecs[3], vec3, "vector 3")
	if _, ok := existingVecs[1]; ok {
		t.Error("vector 1 should not exist in restored data (was deleted)")
	}
}

// TestDumpRestoreFlatMetaVector tests round-trip for a flat-meta vector space.
func TestDumpRestoreFlatMetaVector(t *testing.T) {
	baseDir := t.TempDir()

	spaceName := "test_flat_meta"
	spacePath := filepath.Join(baseDir, spaceName)
	if err := os.MkdirAll(spacePath, 0755); err != nil {
		t.Fatal(err)
	}

	dimension := 3
	meta := spaceMeta{
		LayoutVersion: currentSpaceLayoutVersion,
		Name:          spaceName,
		EngineType:    "vector",
		IndexType:     "Flat",
		Metric:        "L2",
		Dimension:     dimension,
		IndexedMetadataFields: []storage.MetadataFieldSpec{
			{Name: "color", Type: "string"},
		},
	}
	meta = normalizeSpaceMeta(meta)
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(spacePath, spaceMetaFileName), append(metaBytes, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Write flat_meta_data.db.
	dataPath := filepath.Join(spacePath, "flat_meta_data.db")
	dataFile, err := os.Create(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFlatMetaRecord(t, dataFile, 1, []float32{1.0, 2.0, 3.0}, []byte(`{"color":"red"}`), dimension)
	writeFlatMetaRecord(t, dataFile, 2, []float32{4.0, 5.0, 6.0}, []byte(`{"color":"blue"}`), dimension)
	writeFlatMetaTombstone(t, dataFile, 1, []byte(`{}`)) // delete record 1
	dataFile.Close()

	// Write spaces manifest.
	manifestPath := filepath.Join(baseDir, spacesManifestFileName)
	record := manifestRecord{Version: currentSpaceLayoutVersion, Op: manifestOpCreate, Space: spaceName}
	line, _ := json.Marshal(record)
	if err := os.WriteFile(manifestPath, append(line, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Dump.
	var buf bytes.Buffer
	stats, err := DumpAll(baseDir, &buf, "")
	if err != nil {
		t.Fatalf("DumpAll failed: %v", err)
	}
	if stats.VectorRecords != 1 { // only record 2
		t.Errorf("expected 1 vector dumped, got %d", stats.VectorRecords)
	}

	// Verify the dump contains metadata.
	dumpLines := splitJSONLines(t, buf.String())
	foundVec := false
	for _, line := range dumpLines {
		var peek struct {
			RecordType string `json:"record_type"`
		}
		json.Unmarshal(line, &peek)
		if peek.RecordType == "vector" {
			var rec DumpVectorRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				t.Fatalf("parse vector record: %v", err)
			}
			if rec.ID != 2 {
				t.Errorf("expected vector ID 2, got %d", rec.ID)
			}
			if rec.Metadata == nil {
				t.Error("expected metadata, got nil")
			} else {
				var md map[string]any
				json.Unmarshal(*rec.Metadata, &md)
				if md["color"] != "blue" {
					t.Errorf("expected color=blue, got %v", md["color"])
				}
			}
			foundVec = true
		}
	}
	if !foundVec {
		t.Error("no vector record found in dump")
	}

	// Restore.
	restoreDir := t.TempDir()
	restoreStats, err := RestoreAll(restoreDir, bytes.NewReader(buf.Bytes()), "overwrite")
	if err != nil {
		t.Fatalf("RestoreAll failed: %v", err)
	}
	if restoreStats.VectorRecords != 1 {
		t.Errorf("expected 1 vector restored, got %d", restoreStats.VectorRecords)
	}
}

// TestDumpSpaceFilter tests that --space filters correctly.
func TestDumpSpaceFilter(t *testing.T) {
	baseDir := t.TempDir()

	// Create two spaces.
	for _, name := range []string{"space_a", "space_b"} {
		spacePath := filepath.Join(baseDir, name)
		os.MkdirAll(spacePath, 0755)
		meta := spaceMeta{
			LayoutVersion: currentSpaceLayoutVersion,
			Name:          name,
			EngineType:    "key-value",
			IndexType:     "btree",
		}
		meta = normalizeSpaceMeta(meta)
		metaBytes, _ := json.MarshalIndent(meta, "", "  ")
		os.WriteFile(filepath.Join(spacePath, spaceMetaFileName), append(metaBytes, '\n'), 0644)

		dataFile, _ := os.Create(filepath.Join(spacePath, "data.db"))
		writeKVRecord(t, dataFile, name+"_key", name+"_val")
		dataFile.Close()
	}

	// Dump only space_a.
	var buf bytes.Buffer
	stats, err := DumpAll(baseDir, &buf, "space_a")
	if err != nil {
		t.Fatalf("DumpAll with filter failed: %v", err)
	}
	if stats.SpacesDumped != 1 {
		t.Errorf("expected 1 space, got %d", stats.SpacesDumped)
	}
	if stats.KVRecords != 1 {
		t.Errorf("expected 1 KV record, got %d", stats.KVRecords)
	}

	// Non-existent space filter should error.
	var buf2 bytes.Buffer
	_, err = DumpAll(baseDir, &buf2, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent space filter")
	}
}

// TestRestoreMergeMode tests merge mode preserves existing data.
func TestRestoreMergeMode(t *testing.T) {
	// First create a dump from a source.
	srcDir := t.TempDir()
	spaceName := "merge_test"
	spacePath := filepath.Join(srcDir, spaceName)
	os.MkdirAll(spacePath, 0755)

	meta := spaceMeta{
		LayoutVersion: currentSpaceLayoutVersion,
		Name:          spaceName,
		EngineType:    "key-value",
		IndexType:     "btree",
	}
	meta = normalizeSpaceMeta(meta)
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(filepath.Join(spacePath, spaceMetaFileName), append(metaBytes, '\n'), 0644)

	srcData, _ := os.Create(filepath.Join(spacePath, "data.db"))
	writeKVRecord(t, srcData, "shared_key", "dump_value")
	writeKVRecord(t, srcData, "dump_only", "from_dump")
	srcData.Close()

	var dumpBuf bytes.Buffer
	_, err := DumpAll(srcDir, &dumpBuf, "")
	if err != nil {
		t.Fatalf("DumpAll failed: %v", err)
	}

	// Create destination with existing data.
	dstDir := t.TempDir()
	dstPath := filepath.Join(dstDir, spaceName)
	os.MkdirAll(dstPath, 0755)
	os.WriteFile(filepath.Join(dstPath, spaceMetaFileName), append(metaBytes, '\n'), 0644)

	dstData, _ := os.Create(filepath.Join(dstPath, "data.db"))
	writeKVRecord(t, dstData, "shared_key", "existing_value")
	writeKVRecord(t, dstData, "existing_only", "from_existing")
	dstData.Close()

	// Restore in merge mode.
	stats, err := RestoreAll(dstDir, bytes.NewReader(dumpBuf.Bytes()), "merge")
	if err != nil {
		t.Fatalf("RestoreAll merge failed: %v", err)
	}
	if stats.KVRecords != 3 {
		t.Errorf("expected 3 merged KV records, got %d", stats.KVRecords)
	}

	// Verify merged data.
	restored := make(map[string]string)
	restoredDel := make(map[string]bool)
	scanKVDataFile(filepath.Join(dstDir, spaceName, "data.db"), restored, restoredDel)

	if restored["shared_key"] != "dump_value" {
		t.Errorf("shared_key: expected 'dump_value' (dump wins), got %q", restored["shared_key"])
	}
	if restored["dump_only"] != "from_dump" {
		t.Errorf("dump_only: expected 'from_dump', got %q", restored["dump_only"])
	}
	if restored["existing_only"] != "from_existing" {
		t.Errorf("existing_only: expected 'from_existing', got %q", restored["existing_only"])
	}
}

// TestRestoreInvalidVersion tests that restore rejects incompatible dump versions.
func TestRestoreInvalidVersion(t *testing.T) {
	header := DumpHeader{Version: 999, CreatedAt: "2026-01-01T00:00:00Z"}
	data, _ := json.Marshal(header)
	data = append(data, '\n')

	_, err := RestoreAll(t.TempDir(), bytes.NewReader(data), "overwrite")
	if err == nil {
		t.Error("expected error for unsupported dump version")
	}
	if !strings.Contains(err.Error(), "unsupported dump format version") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestRestoreInvalidMode tests that restore rejects invalid mode values.
func TestRestoreInvalidMode(t *testing.T) {
	header := DumpHeader{Version: DumpFormatVersion, CreatedAt: "2026-01-01T00:00:00Z"}
	data, _ := json.Marshal(header)
	data = append(data, '\n')

	_, err := RestoreAll(t.TempDir(), bytes.NewReader(data), "invalid")
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}

// TestUpdateManifestAfterRestore tests manifest rewrite.
func TestUpdateManifestAfterRestore(t *testing.T) {
	baseDir := t.TempDir()

	// Create two space dirs with metadata.
	for _, name := range []string{"alpha", "beta"} {
		spacePath := filepath.Join(baseDir, name)
		os.MkdirAll(spacePath, 0755)
		meta := spaceMeta{Name: name, EngineType: "key-value", IndexType: "btree"}
		metaBytes, _ := json.MarshalIndent(normalizeSpaceMeta(meta), "", "  ")
		os.WriteFile(filepath.Join(spacePath, spaceMetaFileName), append(metaBytes, '\n'), 0644)
	}

	if err := UpdateManifestAfterRestore(baseDir); err != nil {
		t.Fatalf("UpdateManifestAfterRestore failed: %v", err)
	}

	// Verify manifest.
	manifestPath := filepath.Join(baseDir, spacesManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

// --- Test helpers ---

func writeKVRecord(t *testing.T, f *os.File, key, value string) {
	t.Helper()
	keyBytes := []byte(key)
	valBytes := []byte(value)
	buf := make([]byte, 8+len(keyBytes)+len(valBytes))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keyBytes)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(valBytes)))
	copy(buf[8:], keyBytes)
	copy(buf[8+len(keyBytes):], valBytes)
	if _, err := f.Write(buf); err != nil {
		t.Fatal(err)
	}
}

func writeKVDeleteRecord(t *testing.T, f *os.File, key string) {
	t.Helper()
	keyBytes := []byte(key)
	buf := make([]byte, 8+len(keyBytes))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keyBytes)))
	binary.LittleEndian.PutUint32(buf[4:8], 0) // valSize=0 → tombstone
	copy(buf[8:], keyBytes)
	if _, err := f.Write(buf); err != nil {
		t.Fatal(err)
	}
}

func writeVectorRecord(t *testing.T, f *os.File, id int64, vec []float32, dim int) {
	t.Helper()
	buf := make([]byte, 8+4*dim)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[8+i*4:], math.Float32bits(v))
	}
	if _, err := f.Write(buf); err != nil {
		t.Fatal(err)
	}
}

func writeVectorTombstone(t *testing.T, f *os.File, id int64, dim int) {
	t.Helper()
	buf := make([]byte, 8+4*dim)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(id))
	// Tombstone marker in first float32 position.
	binary.LittleEndian.PutUint32(buf[8:12], tombstoneMarkerUint32)
	if _, err := f.Write(buf); err != nil {
		t.Fatal(err)
	}
}

func writeFlatMetaRecord(t *testing.T, f *os.File, id int64, vec []float32, metaJSON []byte, dim int) {
	t.Helper()
	header := make([]byte, 13)
	binary.LittleEndian.PutUint64(header[0:8], uint64(id))
	header[8] = 0 // live
	binary.LittleEndian.PutUint32(header[9:13], uint32(len(metaJSON)))
	f.Write(header)
	f.Write(metaJSON)
	vecBuf := make([]byte, 4*dim)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(vecBuf[i*4:], math.Float32bits(v))
	}
	f.Write(vecBuf)
}

func writeFlatMetaTombstone(t *testing.T, f *os.File, id int64, metaJSON []byte) {
	t.Helper()
	header := make([]byte, 13)
	binary.LittleEndian.PutUint64(header[0:8], uint64(id))
	header[8] = 1 // tombstone
	binary.LittleEndian.PutUint32(header[9:13], uint32(len(metaJSON)))
	f.Write(header)
	f.Write(metaJSON)
}

func splitJSONLines(t *testing.T, s string) []json.RawMessage {
	t.Helper()
	var lines []json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, json.RawMessage(line))
	}
	return lines
}

func assertFloat32SliceEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length mismatch: got %d, want %d", label, len(got), len(want))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s[%d]: got %f, want %f", label, i, got[i], want[i])
		}
	}
}
