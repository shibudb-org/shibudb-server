package gpudist

import "testing"

func TestCheckForcedOff(t *testing.T) {
	t.Setenv("SHIBUDB_FLAT_META_GPU", "0")
	st := Check(false)
	if st.Ready {
		t.Fatalf("expected not ready when forced off")
	}
	if !st.ForcedOff {
		t.Fatalf("expected ForcedOff=true")
	}
}

func TestBatchDistancesFallsBackWhenForcedOff(t *testing.T) {
	t.Setenv("SHIBUDB_FLAT_META_GPU", "0")
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

func TestFormatStatus(t *testing.T) {
	st := Status{
		PlatformSupported: true,
		Ready:             false,
		Message:           "missing library",
		Hints:             []string{"install lib"},
	}
	out := FormatStatus(st)
	if out == "" {
		t.Fatal("empty format")
	}
}
