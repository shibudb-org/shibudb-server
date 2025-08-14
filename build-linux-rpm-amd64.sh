#!/bin/bash
set -e

APP_NAME="shibudb"
VERSION=$(./scripts/get_version.sh)
BUILDROOT="$PWD/build/rpm"
RPMBUILD="$PWD/build/rpmbuild"
PKG_ROOT="$BUILDROOT"
FINAL_INSTALLER_DIR="$PWD/build/linux/rpm/amd64"
FINAL_RPM="$FINAL_INSTALLER_DIR/${APP_NAME}-${VERSION}-1.x86_64.rpm"

# Clean up old builds
rm -rf "$BUILDROOT" "$RPMBUILD"
mkdir -p "$BUILDROOT" "$RPMBUILD"/{BUILD,RPMS,SOURCES,SPECS,SRPMS,BUILDROOT}
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

# Build binary for AMD64
echo "🔨 Building $APP_NAME binary for AMD64..."
GOOS=linux GOARCH=amd64 \
go build -tags faiss -ldflags "-X main.Version=$VERSION -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S')" -o "$PKG_ROOT/usr/local/bin/$APP_NAME" main.go

# Add logs and data folders
mkdir -p "$PKG_ROOT/usr/local/var/log/$APP_NAME"
mkdir -p "$PKG_ROOT/usr/local/var/lib/$APP_NAME"

# Copy FAISS libraries to package
echo "📦 Copying FAISS libraries to package..."
mkdir -p "$PKG_ROOT/usr/local/lib"
cp resources/lib/linux/amd64/libfaiss.so "$PKG_ROOT/usr/local/lib/"
cp resources/lib/linux/amd64/libfaiss_c.so "$PKG_ROOT/usr/local/lib/"

# Copy resources and docs
mkdir -p "$PKG_ROOT/usr/local/share/$APP_NAME"
# Copy resources but exclude ARM64 libraries to prevent strip errors
cp -r resources/* "$PKG_ROOT/usr/local/share/$APP_NAME/" 2>/dev/null || true
# Remove ARM64 libraries from the package to prevent strip errors
rm -rf "$PKG_ROOT/usr/local/share/$APP_NAME/lib/linux/arm64" 2>/dev/null || true
cp LICENSE "$PKG_ROOT/"
cp README.md "$PKG_ROOT/" 2>/dev/null || true

# Create tar.gz source in correct structure
cd "$BUILDROOT"
mkdir -p "$RPMBUILD/SOURCES"
TMP_TAR_DIR="$(mktemp -d)"
mkdir -p "$TMP_TAR_DIR/$APP_NAME-$VERSION"
cp -r . "$TMP_TAR_DIR/$APP_NAME-$VERSION"
cd "$TMP_TAR_DIR"
tar -czvf "$RPMBUILD/SOURCES/$APP_NAME-$VERSION.tar.gz" "$APP_NAME-$VERSION"
cd -
rm -rf "$TMP_TAR_DIR"

# Write .spec file
cat > "$RPMBUILD/SPECS/$APP_NAME.spec" <<EOF
Name:           $APP_NAME
Version:        $VERSION
Release:        1%{?dist}
Summary:        Lightweight Embedded Database

License:        MIT
URL:            https://github.com/yourusername/ShibuDB
Source0:        %{name}-%{version}.tar.gz

BuildArch:      x86_64

%description
ShibuDB is a lightweight embedded database optimized for high-performance storage and FAISS-based vector search.

%prep
%setup -q

%install
cp -a usr %{buildroot}/

%files
/usr/local/bin/$APP_NAME
/usr/local/lib/libfaiss.so
/usr/local/lib/libfaiss_c.so
/usr/local/var/log/$APP_NAME
/usr/local/var/lib/$APP_NAME
/usr/local/share/$APP_NAME

%license LICENSE
%doc README.md

%post
/sbin/ldconfig

%changelog
* Tue Jul 29 2025 Your Name <you@example.com> - $VERSION-1
- Initial RPM release for AMD64
EOF

# Build the RPM
rpmbuild --define "_topdir $RPMBUILD" -ba "$RPMBUILD/SPECS/$APP_NAME.spec"

# Move final RPM to organized location
cp "$RPMBUILD/RPMS/x86_64/${APP_NAME}-${VERSION}-1.x86_64.rpm" "$FINAL_RPM"

# Clean up temporary files
echo "🧹 Cleaning up temporary files..."
rm -rf "$BUILDROOT" "$RPMBUILD"

echo "✅ RPM built! Find it in: $FINAL_RPM" 