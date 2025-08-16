#!/bin/bash

# Test script with proper RPATH handling for FAISS libraries
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Detect OS
OS=$(uname -s)
ARCH=$(uname -m)

echo -e "${YELLOW}🔧 Setting up test environment with FAISS RPATH...${NC}"
echo -e "${YELLOW}📋 Detected OS: $OS, Architecture: $ARCH${NC}"

# Function to setup FAISS libraries based on OS
setup_faiss_libraries() {
    if [[ "$OS" == "Darwin" ]]; then
        # macOS setup
        if [ ! -f "/usr/local/lib/libfaiss.dylib" ] || [ ! -f "/usr/local/lib/libfaiss_c.dylib" ]; then
            echo -e "${YELLOW}📦 Copying FAISS libraries to /usr/local/lib...${NC}"
            sudo cp resources/lib/mac/apple_silicon/libfaiss.dylib /usr/local/lib/
            sudo cp resources/lib/mac/apple_silicon/libfaiss_c.dylib /usr/local/lib/
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
            sudo cp resources/lib/linux/$LIB_DIR/libfaiss.so /usr/local/lib/
            sudo cp resources/lib/linux/$LIB_DIR/libfaiss_c.so /usr/local/lib/
            sudo chmod 755 /usr/local/lib/libfaiss*.so
            sudo ldconfig
        fi
    else
        echo -e "${RED}❌ Unsupported operating system: $OS${NC}"
        exit 1
    fi
}

# Function to run tests with RPATH patching for a single package
run_tests_with_rpath() {
    local test_package="$1"
    shift  # Remove the first argument (package name)
    local test_args=("$@")
    
    echo -e "${YELLOW}🧪 Building test binary for $test_package...${NC}"
    
    # Create a temporary directory for the test binary
    local temp_dir=$(mktemp -d)
    local test_binary="$temp_dir/test_binary"
    
    # Build the test binary (without test arguments)
    if [[ "$OS" == "Darwin" ]]; then
        # macOS: use -lc++
        CGO_ENABLED=1 \
        CGO_CXXFLAGS="-I/usr/local/include" \
        CGO_LDFLAGS="-L/usr/local/lib -lfaiss -lfaiss_c -lc++" \
        go test -c "$test_package" -o "$test_binary"
    elif [[ "$OS" == "Linux" ]]; then
        # Linux: use -lstdc++
        CGO_ENABLED=1 \
        CGO_CXXFLAGS="-I/usr/local/include" \
        CGO_LDFLAGS="-L/usr/local/lib -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas" \
        go test -c "$test_package" -o "$test_binary"
    fi
    
    # Add RPATH to the test binary based on OS
    if [[ "$OS" == "Darwin" ]]; then
        # macOS: use install_name_tool
        echo -e "${YELLOW}🔧 Adding RPATH to test binary (macOS)...${NC}"
        install_name_tool -add_rpath "/usr/local/lib" "$test_binary"
    elif [[ "$OS" == "Linux" ]]; then
        # Linux: use patchelf if available, otherwise use LD_LIBRARY_PATH
        if command -v patchelf >/dev/null 2>&1; then
            echo -e "${YELLOW}🔧 Adding RPATH to test binary (Linux with patchelf)...${NC}"
            patchelf --set-rpath "/usr/local/lib" "$test_binary"
        else
            echo -e "${YELLOW}⚠️  patchelf not found, using LD_LIBRARY_PATH...${NC}"
            export LD_LIBRARY_PATH="/usr/local/lib:$LD_LIBRARY_PATH"
        fi
    fi
    
    # Run the test binary with arguments
    echo -e "${GREEN}🚀 Running tests for $test_package...${NC}"
    if [[ "$OS" == "Linux" && ! -f "$(command -v patchelf)" ]]; then
        # Use LD_LIBRARY_PATH for Linux when patchelf is not available
        LD_LIBRARY_PATH="/usr/local/lib:$LD_LIBRARY_PATH" "$test_binary" -test.v "${test_args[@]}"
    else
        "$test_binary" -test.v "${test_args[@]}"
    fi
    
    # Clean up
    rm -rf "$temp_dir"
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
    
    echo -e "${GREEN}🧪 Running all tests with RPATH...${NC}"
    
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
        # First try the standard way
        if go test -list . "$package" 2>/dev/null | grep -q "Test"; then
            echo -e "${YELLOW}📦 Testing package: $package${NC}"
            run_tests_with_rpath "$package" "${additional_args[@]}"
        else
            # If that fails, check if there are test files in the package directory
            package_path=$(echo "$package" | sed 's|github.com/Podcopic-Labs/ShibuDb/||')
            if [ -d "$package_path" ] && find "$package_path" -name "*_test.go" -type f | grep -q .; then
                echo -e "${YELLOW}📦 Testing package: $package (found test files)${NC}"
                run_tests_with_rpath "$package" "${additional_args[@]}"
            else
                echo -e "${YELLOW}⏭️  Skipping $package (no tests found)${NC}"
            fi
        fi
    done
}

# Setup FAISS libraries
setup_faiss_libraries

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
    run_tests_with_rpath "./benchmark/" "$@"
elif [[ "$1" == "./E2ETests/" ]]; then
    # Run E2E tests with additional arguments
    shift  # Remove the E2E package
    run_tests_with_rpath "./E2ETests/" "$@"
else
    # Run specific test package
    local package="$1"
    shift  # Remove the package name
    echo -e "${GREEN}🧪 Running tests for: $package${NC}"
    run_tests_with_rpath "$package" "$@"
fi

echo -e "${GREEN}✅ Tests completed!${NC}" 