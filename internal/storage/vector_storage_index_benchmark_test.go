package storage

import (
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

// Benchmarks for the FAISS-backed vector engine (VectorEngineImpl) across the
// supported index types. They cover the two hot-path operations: insert and
// top-k search.
//
// Training indexes (IVF/PQ) begin on the internal Flat fallback and promote
// once enough vectors have been persisted.

const (
	benchVecDim     = 64
	benchVecDataset = 10000
	benchVecK       = 10
	benchVecPool    = 4096
)

var benchVectorIndexTypes = []string{"HNSW32", "HNSW64", "IVF32", "PQ8"}

func newBenchVectorEngine(b *testing.B, indexType string) *VectorEngineImpl {
	b.Helper()
	dir := b.TempDir()
	e, err := NewVectorEngine(
		dir+"/vec_data.db",
		dir+"/vec_index.faiss",
		dir+"/vec_wal.db",
		benchVecDim, indexType, faiss.MetricL2, false,
	)
	if err != nil {
		b.Fatalf("NewVectorEngine(%s): %v", indexType, err)
	}
	b.Cleanup(func() { _ = e.Close() })
	return e
}

func BenchmarkVectorEngineInsert(b *testing.B) {
	vecs := make([][]float32, benchVecPool)
	for i := range vecs {
		vecs[i] = randomVector(benchVecDim)
	}

	for _, indexType := range benchVectorIndexTypes {
		b.Run(indexType, func(b *testing.B) {
			e := newBenchVectorEngine(b, indexType)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := e.InsertVector(int64(i+1), vecs[i%benchVecPool]); err != nil {
					b.Fatalf("insert(%s): %v", indexType, err)
				}
			}
		})
	}
}

// seedBenchVectorEngine creates an engine of the given index type, inserts the
// standard dataset, flushes (so persisted fileOffsets exist — search filters
// candidates by them), and returns the engine plus a pool of query vectors.
func seedBenchVectorEngine(b *testing.B, indexType string) (*VectorEngineImpl, [][]float32) {
	b.Helper()
	e := newBenchVectorEngine(b, indexType)
	queries := make([][]float32, 0, 256)
	for i := 0; i < benchVecDataset; i++ {
		vec := randomVector(benchVecDim)
		if err := e.InsertVector(int64(i+1), vec); err != nil {
			b.Fatalf("seed insert(%s): %v", indexType, err)
		}
		if len(queries) < cap(queries) {
			queries = append(queries, vec)
		}
	}
	e.flushData(true)
	if requiredTrainCountForIndex(indexType) > 0 {
		deadline := time.Now().Add(30 * time.Second)
		for {
			e.lock.RLock()
			trained := e.indexMeta.Mode == VectorIndexModeTrained
			e.lock.RUnlock()
			if trained {
				break
			}
			if time.Now().After(deadline) {
				b.Fatalf("%s did not promote to its trained index", indexType)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return e, queries
}

func BenchmarkVectorEngineSearchTopK(b *testing.B) {
	for _, indexType := range benchVectorIndexTypes {
		e, queries := seedBenchVectorEngine(b, indexType)
		b.Run(indexType, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := e.SearchTopK(queries[i%len(queries)], benchVecK); err != nil {
					b.Fatalf("search(%s): %v", indexType, err)
				}
			}
		})
	}
}

func BenchmarkVectorEngineRangeSearch(b *testing.B) {
	// Squared-L2 distances between random [0,1)^dim vectors cluster around
	// ~dim/6, so this radius returns a moderate (non-trivial, non-everything)
	// candidate set.
	const radius = float32(8.0)
	for _, indexType := range benchVectorIndexTypes {
		e, queries := seedBenchVectorEngine(b, indexType)
		b.Run(indexType, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := e.RangeSearch(queries[i%len(queries)], radius); err != nil {
					b.Fatalf("range search(%s): %v", indexType, err)
				}
			}
		})
	}
}
