package spaces

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestRestoreSegmentedKVMerge tests that merge restore scans all KV segments, preserving keys from data_segment_*.db.
func TestRestoreSegmentedKVMerge(t *testing.T) {
	baseDir := t.TempDir()

	spaceName := "kv_segmented_merge"
	spacePath := filepath.Join(baseDir, spaceName)
	if err := os.MkdirAll(spacePath, 0755); err != nil {
		t.Fatal(err)
	}

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

	// Primary data.db has key1.
	f1, _ := os.Create(filepath.Join(spacePath, "data.db"))
	writeKVRecord(t, f1, "key1", "val1_existing")
	f1.Close()

	// Segment 2 file has key2 (which is NOT in data.db).
	f2, _ := os.Create(filepath.Join(spacePath, "data_segment_000002.db"))
	writeKVRecord(t, f2, "key2", "val2_segment")
	f2.Close()

	// Write manifest listing segment 2.
	manifestContent := []byte(`{
		"version": 1,
		"next_segment_id": 3,
		"active_segment_id": 2,
		"segments": [
			{"id": 1, "state": "cold", "data_file": "data.db"},
			{"id": 2, "state": "hot", "data_file": "data_segment_000002.db"}
		]
	}`)
	if err := os.WriteFile(filepath.Join(spacePath, "data_segments.manifest.json"), manifestContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Prepare a dump with key3 and updated key1.
	dumpInput := `{"version":1,"created_at":"2026-01-01T00:00:00Z"}
{"record_type":"space_meta","name":"kv_segmented_merge","engine_type":"key-value","index_type":"btree"}
{"record_type":"kv","space":"kv_segmented_merge","key":"key1","value":"val1_dump"}
{"record_type":"kv","space":"kv_segmented_merge","key":"key3","value":"val3_dump"}
`

	// Restore in merge mode.
	stats, err := RestoreAll(baseDir, strings.NewReader(dumpInput), "merge")
	if err != nil {
		t.Fatalf("RestoreAll merge failed: %v", err)
	}
	if stats.KVRecords != 3 { // key1 (dump), key2 (from segment 2), key3 (dump)
		t.Errorf("expected 3 total merged KV records, got %d", stats.KVRecords)
	}

	// Verify all 3 keys exist in restored data.db.
	restored := make(map[string]string)
	restoredDel := make(map[string]bool)
	if err := scanKVDataFile(filepath.Join(spacePath, "data.db"), restored, restoredDel); err != nil {
		t.Fatalf("scan restored data.db: %v", err)
	}

	if restored["key1"] != "val1_dump" {
		t.Errorf("key1: expected 'val1_dump', got %q", restored["key1"])
	}
	if restored["key2"] != "val2_segment" {
		t.Errorf("key2 (from segment): expected 'val2_segment', got %q", restored["key2"])
	}
	if restored["key3"] != "val3_dump" {
		t.Errorf("key3: expected 'val3_dump', got %q", restored["key3"])
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

// TestRestoreMalformedDump verifies that malformed or corrupted dump files return clear error messages without panicking.
func TestRestoreMalformedDump(t *testing.T) {
	tests := []struct {
		name      string
		dumpData  string
		wantError string
	}{
		{
			name:      "missing header",
			dumpData:  `{"record_type":"kv","space":"s","key":"k","value":"v"}` + "\n",
			wantError: "unsupported dump format version",
		},
		{
			name:      "kv record before space_meta",
			dumpData:  `{"version":1,"created_at":"2026-01-01T00:00:00Z"}` + "\n" + `{"record_type":"kv","space":"s","key":"k","value":"v"}` + "\n",
			wantError: "dump appears corrupted or malformed: found kv record before space_meta",
		},
		{
			name:      "vector record before space_meta",
			dumpData:  `{"version":1,"created_at":"2026-01-01T00:00:00Z"}` + "\n" + `{"record_type":"vector","space":"s","id":1,"vector":[1,2]}` + "\n",
			wantError: "dump appears corrupted or malformed: found vector record before space_meta",
		},
		{
			name:      "unknown record type",
			dumpData:  `{"version":1,"created_at":"2026-01-01T00:00:00Z"}` + "\n" + `{"record_type":"invalid_type"}` + "\n",
			wantError: "dump appears corrupted or malformed: unknown record type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RestoreAll(t.TempDir(), strings.NewReader(tt.dumpData), "overwrite")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantError)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("expected error to contain %q, got %q", tt.wantError, err.Error())
			}
		})
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
