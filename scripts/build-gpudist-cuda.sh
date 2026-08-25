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
# Important: nvcc/libcudart must be <= the driver CUDA version from nvidia-smi
# (e.g. "CUDA Version: 12.4" → use cuda-nvcc-12-4, not 12.6+).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CUDA_DIR="$ROOT/internal/storage/gpudist/cuda"
OUT="$CUDA_DIR/libshibudb_gpudist.so"

driver_cuda_version() {
	if ! command -v nvidia-smi >/dev/null 2>&1; then
		return 1
	fi
	nvidia-smi 2>/dev/null | sed -n 's/.*CUDA Version: \([0-9]\+\.[0-9]\+\).*/\1/p' | head -n1
}

prefer_nvcc_for_driver() {
	local ver="$1"
	local major minor candidate
	major="${ver%%.*}"
	minor="${ver##*.}"
	for candidate in \
		"/usr/local/cuda-${major}.${minor}/bin/nvcc" \
		"/usr/local/cuda-${major}-${minor}/bin/nvcc" \
		"/usr/local/cuda/bin/nvcc"; do
		if [[ -x "$candidate" ]]; then
			echo "$candidate"
			return 0
		fi
	done
	return 1
}

version_le() {
	# Returns success if $1 <= $2 (dotted versions like 12.4).
	[[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" == "$1" ]]
}

NVCC_BIN=""
if [[ -n "${SHIBUDB_NVCC:-}" && -x "${SHIBUDB_NVCC}" ]]; then
	NVCC_BIN="$SHIBUDB_NVCC"
elif DRIVER_CUDA="$(driver_cuda_version)"; then
	echo "NVIDIA driver reports CUDA Version: $DRIVER_CUDA"
	if preferred="$(prefer_nvcc_for_driver "$DRIVER_CUDA")"; then
		NVCC_BIN="$preferred"
	fi
fi

if [[ -z "$NVCC_BIN" ]]; then
	if command -v nvcc >/dev/null 2>&1; then
		NVCC_BIN="$(command -v nvcc)"
	else
		echo "error: nvcc not found; install a CUDA toolkit <= your driver CUDA version" >&2
		echo "  e.g. for driver CUDA 12.4: sudo apt-get install -y cuda-nvcc-12-4 cuda-cudart-dev-12-4" >&2
		exit 1
	fi
fi

export PATH="$(dirname "$NVCC_BIN"):${PATH:-}"
echo "Using nvcc: $NVCC_BIN"
"$NVCC_BIN" --version || true

if DRIVER_CUDA="$(driver_cuda_version)"; then
	# nvcc --version contains "release 12.6," etc.
	NVCC_REL="$("$NVCC_BIN" --version 2>/dev/null | sed -n 's/.*release \([0-9]\+\.[0-9]\+\).*/\1/p' | head -n1)"
	if [[ -n "$NVCC_REL" ]] && ! version_le "$NVCC_REL" "$DRIVER_CUDA"; then
		cat >&2 <<EOF
error: nvcc $NVCC_REL is newer than driver CUDA $DRIVER_CUDA.
This produces: "CUDA driver version is insufficient for CUDA runtime version".

Install/use a matching toolkit, then rebuild:
  sudo apt-get install -y cuda-nvcc-${DRIVER_CUDA/./-} cuda-cudart-dev-${DRIVER_CUDA/./-}
  export PATH=/usr/local/cuda-${DRIVER_CUDA}/bin:\$PATH
  $0
EOF
		exit 1
	fi
fi

ARCH_FLAGS="${SHIBUDB_CUDA_ARCH:--arch=native}"

echo "Building $OUT ($NVCC_BIN $ARCH_FLAGS)..."
# shellcheck disable=SC2086
if ! "$NVCC_BIN" -shared -Xcompiler -fPIC -O3 $ARCH_FLAGS \
	-o "$OUT" \
	"$CUDA_DIR/distances.cu"; then
	echo "nvcc -arch=native failed; retrying with sm_75 (Turing / T4)..."
	"$NVCC_BIN" -shared -Xcompiler -fPIC -O3 -gencode arch=compute_75,code=sm_75 \
		-o "$OUT" \
		"$CUDA_DIR/distances.cu"
fi

echo "Built $OUT"
ls -la "$OUT"
ldd "$OUT" | grep -E 'cudart|not found' || true
