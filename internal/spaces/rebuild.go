package spaces

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shibudb.org/shibudb-server/internal/storage"
)

// RebuildSpaceIndex rebuilds the on-disk index for a single space directly
// from that space's data files. The server should be stopped before running it.
func RebuildSpaceIndex(baseDir, space string) (string, error) {
	space = strings.TrimSpace(space)
	if space == "" {
		return "", fmt.Errorf("space name must not be empty")
	}

	sm := &SpaceManager{}
	metaPath := filepath.Join(baseDir, space, spaceMetaFileName)
	meta, err := sm.readSpaceMetaFile(metaPath)
	if err != nil {
		return "", fmt.Errorf("read space metadata: %w", err)
	}

	spacePath := filepath.Join(baseDir, meta.Name)
	switch meta.EngineType {
	case "key-value":
		dataPath := filepath.Join(spacePath, "data.db")
		indexPath := filepath.Join(spacePath, "index.dat")
		stats, err := storage.RebuildKeyValueIndex(dataPath, indexPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"Rebuilt key-value index for space %q at %s (%d records scanned, %d live keys).",
			meta.Name, indexPath, stats.RecordsScanned, stats.LiveKeys,
		), nil
	case "vector":
		dataPath := filepath.Join(spacePath, "vector_data.db")
		indexPath := filepath.Join(spacePath, "vector_index.faiss")
		stats, err := storage.RebuildVectorIndex(dataPath, indexPath, meta.Dimension, meta.IndexType, getFAISSMetric(meta.Metric))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"Rebuilt vector index for space %q at %s (%d records scanned, %d live vectors, %d tombstoned IDs, %d training samples).",
			meta.Name, indexPath, stats.RecordsScanned, stats.LiveVectors, stats.TombstonedIDs, stats.TrainingSamples,
		), nil
	default:
		return "", fmt.Errorf("unsupported engine type %q", meta.EngineType)
	}
}
