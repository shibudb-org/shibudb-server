#!/bin/bash

# Linux-specific test script for ShibuDb
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}🔧 Setting up test environment for Linux...${NC}"

# Detect architecture
ARCH=$(uname -m)
if [[ "$ARCH" == "x86_64" ]]; then
    LIB_DIR="amd64"
elif [[ "$ARCH" == "aarch64" ]]; then
    LIB_DIR="arm64"
else
    echo -e "${RED}❌ Unsupported architecture: $ARCH${NC}"
    exit 1
fi

echo -e "${YELLOW}📋 Detected Architecture: $ARCH (using $LIB_DIR libraries)${NC}"

# Setup FAISS libraries
if [ ! -f "/usr/local/lib/libfaiss.so" ] || [ ! -f "/usr/local/lib/libfaiss_c.so" ]; then
    echo -e "${YELLOW}📦 Copying FAISS libraries to /usr/local/lib...${NC}"
    sudo cp resources/lib/linux/$LIB_DIR/libfaiss.so /usr/local/lib/
    sudo cp resources/lib/linux/$LIB_DIR/libfaiss_c.so /usr/local/lib/
    sudo chmod 755 /usr/local/lib/libfaiss*.so
    sudo ldconfig
fi

# Function to run tests for a single package
run_tests() {
    local test_package="$1"
    shift  # Remove the first argument (package name)
    local test_args=("$@")
    
    echo -e "${GREEN}🚀 Running tests for $test_package...${NC}"
    
    # Run tests with LD_LIBRARY_PATH set
    LD_LIBRARY_PATH="/usr/local/lib:$LD_LIBRARY_PATH" \
    CGO_ENABLED=1 \
    CGO_CXXFLAGS="-I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas" \
    go test -v "$test_package" "${test_args[@]}"
}

# Function to run tests for multiple packages
run_all_tests() {
    local exclude_benchmark=false
    local exclude_e2e=false
    local additional_args=()
    
    # Check for exclusion flags
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --exclude-benchmark)
                exclude_benchmark=true
                shift
                ;;
            --exclude-e2e)
                exclude_e2e=true
                shift
                ;;
            *)
                additional_args+=("$1")
                shift
                ;;
        esac
    done
    
    echo -e "${GREEN}🧪 Running all tests...${NC}"
    
    # Get all packages that have tests
    local packages=$(go list ./... | grep -v "/vendor/")
    
    for package in $packages; do
        # Skip benchmark package if exclude flag is set
        if [[ "$exclude_benchmark" == "true" && "$package" == *"/benchmark" ]]; then
            echo -e "${YELLOW}⏭️  Skipping $package (benchmark excluded)${NC}"
            continue
        fi
        
        # Skip E2E package if exclude flag is set
        if [[ "$exclude_e2e" == "true" && "$package" == *"/E2ETests" ]]; then
            echo -e "${YELLOW}⏭️  Skipping $package (E2E excluded)${NC}"
            continue
        fi
        
        # Check if the package has tests
        if go test -list . "$package" 2>/dev/null | grep -q "Test"; then
            echo -e "${YELLOW}📦 Testing package: $package${NC}"
            run_tests "$package" "${additional_args[@]}"
        else
            echo -e "${YELLOW}⏭️  Skipping $package (no tests found)${NC}"
        fi
    done
}

# Main execution
if [ $# -eq 0 ]; then
    # Run all tests
    run_all_tests
elif [[ "$1" == "--exclude-benchmark" ]] || [[ "$1" == "--exclude-e2e" ]]; then
    # Run all tests with exclusion flags
    run_all_tests "$@"
elif [[ "$1" == "./benchmark/" ]]; then
    # Run benchmark tests with additional arguments
    shift  # Remove the benchmark package
    run_tests "./benchmark/" "$@"
elif [[ "$1" == "./E2ETests/" ]]; then
    # Run E2E tests with additional arguments
    shift  # Remove the E2E package
    run_tests "./E2ETests/" "$@"
else
    # Run specific test package
    local package="$1"
    shift  # Remove the package name
    echo -e "${GREEN}🧪 Running tests for: $package${NC}"
    run_tests "$package" "$@"
fi

echo -e "${GREEN}✅ Tests completed!${NC}"
