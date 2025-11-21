#!/bin/bash

# Script to clean build directory and show organized structure

echo "🧹 Cleaning build directory..."
rm -rf build/

echo "📁 Creating organized build directory structure..."
mkdir -p build/mac/apple_silicon
mkdir -p build/linux/deb/amd64
mkdir -p build/linux/deb/arm64
mkdir -p build/linux/rpm/amd64
mkdir -p build/linux/rpm/arm64

echo "✅ Build directory cleaned and organized!"
echo ""
echo "📂 New build directory structure:"
echo "build/"
echo "├── mac/"
echo "│   └── apple_silicon/"
echo "│       ├── shibudb-{version}-apple_silicon.pkg"
echo "│       └── shibudb-{version}-apple_silicon.dmg"
echo "├── linux/"
echo "│   ├── deb/"
echo "│   │   ├── amd64/"
echo "│   │   │   └── shibudb_{version}_amd64.deb"
echo "│   │   └── arm64/"
echo "│   │       └── shibudb_{version}_arm64.deb"
echo "│   └── rpm/"
echo "│       ├── amd64/"
echo "│       │   └── shibudb-{version}-1.x86_64.rpm"
echo "│       └── arm64/"
echo "│           └── shibudb-{version}-1.aarch64.rpm"
echo ""
echo "🎯 Now you can run any build script and the installers will be organized in their respective folders!" 