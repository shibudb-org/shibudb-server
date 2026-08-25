package gpudist

import "testing"

func TestAvailableWithoutCUDATag(t *testing.T) {
	if Available() {
		t.Fatalf("Available() should be false without -tags cuda")
	}
}

func TestBatchDistancesFallsBack(t *testing.T) {
	query := []float32{1, 2}
	matrix := []float32{1, 2, 3, 4}
	out := make([]float32, 2)
	if BatchDistances(MetricL2, query, matrix, 2, 2, out) {
		t.Fatalf("BatchDistances should return false without CUDA")
	}
}

func TestMinCandidatesFromEnv(t *testing.T) {
	t.Setenv("SHIBUDB_FLAT_META_GPU_MIN", "")
	if got := MinCandidatesFromEnv(); got != MinCandidates {
		t.Fatalf("default min: got %d want %d", got, MinCandidates)
	}
	t.Setenv("SHIBUDB_FLAT_META_GPU_MIN", "1024")
	if got := MinCandidatesFromEnv(); got != 1024 {
		t.Fatalf("override min: got %d want 1024", got)
	}
}
