//go:build !linux

package gpudist

func platformSupportsGPU() bool { return false }

func probeGPU() bool { return false }

func gpuDeviceAvailable() bool { return false }

func gpuLastError() string { return "" }

func batchDistancesGPU(metric int, query []float32, matrix []float32, n, dim int, out []float32) bool {
	return false
}

func loadGPULibraryPath() (string, bool) { return "", false }
