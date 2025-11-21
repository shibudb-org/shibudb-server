#!/bin/bash

set -e

# === Config ===
APP_NAME="shibudb"
VERSION=$(./scripts/get_version.sh)
IDENTIFIER="com.shibudb.core"
BUILD_DIR="build"
TEMP_BUILD_DIR="$BUILD_DIR/temp"
PKG_ROOT="$TEMP_BUILD_DIR/pkg-root"
RESOURCES_DIR="resources"
COMPONENT_PKG="$TEMP_BUILD_DIR/${APP_NAME}-component.pkg"
OUTPUT_PKG="$TEMP_BUILD_DIR/${APP_NAME}-installer.pkg"
OUTPUT_DMG="$TEMP_BUILD_DIR/${APP_NAME}-installer.dmg"
FINAL_INSTALLER_DIR="$BUILD_DIR/mac/apple_silicon"
FINAL_PKG="$FINAL_INSTALLER_DIR/${APP_NAME}-${VERSION}-apple_silicon.pkg"
FINAL_DMG="$FINAL_INSTALLER_DIR/${APP_NAME}-${VERSION}-apple_silicon.dmg"
ICON_SOURCE="resources/shibudb.png"
ICON_ICNS="$TEMP_BUILD_DIR/shibudb.icns"
DISTRIBUTION_XML="$TEMP_BUILD_DIR/distribution.xml"
WELCOME_HTML="$TEMP_BUILD_DIR/resources/welcome.html"
GOARCH="arm64"  # Change to arm64 for Apple Silicon

# === Clean old builds ===
rm -rf "$TEMP_BUILD_DIR"
mkdir -p "$PKG_ROOT/usr/local/bin"
mkdir -p "$TEMP_BUILD_DIR/resources.iconset"
mkdir -p "$FINAL_INSTALLER_DIR"


# === Copy faiss binary ===
echo "📦 Copying FAISS shared libraries..."
mkdir -p "$PKG_ROOT/usr/local/lib"
cp resources/lib/mac/apple_silicon/libfaiss.dylib "$PKG_ROOT/usr/local/lib/"
cp resources/lib/mac/apple_silicon/libfaiss_c.dylib "$PKG_ROOT/usr/local/lib/"

# === Build Go binary ===
echo "🔨 Building $APP_NAME binary..."
CGO_ENABLED=1 \
CGO_CFLAGS="-I$(pwd)/resources/lib/include" \
CGO_CXXFLAGS="-I$(pwd)/resources/lib/include" \
CGO_LDFLAGS="-L$(pwd)/resources/lib/mac/apple_silicon -lfaiss -lfaiss_c -lc++" \
GOOS=darwin GOARCH=$GOARCH \
go build -tags faiss -ldflags "-X main.Version=$VERSION -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S')" -o "$PKG_ROOT/usr/local/bin/$APP_NAME" main.go

# === Patch binary with rpath to find FAISS libs ===
echo "🛠️ Adding RPATH to binary..."
install_name_tool -add_rpath "/usr/local/lib" "$PKG_ROOT/usr/local/bin/$APP_NAME"

# === Copy background image to proper resources folder ===
mkdir -p "$TEMP_BUILD_DIR/resources"


# === Create Welcome HTML ===
cat > "$WELCOME_HTML" <<EOF
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Welcome to ShibuDB</title>
  <style>
    :root {
      --bg-color: #f4f4f7;
      --text-color: #333;
      --card-bg: #fff;
      --highlight: #eef;
      --footer-color: #888;
    }

    @media (prefers-color-scheme: dark) {
      :root {
        --bg-color: #121212;
        --text-color: #e0e0e0;
        --card-bg: #1e1e1e;
        --highlight: #2a2a40;
        --footer-color: #aaa;
      }
    }

    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
      background-color: var(--bg-color);
      margin: 0;
      padding: 40px;
      color: var(--text-color);
    }

    .container {
      background: var(--card-bg);
      padding: 30px 40px;
      border-radius: 12px;
      max-width: 600px;
      margin: 50px auto;
      box-shadow: 0 8px 20px rgba(0, 0, 0, 0.2);
    }

    h2 {
      margin-top: 0;
      font-size: 28px;
      color: var(--text-color);
    }

    p {
      font-size: 16px;
      line-height: 1.6;
    }

    code {
      background: var(--highlight);
      padding: 2px 6px;
      border-radius: 4px;
      font-family: monospace;
    }

    .footer {
      text-align: center;
      font-size: 12px;
      color: var(--footer-color);
      margin-top: 20px;
    }
  </style>
</head>
<body>
  <div class="container">
    <h2>Welcome to ShibuDB Installer</h2>
    <p>You're just a few clicks away from installing <strong>ShibuDB</strong>, a lightweight embedded database engine.</p>
    <p>Once installed, you can run the CLI from your terminal using:</p>
    <p><code>shibudb</code></p>
    <p>The binary will be installed to: <code>/usr/local/bin</code></p>

    <div class="footer">© 2025 ShibuDB. All rights reserved.</div>
  </div>
</body>
</html>
EOF

# === Create Distribution XML ===
cat > "$DISTRIBUTION_XML" <<EOF
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="1">
  <title>ShibuDB Installer</title>
  <options customize="never" allow-external-scripts="no"/>
  <domains enable_anywhere="true"/>
  <background file="installer_bg.png" alignment="center"/>
  <welcome file="welcome.html"/>
  <choices-outline>
    <line choice="default">
      <pkg-ref id="com.shibudb.core"/>
    </line>
  </choices-outline>
  <choice id="default" visible="false" title="Default">
    <pkg-ref id="com.shibudb.core"/>
  </choice>
  <pkg-ref id="com.shibudb.core" auth="Root">shibudb-component.pkg</pkg-ref>
</installer-gui-script>
EOF

echo "📦 Creating component package..."
pkgbuild \
  --root "$PKG_ROOT" \
  --identifier "$IDENTIFIER" \
  --version "$VERSION" \
  --install-location "/" \
  "$COMPONENT_PKG"

# === Assemble final product package ===
echo "🎁 Building final installer package..."
productbuild \
  --distribution "$DISTRIBUTION_XML" \
  --resources "$TEMP_BUILD_DIR/resources" \
  --package-path "$TEMP_BUILD_DIR" \
  "$OUTPUT_PKG"

# === Create DMG ===
echo "💽 Creating DMG..."
hdiutil create \
  -volname "ShibuDB Installer" \
  -srcfolder "$OUTPUT_PKG" \
  -ov \
  -format UDZO \
  "$OUTPUT_DMG"

# === Organize final installers ===
echo "📁 Organizing installers..."
cp "$OUTPUT_PKG" "$FINAL_PKG"
cp "$OUTPUT_DMG" "$FINAL_DMG"

# === Clean up temporary files ===
echo "🧹 Cleaning up temporary files..."
rm -rf "$TEMP_BUILD_DIR"

echo "✅ Done!"
echo " - PKG: $FINAL_PKG"
echo " - DMG: $FINAL_DMG"
