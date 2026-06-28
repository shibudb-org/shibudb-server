package spaces

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/storage"

	"github.com/DataIntelligenceCrew/go-faiss"
)

var allowedIndexTypes = []string{"Flat", "HNSW", "IVF", "PQ"}
var allowedMetrics = []string{"L2", "InnerProduct", "L1", "Lp", "Canberra", "BrayCurtis", "JensenShannon", "Linf"}

const (
	currentSpaceLayoutVersion = 2
	spaceMetaFileName         = "space.meta.json"
	spacesManifestFileName    = "spaces.manifest"
	legacyMetadataFileName    = "metadata.json"
	manifestOpCreate          = "create"
	manifestOpDelete          = "delete"
	spaceLoadProgressStep     = 500
	spaceLoadProgressInterval = 5 * time.Second
)

func isPowerOf2InRange(n int) bool {
	if n < 2 || n > 256 {
		return false
	}
	return (n & (n - 1)) == 0
}

func isAllowedIndexType(indexType string) bool {
	parts := strings.Split(indexType, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}

		// e.g. HNSW32, IVF32, PQ4, Flat
		var base string
		num := -1

		// Find where the letters end and numbers begin
		for i, c := range part {
			if c >= '0' && c <= '9' {
				base = part[:i]
				fmt.Sscanf(part[i:], "%d", &num)
				break
			}
		}
		if base == "" {
			base = part
		}

		// Check if base type is allowed
		allowed := false
		for _, t := range allowedIndexTypes {
			if t == base {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}

		// For HNSW, IVF, and PQ, number suffix is required and must be power of 2 in range 2-256
		if base == "HNSW" || base == "IVF" || base == "PQ" {
			if num == -1 {
				return false // Number suffix is required
			}
			if !isPowerOf2InRange(num) {
				return false // Must be power of 2 in range 2-256
			}
		}

		// For Flat, no number suffix should be present
		if base == "Flat" && num != -1 {
			return false
		}
	}
	return true
}

func isAllowedMetric(metric string) bool {
	for _, m := range allowedMetrics {
		if m == metric {
			return true
		}
	}
	return false
}

type spaceMeta struct {
	LayoutVersion          int    `json:"layout_version,omitempty"`
	Name                   string `json:"name"`
	EngineType             string `json:"engine_type"`
	Dimension              int    `json:"dimension,omitempty"`
	IndexType              string `json:"index_type,omitempty"`
	Metric                 string `json:"metric,omitempty"`
	EnableWAL              bool   `json:"enable_wal,omitempty"`
	SegmentRolloverBytes   int64  `json:"segment_rollover_bytes,omitempty"`
	MaxSegmentsBeforeMerge int    `json:"max_segments_before_merge,omitempty"`

	IndexedMetadataFields []storage.MetadataFieldSpec `json:"indexed_metadata_fields,omitempty"`
}

type manifestRecord struct {
	Version int    `json:"version"`
	Op      string `json:"op"`
	Space   string `json:"space"`
}

type SpaceManager struct {
	lock             sync.RWMutex
	spaces           map[string]interface{} // can be KeyValueEngine or VectorEngine
	spaceMetas       map[string]spaceMeta
	baseDir          string
	metaFilePath     string
	manifestFilePath string
	writeJSONFile    func(string, interface{}) error
	appendManifest   func(manifestRecord) error
	rewriteManifest  func([]string) error
}

func NewSpaceManager(basePath string) *SpaceManager {
	os.MkdirAll(basePath, 0755)

	manager := &SpaceManager{
		spaces:           make(map[string]interface{}),
		spaceMetas:       make(map[string]spaceMeta),
		baseDir:          basePath,
		metaFilePath:     filepath.Join(basePath, legacyMetadataFileName),
		manifestFilePath: filepath.Join(basePath, spacesManifestFileName),
	}
	manager.writeJSONFile = writeAtomicJSON
	manager.appendManifest = func(record manifestRecord) error {
		return appendManifestRecord(manager.manifestFilePath, record)
	}
	manager.rewriteManifest = func(spaces []string) error {
		return rewriteManifest(manager.manifestFilePath, spaces)
	}
	if err := manager.loadSpaceMetas(); err != nil {
		fmt.Printf("Warning: failed to load space metadata: %v\n", err)
	}
	return manager
}

