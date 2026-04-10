package spaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/storage"
)

func TestIsAllowedIndexType(t *testing.T) {
	tests := []struct {
		name      string
		indexType string
		expected  bool
	}{
		// Single index types
		{"Flat", "Flat", true},
		{"Flat with number", "Flat32", false}, // Flat should not have number suffix

		// HNSW variants (powers of 2 from 2 to 256)
		{"HNSW2", "HNSW2", true},
		{"HNSW4", "HNSW4", true},
		{"HNSW8", "HNSW8", true},
		{"HNSW16", "HNSW16", true},
		{"HNSW32", "HNSW32", true},
		{"HNSW64", "HNSW64", true},
		{"HNSW128", "HNSW128", true},
		{"HNSW256", "HNSW256", true},
		{"HNSW without number", "HNSW", false}, // HNSW requires number suffix
		{"HNSW512", "HNSW512", false},          // Out of range
		{"HNSW1", "HNSW1", false},              // Out of range
		{"HNSW3", "HNSW3", false},              // Not power of 2
		{"HNSW7", "HNSW7", false},              // Not power of 2

		// IVF variants (powers of 2 from 2 to 256)
		{"IVF2", "IVF2", true},
		{"IVF4", "IVF4", true},
		{"IVF8", "IVF8", true},
		{"IVF16", "IVF16", true},
		{"IVF32", "IVF32", true},
		{"IVF64", "IVF64", true},
		{"IVF128", "IVF128", true},
		{"IVF256", "IVF256", true},
		{"IVF without number", "IVF", false}, // IVF requires number suffix
		{"IVF512", "IVF512", false},          // Out of range
		{"IVF1", "IVF1", false},              // Out of range
		{"IVF3", "IVF3", false},              // Not power of 2
		{"IVF7", "IVF7", false},              // Not power of 2

		// PQ variants (powers of 2 from 2 to 256)
		{"PQ2", "PQ2", true},
		{"PQ4", "PQ4", true},
		{"PQ8", "PQ8", true},
		{"PQ16", "PQ16", true},
		{"PQ32", "PQ32", true},
		{"PQ64", "PQ64", true},
		{"PQ128", "PQ128", true},
		{"PQ256", "PQ256", true},
		{"PQ without number", "PQ", false}, // PQ requires number suffix
		{"PQ512", "PQ512", false},          // Out of range
		{"PQ1", "PQ1", false},              // Out of range
		{"PQ3", "PQ3", false},              // Not power of 2
		{"PQ7", "PQ7", false},              // Not power of 2

		// Composite indices
		{"IVF32,Flat", "IVF32,Flat", true},
		{"HNSW64,Flat", "HNSW64,Flat", true},
		{"PQ8,Flat", "PQ8,Flat", true},
		{"IVF64,PQ16", "IVF64,PQ16", true},
		{"HNSW128,PQ32", "HNSW128,PQ32", true},

		// Invalid composite indices
		{"Invalid composite 1", "IVF32,Invalid", false},
		{"Invalid composite 2", "Invalid,Flat", false},
		{"Invalid composite 3", "HNSW,Flat", false},   // HNSW without number
		{"Invalid composite 4", "IVF,Flat", false},    // IVF without number
		{"Invalid composite 5", "PQ,Flat", false},     // PQ without number
		{"Invalid composite 6", "Flat32,Flat", false}, // Flat with number

		// Edge cases
		{"Empty string", "", false},
		{"Only comma", ",", false},
		{"Multiple commas", "HNSW32,,Flat", false},
		{"Whitespace", " HNSW32 ", true},                  // Should trim whitespace
		{"Whitespace composite", " HNSW32 , Flat ", true}, // Should trim whitespace

		// Invalid index types
		{"Invalid type", "Invalid", false},
		{"Unknown type", "Unknown32", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAllowedIndexType(tt.indexType)
			if result != tt.expected {
				t.Errorf("isAllowedIndexType(%q) = %v, want %v", tt.indexType, result, tt.expected)
			}
		})
	}
}

