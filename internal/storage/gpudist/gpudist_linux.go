//go:build linux

package gpudist

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef int (*shibudb_avail_fn)(void);
typedef int (*shibudb_batch_fn)(
    int metric,
    const float* query,
    const float* matrix,
    int n,
    int dim,
    float* out);

static void* shibudb_gpu_handle;
static shibudb_avail_fn shibudb_gpu_avail;
static shibudb_batch_fn shibudb_gpu_batch;

static int shibudb_gpu_load(const char* path) {
	if (shibudb_gpu_handle != NULL) {
		return 1;
	}
	shibudb_gpu_handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (shibudb_gpu_handle == NULL) {
		return 0;
	}
	shibudb_gpu_avail = (shibudb_avail_fn)dlsym(shibudb_gpu_handle, "shibudb_gpudist_available");
	shibudb_gpu_batch = (shibudb_batch_fn)dlsym(shibudb_gpu_handle, "shibudb_gpudist_batch");
	if (shibudb_gpu_avail == NULL || shibudb_gpu_batch == NULL) {
		dlclose(shibudb_gpu_handle);
		shibudb_gpu_handle = NULL;
		shibudb_gpu_avail = NULL;
		shibudb_gpu_batch = NULL;
		return 0;
	}
	return 1;
}

static int shibudb_gpu_available(void) {
	if (shibudb_gpu_avail == NULL) {
		return 0;
	}
	return shibudb_gpu_avail();
}

static int shibudb_gpu_batch_call(
    int metric,
    const float* query,
    const float* matrix,
    int n,
    int dim,
    float* out) {
	if (shibudb_gpu_batch == NULL) {
		return -1;
	}
	return shibudb_gpu_batch(metric, query, matrix, n, dim, out);
}
*/
import "C"
import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

var (
	loadedPathMu sync.Mutex
	loadedPath   string
)

func platformSupportsGPU() bool { return true }

func probeGPU() bool {
	if gpuForcedOff() {
		return false
	}
	if _, ok := loadGPULibraryPath(); !ok {
		return false
	}
	return gpuDeviceAvailable()
}

func gpuDeviceAvailable() bool {
	if _, ok := loadGPULibraryPath(); !ok {
		return false
	}
	return C.shibudb_gpu_available() != 0
}

func batchDistancesGPU(metric int, query []float32, matrix []float32, n, dim int, out []float32) bool {
	if _, ok := loadGPULibraryPath(); !ok {
		return false
	}
	rc := C.shibudb_gpu_batch_call(
		C.int(metric),
		(*C.float)(unsafe.Pointer(&query[0])),
		(*C.float)(unsafe.Pointer(&matrix[0])),
		C.int(n),
		C.int(dim),
		(*C.float)(unsafe.Pointer(&out[0])),
	)
	return rc == 0
}

func loadGPULibrary() bool {
	_, ok := loadGPULibraryPath()
	return ok
}

func loadGPULibraryPath() (string, bool) {
	loadedPathMu.Lock()
	defer loadedPathMu.Unlock()
	if loadedPath != "" {
		return loadedPath, true
	}
	for _, path := range candidateLibraryPaths() {
		cpath := C.CString(path)
		ok := C.shibudb_gpu_load(cpath) != 0
		C.free(unsafe.Pointer(cpath))
		if ok {
			loadedPath = path
			return loadedPath, true
		}
	}
	return "", false
}

func candidateLibraryPaths() []string {
	paths := make([]string, 0, 8)
	if p := os.Getenv("SHIBUDB_GPUDIST_LIB"); p != "" {
		paths = append(paths, p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(dir, "libshibudb_gpudist.so"),
			filepath.Join(dir, "..", "lib", "libshibudb_gpudist.so"),
		)
	}
	paths = append(paths,
		"libshibudb_gpudist.so",
		"/usr/local/lib/libshibudb_gpudist.so",
		"/usr/lib/libshibudb_gpudist.so",
	)
	return paths
}