func (sm *SpaceManager) loadSpaceMetas() error {
	loadedCurrent, err := sm.loadCurrentSpaceCatalog()
	if err != nil {
		return err
	}
	if loadedCurrent {
		return nil
	}
	return sm.loadLegacyMetadataJSON()
}

func (sm *SpaceManager) loadCurrentSpaceCatalog() (bool, error) {
	discovered, err := sm.discoverSpaceMetaFiles()
	if err != nil {
		return false, err
	}

	manifestSpaces, manifestExists, manifestErr := sm.readManifestSpaces()
	if manifestErr != nil {
		fmt.Printf("Warning: failed to read %s: %v\n", sm.manifestFilePath, manifestErr)
	}

	if !manifestExists && len(discovered) == 0 {
		return false, nil
	}

	names := sortedSpaceNames(discovered)
	startedAt := time.Now()
	lastLogAt := startedAt
	if len(names) > 0 {
		fmt.Printf("Restoring %d spaces from %s before accepting client connections...\n", len(names), sm.baseDir)
	}

	validSpaces := make(map[string]struct{}, len(discovered))
	failures := 0
	for idx, name := range names {
		meta, err := sm.readSpaceMetaFile(filepath.Join(sm.baseDir, name, spaceMetaFileName))
		if err != nil {
			fmt.Printf("Warning: skipping space %q due to invalid metadata: %v\n", name, err)
			failures++
			maybeLogSpaceRestoreProgress(idx+1, len(names), len(validSpaces), failures, startedAt, &lastLogAt, name)
			continue
		}
		if err := sm.loadSpace(meta); err != nil {
			fmt.Printf("❌ Failed to open space '%s': %v\n", meta.Name, err)
			failures++
			maybeLogSpaceRestoreProgress(idx+1, len(names), len(validSpaces), failures, startedAt, &lastLogAt, meta.Name)
			continue
		}
		validSpaces[meta.Name] = struct{}{}
		maybeLogSpaceRestoreProgress(idx+1, len(names), len(validSpaces), failures, startedAt, &lastLogAt, meta.Name)
	}

	if len(names) > 0 {
		fmt.Printf(
			"Finished restoring spaces: loaded %d/%d (failed: %d, elapsed: %s)\n",
			len(validSpaces),
			len(names),
			failures,
			formatSpaceRestoreDuration(time.Since(startedAt)),
		)
	}

	if manifestErr != nil || !manifestExists || !sameSpaceSet(manifestSpaces, validSpaces) {
		if err := sm.rewriteManifest(sortedSpaceNames(validSpaces)); err != nil {
			fmt.Printf("Warning: failed to reconcile %s: %v\n", sm.manifestFilePath, err)
		}
	}

	return true, nil
}