func TestIsPowerOf2InRange(t *testing.T) {
	tests := []struct {
		name     string
		n        int
		expected bool
	}{
		// Valid powers of 2 in range 2-256
		{"2", 2, true},
		{"4", 4, true},
		{"8", 8, true},
		{"16", 16, true},
		{"32", 32, true},
		{"64", 64, true},
		{"128", 128, true},
		{"256", 256, true},

		// Invalid: out of range
		{"1", 1, false},
		{"512", 512, false},
		{"1024", 1024, false},
		{"0", 0, false},
		{"-1", -1, false},

		// Invalid: not power of 2
		{"3", 3, false},
		{"5", 5, false},
		{"6", 6, false},
		{"7", 7, false},
		{"9", 9, false},
		{"10", 10, false},
		{"15", 15, false},
		{"17", 17, false},
		{"31", 31, false},
		{"33", 33, false},
		{"63", 63, false},
		{"65", 65, false},
		{"127", 127, false},
		{"129", 129, false},
		{"255", 255, false},
		{"257", 257, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPowerOf2InRange(tt.n)
			if result != tt.expected {
				t.Errorf("isPowerOf2InRange(%d) = %v, want %v", tt.n, result, tt.expected)
			}
		})
	}
}

func TestSpaceManager_PersistsSpacesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)

	if _, err := sm.CreateSpaceWithWAL("alpha", "key-value", 0, "", "", true); err != nil {
		t.Fatalf("CreateSpaceWithWAL failed: %v", err)
	}
	if _, err := sm.CreateSpaceWithWAL("beta", "key-value", 0, "", "", true); err != nil {
		t.Fatalf("CreateSpaceWithWAL failed: %v", err)
	}
	sm.CloseAll()

	reloaded := NewSpaceManager(dir)
	defer reloaded.CloseAll()

	got := reloaded.ListSpaces()
	sort.Strings(got)
	want := []string{"alpha", "beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ListSpaces() = %v, want %v", got, want)
	}

	for _, name := range want {
		if _, ok := reloaded.GetSpace(name); !ok {
			t.Fatalf("space %q was not reopened after restart", name)
		}
		if _, err := os.Stat(filepath.Join(dir, name, spaceMetaFileName)); err != nil {
			t.Fatalf("space meta missing for %q: %v", name, err)
		}
	}
}

