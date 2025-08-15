#!/bin/bash

# Script to stop ShibuDB test server

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Stopping ShibuDB test server...${NC}"

# Get the current directory (project root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

# Stop server if PID file exists
if [ -f server.pid ]; then
    SERVER_PID=$(cat server.pid)
    echo -e "${YELLOW}Stopping server with PID: $SERVER_PID${NC}"
    
    # Try to kill the process gracefully
    kill $SERVER_PID 2>/dev/null || true
    
    # Wait a moment for graceful shutdown
    sleep 2
    
    # Force kill if still running
    if kill -0 $SERVER_PID 2>/dev/null; then
        echo -e "${YELLOW}Force killing server...${NC}"
        kill -9 $SERVER_PID 2>/dev/null || true
    fi
    
    # Remove PID file
    rm server.pid
    
    echo -e "${GREEN}Server stopped${NC}"
else
    echo -e "${YELLOW}No server PID file found${NC}"
fi

# Clean up log file if it exists
if [ -f server.log ]; then
    rm server.log
fi

# Clean up temporary directories (look for directories created by mktemp)
for temp_dir in /tmp/test-server-* /tmp/shibudb-server-*; do
    if [ -d "$temp_dir" ]; then
        echo -e "${YELLOW}Cleaning up temporary directory: $temp_dir${NC}"
        rm -rf "$temp_dir"
    fi
done

# Clean up any generated server binaries
rm -f shibudb-server
rm -rf cmd/test_server

echo -e "${GREEN}Cleanup complete${NC}"
