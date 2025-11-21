#!/bin/bash
set -e

APP_NAME="shibudb"
VERSION=$(./scripts/get_version.sh)
BUILDROOT="$PWD/build/rpm"
RPMBUILD="$PWD/build/rpmbuild"
PKG_ROOT="$BUILDROOT"
FINAL_INSTALLER_DIR="$PWD/build/linux/rpm/arm64"

# Clean up old builds
rm -rf "$BUILDROOT" "$RPMBUILD"
mkdir -p "$BUILDROOT" "$RPMBUILD"/{BUILD,RPMS,SOURCES,SPECS,SRPMS,BUILDROOT}
mkdir -p "$FINAL_INSTALLER_DIR"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed. Please install Go first:"
    echo "   wget https://go.dev/dl/go1.21.0.linux-arm64.tar.gz"
    echo "   sudo tar -C /usr/local -xzf go1.21.0.linux-arm64.tar.gz"
    echo "   export PATH=\"/usr/local/go/bin:\$PATH\""
    exit 1
fi

# Helper functions
ensure_openblas_installed() {
    if ldconfig -p | grep -q openblas; then
        return
    fi

    echo "📦 Installing OpenBLAS dependency..."
    if command -v apt-get &> /dev/null; then
        sudo apt-get update
        sudo apt-get install -y libopenblas-dev
    elif command -v yum &> /dev/null; then
        sudo yum install -y openblas-devel
    elif command -v dnf &> /dev/null; then
        sudo dnf install -y openblas-devel
    else
        echo "❌ No supported package manager found to install OpenBLAS."
        exit 1
    fi
}

# Copy FAISS libraries to system locations for build
echo "📦 Installing FAISS libraries for build..."
sudo mkdir -p /usr/local/lib
sudo cp resources/lib/linux/arm64/libfaiss.so /usr/local/lib/
sudo cp resources/lib/linux/arm64/libfaiss_c.so /usr/local/lib/
sudo chmod 755 /usr/local/lib/libfaiss*.so
sudo ldconfig

# Ensure OpenBLAS dependency is installed for linking
ensure_openblas_installed

# Build binary
echo "🔨 Building $APP_NAME binary..."
FAISS_INCLUDE_DIR="$(pwd)/resources/lib/include"
FAISS_LIB_DIR="$(pwd)/resources/lib/linux/arm64"
mkdir -p "$PKG_ROOT/usr/local/bin"
CGO_ENABLED=1 \
CGO_CFLAGS="-I${FAISS_INCLUDE_DIR}" \
CGO_CXXFLAGS="-I${FAISS_INCLUDE_DIR}" \
CGO_LDFLAGS="-L${FAISS_LIB_DIR} -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas" \
GOOS=linux GOARCH=arm64 \
go build -tags faiss -ldflags "-X main.Version=$VERSION -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S')" -o "$PKG_ROOT/usr/local/bin/$APP_NAME" main.go

# Add logs and data folders
mkdir -p "$PKG_ROOT/usr/local/var/log/$APP_NAME"
mkdir -p "$PKG_ROOT/usr/local/var/lib/$APP_NAME"

# Copy FAISS libraries to package
echo "📦 Copying FAISS libraries to package..."
mkdir -p "$PKG_ROOT/usr/local/lib"
cp resources/lib/linux/arm64/libfaiss.so "$PKG_ROOT/usr/local/lib/"
cp resources/lib/linux/arm64/libfaiss_c.so "$PKG_ROOT/usr/local/lib/"

# Copy resources and docs
mkdir -p "$PKG_ROOT/usr/local/share/$APP_NAME"
# Copy resources but exclude AMD64 libraries to prevent strip errors
cp -r resources/* "$PKG_ROOT/usr/local/share/$APP_NAME/" 2>/dev/null || true
# Remove AMD64 libraries from the package to prevent strip errors
rm -rf "$PKG_ROOT/usr/local/share/$APP_NAME/lib/linux/amd64" 2>/dev/null || true
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
Release:        1
Summary:        Lightweight Embedded Database

License:        MIT
URL:            https://github.com/yourusername/ShibuDB
Source0:        %{name}-%{version}.tar.gz

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
- Initial RPM release
EOF

# Build the RPM
rpmbuild --define "_topdir $RPMBUILD" -ba "$RPMBUILD/SPECS/$APP_NAME.spec"

# Find the actual generated RPM file and move it to organized location
GENERATED_RPM=$(find "$RPMBUILD/RPMS/aarch64" -name "${APP_NAME}-${VERSION}-*.aarch64.rpm" | head -1)
if [ -z "$GENERATED_RPM" ]; then
    echo "❌ Error: Could not find generated RPM file"
    exit 1
fi

FINAL_RPM="$FINAL_INSTALLER_DIR/$(basename "$GENERATED_RPM")"
cp "$GENERATED_RPM" "$FINAL_RPM"

# Clean up temporary files
echo "🧹 Cleaning up temporary files..."
rm -rf "$BUILDROOT" "$RPMBUILD"

echo "✅ RPM built! Find it in: $FINAL_RPM"

