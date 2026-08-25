//go:build cuda

package gpudist

/*
#cgo CFLAGS: -I${SRCDIR}/cuda
#cgo LDFLAGS: -L${SRCDIR}/cuda -lshibudb_gpudist -lcudart -Wl,-rpath,${SRCDIR}/cuda
#include "distances.h"
*/
import "C"
import (
	"os"
	"unsafe"
)

func probeGPU() bool {
	if v := os.Getenv("SHIBUDB_FLAT_META_GPU"); v == "0" || v == "false" || v == "off" {
		return false
	}
	return C.shibudb_gpudist_available() != 0
}

func batchDistancesGPU(metric int, query []float32, matrix []float32, n, dim int, out []float32) bool {
	rc := C.shibudb_gpudist_batch(
		C.int(metric),
		(*C.float)(unsafe.Pointer(&query[0])),
		(*C.float)(unsafe.Pointer(&matrix[0])),
		C.int(n),
		C.int(dim),
		(*C.float)(unsafe.Pointer(&out[0])),
	)
	return rc == 0
}