func (sm *SpaceManager) loadLegacyMetadataJSON() error {
	data, err := os.ReadFile(sm.metaFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var metas []spaceMeta
	if err := json.Unmarshal(data, &metas); err != nil {
		return fmt.Errorf("failed to parse legacy metadata.json: %w", err)
	}

	normalized := make([]spaceMeta, 0, len(metas))
	for _, meta := range metas {
		meta = normalizeSpaceMeta(meta)
		if err := sm.loadSpace(meta); err != nil {
			fmt.Printf("❌ Failed to open legacy space '%s': %v\n", meta.Name, err)
			continue
		}
		normalized = append(normalized, meta)
	}

	if len(normalized) == 0 {
		return nil
	}

	if err := sm.migrateLegacyMetadata(normalized); err != nil {
		fmt.Printf("Warning: failed to migrate legacy metadata.json to current layout: %v\n", err)
	}
	return nil
}

func (sm *SpaceManager) migrateLegacyMetadata(metas []spaceMeta) error {
	written := make([]string, 0, len(metas))

	// TODO: Remove this compatibility path when support for 1.0.5 is dropped.
	for _, meta := range metas {
		metaPath := filepath.Join(sm.baseDir, meta.Name, spaceMetaFileName)
		if _, err := os.Stat(metaPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}

		if err := sm.writeJSONFile(metaPath, meta); err != nil {
			for _, writtenPath := range written {
				_ = os.Remove(writtenPath)
			}
			return err
		}
		written = append(written, metaPath)
	}

	names := make([]string, 0, len(metas))
	for _, meta := range metas {
		names = append(names, meta.Name)
	}
	sort.Strings(names)
	if err := sm.rewriteManifest(names); err != nil {
		for _, writtenPath := range written {
			_ = os.Remove(writtenPath)
		}
		return err
	}
	return nil
}

func (sm *SpaceManager) discoverSpaceMetaFiles() (map[string]struct{}, error) {
	entries, err := os.ReadDir(sm.baseDir)
	if err != nil {
		return nil, err
	}

	discovered := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metaPath := filepath.Join(sm.baseDir, entry.Name(), spaceMetaFileName)
		info, err := os.Stat(metaPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		discovered[entry.Name()] = struct{}{}
	}

	return discovered, nil
}

func (sm *SpaceManager) readManifestSpaces() (map[string]struct{}, bool, error) {
	file, err := os.Open(sm.manifestFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer file.Close()

	active := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record manifestRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, true, err
		}

		switch record.Op {
		case manifestOpCreate:
			active[record.Space] = struct{}{}
		case manifestOpDelete:
			delete(active, record.Space)
		default:
			return nil, true, fmt.Errorf("unknown manifest op %q", record.Op)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, true, err
	}
	return active, true, nil
}

func (sm *SpaceManager) readSpaceMetaFile(path string) (spaceMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return spaceMeta{}, err
	}

	var meta spaceMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return spaceMeta{}, err
	}
	return normalizeSpaceMeta(meta), nil
}

func (sm *SpaceManager) loadSpace(meta spaceMeta) error {
	engine, err := sm.openSpaceEngine(meta)
	if err != nil {
		return err
	}
	sm.spaceMetas[meta.Name] = meta
	sm.spaces[meta.Name] = engine
	return nil
}

func normalizeSpaceMeta(meta spaceMeta) spaceMeta {
	if meta.LayoutVersion == 0 {
		meta.LayoutVersion = currentSpaceLayoutVersion
	}
	switch meta.EngineType {
	case "vector":
		if meta.IndexType == "" {
			meta.IndexType = "Flat"
		}
	case "key-value":
		meta.Dimension = 0
		meta.IndexType = ""
		meta.Metric = ""
		meta.IndexedMetadataFields = nil
	}
	if meta.EngineType == "vector" {
		settings := storage.NormalizeVectorSpaceSettings(meta.IndexType, storage.SpaceSettings{
			SegmentRolloverBytes:   meta.SegmentRolloverBytes,
			MaxSegmentsBeforeMerge: meta.MaxSegmentsBeforeMerge,
		})
		meta.SegmentRolloverBytes = settings.SegmentRolloverBytes
		meta.MaxSegmentsBeforeMerge = settings.MaxSegmentsBeforeMerge
	} else {
		settings := storage.NormalizeSpaceSettings(storage.SpaceSettings{
			SegmentRolloverBytes:   meta.SegmentRolloverBytes,
			MaxSegmentsBeforeMerge: meta.MaxSegmentsBeforeMerge,
		})
		meta.SegmentRolloverBytes = settings.SegmentRolloverBytes
		meta.MaxSegmentsBeforeMerge = settings.MaxSegmentsBeforeMerge
	}
	return meta
}

func (sm *SpaceManager) writeSpaceMeta(meta spaceMeta) error {
	return sm.writeJSONFile(filepath.Join(sm.baseDir, meta.Name, spaceMetaFileName), normalizeSpaceMeta(meta))
}

func (sm *SpaceManager) removeSpaceMeta(space string) error {
	metaPath := filepath.Join(sm.baseDir, space, spaceMetaFileName)
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDir(filepath.Dir(metaPath))
}

