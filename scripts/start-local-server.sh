#!/usr/bin/env bash
# Run the real ShibuDB server from source (foreground) with FAISS/CGO configured.
# Same linking pattern as scripts/connect-client.sh. Ctrl+C to stop.
#
# Optional: SHIBUDB_LOCAL_PORT (default 4444). Extra args are passed to `run`, e.g.:
#   ./scripts/start-local-server.sh --data-dir /tmp/mydata

set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ ! -f "main.go" ]] || [[ ! -f "Makefile" ]]; then
	echo "Error: run from repo root or via make start-local-server" >&2
	exit 1
fi

export CGO_ENABLED=1
export CGO_CFLAGS="-I$ROOT/resources/lib/include"
export CGO_CXXFLAGS="-I$ROOT/resources/lib/include"

OS=$(uname -s)
ARCH=$(uname -m)

if [[ "$OS" == "Darwin" ]]; then
	export CGO_LDFLAGS="-L$ROOT/resources/lib/mac/apple_silicon -lfaiss -lfaiss_c -lc++"
	export DYLD_LIBRARY_PATH="$ROOT/resources/lib/mac/apple_silicon:${DYLD_LIBRARY_PATH:-}"
elif [[ "$OS" == "Linux" ]]; then
	if [[ "$ARCH" == "x86_64" ]]; then
		LIB_DIR="amd64"
	elif [[ "$ARCH" == "aarch64" ]]; then
		LIB_DIR="arm64"
	else
		echo "Error: Unsupported architecture: $ARCH" >&2
		exit 1
	fi
	export CGO_LDFLAGS="-L$ROOT/resources/lib/linux/$LIB_DIR -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas"
	export LD_LIBRARY_PATH="$ROOT/resources/lib/linux/$LIB_DIR:${LD_LIBRARY_PATH:-}"
else
	echo "Error: Unsupported OS: $OS" >&2
	exit 1
fi

PORT="${SHIBUDB_LOCAL_PORT:-4444}"

echo "Starting ShibuDB (go run) on client port ${PORT} (management default 5444)."
echo "Bootstrap admin: admin / admin (override by editing this script or passing flags after --)."
echo "Connect in another terminal: make connect-local-client"
echo ""

exec go run . run --port "$PORT" --admin-user admin --admin-password admin "$@"
