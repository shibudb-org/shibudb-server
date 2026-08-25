package gpudist

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

// Status is a diagnostic snapshot of FlatMeta GPU readiness.
type Status struct {
	PlatformSupported bool   `json:"platform_supported"`
	ForcedOff         bool   `json:"forced_off"`
	LibraryLoaded     bool   `json:"library_loaded"`
	LibraryPath       string `json:"library_path,omitempty"`
	DeviceAvailable   bool   `json:"device_available"`
	Ready             bool   `json:"ready"`
	MinCandidates     int    `json:"min_candidates"`
	SmokeOK           bool   `json:"smoke_ok,omitempty"`
	SmokeRan          bool   `json:"smoke_ran,omitempty"`
	Message           string `json:"message"`
	Hints             []string `json:"hints,omitempty"`
}

// Available reports whether GPU distance scoring should be used.
func Available() bool {
	if gpuForcedOff() {
		return false
	}
	initOnce.Do(func() {
		enabled = probeGPU()
	})
	return enabled
}

func gpuForcedOff() bool {
	v := os.Getenv("SHIBUDB_FLAT_META_GPU")
	return v == "0" || v == "false" || v == "off"
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

// Check reports whether FlatMeta GPU scoring is ready. When runSmoke is true and
// the device is available, it also runs a tiny distance kernel as a functional test.
func Check(runSmoke bool) Status {
	st := Status{
		PlatformSupported: platformSupportsGPU(),
		ForcedOff:         gpuForcedOff(),
		MinCandidates:     MinCandidatesFromEnv(),
	}

	if !st.PlatformSupported {
		st.Message = "FlatMeta GPU scoring is only supported on Linux"
		st.Hints = []string{"Use a Linux host with NVIDIA drivers and libshibudb_gpudist.so"}
		return st
	}

	if st.ForcedOff {
		st.Message = "FlatMeta GPU scoring is forced off by SHIBUDB_FLAT_META_GPU"
		st.Hints = []string{"Unset SHIBUDB_FLAT_META_GPU or set it to 1 to allow GPU"}
		return st
	}

	libPath, libOK := loadGPULibraryPath()
	st.LibraryLoaded = libOK
	st.LibraryPath = libPath
	if !libOK {
		st.Message = "libshibudb_gpudist.so was not found or could not be loaded"
		st.Hints = []string{
			"Install with: make build-gpudist-cuda && sudo install -m 0755 internal/storage/gpudist/cuda/libshibudb_gpudist.so /usr/local/lib/ && sudo ldconfig",
			"Or re-run scripts/install-linux.sh on a machine with nvcc",
			"Override path with SHIBUDB_GPUDIST_LIB=/path/to/libshibudb_gpudist.so",
		}
		return st
	}

	st.DeviceAvailable = gpuDeviceAvailable()
	if !st.DeviceAvailable {
		st.Message = "GPU library loaded, but no usable CUDA device was detected"
		st.Hints = []string{
			"Check nvidia-smi and that the NVIDIA driver is installed",
			"Confirm libcudart is resolvable: ldd $(SHIBUDB_GPUDIST_LIB or /usr/local/lib/libshibudb_gpudist.so)",
		}
		return st
	}

	st.Ready = true
	initOnce.Do(func() { enabled = true })

	if runSmoke {
		st.SmokeRan = true
		st.SmokeOK = runSmokeTest()
		if !st.SmokeOK {
			st.Ready = false
			st.Message = "CUDA device detected, but FlatMeta GPU smoke test failed"
			st.Hints = []string{"Check NVIDIA driver/CUDA runtime compatibility with the built library"}
			return st
		}
		st.Message = fmt.Sprintf("FlatMeta GPU scoring is ready (library=%s, smoke=ok, min_candidates=%d)", st.LibraryPath, st.MinCandidates)
		return st
	}

	st.Message = fmt.Sprintf("FlatMeta GPU scoring is ready (library=%s, min_candidates=%d)", st.LibraryPath, st.MinCandidates)
	return st
}

// FormatStatus returns a human-readable multi-line status report.
func FormatStatus(st Status) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FlatMeta GPU status\n")
	fmt.Fprintf(&b, "  ready:             %v\n", st.Ready)
	fmt.Fprintf(&b, "  platform:          %s\n", map[bool]string{true: "linux (supported)", false: "unsupported"}[st.PlatformSupported])
	fmt.Fprintf(&b, "  forced_off:        %v\n", st.ForcedOff)
	fmt.Fprintf(&b, "  library_loaded:    %v\n", st.LibraryLoaded)
	if st.LibraryPath != "" {
		fmt.Fprintf(&b, "  library_path:      %s\n", st.LibraryPath)
	}
	fmt.Fprintf(&b, "  device_available:  %v\n", st.DeviceAvailable)
	fmt.Fprintf(&b, "  min_candidates:    %d\n", st.MinCandidates)
	if st.SmokeRan {
		fmt.Fprintf(&b, "  smoke_test:        %v\n", st.SmokeOK)
	}
	fmt.Fprintf(&b, "  message:           %s\n", st.Message)
	if len(st.Hints) > 0 {
		fmt.Fprintf(&b, "  hints:\n")
		for _, h := range st.Hints {
			fmt.Fprintf(&b, "    - %s\n", h)
		}
	}
	return b.String()
}

func runSmokeTest() bool {
	const dim = 4
	const n = 8
	query := []float32{0.1, 0.2, 0.3, 0.4}
	matrix := make([]float32, n*dim)
	for i := 0; i < n; i++ {
		for d := 0; d < dim; d++ {
			matrix[i*dim+d] = float32(i)*0.01 + float32(d)*0.1
		}
	}
	out := make([]float32, n)
	if !batchDistancesGPU(MetricL2, query, matrix, n, dim, out) {
		return false
	}
	// Sanity: first distance should be finite and non-negative for L2.
	if out[0] < 0 || out[0] != out[0] {
		return false
	}
	return true
}
