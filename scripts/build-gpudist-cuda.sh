#!/usr/bin/env bash
# Build the optional FlatMeta GPU distance shared library (Linux + NVIDIA CUDA).
#
# Prerequisites:
#   - nvcc (CUDA toolkit)
#   - NVIDIA driver with a usable GPU (for runtime use)
#
# Output:
#   internal/storage/gpudist/cuda/libshibudb_gpudist.so
#
# The normal ShibuDB binary always includes GPU support and loads this library
# via dlopen when present. Prefer:
#   ./scripts/install-linux.sh --source .

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CUDA_DIR="$ROOT/internal/storage/gpudist/cuda"
OUT="$CUDA_DIR/libshibudb_gpudist.so"

if ! command -v nvcc >/dev/null 2>&1; then
	echo "error: nvcc not found; install the CUDA toolkit first" >&2
	exit 1
fi

ARCH_FLAGS="${SHIBUDB_CUDA_ARCH:--arch=native}"

echo "Building $OUT (nvcc $ARCH_FLAGS)..."
nvcc -shared -Xcompiler -fPIC -O3 $ARCH_FLAGS \
	-o "$OUT" \
	"$CUDA_DIR/distances.cu"

echo "Built $OUT"
ls -la "$OUT"
