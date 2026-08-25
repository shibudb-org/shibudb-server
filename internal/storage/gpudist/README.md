# FlatMeta GPU distance (optional runtime)

ShibuDB always ships FlatMeta GPU **support** in the Linux binary. Distance
scoring uses an NVIDIA GPU at runtime when:

1. `libshibudb_gpudist.so` is installed (built during Linux install when CUDA
   toolkit is present), and
2. a usable CUDA device is available.

Otherwise FlatMeta falls back to CPU automatically. This is **not** FAISS-GPU.

## Supported metrics

| Metric | FAISS id | Notes |
|--------|----------|-------|
| InnerProduct | 0 | |
| L2 (squared) | 1 | |
| L1 | 2 | |
| Linf | 3 | |
| Lp | 4 | `p=2` (same as CPU FlatMeta) |
| Canberra | 20 | |
| BrayCurtis | 21 | |
| JensenShannon | 22 | |

GPU math uses **float32**. CPU FlatMeta uses float64, so tiny numeric
differences are expected.

## Install (Linux)

One installer. No separate GPU package:

```bash
./scripts/install-linux.sh --source .
```

- If `nvcc` + CUDA runtime are present → builds and installs
  `libshibudb_gpudist.so` next to FAISS libs.
- Binary always includes GPU support (loads the library via `dlopen`).
- At runtime: GPU if library + device available, else CPU.

Skip building the GPU library (CPU-only install of the .so):

```bash
./scripts/install-linux.sh --source . --without-cuda
```

Manual library build:

```bash
make build-gpudist-cuda
```

## Verify

```bash
shibudb check-gpu
shibudb check-gpu --json
shibudb check-gpu --smoke=false
```

Exit code `0` means FlatMeta GPU scoring is ready. Exit code `1` means it will
fall back to CPU (see printed hints).

## Runtime controls

| Env var | Effect |
|---------|--------|
| `SHIBUDB_FLAT_META_GPU=0` | Force CPU |
| `SHIBUDB_FLAT_META_GPU_MIN=N` | Min candidates before GPU (default 256) |
| `SHIBUDB_GPUDIST_LIB=/path/to/libshibudb_gpudist.so` | Explicit library path |
