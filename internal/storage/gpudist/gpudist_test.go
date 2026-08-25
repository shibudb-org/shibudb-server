package gpudist

import "testing"

func TestAvailableWithoutLibrary(t *testing.T) {
	t.Setenv("SHIBUDB_FLAT_META_GPU", "0")
	// Force re-init is not possible after Available() once; this env is checked
	// inside probeGPU on first Available() call. Use a fresh process semantics:
	// without CUDA library / with GPU forced off, BatchDistances must fail.
	query := []float32{1, 2}
	matrix := []float32{1, 2, 3, 4}
	out := make([]float32, 2)
	if BatchDistances(MetricL2, query, matrix, 2, 2, out) {
		t.Fatalf("BatchDistances should return false when GPU is disabled")
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
