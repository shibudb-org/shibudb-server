// Package gpudist provides optional GPU batch distance computation for the
// in-house Flat metadata vector engine. When built without CUDA support, or
// when no usable GPU is present at runtime, callers fall back to CPU scoring.
package gpudist

import (
	"os"
	"strconv"
	"sync"
)

// Metric constants mirror faiss.MetricType so FlatMeta can pass engine.metric
// through unchanged.
const (
	MetricInnerProduct  = 0
	MetricL2            = 1
	MetricL1            = 2
	MetricLinf          = 3
	MetricLp            = 4
	MetricCanberra      = 20
	MetricBrayCurtis    = 21
	MetricJensenShannon = 22
)

// MinCandidates is the default candidate-count threshold below which GPU
// launch overhead is unlikely to win. Override with SHIBUDB_FLAT_META_GPU_MIN.
const MinCandidates = 256

var (
	initOnce sync.Once
	enabled  bool
)

// Available reports whether GPU distance scoring should be used.
func Available() bool {
	initOnce.Do(func() {
		enabled = probeGPU()
	})
	return enabled
}

// MinCandidatesFromEnv returns the configured minimum candidate count for GPU.
func MinCandidatesFromEnv() int {
	v := os.Getenv("SHIBUDB_FLAT_META_GPU_MIN")
	if v == "" {
		return MinCandidates
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return MinCandidates
	}
	return n
}

// BatchDistances computes one distance per row of matrix against query.
// matrix is row-major with n rows of dim floats. out must have length n.
// Returns false when the GPU path cannot be used (caller should CPU-score).
func BatchDistances(metric int, query []float32, matrix []float32, n, dim int, out []float32) bool {
	if !Available() || n <= 0 || dim <= 0 {
		return false
	}
	if len(query) < dim || len(matrix) < n*dim || len(out) < n {
		return false
	}
	return batchDistancesGPU(metric, query, matrix, n, dim, out)
}
