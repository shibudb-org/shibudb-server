#!/bin/bash

# Test script to verify version integration in build scripts

echo "🧪 Testing version integration in build scripts..."
echo ""

# Get current version
CURRENT_VERSION=$(./scripts/get_version.sh)
echo "Current version from changelog: $CURRENT_VERSION"
echo ""

# Test each build script
BUILD_SCRIPTS=(
    "build-mac-pkg-arm64.sh"
    "build-linux-deb-amd64.sh"
    "build-linux-deb-arm64.sh"
    "build-linux-rpm-amd64.sh"
    "build-linux-rpm-arm64.sh"
)

for script in "${BUILD_SCRIPTS[@]}"; do
    if [ -f "$script" ]; then
        echo "Testing $script..."
        # Extract VERSION line from script
        SCRIPT_VERSION=$(grep "^VERSION=" "$script" | head -1 | sed 's/VERSION=//' | sed 's/"//g')
        
        if [ "$SCRIPT_VERSION" = "\$(./scripts/get_version.sh)" ]; then
            echo "✅ $script: Using dynamic version"
        else
            echo "❌ $script: Still using hardcoded version: $SCRIPT_VERSION"
        fi
    else
        echo "⚠️  $script: File not found"
    fi
done

echo ""
echo "🎯 Summary:"
echo "- All build scripts should now use: VERSION=\$(./scripts/get_version.sh)"
echo "- Current version: $CURRENT_VERSION"
echo "- To update version: ./scripts/update_version.sh <new_version>" 