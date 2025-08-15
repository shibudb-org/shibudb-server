#!/bin/bash

# Script to start ShibuDB server for E2E tests
# This script starts the server on port 4444 with admin/admin credentials

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting ShibuDB test server...${NC}"

# Get the current directory (project root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Detect OS and architecture
OS=$(uname -s)
ARCH=$(uname -m)

echo -e "${YELLOW}📋 Detected OS: $OS, Architecture: $ARCH${NC}"

# Setup FAISS libraries based on OS
setup_faiss_libraries() {
    if [[ "$OS" == "Darwin" ]]; then
        # macOS setup
        if [ ! -f "/usr/local/lib/libfaiss.dylib" ] || [ ! -f "/usr/local/lib/libfaiss_c.dylib" ]; then
            echo -e "${YELLOW}📦 Copying FAISS libraries to /usr/local/lib...${NC}"
            sudo cp "$PROJECT_ROOT/resources/lib/mac/apple_silicon/libfaiss.dylib" /usr/local/lib/
            sudo cp "$PROJECT_ROOT/resources/lib/mac/apple_silicon/libfaiss_c.dylib" /usr/local/lib/
            sudo chmod 755 /usr/local/lib/libfaiss*.dylib
        fi
    elif [[ "$OS" == "Linux" ]]; then
        # Linux setup
        if [[ "$ARCH" == "x86_64" ]]; then
            LIB_DIR="amd64"
        elif [[ "$ARCH" == "aarch64" ]]; then
            LIB_DIR="arm64"
        else
            echo -e "${RED}❌ Unsupported architecture: $ARCH${NC}"
            exit 1
        fi
        
        if [ ! -f "/usr/local/lib/libfaiss.so" ] || [ ! -f "/usr/local/lib/libfaiss_c.so" ]; then
            echo -e "${YELLOW}📦 Copying FAISS libraries to /usr/local/lib...${NC}"
            sudo cp "$PROJECT_ROOT/resources/lib/linux/$LIB_DIR/libfaiss.so" /usr/local/lib/
            sudo cp "$PROJECT_ROOT/resources/lib/linux/$LIB_DIR/libfaiss_c.so" /usr/local/lib/
            sudo chmod 755 /usr/local/lib/libfaiss*.so
            sudo ldconfig
        fi
    else
        echo -e "${RED}❌ Unsupported operating system: $OS${NC}"
        exit 1
    fi
}

# Setup FAISS libraries
setup_faiss_libraries

# Create testdata directory and config file
TESTDATA_DIR="$PROJECT_ROOT/cmd/server/testdata"
CONFIG_FILE="$TESTDATA_DIR/config.json"
DATA_DIR="$PROJECT_ROOT/test-data"

mkdir -p "$TESTDATA_DIR"
mkdir -p "$DATA_DIR"

# Create config file with admin user data
cat > "$CONFIG_FILE" << 'EOF'
{
  "admin": {
    "username": "admin",
    "password": "$2a$10$Ag11HDzTDQmQp7QOP6cPk.EZtogMEI868tSz90Y.WHqgyTmYHDDbu",
    "role": "admin",
    "permissions": {}
  }
}
EOF

echo -e "${GREEN}Created config file with admin credentials${NC}"

# Build the test server binary
echo -e "${GREEN}Building test server binary...${NC}"
cd "$PROJECT_ROOT"

# Create a temporary directory for the server binary
TEMP_DIR=$(mktemp -d)
SERVER_BINARY="$TEMP_DIR/test-server"

# Build the test server binary with proper environment variables
if [[ "$OS" == "Darwin" ]]; then
    # macOS: use -lc++
    CGO_ENABLED=1 \
    CGO_CXXFLAGS="-I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib -lfaiss -lfaiss_c -lc++" \
    go build -o "$SERVER_BINARY" cmd/test_server/main.go
    
    # Add RPATH to the server binary
    echo -e "${YELLOW}🔧 Adding RPATH to server binary (macOS)...${NC}"
    install_name_tool -add_rpath "/usr/local/lib" "$SERVER_BINARY"
elif [[ "$OS" == "Linux" ]]; then
    # Linux: use -lstdc++
    CGO_ENABLED=1 \
    CGO_CXXFLAGS="-I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas" \
    go build -o "$SERVER_BINARY" cmd/test_server/main.go
    
    # Add RPATH to the server binary if patchelf is available
    if command -v patchelf >/dev/null 2>&1; then
        echo -e "${YELLOW}🔧 Adding RPATH to server binary (Linux with patchelf)...${NC}"
        patchelf --set-rpath "/usr/local/lib" "$SERVER_BINARY"
    else
        echo -e "${YELLOW}⚠️  patchelf not found, using LD_LIBRARY_PATH...${NC}"
        export LD_LIBRARY_PATH="/usr/local/lib:$LD_LIBRARY_PATH"
    fi
fi

echo -e "${GREEN}Test server binary built successfully${NC}"

# Start server in background
echo -e "${GREEN}Starting server on port 4444...${NC}"
if [[ "$OS" == "Linux" && ! -f "$(command -v patchelf)" ]]; then
    # Use LD_LIBRARY_PATH for Linux when patchelf is not available
    LD_LIBRARY_PATH="/usr/local/lib:$LD_LIBRARY_PATH" "$SERVER_BINARY" > server.log 2>&1 &
else
    "$SERVER_BINARY" > server.log 2>&1 &
fi

SERVER_PID=$!

# Store PID for cleanup
echo $SERVER_PID > server.pid

# Wait for server to start (check if port 4444 is listening)
echo -e "${YELLOW}Waiting for server to start on port 4444...${NC}"
timeout=30
counter=0

while ! nc -z localhost 4444 && [ $counter -lt $timeout ]; do
    sleep 1
    counter=$((counter + 1))
    echo -n "."
done

echo ""

if [ $counter -eq $timeout ]; then
    echo -e "${RED}Server failed to start within $timeout seconds${NC}"
    echo -e "${RED}Server logs:${NC}"
    cat server.log
    # Clean up
    if [ -f server.pid ]; then
        kill $(cat server.pid) 2>/dev/null || true
        rm server.pid
    fi
    rm -rf "$TEMP_DIR"
    exit 1
fi

echo -e "${GREEN}Server is running on port 4444 (PID: $SERVER_PID)${NC}"
echo -e "${GREEN}Ready for E2E tests!${NC}"

# Exit successfully, leaving the server running
exit 0