func TestSpaceManager_KeyValueMetadataOmitsVectorFieldsAndDefaultsWALOff(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	if _, err := sm.CreateSpace("kv_default", "key-value", 128, "Flat", "L2"); err != nil {
		t.Fatalf("CreateSpace failed: %v", err)
	}

	meta, ok := sm.SpaceMeta("kv_default")
	if !ok {
		t.Fatal("SpaceMeta did not return created space")
	}
	if meta.EnableWAL {
		t.Fatal("expected WAL to default to off")
	}
	if meta.Dimension != 0 {
		t.Fatalf("Dimension = %d, want 0 for key-value", meta.Dimension)
	}
	if meta.IndexType != "" {
		t.Fatalf("IndexType = %q, want empty for key-value", meta.IndexType)
	}
	if meta.Metric != "" {
		t.Fatalf("Metric = %q, want empty for key-value", meta.Metric)
	}
	if meta.SegmentRolloverBytes != storage.DefaultSegmentRolloverBytes {
		t.Fatalf("SegmentRolloverBytes = %d, want %d", meta.SegmentRolloverBytes, storage.DefaultSegmentRolloverBytes)
	}
	if meta.MaxSegmentsBeforeMerge != storage.DefaultMaxSegmentsBeforeMerge {
		t.Fatalf("MaxSegmentsBeforeMerge = %d, want %d", meta.MaxSegmentsBeforeMerge, storage.DefaultMaxSegmentsBeforeMerge)
	}

	data, err := os.ReadFile(filepath.Join(dir, "kv_default", spaceMetaFileName))
	if err != nil {
		t.Fatalf("os.ReadFile failed: %v", err)
	}
	content := string(data)
	for _, forbidden := range []string{`"dimension"`, `"index_type"`, `"metric"`, `"enable_wal"`} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("key-value metadata unexpectedly contains %s: %s", forbidden, content)
		}
	}
	for _, expected := range []string{`"segment_rollover_bytes"`, `"max_segments_before_merge"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("key-value metadata missing %s: %s", expected, content)
		}
	}
}

func TestSpaceManager_UpdateSpaceSettingsPersistsAndAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	if _, err := sm.CreateSpace("settings_space", "key-value", 0, "", ""); err != nil {
		t.Fatalf("CreateSpace failed: %v", err)
	}

	if err := sm.UpdateSpaceSettings("settings_space", storage.SpaceSettings{
		SegmentRolloverBytes:   1024,
		MaxSegmentsBeforeMerge: 7,
	}); err != nil {
		t.Fatalf("UpdateSpaceSettings failed: %v", err)
	}

	meta, ok := sm.SpaceMeta("settings_space")
	if !ok {
		t.Fatal("SpaceMeta did not return updated space")
	}
	if meta.SegmentRolloverBytes != 1024 {
		t.Fatalf("SegmentRolloverBytes = %d, want 1024", meta.SegmentRolloverBytes)
	}
	if meta.MaxSegmentsBeforeMerge != 7 {
		t.Fatalf("MaxSegmentsBeforeMerge = %d, want 7", meta.MaxSegmentsBeforeMerge)
	}

	sm.CloseAll()
	reloaded := NewSpaceManager(dir)
	defer reloaded.CloseAll()

	meta, ok = reloaded.SpaceMeta("settings_space")
	if !ok {
		t.Fatal("SpaceMeta missing after reload")
	}
	if meta.SegmentRolloverBytes != 1024 {
		t.Fatalf("reloaded SegmentRolloverBytes = %d, want 1024", meta.SegmentRolloverBytes)
	}
	if meta.MaxSegmentsBeforeMerge != 7 {
		t.Fatalf("reloaded MaxSegmentsBeforeMerge = %d, want 7", meta.MaxSegmentsBeforeMerge)
	}
}

func TestSpaceManager_UpdateSpaceSettingsPreservesUnspecifiedValues(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	if _, err := sm.CreateSpaceWithSettings("settings_space", "key-value", 0, "", "", false, storage.SpaceSettings{
		SegmentRolloverBytes:   4096,
		MaxSegmentsBeforeMerge: 11,
	}); err != nil {
		t.Fatalf("CreateSpaceWithSettings failed: %v", err)
	}

	if err := sm.UpdateSpaceSettings("settings_space", storage.SpaceSettings{
		SegmentRolloverBytes: 2048,
	}); err != nil {
		t.Fatalf("UpdateSpaceSettings failed: %v", err)
	}

	meta, ok := sm.SpaceMeta("settings_space")
	if !ok {
		t.Fatal("SpaceMeta missing")
	}
	if meta.SegmentRolloverBytes != 2048 {
		t.Fatalf("SegmentRolloverBytes = %d, want 2048", meta.SegmentRolloverBytes)
	}
	if meta.MaxSegmentsBeforeMerge != 11 {
		t.Fatalf("MaxSegmentsBeforeMerge = %d, want 11", meta.MaxSegmentsBeforeMerge)
	}
}

func TestSpaceManager_RebuildsManifestFromSpaceMetaFiles(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)

	if _, err := sm.CreateSpaceWithWAL("alpha", "key-value", 0, "", "", true); err != nil {
		t.Fatalf("CreateSpaceWithWAL failed: %v", err)
	}
	if _, err := sm.CreateSpaceWithWAL("beta", "key-value", 0, "", "", true); err != nil {
		t.Fatalf("CreateSpaceWithWAL failed: %v", err)
	}
	sm.CloseAll()

	staleLine, err := json.Marshal(manifestRecord{
		Version: currentSpaceLayoutVersion,
		Op:      manifestOpCreate,
		Space:   "alpha",
	})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, spacesManifestFileName), append(staleLine, '\n'), 0644); err != nil {
		t.Fatalf("os.WriteFile failed: %v", err)
	}

	reloaded := NewSpaceManager(dir)
	defer reloaded.CloseAll()

	got := reloaded.ListSpaces()
	sort.Strings(got)
	want := []string{"alpha", "beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ListSpaces() = %v, want %v", got, want)
	}

	manifestSpaces, exists, err := reloaded.readManifestSpaces()
	if err != nil {
		t.Fatalf("readManifestSpaces() failed: %v", err)
	}
	if !exists {
		t.Fatal("manifest was not recreated")
	}
	if len(manifestSpaces) != 2 {
		t.Fatalf("manifest space count = %d, want 2", len(manifestSpaces))
	}
}

func TestSpaceManager_LoadsLegacyMetadataJSONAndMigrates(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)

	if _, err := sm.CreateSpaceWithWAL("legacy_space", "key-value", 0, "", "", true); err != nil {
		t.Fatalf("CreateSpaceWithWAL failed: %v", err)
	}
	sm.CloseAll()

	if err := os.Remove(filepath.Join(dir, spacesManifestFileName)); err != nil {
		t.Fatalf("os.Remove manifest failed: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "legacy_space", spaceMetaFileName)); err != nil {
		t.Fatalf("os.Remove space meta failed: %v", err)
	}

	legacyMetas := []spaceMeta{{
		Name:       "legacy_space",
		EngineType: "key-value",
		EnableWAL:  true,
	}}
	legacyData, err := json.MarshalIndent(legacyMetas, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}
	legacyData = append(legacyData, '\n')
	if err := os.WriteFile(filepath.Join(dir, legacyMetadataFileName), legacyData, 0644); err != nil {
		t.Fatalf("os.WriteFile legacy metadata failed: %v", err)
	}

	reloaded := NewSpaceManager(dir)
	defer reloaded.CloseAll()

	if got := reloaded.ListSpaces(); len(got) != 1 || got[0] != "legacy_space" {
		t.Fatalf("ListSpaces() = %v, want [legacy_space]", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "legacy_space", spaceMetaFileName)); err != nil {
		t.Fatalf("legacy migration did not write space meta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, spacesManifestFileName)); err != nil {
		t.Fatalf("legacy migration did not write manifest: %v", err)
	}
}

func TestSpaceManager_CreateSpaceMetadataWriteFailureDoesNotPublishSpace(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	sm.writeJSONFile = func(path string, value interface{}) error {
		if strings.HasSuffix(path, spaceMetaFileName) {
			return errors.New("metadata write failed")
		}
		return writeAtomicJSON(path, value)
	}

	if _, err := sm.CreateSpaceWithWAL("broken", "key-value", 0, "", "", true); err == nil {
		t.Fatal("CreateSpaceWithWAL() error = nil, want failure")
	}
	if got := sm.ListSpaces(); len(got) != 0 {
		t.Fatalf("ListSpaces() = %v, want empty", got)
	}
	if _, ok := sm.GetSpace("broken"); ok {
		t.Fatal("space was published in memory after metadata failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "broken")); !os.IsNotExist(err) {
		t.Fatalf("space directory should be rolled back, got err=%v", err)
	}
}

func TestSpaceManager_ConcurrentCreateSpaceDoesNotLoseManifestEntries(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	const totalSpaces = 32
	var wg sync.WaitGroup
	errCh := make(chan error, totalSpaces)

	for i := 0; i < totalSpaces; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			spaceName := fmt.Sprintf("space_%03d", i)
			if _, err := sm.CreateSpaceWithWAL(spaceName, "key-value", 0, "", "", true); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("CreateSpaceWithWAL failed: %v", err)
		}
	}

	sm.CloseAll()
	reloaded := NewSpaceManager(dir)
	defer reloaded.CloseAll()

	if got := len(reloaded.ListSpaces()); got != totalSpaces {
		t.Fatalf("reloaded space count = %d, want %d", got, totalSpaces)
	}
	manifestSpaces, exists, err := reloaded.readManifestSpaces()
	if err != nil {
		t.Fatalf("readManifestSpaces() failed: %v", err)
	}
	if !exists {
		t.Fatal("manifest missing after concurrent create")
	}
	if got := len(manifestSpaces); got != totalSpaces {
		t.Fatalf("manifest space count = %d, want %d", got, totalSpaces)
	}
}

func TestSpaceManager_DeleteSpacePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)

	if _, err := sm.CreateSpaceWithWAL("alpha", "key-value", 0, "", "", true); err != nil {
		t.Fatalf("CreateSpaceWithWAL failed: %v", err)
	}
	if err := sm.DeleteSpace("alpha"); err != nil {
		t.Fatalf("DeleteSpace failed: %v", err)
	}
	sm.CloseAll()

	reloaded := NewSpaceManager(dir)
	defer reloaded.CloseAll()

	if got := reloaded.ListSpaces(); len(got) != 0 {
		t.Fatalf("ListSpaces() after delete = %v, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("deleted space directory still exists, err=%v", err)
	}
}

func TestSpaceManager_VectorTrainingIndexClearsSegmentSettings(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	if _, err := sm.CreateSpaceWithSettings("ivf_seg", "vector", 8, "IVF32,Flat", "L2", true, storage.SpaceSettings{
		SegmentRolloverBytes:   99,
		MaxSegmentsBeforeMerge: 3,
	}); err != nil {
		t.Fatalf("CreateSpaceWithSettings: %v", err)
	}
	meta, ok := sm.SpaceMeta("ivf_seg")
	if !ok {
		t.Fatal("SpaceMeta missing")
	}
	if meta.SegmentRolloverBytes != 0 || meta.MaxSegmentsBeforeMerge != 0 {
		t.Fatalf("IVF space should persist zero segment settings, got rollover=%d merge=%d",
			meta.SegmentRolloverBytes, meta.MaxSegmentsBeforeMerge)
	}
}

func TestSpaceManager_UpdateSpaceSettingsRejectsSegmentForTrainingVector(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	if _, err := sm.CreateSpaceWithSettings("ivf_upd", "vector", 8, "IVF32,Flat", "L2", true, storage.SpaceSettings{}); err != nil {
		t.Fatalf("CreateSpaceWithSettings: %v", err)
	}
	if err := sm.UpdateSpaceSettings("ivf_upd", storage.SpaceSettings{
		SegmentRolloverBytes: 1024,
	}); err == nil {
		t.Fatal("expected error when updating segment settings for IVF space")
	}
	if err := sm.UpdateSpaceSettings("ivf_upd", storage.SpaceSettings{}); err != nil {
		t.Fatalf("no-op update: %v", err)
	}
}

func TestSpaceManager_VectorFlatKeepsSegmentSettings(t *testing.T) {
	dir := t.TempDir()
	sm := NewSpaceManager(dir)
	defer sm.CloseAll()

	if _, err := sm.CreateSpaceWithSettings("flat_seg", "vector", 4, "Flat", "L2", true, storage.SpaceSettings{
		SegmentRolloverBytes:   2048,
		MaxSegmentsBeforeMerge: 9,
	}); err != nil {
		t.Fatalf("CreateSpaceWithSettings: %v", err)
	}
	meta, ok := sm.SpaceMeta("flat_seg")
	if !ok {
		t.Fatal("SpaceMeta missing")
	}
	if meta.SegmentRolloverBytes != 2048 || meta.MaxSegmentsBeforeMerge != 9 {
		t.Fatalf("Flat space should keep segment settings, got rollover=%d merge=%d",
			meta.SegmentRolloverBytes, meta.MaxSegmentsBeforeMerge)
	}
}