func (sm *SpaceManager) openSpaceEngine(meta spaceMeta) (interface{}, error) {
	meta = normalizeSpaceMeta(meta)
	spacePath := filepath.Join(sm.baseDir, meta.Name)
	settings := storage.SpaceSettings{
		SegmentRolloverBytes:   meta.SegmentRolloverBytes,
		MaxSegmentsBeforeMerge: meta.MaxSegmentsBeforeMerge,
	}
	if meta.EngineType == "key-value" {
		dataFile := filepath.Join(spacePath, "data.db")
		walFile := filepath.Join(spacePath, "wal.db")
		indexFile := filepath.Join(spacePath, "index.dat")
		return storage.OpenDBWithPathsAndWALAndSettings(dataFile, walFile, indexFile, meta.EnableWAL, settings)
	}
	if meta.EngineType == "vector" {
		if meta.IndexType == "Flat" && len(meta.IndexedMetadataFields) > 0 {
			dataFile := filepath.Join(spacePath, "flat_meta_data.db")
			walFile := filepath.Join(spacePath, "flat_meta_wal.db")
			return storage.NewFlatMetaVectorEngineWithSettings(dataFile, walFile, meta.Dimension, getFAISSMetric(meta.Metric), meta.IndexedMetadataFields, meta.EnableWAL, settings)
		}
		dataFile := filepath.Join(spacePath, "vector_data.db")
		indexFile := filepath.Join(spacePath, "vector_index.faiss")
		walFile := filepath.Join(spacePath, "vector_wal.db")
		return storage.NewVectorEngineWithSettings(dataFile, indexFile, walFile, meta.Dimension, meta.IndexType, getFAISSMetric(meta.Metric), meta.EnableWAL, settings)
	}
	return nil, fmt.Errorf("unknown engine type: %s", meta.EngineType)
}

func (sm *SpaceManager) GetSpace(space string) (interface{}, bool) {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	db, ok := sm.spaces[space]
	return db, ok
}

func (sm *SpaceManager) UseSpace(space string) (interface{}, error) {
	sm.lock.RLock()
	defer sm.lock.RUnlock()

	if db, exists := sm.spaces[space]; exists {
		return db, nil
	}

	return nil, errors.New("space not found")
}

func (sm *SpaceManager) CreateSpace(space, engineType string, dimension int, indexType string, metric string) (interface{}, error) {
	enableWAL := false
	return sm.CreateSpaceWithSettings(space, engineType, dimension, indexType, metric, enableWAL, storage.SpaceSettings{})
}

func (sm *SpaceManager) CreateSpaceWithWAL(space, engineType string, dimension int, indexType string, metric string, enableWAL bool) (interface{}, error) {
	return sm.CreateSpaceWithSettings(space, engineType, dimension, indexType, metric, enableWAL, storage.SpaceSettings{})
}

func (sm *SpaceManager) CreateSpaceWithSettings(space, engineType string, dimension int, indexType string, metric string, enableWAL bool, settings storage.SpaceSettings) (interface{}, error) {
	return sm.CreateSpaceWithSettingsAndMetadata(space, engineType, dimension, indexType, metric, enableWAL, settings, nil)
}

