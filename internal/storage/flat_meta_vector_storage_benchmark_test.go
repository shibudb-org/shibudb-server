package storage

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/DataIntelligenceCrew/go-faiss"
)

// Benchmarks for the filterable Flat vector engine (FlatMetaVectorEngine).
// They exercise the two paths that distinguish this engine from the FAISS
// engine: insert with metadata, and metadata pre-filtered search. The search
// benchmarks vary filter selectivity to show how pre-filtering shrinks the
// candidate set that distances are computed over.

const (
	benchFlatMetaDim        = 64
	benchFlatMetaDataset    = 10000
	benchFlatMetaTenants    = 100 // ~1% of the dataset per user_id
	benchFlatMetaCategories = 10  // ~10% of the dataset per category
	benchFlatMetaK          = 10
	benchFlatMetaPool       = 4096
)

func benchFlatMetaFields() []MetadataFieldSpec {
	return []MetadataFieldSpec{
		{Name: "user_id", Type: MetadataTypeString},
		{Name: "category", Type: MetadataTypeString},
		{Name: "price", Type: MetadataTypeFloat},
		{Name: "year", Type: MetadataTypeInt},
	}
}

// benchFlatMetaMeta builds deterministic metadata for the i-th vector.
func benchFlatMetaMeta(i int) map[string]any {
	return map[string]any{
		"user_id":  fmt.Sprintf("user_%d", i%benchFlatMetaTenants),
		"category": fmt.Sprintf("cat_%d", i%benchFlatMetaCategories),
		"price":    float64(i % 1000),
		"year":     2000 + (i % 25),
	}
}

func newBenchFlatMetaEngine(b *testing.B, enableWAL bool) *FlatMetaVectorEngine {
	b.Helper()
	dir := b.TempDir()
	e, err := NewFlatMetaVectorEngine(
		filepath.Join(dir, "flat_meta_data.db"),
		filepath.Join(dir, "flat_meta_wal.db"),
		benchFlatMetaDim, faiss.MetricL2, benchFlatMetaFields(), enableWAL,
	)
	if err != nil {
		b.Fatalf("NewFlatMetaVectorEngine: %v", err)
	}
	b.Cleanup(func() { _ = e.Close() })
	return e
}

// populateBenchFlatMeta seeds n vectors with metadata and returns a small pool
// of the inserted vectors to reuse as query vectors.
func populateBenchFlatMeta(b *testing.B, e *FlatMetaVectorEngine, n int) [][]float32 {
	b.Helper()
	queries := make([][]float32, 0, 256)
	for i := 0; i < n; i++ {
		vec := randomVector(benchFlatMetaDim)
		if err := e.InsertVectorWithMetadata(int64(i+1), vec, benchFlatMetaMeta(i)); err != nil {
			b.Fatalf("seed insert %d: %v", i, err)
		}
		if len(queries) < cap(queries) {
			queries = append(queries, vec)
		}
	}
	return queries
}

func BenchmarkFlatMetaInsertWithMetadata(b *testing.B)    { benchmarkFlatMetaInsert(b, false) }
func BenchmarkFlatMetaInsertWithMetadataWAL(b *testing.B) { benchmarkFlatMetaInsert(b, true) }

func benchmarkFlatMetaInsert(b *testing.B, enableWAL bool) {
	e := newBenchFlatMetaEngine(b, enableWAL)

	// Pre-build a reusable pool so vector/metadata construction is not timed.
	vecs := make([][]float32, benchFlatMetaPool)
	metas := make([]map[string]any, benchFlatMetaPool)
	for i := 0; i < benchFlatMetaPool; i++ {
		vecs[i] = randomVector(benchFlatMetaDim)
		metas[i] = benchFlatMetaMeta(i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % benchFlatMetaPool
		if err := e.InsertVectorWithMetadata(int64(i+1), vecs[idx], metas[idx]); err != nil {
			b.Fatalf("insert: %v", err)
		}
	}
}

func BenchmarkFlatMetaSearchTopK(b *testing.B) {
	e := newBenchFlatMetaEngine(b, false)
	queries := populateBenchFlatMeta(b, e, benchFlatMetaDataset)

	run := func(name string, filter *MetadataFilter) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q := queries[i%len(queries)]
				var err error
				if filter == nil {
					_, _, err = e.SearchTopK(q, benchFlatMetaK)
				} else {
					_, _, err = e.SearchTopKFiltered(q, benchFlatMetaK, filter)
				}
				if err != nil {
					b.Fatalf("search: %v", err)
				}
			}
		})
	}

	// Baseline: no filter scans the whole dataset.
	run("NoFilter", nil)
	// High selectivity (~1%): single tenant.
	run("FilterOneTenant", &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "user_7"})
	// Medium selectivity (~10%): single category.
	run("FilterOneCategory", &MetadataFilter{Op: FilterOpEq, Field: "category", Value: "cat_3"})
	// Composite: tenant AND numeric comparison.
	run("FilterAndTenantPrice", &MetadataFilter{Op: FilterOpAnd, Filters: []*MetadataFilter{
		{Op: FilterOpEq, Field: "user_id", Value: "user_7"},
		{Op: FilterOpLt, Field: "price", Value: 500.0},
	}})
	// Numeric range over the int field.
	run("FilterBetweenYear", &MetadataFilter{Op: FilterOpBetween, Field: "year", Values: []any{2005, 2010}})
}

func BenchmarkFlatMetaRangeSearchFiltered(b *testing.B) {
	e := newBenchFlatMetaEngine(b, false)
	queries := populateBenchFlatMeta(b, e, benchFlatMetaDataset)

	filter := &MetadataFilter{Op: FilterOpEq, Field: "user_id", Value: "user_7"}
	// Generous squared-L2 radius so the candidate set is fully scored/collected.
	radius := float32(benchFlatMetaDim)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := e.RangeSearchFiltered(queries[i%len(queries)], radius, filter); err != nil {
			b.Fatalf("range search: %v", err)
		}
	}
}
