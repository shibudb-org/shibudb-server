#!/bin/bash

set -e

# === Config ===
APP_NAME="shibudb"
VERSION=$(./scripts/get_version.sh)  # Or hardcode like: VERSION="1.0.0"
DIST_DIR="build/distribution"
OUTPUT_NAME="$APP_NAME-$VERSION-darwin-arm64"
DIST_CONTENT_DIR="$DIST_DIR/$OUTPUT_NAME"

FAISS_LIB_DIR="resources/lib/mac/apple_silicon"

# === Clean previous build ===
echo "🧹 Cleaning old build..."
rm -rf "$DIST_DIR"
mkdir -p "$DIST_CONTENT_DIR"

# === Build Go binary ===
echo "🔨 Building $APP_NAME..."
CGO_ENABLED=1 \
CGO_CXXFLAGS="-I$(pwd)/resources/lib/include" \
CGO_LDFLAGS="-L$(pwd)/resources/lib/mac/apple_silicon -lfaiss -lfaiss_c -lc++" \
GOOS=darwin GOARCH=arm64 \
go build -tags faiss -ldflags "-X main.Version=$VERSION -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S')" -o "$DIST_CONTENT_DIR/$APP_NAME" main.go

# === Patch RPATH to find FAISS libraries in Homebrew lib directory ===
echo "🔧 Patching RPATH for Homebrew layout..."
install_name_tool -add_rpath "@loader_path/../lib" "$DIST_CONTENT_DIR/$APP_NAME"

# === Copy FAISS dependencies ===
echo "📦 Copying FAISS libraries..."
cp "$FAISS_LIB_DIR/libfaiss.dylib" "$DIST_CONTENT_DIR/"
cp "$FAISS_LIB_DIR/libfaiss_c.dylib" "$DIST_CONTENT_DIR/"

# === Optional: Copy LICENSE and README if present ===
[ -f LICENSE ] && cp LICENSE "$DIST_CONTENT_DIR/"
[ -f README.md ] && cp README.md "$DIST_CONTENT_DIR/"

# === Create tar.gz ===
echo "📦 Creating tar.gz archive..."
cd "$DIST_DIR"
tar -czvf "../$OUTPUT_NAME.tar.gz" "$OUTPUT_NAME"
cd - >/dev/null

echo "✅ Done!"
echo " - Output: build/$OUTPUT_NAME.tar.gz"