func (sm *SpaceManager) CreateSpaceWithSettingsAndMetadata(space, engineType string, dimension int, indexType string, metric string, enableWAL bool, settings storage.SpaceSettings, indexedMetadataFields []storage.MetadataFieldSpec) (interface{}, error) {
	sm.lock.Lock()
	defer sm.lock.Unlock()

	if _, exists := sm.spaces[space]; exists {
		return nil, errors.New("space already exists")
	}
	if _, exists := sm.spaceMetas[space]; exists {
		return nil, errors.New("space already exists")
	}

	meta := normalizeSpaceMeta(spaceMeta{
		Name:                   space,
		EngineType:             engineType,
		Dimension:              dimension,
		IndexType:              indexType,
		Metric:                 metric,
		EnableWAL:              enableWAL,
		SegmentRolloverBytes:   settings.SegmentRolloverBytes,
		MaxSegmentsBeforeMerge: settings.MaxSegmentsBeforeMerge,
		IndexedMetadataFields:  indexedMetadataFields,
		LayoutVersion:          currentSpaceLayoutVersion,
	})

	if engineType == "vector" {
		if !isAllowedIndexType(meta.IndexType) {
			return nil, fmt.Errorf("index type '%s' is not allowed", meta.IndexType)
		}
		if !isAllowedMetric(metric) {
			return nil, fmt.Errorf("metric '%s' is not allowed", metric)
		}
		if len(meta.IndexedMetadataFields) > 0 {
			if meta.IndexType != "Flat" {
				return nil, fmt.Errorf("indexed metadata fields are only supported for the Flat index type, got '%s'", meta.IndexType)
			}
			if err := storage.ValidateFieldSpecs(meta.IndexedMetadataFields); err != nil {
				return nil, err
			}
		}
	} else if len(meta.IndexedMetadataFields) > 0 {
		return nil, errors.New("indexed metadata fields are only supported for vector spaces")
	}

	spacePath := filepath.Join(sm.baseDir, space)
	_, statErr := os.Stat(spacePath)
	spaceDirExisted := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if err := os.MkdirAll(spacePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create space dir: %w", err)
	}

	engine, err := sm.openSpaceEngine(meta)
	if err != nil {
		if !spaceDirExisted {
			_ = os.RemoveAll(spacePath)
		}
		return nil, err
	}

	if err := sm.writeSpaceMeta(meta); err != nil {
		closeIfPossible(engine)
		if !spaceDirExisted {
			_ = os.RemoveAll(spacePath)
		}
		return nil, err
	}

	record := manifestRecord{Version: currentSpaceLayoutVersion, Op: manifestOpCreate, Space: space}
	if err := sm.appendManifest(record); err != nil {
		_ = sm.removeSpaceMeta(space)
		closeIfPossible(engine)
		if !spaceDirExisted {
			_ = os.RemoveAll(spacePath)
		}
		return nil, err
	}

	sm.spaces[space] = engine
	sm.spaceMetas[space] = meta
	return engine, nil
}

func (sm *SpaceManager) UpdateSpaceSettings(space string, settings storage.SpaceSettings) error {
	sm.lock.Lock()
	defer sm.lock.Unlock()

	meta, exists := sm.spaceMetas[space]
	if !exists {
		return errors.New("space does not exist")
	}

	var applied storage.SpaceSettings
	if meta.EngineType == "vector" && !storage.VectorSegmentsEnabled(meta.IndexType) {
		if settings.SegmentRolloverBytes > 0 || settings.MaxSegmentsBeforeMerge > 0 {
			return fmt.Errorf("segment settings do not apply to vector index type %q (only Flat and HNSW use segmented storage)", meta.IndexType)
		}
		meta.SegmentRolloverBytes = 0
		meta.MaxSegmentsBeforeMerge = 0
		applied = storage.NormalizeVectorSpaceSettings(meta.IndexType, storage.SpaceSettings{})
	} else {
		if settings.SegmentRolloverBytes > 0 {
			meta.SegmentRolloverBytes = settings.SegmentRolloverBytes
		}
		if settings.MaxSegmentsBeforeMerge > 0 {
			meta.MaxSegmentsBeforeMerge = settings.MaxSegmentsBeforeMerge
		}
		applied = storage.NormalizeSpaceSettings(storage.SpaceSettings{
			SegmentRolloverBytes:   meta.SegmentRolloverBytes,
			MaxSegmentsBeforeMerge: meta.MaxSegmentsBeforeMerge,
		})
		meta.SegmentRolloverBytes = applied.SegmentRolloverBytes
		meta.MaxSegmentsBeforeMerge = applied.MaxSegmentsBeforeMerge
	}

	if engine, ok := sm.spaces[space]; ok {
		applier, ok := engine.(storage.SpaceSettingsApplier)
		if !ok {
			return errors.New("space engine does not support live settings updates")
		}
		if err := applier.UpdateSpaceSettings(applied); err != nil {
			return err
		}
	}

	if err := sm.writeSpaceMeta(meta); err != nil {
		return err
	}

	sm.spaceMetas[space] = meta
	return nil
}

