#!/bin/bash

# ShibuDb Connect Client Script
# This script builds a proper client binary with RPATH set for FAISS libraries

set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}🔧 Building ShibuDb client with proper library linking...${NC}"

# Get the current directory (should be project root)
currentDir=$(pwd)

# Check if we're in the project root
if [ ! -f "main.go" ] || [ ! -f "Makefile" ]; then
    echo -e "${RED}Error: Please run this script from the ShibuDb project root directory${NC}"
    exit 1
fi

# Get the current directory (should be project root)
currentDir=$(pwd)

# Set CGO environment variables based on platform
export CGO_ENABLED=1
export CGO_CXXFLAGS="-I$currentDir/resources/lib/include"

# Detect OS and architecture
OS=$(uname -s)
ARCH=$(uname -m)

if [[ "$OS" == "Darwin" ]]; then
    # macOS
    export CGO_LDFLAGS="-L$currentDir/resources/lib/mac/apple_silicon -lfaiss -lfaiss_c -lc++"
elif [[ "$OS" == "Linux" ]]; then
    # Linux
    if [[ "$ARCH" == "x86_64" ]]; then
        LIB_DIR="amd64"
    elif [[ "$ARCH" == "aarch64" ]]; then
        LIB_DIR="arm64"
    else
        echo -e "${RED}Error: Unsupported architecture: $ARCH${NC}"
        exit 1
    fi
    export CGO_LDFLAGS="-L$currentDir/resources/lib/linux/$LIB_DIR -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas"
else
    echo -e "${RED}Error: Unsupported operating system: $OS${NC}"
    exit 1
fi

# Create a temporary directory for the client binary
tempDir=$(mktemp -d)
clientBinary="$tempDir/shibudb_client"

echo -e "${BLUE}🔧 Building client binary...${NC}"

# Build the client binary
buildCmd="go build -o $clientBinary main.go"
echo "Running: $buildCmd"
if ! eval $buildCmd; then
    echo -e "${RED}Error building client binary${NC}"
    rm -rf "$tempDir"
    exit 1
fi

# Add RPATH to the client binary (platform-specific)
if [[ "$OS" == "Darwin" ]]; then
    echo -e "${BLUE}🔧 Adding RPATH to client binary (macOS)...${NC}"
    if ! install_name_tool -add_rpath "/usr/local/lib" "$clientBinary"; then
        echo -e "${RED}Error adding RPATH${NC}"
        rm -rf "$tempDir"
        exit 1
    fi
elif [[ "$OS" == "Linux" ]]; then
    echo -e "${BLUE}🔧 Setting up library path for Linux...${NC}"
    # On Linux, we'll use LD_LIBRARY_PATH instead of RPATH
    export LD_LIBRARY_PATH="/usr/local/lib:$LD_LIBRARY_PATH"
fi

echo -e "${GREEN}✅ Client binary built successfully!${NC}"
echo -e "${BLUE}Connecting to local development server...${NC}"
echo -e "${YELLOW}Default credentials: admin/admin${NC}"
echo -e "${YELLOW}Default port: 4444${NC}"
echo ""

# Check if port is provided as argument
port=${1:-4444}

# Run the client binary
"$clientBinary" connect "$port"

# Clean up
rm -rf "$tempDir" 