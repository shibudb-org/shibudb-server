# ShibuDb Makefile
# This file provides common development and build tasks

.PHONY: help build test clean install uninstall lint format check-fmt vet coverage benchmark benchmark-flat-meta benchmark-vector-index e2e-test test-all build-all release

# Variables
BINARY_NAME=shibudb
VERSION=$(shell ./scripts/get_version.sh)
BUILD_DIR=build
DIST_DIR=dist
TESTDATA_DIR=cmd/server/testdata

# Go build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(shell date -u '+%Y-%m-%d_%H:%M:%S')"

# Default target
help: ## Show this help message
	@echo "ShibuDb - Available targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# Testing targets
test: ## Run all tests (excluding benchmarks, E2E, and dev-server tests)
	@echo "Running tests (excluding benchmarks, E2E, and dev-server tests)..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh --exclude-benchmark --exclude-e2e --exclude-dev-server; \
	else \
		./scripts/test_with_rpath.sh --exclude-benchmark --exclude-e2e --exclude-dev-server; \
	fi

test-all: ## Run all tests including E2E tests (starts server automatically)
	@echo "Running all tests including E2E tests..."
	@echo "Starting test server..."
	@./scripts/start-test-server.sh
	@echo "Running unit tests..."
	@$(MAKE) test
	@echo "Running E2E tests..."
	@$(MAKE) e2e-test
	@echo "Stopping test server..."
	@./scripts/stop-test-server.sh
	@echo "All tests completed!"

coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Benchmark targets
benchmark: ## Run all benchmarks
	@echo "Running all benchmarks..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/; \
	fi

benchmark-multi-table: ## Run multi-table benchmark
	@echo "Running multi-table benchmark..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/ -test.run TestMultiSpace; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/ -test.run TestMultiSpace; \
	fi

benchmark-single-space: ## Run single space benchmark
	@echo "Running single space benchmark..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/ -test.run TestSingleSpace; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/ -test.run TestSingleSpace; \
	fi

benchmark-vector-multi-space: ## Run vector multi-space benchmark
	@echo "Running vector multi-space benchmark..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/ -test.run TestVectorMultiSpace; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/ -test.run TestVectorMultiSpace; \
	fi

benchmark-vector-single-space: ## Run vector single space benchmark
	@echo "Running vector single space benchmark..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/ -test.run TestVectorSingleSpace; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/ -test.run TestVectorSingleSpace; \
	fi

benchmark-key-value-storage: ## Run key-value storage benchmark
	@echo "Running key-value storage benchmark..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/ -test.bench BenchmarkShibuDB; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/ -test.bench BenchmarkShibuDB; \
	fi

benchmark-btree-index: ## Run BTree index benchmark
	@echo "Running BTree index benchmark..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./benchmark/ -test.bench BenchmarkConcurrentIndexOps; \
	else \
		./scripts/test_with_rpath.sh ./benchmark/ -test.bench BenchmarkConcurrentIndexOps; \
	fi

benchmark-flat-meta: ## Run filterable Flat vector engine (metadata filter) benchmarks
	@echo "Running filterable Flat vector engine benchmarks..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./internal/storage/ -test.bench BenchmarkFlatMeta -test.benchmem -test.run '^$$'; \
	else \
		./scripts/test_with_rpath.sh ./internal/storage/ -test.bench BenchmarkFlatMeta -test.benchmem -test.run '^$$'; \
	fi

benchmark-vector-index: ## Run FAISS vector engine benchmarks across index types (Flat/HNSW/IVF/PQ)
	@echo "Running FAISS vector engine index-type benchmarks..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./internal/storage/ -test.bench BenchmarkVectorEngine -test.benchmem -test.run '^$$'; \
	else \
		./scripts/test_with_rpath.sh ./internal/storage/ -test.bench BenchmarkVectorEngine -test.benchmem -test.run '^$$'; \
	fi

e2e-test: ## Run end-to-end tests. to run E2E test cases install shibudb on local machine and run on port 4444. Make sure the admin credentials are admin:admin
	@echo "Running E2E tests..."
	@if [ "$(shell uname -s)" = "Linux" ]; then \
		./scripts/test_linux.sh ./E2ETests/; \
	else \
		./scripts/test_with_rpath.sh ./E2ETests/; \
	fi

# Cleanup targets
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf $(DIST_DIR)
	rm -f $(BINARY_NAME)
	rm -rf $(TESTDATA_DIR)
	rm -f coverage.out coverage.html
	rm -f *.db *.dat *.faiss
	rm -f *.test
	rm -f shibudb-server
	rm -rf cmd/test_server
	rm -rf testdata
	@echo "Cleaning test files from internal directory..."
	find internal -name "*.db" -delete
	find internal -name "*.dat" -delete
	find internal -name "*.faiss" -delete
	find internal -name "*.test" -delete
	find internal -name "*.prof" -delete
	find internal -name "*.trace" -delete
	find internal -name "*.cpu" -delete
	find internal -name "*.mem" -delete
	find internal -name "*.block" -delete
	find internal -name "*.mutex" -delete
	@echo "Cleanup complete."

start-local-server: ## Run ShibuDB server in foreground (port 4444, admin/admin); Ctrl+C to stop
	@chmod +x scripts/start-local-server.sh
	@./scripts/start-local-server.sh

connect-local-client: ## Connect to local development server using CLI client
	@echo "Connecting to local development server..."
	@echo "Default credentials: admin/admin"
	@echo "Note: shibudb CLI default listen port is 4444; dev/E2E test server also uses 4444"
	./scripts/connect-client.sh 4444

# Version management
version: ## Show current version
	@echo "Current version: $(VERSION)"

# Database files cleanup
clean-db: ## Clean database files
	@echo "Cleaning database files..."
	rm -f *.db *.dat *.faiss
	@echo "Database files cleaned."

# Test FAISS paths
test-paths: ## Test FAISS paths and environment
	@echo "Testing FAISS paths..."
	./scripts/test_paths.sh

# Default target
.DEFAULT_GOAL := help 