func getFAISSMetric(metric string) int {
	faissMetric := faiss.MetricL2
	if metric == "InnerProduct" {
		faissMetric = faiss.MetricInnerProduct
	}
	if metric == "L2" {
		faissMetric = faiss.MetricL2
	}
	if metric == "L1" {
		faissMetric = faiss.MetricL1
	}
	if metric == "Lp" {
		faissMetric = faiss.MetricLp
	}
	if metric == "Canberra" {
		faissMetric = faiss.MetricCanberra
	}
	if metric == "BrayCurtis" {
		faissMetric = faiss.MetricBrayCurtis
	}
	if metric == "JensenShannon" {
		faissMetric = faiss.MetricJensenShannon
	}
	if metric == "Linf" {
		faissMetric = faiss.MetricLinf
	}
	return faissMetric
}

func (sm *SpaceManager) DeleteSpace(space string) error {
	sm.lock.Lock()
	defer sm.lock.Unlock()

	meta, exists := sm.spaceMetas[space]
	if !exists {
		return errors.New("space does not exist")
	}

	if err := sm.removeSpaceMeta(space); err != nil {
		return fmt.Errorf("failed to remove space metadata: %w", err)
	}

	record := manifestRecord{Version: currentSpaceLayoutVersion, Op: manifestOpDelete, Space: space}
	if err := sm.appendManifest(record); err != nil {
		if restoreErr := sm.writeSpaceMeta(meta); restoreErr != nil {
			return fmt.Errorf("failed to append delete manifest: %v (restore failed: %v)", err, restoreErr)
		}
		return fmt.Errorf("failed to append delete manifest: %w", err)
	}

	if db, exists := sm.spaces[space]; exists {
		closeIfPossible(db)
		delete(sm.spaces, space)
	}
	delete(sm.spaceMetas, space)

	spacePath := filepath.Join(sm.baseDir, space)
	if err := os.RemoveAll(spacePath); err != nil {
		return fmt.Errorf("failed to delete space directory: %w", err)
	}
	return nil
}

func (sm *SpaceManager) ListSpaces() []string {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	names := make([]string, 0, len(sm.spaceMetas))
	for name := range sm.spaceMetas {
		names = append(names, name)
	}
	return names
}

func (sm *SpaceManager) CloseAll() {
	sm.lock.Lock()
	defer sm.lock.Unlock()
	for name, db := range sm.spaces {
		if closer, ok := db.(interface{ Close() error }); ok {
			closer.Close()
		}
		delete(sm.spaces, name)
	}
}

func (sm *SpaceManager) SpaceMeta(space string) (spaceMeta, bool) {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	meta, ok := sm.spaceMetas[space]
	return meta, ok
}

func closeIfPossible(value interface{}) {
	if closer, ok := value.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func maybeLogSpaceRestoreProgress(processed, total, loaded, failures int, startedAt time.Time, lastLogAt *time.Time, lastSpace string) {
	if total == 0 {
		return
	}
	if processed != total && processed%spaceLoadProgressStep != 0 && time.Since(*lastLogAt) < spaceLoadProgressInterval {
		return
	}

	*lastLogAt = time.Now()
	fmt.Printf(
		"Space restore progress: %d/%d processed, %d loaded, %d failed (elapsed: %s, last: %s)\n",
		processed,
		total,
		loaded,
		failures,
		formatSpaceRestoreDuration(time.Since(startedAt)),
		lastSpace,
	)
}

func formatSpaceRestoreDuration(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}

func sameSpaceSet(left map[string]struct{}, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for name := range left {
		if _, ok := right[name]; !ok {
			return false
		}
	}
	return true
}

func sortedSpaceNames(spaces map[string]struct{}) []string {
	names := make([]string, 0, len(spaces))
	for name := range spaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeAtomicJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomicFile(path, data, 0644)
}

func rewriteManifest(path string, spaces []string) error {
	var buffer bytes.Buffer
	for _, space := range spaces {
		record := manifestRecord{
			Version: currentSpaceLayoutVersion,
			Op:      manifestOpCreate,
			Space:   space,
		}
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	return writeAtomicFile(path, buffer.Bytes(), 0644)
}

func appendManifestRecord(path string, record manifestRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}

	success = true
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
