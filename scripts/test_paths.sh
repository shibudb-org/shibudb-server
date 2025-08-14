#!/bin/bash

# Test script to verify FAISS paths on Linux
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}🔍 Testing FAISS paths on Linux...${NC}"

# Get current directory
currentDir=$(pwd)
echo -e "${GREEN}Current directory: $currentDir${NC}"

# Detect OS and architecture
OS=$(uname -s)
ARCH=$(uname -m)
echo -e "${GREEN}OS: $OS, Architecture: $ARCH${NC}"

# Test include path
includePath="$currentDir/resources/lib/include"
echo -e "${YELLOW}Testing include path: $includePath${NC}"

if [ -d "$includePath" ]; then
    echo -e "${GREEN}✅ Include directory exists${NC}"
    
    # Test if faiss directory exists
    faissIncludePath="$includePath/faiss"
    if [ -d "$faissIncludePath" ]; then
        echo -e "${GREEN}✅ FAISS include directory exists${NC}"
        
        # Test if c_api directory exists
        cApiPath="$faissIncludePath/c_api"
        if [ -d "$cApiPath" ]; then
            echo -e "${GREEN}✅ FAISS C API directory exists${NC}"
            
            # Test if AutoTune_c.h exists
            if [ -f "$cApiPath/AutoTune_c.h" ]; then
                echo -e "${GREEN}✅ AutoTune_c.h exists${NC}"
            else
                echo -e "${RED}❌ AutoTune_c.h NOT found${NC}"
            fi
        else
            echo -e "${RED}❌ FAISS C API directory NOT found${NC}"
        fi
    else
        echo -e "${RED}❌ FAISS include directory NOT found${NC}"
    fi
else
    echo -e "${RED}❌ Include directory NOT found${NC}"
fi

# Test library path
if [[ "$OS" == "Linux" ]]; then
    if [[ "$ARCH" == "x86_64" ]]; then
        LIB_DIR="amd64"
    elif [[ "$ARCH" == "aarch64" ]]; then
        LIB_DIR="arm64"
    else
        echo -e "${RED}❌ Unsupported architecture: $ARCH${NC}"
        exit 1
    fi
    
    libPath="$currentDir/resources/lib/linux/$LIB_DIR"
    echo -e "${YELLOW}Testing library path: $libPath${NC}"
    
    if [ -d "$libPath" ]; then
        echo -e "${GREEN}✅ Library directory exists${NC}"
        
        # Test if library files exist
        if [ -f "$libPath/libfaiss.so" ]; then
            echo -e "${GREEN}✅ libfaiss.so exists${NC}"
        else
            echo -e "${RED}❌ libfaiss.so NOT found${NC}"
        fi
        
        if [ -f "$libPath/libfaiss_c.so" ]; then
            echo -e "${GREEN}✅ libfaiss_c.so exists${NC}"
        else
            echo -e "${RED}❌ libfaiss_c.so NOT found${NC}"
        fi
    else
        echo -e "${RED}❌ Library directory NOT found${NC}"
    fi
fi

# Test CGO environment variables
echo -e "${YELLOW}Testing CGO environment variables...${NC}"

export CGO_ENABLED=1
export CGO_CXXFLAGS="-I$currentDir/resources/lib/include"

if [[ "$OS" == "Darwin" ]]; then
    export CGO_LDFLAGS="-L$currentDir/resources/lib/mac/apple_silicon -lfaiss -lfaiss_c -lc++"
elif [[ "$OS" == "Linux" ]]; then
    export CGO_LDFLAGS="-L$currentDir/resources/lib/linux/$LIB_DIR -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas"
fi

echo -e "${GREEN}CGO_ENABLED: $CGO_ENABLED${NC}"
echo -e "${GREEN}CGO_CXXFLAGS: $CGO_CXXFLAGS${NC}"
echo -e "${GREEN}CGO_LDFLAGS: $CGO_LDFLAGS${NC}"

echo -e "${YELLOW}✅ Path testing completed!${NC}"
