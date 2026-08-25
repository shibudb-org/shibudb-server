# FlatMeta GPU distance (optional)

Optional CUDA acceleration for the in-house Flat metadata vector engine
(`FlatMetaVectorEngine`). This is **not** FAISS-GPU. It only speeds up the
brute-force distance loop used after metadata filtering.

## Supported metrics

All FlatMeta metrics are implemented on GPU:

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

## Build (Linux + NVIDIA)

Preferred: enable during the Linux source install (auto when `nvcc` + CUDA
runtime are present):

```bash
./scripts/install-linux.sh --source .
# force on / off:
./scripts/install-linux.sh --source . --with-cuda
./scripts/install-linux.sh --source . --without-cuda
```

Manual:

```bash
make build-gpudist-cuda
make build-cuda
```

Requires `nvcc` and a working NVIDIA driver/GPU.

Default builds (no `-tags cuda`) stay CPU-only and do not need CUDA.

## Runtime controls

| Env var | Effect |
|---------|--------|
| `SHIBUDB_FLAT_META_GPU=0` | Force CPU even when CUDA is compiled in |
| `SHIBUDB_FLAT_META_GPU_MIN=N` | Min candidate count before using GPU (default 256) |

If CUDA is unavailable at runtime, FlatMeta automatically falls back to CPU.
