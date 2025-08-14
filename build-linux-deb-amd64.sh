#!/bin/bash
set -e

APP_NAME="shibudb"
VERSION=$(./scripts/get_version.sh)
ARCH="amd64"
BUILD_DIR="build"
TEMP_BUILD_DIR="$BUILD_DIR/temp"
DEB_ROOT="$TEMP_BUILD_DIR/${APP_NAME}_${VERSION}_${ARCH}"
FINAL_INSTALLER_DIR="$BUILD_DIR/linux/deb/amd64"
FINAL_DEB="$FINAL_INSTALLER_DIR/${APP_NAME}_${VERSION}_${ARCH}.deb"

# Clean old build
rm -rf "$TEMP_BUILD_DIR"
mkdir -p "$DEB_ROOT/usr/local/bin"
mkdir -p "$DEB_ROOT/usr/local/var/log/$APP_NAME"
mkdir -p "$DEB_ROOT/usr/local/var/lib/$APP_NAME"
mkdir -p "$DEB_ROOT/usr/local/share/$APP_NAME"
mkdir -p "$DEB_ROOT/DEBIAN"
mkdir -p "$FINAL_INSTALLER_DIR"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed. Please install Go first:"
    echo "   wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz"
    echo "   sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz"
    echo "   export PATH=\"/usr/local/go/bin:\$PATH\""
    exit 1
fi

# Check and install cross-compilation toolchain for AMD64
echo "🔧 Checking cross-compilation toolchain..."
if ! command -v x86_64-linux-gnu-gcc &> /dev/null; then
    echo "📦 Installing cross-compilation toolchain for AMD64..."
    sudo apt-get update
    sudo apt-get install -y gcc-multilib g++-multilib
    # Install cross-compilation packages
    sudo apt-get install -y gcc-x86-64-linux-gnu g++-x86-64-linux-gnu
fi

# Install x86_64 runtime libraries needed for linking
echo "📦 Installing x86_64 runtime libraries..."
sudo apt-get install -y libopenblas-dev:amd64 libgomp1:amd64 libstdc++6:amd64

# Set cross-compilation environment variables
export CC=x86_64-linux-gnu-gcc
export CXX=x86_64-linux-gnu-g++
export CGO_ENABLED=1
export CGO_CFLAGS="-I$(pwd)/resources/lib/include" \
export CGO_CXXFLAGS="-I$(pwd)/resources/lib/include" \
export CGO_LDFLAGS="-L$(pwd)/resources/lib/linux/amd64 -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas" \

# Copy FAISS libraries to system locations for build
echo "📦 Installing FAISS libraries for build..."
sudo mkdir -p /usr/local/lib
sudo cp resources/lib/linux/amd64/libfaiss.so /usr/local/lib/
sudo cp resources/lib/linux/amd64/libfaiss_c.so /usr/local/lib/
sudo chmod 755 /usr/local/lib/libfaiss*.so
sudo ldconfig

# Copy FAISS headers if they exist in resources
if [ -d "resources/include" ]; then
    echo "📦 Installing FAISS headers..."
    sudo mkdir -p /usr/local/include
    sudo cp -r resources/include/* /usr/local/include/
fi

# Build Go binary for AMD64
echo "🔨 Building $APP_NAME binary for AMD64..."
GOOS=linux GOARCH=amd64 \
go build -tags faiss -ldflags "-X main.Version=$VERSION -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S')" -o "$DEB_ROOT/usr/local/bin/$APP_NAME" main.go

# Copy FAISS libraries to package
echo "📦 Copying FAISS libraries to package..."
mkdir -p "$DEB_ROOT/usr/local/lib"
cp resources/lib/linux/amd64/libfaiss.so "$DEB_ROOT/usr/local/lib/"
cp resources/lib/linux/amd64/libfaiss_c.so "$DEB_ROOT/usr/local/lib/"

# Copy assets
cp -r resources/* "$DEB_ROOT/usr/local/share/$APP_NAME/" 2>/dev/null || true
# Remove ARM64 libraries to prevent strip errors
rm -rf "$DEB_ROOT/usr/local/share/$APP_NAME/lib/linux/arm64" 2>/dev/null || true
cp LICENSE "$DEB_ROOT/usr/local/share/$APP_NAME/"
cp README.md "$DEB_ROOT/usr/local/share/$APP_NAME/" 2>/dev/null || true

# Create DEBIAN/control file
cat > "$DEB_ROOT/DEBIAN/control" <<EOF
Package: $APP_NAME
Version: $VERSION
Architecture: $ARCH
Maintainer: Your Name <you@example.com>
Depends: libc6, libstdc++6, libgomp1, libopenblas0
Description: Lightweight embedded database with FAISS vector search.
 ShibuDB is a high-performance embedded database engine
 optimized for vector search using FAISS.
EOF

# Create postinst script to run ldconfig
cat > "$DEB_ROOT/DEBIAN/postinst" <<EOF
#!/bin/bash
/sbin/ldconfig
EOF
chmod 755 "$DEB_ROOT/DEBIAN/postinst"

# Build .deb
dpkg-deb --build "$DEB_ROOT" "$TEMP_BUILD_DIR/${APP_NAME}_${VERSION}_${ARCH}.deb"

# === Organize final installer ===
echo "📁 Organizing installer..."
cp "$TEMP_BUILD_DIR/${APP_NAME}_${VERSION}_${ARCH}.deb" "$FINAL_DEB"

# === Clean up temporary files ===
echo "🧹 Cleaning up temporary files..."
rm -rf "$TEMP_BUILD_DIR"

echo "✅ .deb package built at: $FINAL_DEB" 