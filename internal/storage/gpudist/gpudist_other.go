//go:build !linux

package gpudist

func probeGPU() bool { return false }

func batchDistancesGPU(metric int, query []float32, matrix []float32, n, dim int, out []float32) bool {
	return false
}
