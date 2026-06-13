package storage

const (
	DefaultSegmentRolloverBytes   int64 = 50 * 1024 * 1024
	DefaultMaxSegmentsBeforeMerge       = 20
)

type SpaceSettings struct {
	SegmentRolloverBytes   int64 `json:"segment_rollover_bytes,omitempty"`
	MaxSegmentsBeforeMerge int   `json:"max_segments_before_merge,omitempty"`
}

func NormalizeSpaceSettings(settings SpaceSettings) SpaceSettings {
	if settings.SegmentRolloverBytes <= 0 {
		settings.SegmentRolloverBytes = DefaultSegmentRolloverBytes
	}
	if settings.MaxSegmentsBeforeMerge <= 0 {
		settings.MaxSegmentsBeforeMerge = DefaultMaxSegmentsBeforeMerge
	}
	return settings
}

// VectorSegmentsEnabled is true for index types that use segmented vector storage
// (Flat, HNSW). Training-based indexes (IVF, PQ, …) use a single file and ignore
// segment rollover / merge settings.
func VectorSegmentsEnabled(indexType string) bool {
	return requiredTrainCountForIndex(indexType) == 0
}

// NormalizeVectorSpaceSettings applies segment defaults for Flat/HNSW, and clears
// segment fields for training-based vector index types.
func NormalizeVectorSpaceSettings(indexType string, settings SpaceSettings) SpaceSettings {
	if !VectorSegmentsEnabled(indexType) {
		return SpaceSettings{}
	}
	return NormalizeSpaceSettings(settings)
}

type SpaceSettingsApplier interface {
	UpdateSpaceSettings(settings SpaceSettings) error
}

type KeyValueEngine interface {
	Close() error
	Put(key, value string) error
	Get(key string) (string, error)
	Delete(key string) error
}

type VectorEngine interface {
	InsertVector(id int64, vector []float32) error
	RemoveVector(id int64) error
	SearchTopK(query []float32, k int) ([]int64, []float32, error)
	RangeSearch(query []float32, radius float32) ([]int64, []float32, error)
	GetVectorByID(id int64) ([]float32, error)
	Close() error
}

// FilterableVectorEngine is a VectorEngine that stores per-vector metadata for
// a set of declared indexed fields and supports metadata-filtered search.
type FilterableVectorEngine interface {
	VectorEngine
	IndexedFields() []MetadataFieldSpec
	InsertVectorWithMetadata(id int64, vector []float32, metadata map[string]any) error
	SearchTopKFiltered(query []float32, k int, filter *MetadataFilter) ([]int64, []float32, error)
	RangeSearchFiltered(query []float32, radius float32, filter *MetadataFilter) ([]int64, []float32, error)
}
