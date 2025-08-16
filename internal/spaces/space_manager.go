package spaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Podcopic-Labs/ShibuDb/internal/storage"

	"github.com/DataIntelligenceCrew/go-faiss"
)

var allowedIndexTypes = []string{"Flat", "HNSW", "IVF", "PQ"}
var allowedMetrics = []string{"L2", "InnerProduct", "L1", "Lp", "Canberra", "BrayCurtis", "JensenShannon", "Linf"}

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
	Name       string `json:"name"`
	EngineType string `json:"engine_type"`
	Dimension  int    `json:"dimension,omitempty"`
	IndexType  string `json:"index_type,omitempty"`
	Metric     string `json:"metric,omitempty"`
}

type SpaceManager struct {
	lock         sync.RWMutex
	spaces       map[string]interface{} // can be KeyValueEngine or VectorEngine
	spaceMetas   map[string]spaceMeta
	baseDir      string
	metaFilePath string
}

func NewSpaceManager(basePath string) *SpaceManager {
	os.MkdirAll(basePath, 0755)

	manager := &SpaceManager{
		spaces:       make(map[string]interface{}),
		spaceMetas:   make(map[string]spaceMeta),
		baseDir:      basePath,
		metaFilePath: filepath.Join(basePath, "metadata.json"),
	}
	manager.loadSpaceMetas()
	return manager
}

func (sm *SpaceManager) loadSpaceMetas() {
	data, err := os.ReadFile(sm.metaFilePath)
	if err != nil {
		return // file might not exist yet
	}
	var metas []spaceMeta
	if err := json.Unmarshal(data, &metas); err == nil {
		for _, meta := range metas {
			sm.spaceMetas[meta.Name] = meta
			spacePath := filepath.Join(sm.baseDir, meta.Name)
			if meta.EngineType == "key-value" {
				dataFile := filepath.Join(spacePath, "data.db")
				walFile := filepath.Join(spacePath, "wal.db")
				indexFile := filepath.Join(spacePath, "index.dat")
				db, err := storage.OpenDBWithPaths(dataFile, walFile, indexFile)
				if err == nil {
					sm.spaces[meta.Name] = db
				} else {
					fmt.Printf("❌ Failed to open key-value space '%s': %v\n", meta.Name, err)
				}
			} else if meta.EngineType == "vector" {
				dataFile := filepath.Join(spacePath, "vector_data.db")
				indexFile := filepath.Join(spacePath, "vector_index.faiss")
				walFile := filepath.Join(spacePath, "vector_wal.db")

				// Use stored index type and metric, with defaults
				indexType := meta.IndexType
				if indexType == "" {
					indexType = "Flat"
				}

				metric := getFAISSMetric(meta.Metric)
				ve, err := storage.NewVectorEngine(dataFile, indexFile, walFile, meta.Dimension, indexType, metric)
				if err == nil {
					sm.spaces[meta.Name] = ve
				} else {
					fmt.Printf("❌ Failed to open vector space '%s': %v\n", meta.Name, err)
				}
			}
		}
	}
}

func (sm *SpaceManager) saveSpaceMetas() {
	metas := make([]spaceMeta, 0, len(sm.spaceMetas))
	for _, meta := range sm.spaceMetas {
		metas = append(metas, meta)
	}
	data, _ := json.MarshalIndent(metas, "", "  ")
	_ = os.WriteFile(sm.metaFilePath, data, 0644)
}

func (sm *SpaceManager) GetSpace(space string) (interface{}, bool) {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	db, ok := sm.spaces[space]
	return db, ok
}

func (sm *SpaceManager) UseSpace(space string) (interface{}, error) {
	sm.lock.Lock()
	defer sm.lock.Unlock()

	if db, exists := sm.spaces[space]; exists {
		return db, nil
	}

	return nil, errors.New("space not found")
}

func (sm *SpaceManager) CreateSpace(space, engineType string, dimension int, indexType string, metric string) (interface{}, error) {
	sm.lock.Lock()
	defer sm.lock.Unlock()

	if _, exists := sm.spaces[space]; exists {
		return nil, errors.New("space already exists")
	}
	if _, exists := sm.spaceMetas[space]; exists {
		return nil, errors.New("space already exists")
	}

	meta := spaceMeta{Name: space, EngineType: engineType, Dimension: dimension, IndexType: indexType, Metric: metric}
	spacePath := filepath.Join(sm.baseDir, space)
	if err := os.MkdirAll(spacePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create space dir: %w", err)
	}

	var engine interface{}
	if engineType == "key-value" {
		dataFile := filepath.Join(spacePath, "data.db")
		walFile := filepath.Join(spacePath, "wal.db")
		indexFile := filepath.Join(spacePath, "index.dat")
		db, err := storage.OpenDBWithPaths(dataFile, walFile, indexFile)
		if err != nil {
			return nil, err
		}
		engine = db
	} else if engineType == "vector" {
		if !isAllowedIndexType(indexType) {
			return nil, fmt.Errorf("index type '%s' is not allowed", indexType)
		}
		if !isAllowedMetric(metric) {
			return nil, fmt.Errorf("metric '%s' is not allowed", metric)
		}
		dataFile := filepath.Join(spacePath, "vector_data.db")
		indexFile := filepath.Join(spacePath, "vector_index.faiss")
		walFile := filepath.Join(spacePath, "vector_wal.db")
		ve, err := storage.NewVectorEngine(dataFile, indexFile, walFile, dimension, indexType, getFAISSMetric(metric))
		if err != nil {
			return nil, err
		}
		engine = ve
	} else {
		return nil, fmt.Errorf("unknown engine type: %s", engineType)
	}

	sm.spaces[space] = engine
	sm.spaceMetas[space] = meta
	sm.saveSpaceMetas()
	return engine, nil
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

	if _, exists := sm.spaceMetas[space]; !exists {
		return errors.New("space does not exist")
	}

	if db, exists := sm.spaces[space]; exists {
		if closer, ok := db.(interface{ Close() error }); ok {
			closer.Close()
		}
		delete(sm.spaces, space)
	}

	spacePath := filepath.Join(sm.baseDir, space)
	if err := os.RemoveAll(spacePath); err != nil {
		return fmt.Errorf("failed to delete space directory: %w", err)
	}

	delete(sm.spaceMetas, space)
	sm.saveSpaceMetas()
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
	meta, ok := sm.spaceMetas[space]
	return meta, ok
}
