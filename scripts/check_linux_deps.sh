#!/bin/bash

# Check Linux dependencies for ShibuDb
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}🔍 Checking Linux dependencies for ShibuDb...${NC}"

# Check for required packages
check_package() {
    local package="$1"
    local description="$2"
    
    if dpkg -l | grep -q "^ii.*$package"; then
        echo -e "${GREEN}✅ $description ($package) is installed${NC}"
        return 0
    else
        echo -e "${RED}❌ $description ($package) is NOT installed${NC}"
        return 1
    fi
}

# Check for required libraries
check_library() {
    local library="$1"
    local description="$2"
    
    if ldconfig -p | grep -q "$library"; then
        echo -e "${GREEN}✅ $description ($library) is available${NC}"
        return 0
    else
        echo -e "${RED}❌ $description ($library) is NOT available${NC}"
        return 1
    fi
}

# Check for required tools
check_tool() {
    local tool="$1"
    local description="$2"
    
    if command -v "$tool" >/dev/null 2>&1; then
        echo -e "${GREEN}✅ $description ($tool) is available${NC}"
        return 0
    else
        echo -e "${RED}❌ $description ($tool) is NOT available${NC}"
        return 1
    fi
}

echo -e "${YELLOW}📦 Checking system packages...${NC}"

# Essential build tools
check_tool "gcc" "GCC compiler"
check_tool "g++" "G++ compiler"
check_tool "make" "Make build tool"
check_tool "go" "Go compiler"

echo -e "${YELLOW}📚 Checking libraries...${NC}"

# Check for C++ standard library
if ldconfig -p | grep -q "libstdc++"; then
    echo -e "${GREEN}✅ C++ Standard Library (libstdc++) is available${NC}"
else
    echo -e "${RED}❌ C++ Standard Library (libstdc++) is NOT available${NC}"
    echo -e "${YELLOW}💡 Install with: sudo apt-get install libstdc++6${NC}"
fi

# Check for math library
if ldconfig -p | grep -q "libm"; then
    echo -e "${GREEN}✅ Math Library (libm) is available${NC}"
else
    echo -e "${RED}❌ Math Library (libm) is NOT available${NC}"
fi

# Check for OpenMP
if ldconfig -p | grep -q "libgomp"; then
    echo -e "${GREEN}✅ OpenMP Library (libgomp) is available${NC}"
else
    echo -e "${RED}❌ OpenMP Library (libgomp) is NOT available${NC}"
    echo -e "${YELLOW}💡 Install with: sudo apt-get install libgomp1${NC}"
fi

# Check for OpenBLAS
if ldconfig -p | grep -q "libopenblas"; then
    echo -e "${GREEN}✅ OpenBLAS Library (libopenblas) is available${NC}"
else
    echo -e "${RED}❌ OpenBLAS Library (libopenblas) is NOT available${NC}"
    echo -e "${YELLOW}💡 Install with: sudo apt-get install libopenblas-dev${NC}"
fi

echo -e "${YELLOW}🔧 Checking FAISS libraries...${NC}"

# Check if FAISS libraries are in /usr/local/lib
if [ -f "/usr/local/lib/libfaiss.so" ]; then
    echo -e "${GREEN}✅ FAISS library (libfaiss.so) is installed${NC}"
else
    echo -e "${RED}❌ FAISS library (libfaiss.so) is NOT installed${NC}"
    echo -e "${YELLOW}💡 Run the test script to install FAISS libraries${NC}"
fi

if [ -f "/usr/local/lib/libfaiss_c.so" ]; then
    echo -e "${GREEN}✅ FAISS C library (libfaiss_c.so) is installed${NC}"
else
    echo -e "${RED}❌ FAISS C library (libfaiss_c.so) is NOT installed${NC}"
    echo -e "${YELLOW}💡 Run the test script to install FAISS libraries${NC}"
fi

echo -e "${YELLOW}📋 System information...${NC}"
echo -e "${GREEN}OS: $(uname -s)${NC}"
echo -e "${GREEN}Architecture: $(uname -m)${NC}"
echo -e "${GREEN}Kernel: $(uname -r)${NC}"

# Check Go version
if command -v go >/dev/null 2>&1; then
    echo -e "${GREEN}Go version: $(go version)${NC}"
else
    echo -e "${RED}Go is not installed${NC}"
fi

echo -e "${YELLOW}✅ Dependency check completed!${NC}"
