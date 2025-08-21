# ShibuDb Vector Engine Guide

## Table of Contents

- [Overview](#overview)
- [Vector Space Management](#vector-space-management)
- [WAL Configuration for Vector Spaces](#wal-configuration-for-vector-spaces)
- [FAISS Index Types](#faiss-index-types)
- [Basic Vector Operations](#basic-vector-operations)
- [Advanced Search Operations](#advanced-search-operations)
- [Distance Metrics](#distance-metrics)
- [Performance Optimization](#performance-optimization)
- [Best Practices](#best-practices)
- [Examples and Use Cases](#examples-and-use-cases)
- [Troubleshooting](#troubleshooting)

## Overview

The Vector Engine in ShibuDb provides high-performance similarity search capabilities powered by FAISS (Facebook AI Similarity Search). It enables efficient storage and retrieval of high-dimensional vectors for applications like recommendation systems, image search, natural language processing, and machine learning.

### Key Features

- **Multiple Index Types**: Support for Flat, HNSW, IVF, and PQ indexes
- **Various Distance Metrics**: L2, Inner Product, L1, and more
- **High Performance**: Optimized for large-scale vector operations
- **Automatic Training**: Index training happens automatically
- **Batch Operations**: Efficient bulk vector insertion
- **Real-time Search**: Fast similarity search with configurable parameters

### Architecture

```
┌─────────────────────────────────────┐
│           Vector Space              │
├─────────────────────────────────────┤
│  FAISS Index (similarity search)    │
├─────────────────────────────────────┤
│  In-Memory Buffer (batch ops)       │
├─────────────────────────────────────┤
│  Write-Ahead Log (durability)       │
├─────────────────────────────────────┤
│  Data Files (persistent storage)    │
└─────────────────────────────────────┘
```

## Vector Space Management

### Creating a Vector Space

```bash
# Create a basic vector space (128 dimensions, Flat index, L2 metric)
CREATE-SPACE embeddings --engine vector --dimension 128

# Create with specific index type
CREATE-SPACE image_vectors --engine vector --dimension 512 --index-type HNSW32 --metric L2

# Create with custom parameters
CREATE-SPACE text_embeddings --engine vector --dimension 768 --index-type IVF32 --metric InnerProduct

# Create with different HNSW configurations
CREATE-SPACE fast_search --engine vector --dimension 128 --index-type HNSW64 --metric L2
CREATE-SPACE ultra_fast --engine vector --dimension 128 --index-type HNSW256 --metric L2

# Create with different IVF configurations
CREATE-SPACE large_dataset --engine vector --dimension 128 --index-type IVF64 --metric L2
CREATE-SPACE huge_dataset --engine vector --dimension 128 --index-type IVF256 --metric L2

# Create with different PQ configurations
CREATE-SPACE memory_efficient --engine vector --dimension 128 --index-type PQ8 --metric L2
CREATE-SPACE ultra_efficient --engine vector --dimension 128 --index-type PQ32 --metric L2

# Create with composite indices
CREATE-SPACE accurate_large --engine vector --dimension 128 --index-type IVF32,Flat --metric L2
CREATE-SPACE fast_accurate --engine vector --dimension 128 --index-type HNSW64,Flat --metric L2
CREATE-SPACE efficient_accurate --engine vector --dimension 128 --index-type PQ8,Flat --metric L2
CREATE-SPACE balanced_large --engine vector --dimension 128 --index-type IVF64,PQ16 --metric L2
CREATE-SPACE fast_efficient --engine vector --dimension 128 --index-type HNSW128,PQ32 --metric L2

# Create with WAL enabled (for enhanced durability)
CREATE-SPACE durable_embeddings --engine vector --dimension 128 --enable-wal

# Create with WAL disabled (default, for maximum performance)
CREATE-SPACE fast_embeddings --engine vector --dimension 128 --disable-wal
```

**Parameters:**
- `--engine vector`: Specifies vector engine type
- `--dimension N`: Vector dimension (required for vector spaces)
- `--index-type TYPE`: FAISS index type (default: Flat)
- `--metric METRIC`: Distance metric (default: L2)
- `--enable-wal`: Enable Write-Ahead Logging for enhanced durability (default: disabled for vector spaces)
- `--disable-wal`: Disable Write-Ahead Logging for maximum performance (default for vector spaces)

### Minimum Vector Requirements

Different index types have different minimum vector requirements before search operations become available:

- **Flat**: No minimum required (search available immediately)
- **HNSW{n}**: No minimum required (search available immediately)
- **IVF{n}**: Minimum n vectors required (n = number of clusters)
- **PQ{n}**: Minimum 256 vectors required (for training)
- **Composite indices**: Follow the higher requirement of their components

**Examples:**
```bash
# HNSW32 - search available immediately
CREATE-SPACE hnsw_space --engine vector --dimension 128 --index-type HNSW32
USE hnsw_space
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5  # Works immediately

# IVF32 - search available after 32 vectors
CREATE-SPACE ivf_space --engine vector --dimension 128 --index-type IVF32
USE ivf_space
# Need to insert at least 32 vectors before search works
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
# ... insert 31 more vectors ...
INSERT-VECTOR 32 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5  # Now works

# PQ8 - search available after 256 vectors
CREATE-SPACE pq_space --engine vector --dimension 128 --index-type PQ8
USE pq_space
# Need to insert at least 256 vectors before search works
# ... insert 256 vectors ...
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5  # Now works
```

### Supported Index Types

ShibuDb supports various FAISS index types with hardcoded configurations and composite indices for different use cases.

#### Single Index Types

| Index Type | Description | Use Case | Memory | Speed | Min Vectors Required |
|------------|-------------|----------|--------|-------|---------------------|
| `Flat` | Exact search | Small datasets, high accuracy | High | Slow | 0 |
| `HNSW{n}` | Hierarchical Navigable Small World | Fast similarity search | Medium | Fast | 0 |
| `IVF{n}` | Inverted file index | Large datasets | Low | Medium | n |
| `PQ{n}` | Product quantization | Very large datasets | Very Low | Fast | 256 |

#### Hardcoded Index Variants

**HNSW Indices**: `HNSW{n}` where n is a power of 2 from 2 to 256
- Examples: `HNSW2`, `HNSW4`, `HNSW8`, `HNSW16`, `HNSW32`, `HNSW64`, `HNSW128`, `HNSW256`
- **Minimum vectors required**: 0 (search enabled immediately)
- **Use case**: Fast approximate similarity search with configurable neighbor count

**IVF Indices**: `IVF{n}` where n is a power of 2 from 2 to 256
- Examples: `IVF2`, `IVF4`, `IVF8`, `IVF16`, `IVF32`, `IVF64`, `IVF128`, `IVF256`
- **Minimum vectors required**: n (number of clusters)
- **Use case**: Large dataset indexing with configurable cluster count

**PQ Indices**: `PQ{n}` where n is a power of 2 from 2 to 256
- Examples: `PQ2`, `PQ4`, `PQ8`, `PQ16`, `PQ32`, `PQ64`, `PQ128`, `PQ256`
- **Minimum vectors required**: 256 (always required for PQ training)
- **Use case**: Memory-efficient indexing for very large datasets

#### Composite Index Types

Composite indices combine multiple index types for enhanced performance and functionality:

| Composite Index | Description | Min Vectors Required | Use Case |
|-----------------|-------------|---------------------|----------|
| `IVF{n},Flat` | IVF clustering with exact search refinement | max(n, 1) | Large datasets with high accuracy |
| `HNSW{n},Flat` | HNSW search with exact search refinement | 0 | Fast search with high accuracy |
| `PQ{n},Flat` | PQ quantization with exact search refinement | 256 | Memory-efficient with high accuracy |
| `IVF{n},PQ{m}` | IVF clustering with PQ quantization | max(n, 256) | Very large datasets with balanced performance |
| `HNSW{n},PQ{m}` | HNSW search with PQ quantization | 256 | Fast search with memory efficiency |

**Composite Index Examples:**
- `IVF32,Flat`: 32 clusters with exact search refinement (min 32 vectors)
- `HNSW64,Flat`: 64 neighbors with exact search refinement (min 0 vectors)
- `PQ8,Flat`: 8-bit quantization with exact search refinement (min 256 vectors)
- `IVF64,PQ16`: 64 clusters with 16-bit quantization (min 256 vectors)
- `HNSW128,PQ32`: 128 neighbors with 32-bit quantization (min 256 vectors)

### Using a Vector Space

```bash
# Switch to vector space
USE embeddings

# Verify current space (prompt will show current space)
[embeddings]>
```

### WAL Configuration for Vector Spaces

Vector spaces support configurable Write-Ahead Logging (WAL) to balance performance and durability:

#### Default Behavior (WAL Disabled)
- **Performance**: Maximum write performance (~20-30% faster)
- **Durability**: Basic durability through data file persistence
- **Recovery**: Limited recovery capabilities
- **Use Case**: High-performance vector operations where some data loss is acceptable

#### WAL Enabled
- **Performance**: Slightly slower due to additional logging overhead
- **Durability**: Enhanced durability with full crash recovery
- **Recovery**: Complete recovery from crashes and unexpected shutdowns
- **Use Case**: Production systems requiring strong durability guarantees

#### WAL Configuration Examples

```bash
# Create vector space with WAL enabled (enhanced durability)
CREATE-SPACE production_vectors --engine vector --dimension 128 --index-type HNSW32 --enable-wal

# Create vector space with WAL disabled (maximum performance, default)
CREATE-SPACE fast_vectors --engine vector --dimension 128 --index-type HNSW32 --disable-wal

# Create vector space with WAL disabled (explicit, same as default)
CREATE-SPACE performance_vectors --engine vector --dimension 128 --index-type HNSW32
```

#### When to Use WAL

**Use WAL Enabled (`--enable-wal`) for:**
- Production systems with critical data
- Applications requiring strong durability guarantees
- Systems where data loss is unacceptable
- Long-running vector operations

**Use WAL Disabled (`--disable-wal` or default) for:**
- Development and testing environments
- High-throughput applications where some data loss is acceptable
- Real-time vector processing with strict latency requirements
- Temporary or cache-like vector storage

## FAISS Index Types

### 1. Flat Index (Exact Search)

**Best for**: Small datasets (< 1M vectors), high accuracy requirements

```bash
# Create flat index
CREATE-SPACE exact_search --engine vector --dimension 128 --index-type Flat --metric L2
USE exact_search

# Insert vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1,6.1,7.1,8.1
INSERT-VECTOR 3 9.0,8.0,7.0,6.0,5.0,4.0,3.0,2.0

# Search for similar vectors
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 3

Note: Only numerical IDs are supported for vector spaces.
```

**Characteristics:**
- 100% accuracy (exact search)
- High memory usage
- Slower for large datasets
- No training required

### 2. HNSW Index (Hierarchical Navigable Small World)

**Best for**: Fast similarity search, medium datasets

```bash
# Create HNSW index (32 neighbors)
CREATE-SPACE fast_search --engine vector --dimension 128 --index-type HNSW32 --metric L2
USE fast_search

# Insert vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1,6.1,7.1,8.1
INSERT-VECTOR 3 9.0,8.0,7.0,6.0,5.0,4.0,3.0,2.0

# Fast similarity search
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5
```

**Available HNSW Variants:**
```bash
# Different neighbor configurations (power of 2 from 2 to 256)
CREATE-SPACE hnsw2 --engine vector --dimension 128 --index-type HNSW2   # 2 neighbors
CREATE-SPACE hnsw4 --engine vector --dimension 128 --index-type HNSW4   # 4 neighbors
CREATE-SPACE hnsw8 --engine vector --dimension 128 --index-type HNSW8   # 8 neighbors
CREATE-SPACE hnsw16 --engine vector --dimension 128 --index-type HNSW16 # 16 neighbors
CREATE-SPACE hnsw32 --engine vector --dimension 128 --index-type HNSW32 # 32 neighbors
CREATE-SPACE hnsw64 --engine vector --dimension 128 --index-type HNSW64 # 64 neighbors
CREATE-SPACE hnsw128 --engine vector --dimension 128 --index-type HNSW128 # 128 neighbors
CREATE-SPACE hnsw256 --engine vector --dimension 128 --index-type HNSW256 # 256 neighbors
```

**Characteristics:**
- Very fast search
- Good accuracy
- Medium memory usage
- No training required
- Search available immediately (no minimum vectors required)
- Number suffix indicates number of neighbors (higher = more accurate but slower)

### 3. IVF Index (Inverted File Index)

**Best for**: Large datasets, balanced performance

```bash
# Create IVF index (32 clusters)
CREATE-SPACE large_dataset --engine vector --dimension 128 --index-type IVF32 --metric L2
USE large_dataset

# Insert many vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1,6.1,7.1,8.1
# ... insert thousands more vectors

# Search in large dataset
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

**Available IVF Variants:**
```bash
# Different cluster configurations (power of 2 from 2 to 256)
CREATE-SPACE ivf2 --engine vector --dimension 128 --index-type IVF2   # 2 clusters
CREATE-SPACE ivf4 --engine vector --dimension 128 --index-type IVF4   # 4 clusters
CREATE-SPACE ivf8 --engine vector --dimension 128 --index-type IVF8   # 8 clusters
CREATE-SPACE ivf16 --engine vector --dimension 128 --index-type IVF16 # 16 clusters
CREATE-SPACE ivf32 --engine vector --dimension 128 --index-type IVF32 # 32 clusters
CREATE-SPACE ivf64 --engine vector --dimension 128 --index-type IVF64 # 64 clusters
CREATE-SPACE ivf128 --engine vector --dimension 128 --index-type IVF128 # 128 clusters
CREATE-SPACE ivf256 --engine vector --dimension 128 --index-type IVF256 # 256 clusters
```

**Characteristics:**
- Good for large datasets
- Requires training (automatic)
- Lower memory usage
- Minimum vectors required = number of clusters (n)
- Number suffix indicates number of clusters (higher = more clusters, better for larger datasets)

### 4. PQ Index (Product Quantization)

**Best for**: Very large datasets, memory-constrained environments

```bash
# Create PQ index (4 bits per sub-vector)
CREATE-SPACE huge_dataset --engine vector --dimension 128 --index-type PQ4 --metric L2
USE huge_dataset

# Insert millions of vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
# ... insert millions more

# Search in huge dataset
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 20
```

**Available PQ Variants:**
```bash
# Different quantization levels (power of 2 from 2 to 256)
CREATE-SPACE pq2 --engine vector --dimension 128 --index-type PQ2   # 2 bits per sub-vector
CREATE-SPACE pq4 --engine vector --dimension 128 --index-type PQ4   # 4 bits per sub-vector
CREATE-SPACE pq8 --engine vector --dimension 128 --index-type PQ8   # 8 bits per sub-vector
CREATE-SPACE pq16 --engine vector --dimension 128 --index-type PQ16 # 16 bits per sub-vector
CREATE-SPACE pq32 --engine vector --dimension 128 --index-type PQ32 # 32 bits per sub-vector
CREATE-SPACE pq64 --engine vector --dimension 128 --index-type PQ64 # 64 bits per sub-vector
CREATE-SPACE pq128 --engine vector --dimension 128 --index-type PQ128 # 128 bits per sub-vector
CREATE-SPACE pq256 --engine vector --dimension 128 --index-type PQ256 # 256 bits per sub-vector
```

**Characteristics:**
- Very low memory usage
- Fast search
- Lower accuracy
- Requires training (automatic)
- Minimum 256 vectors required for training
- Number suffix indicates bits per sub-vector (higher = more accurate but more memory)

### 5. Composite Indices

Composite indices combine multiple index types to achieve better performance characteristics than single indices alone.

#### IVF{n},Flat - Clustering with Exact Refinement

**Best for**: Large datasets requiring high accuracy

```bash
# Create IVF32,Flat index
CREATE-SPACE accurate_large --engine vector --dimension 128 --index-type IVF32,Flat --metric L2
USE accurate_large

# Insert vectors (minimum 32 required)
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
# ... insert at least 31 more vectors ...
INSERT-VECTOR 32 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0

# Search with high accuracy
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

**Characteristics:**
- IVF clustering for fast candidate selection
- Flat index for exact distance computation
- Higher accuracy than IVF alone
- Minimum vectors required = max(n, 1) where n is number of clusters

#### HNSW{n},Flat - Fast Search with Exact Refinement

**Best for**: Fast search with high accuracy

```bash
# Create HNSW64,Flat index
CREATE-SPACE fast_accurate --engine vector --dimension 128 --index-type HNSW64,Flat --metric L2
USE fast_accurate

# Insert vectors (no minimum required)
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1,6.1,7.1,8.1

# Fast search with high accuracy
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

**Characteristics:**
- HNSW for fast candidate selection
- Flat index for exact distance computation
- Very fast search with high accuracy
- No minimum vectors required

#### PQ{n},Flat - Memory Efficient with Exact Refinement

**Best for**: Memory-constrained environments requiring high accuracy

```bash
# Create PQ8,Flat index
CREATE-SPACE efficient_accurate --engine vector --dimension 128 --index-type PQ8,Flat --metric L2
USE efficient_accurate

# Insert vectors (minimum 256 required)
# ... insert at least 256 vectors ...

# Memory-efficient search with high accuracy
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

**Characteristics:**
- PQ for memory-efficient candidate selection
- Flat index for exact distance computation
- Low memory usage with high accuracy
- Minimum 256 vectors required

#### IVF{n},PQ{m} - Balanced Performance for Very Large Datasets

**Best for**: Very large datasets with balanced performance

```bash
# Create IVF64,PQ16 index
CREATE-SPACE balanced_large --engine vector --dimension 128 --index-type IVF64,PQ16 --metric L2
USE balanced_large

# Insert vectors (minimum 256 required)
# ... insert at least 256 vectors ...

# Balanced search performance
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

**Characteristics:**
- IVF clustering for fast candidate selection
- PQ for memory-efficient distance computation
- Good balance of speed and memory usage
- Minimum vectors required = max(n, 256) where n is number of clusters

#### HNSW{n},PQ{m} - Fast Search with Memory Efficiency

**Best for**: Fast search in memory-constrained environments

```bash
# Create HNSW128,PQ32 index
CREATE-SPACE fast_efficient --engine vector --dimension 128 --index-type HNSW128,PQ32 --metric L2
USE fast_efficient

# Insert vectors (minimum 256 required)
# ... insert at least 256 vectors ...

# Fast and memory-efficient search
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

**Characteristics:**
- HNSW for fast candidate selection
- PQ for memory-efficient distance computation
- Fast search with low memory usage
- Minimum 256 vectors required

## Basic Vector Operations

### INSERT-VECTOR - Store Vectors

```bash
# Insert a single vector
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0

# Insert with numeric ID
INSERT-VECTOR 1001 1.5,2.5,3.5,4.5,5.5,6.5,7.5,8.5
```

**Format**: `INSERT-VECTOR <id> <comma-separated-floats>`

### GET-VECTOR - Retrieve Vectors

```bash
# Get vector by ID
GET-VECTOR 1
```

### SEARCH-TOPK - Similarity Search

```bash
# Find top 5 most similar vectors
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5

# Find top 1 most similar vector
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 1
```

**Format**: `SEARCH-TOPK <query-vector> <k>`

### RANGE-SEARCH - Radius Search

```bash
# Find all vectors within radius 0.5
RANGE-SEARCH 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 0.5

# Find all vectors within radius 1.0
RANGE-SEARCH 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 1.0
```

**Format**: `RANGE-SEARCH <query-vector> <radius>`

## Advanced Search Operations

### Batch Vector Operations

```bash
# Insert multiple vectors efficiently
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1,6.1,7.1,8.1
INSERT-VECTOR 3 1.2,2.2,3.2,4.2,5.2,6.2,7.2,8.2
INSERT-VECTOR 4 1.3,2.3,3.3,4.3,5.3,6.3,7.3,8.3
INSERT-VECTOR 5 1.4,2.4,3.4,4.4,5.4,6.4,7.4,8.4

# Search for similar vectors
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

### Multi-Query Search

```bash
# Search with different query vectors
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5
SEARCH-TOPK 9.0,8.0,7.0,6.0,5.0,4.0,3.0,2.0 5
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 5
```

### Hybrid Search Strategies

```bash
# Use range search to find candidates
RANGE-SEARCH 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 1.0

# Then use top-k search for ranking
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10
```

## Distance Metrics

### Supported Metrics

| Metric | Description | Use Case | Formula |
|--------|-------------|----------|---------|
| `L2` | Euclidean distance | General purpose | √(Σ(x₁-y₁)²) |
| `InnerProduct` | Inner product similarity | Cosine similarity (normalized vectors) | Σ(x₁y₁) |
| `L1` | Manhattan distance | Robust to outliers | Σ|x₁-y₁| |
| `Lp` | Lp norm distance | Configurable norm | (Σ|x₁-y₁|ᵖ)^(1/p) |
| `Canberra` | Canberra distance | Weighted differences | Σ|x₁-y₁|/(|x₁|+|y₁|) |
| `BrayCurtis` | Bray-Curtis distance | Ecological data | Σ|x₁-y₁|/Σ(x₁+y₁) |
| `JensenShannon` | Jensen-Shannon divergence | Probability distributions | JS(P||Q) |
| `Linf` | L-infinity distance | Maximum difference | max|x₁-y₁| |

### Choosing the Right Metric

#### L2 (Euclidean Distance) - Default
```bash
# Good for general-purpose similarity
CREATE-SPACE general --engine vector --dimension 128 --metric L2
```

#### InnerProduct (Cosine Similarity)
```bash
# Good for normalized vectors (embeddings)
CREATE-SPACE embeddings --engine vector --dimension 768 --metric InnerProduct
```

#### L1 (Manhattan Distance)
```bash
# Good for robust similarity (outlier-resistant)
CREATE-SPACE robust --engine vector --dimension 128 --metric L1
```

## Performance Optimization

### Index Selection Guidelines

#### Small Datasets (< 100K vectors)
```bash
# Use Flat index for exact search
CREATE-SPACE small_dataset --engine vector --dimension 128 --index-type Flat

# Or use HNSW for faster approximate search
CREATE-SPACE small_fast --engine vector --dimension 128 --index-type HNSW16
```

#### Medium Datasets (100K - 1M vectors)
```bash
# Use HNSW for fast approximate search
CREATE-SPACE medium_dataset --engine vector --dimension 128 --index-type HNSW32

# For higher accuracy, use HNSW64 or HNSW128
CREATE-SPACE medium_accurate --engine vector --dimension 128 --index-type HNSW64

# For very high accuracy with exact refinement
CREATE-SPACE medium_exact --engine vector --dimension 128 --index-type HNSW32,Flat
```

#### Large Datasets (1M - 10M vectors)
```bash
# Use IVF for balanced performance
CREATE-SPACE large_dataset --engine vector --dimension 128 --index-type IVF32

# For larger datasets, use more clusters
CREATE-SPACE large_many_clusters --engine vector --dimension 128 --index-type IVF64

# For high accuracy with exact refinement
CREATE-SPACE large_accurate --engine vector --dimension 128 --index-type IVF32,Flat
```

#### Very Large Datasets (> 10M vectors)
```bash
# Use PQ for memory efficiency
CREATE-SPACE huge_dataset --engine vector --dimension 128 --index-type PQ8

# For better accuracy, use higher PQ bits
CREATE-SPACE huge_accurate --engine vector --dimension 128 --index-type PQ16

# For balanced performance with clustering
CREATE-SPACE huge_balanced --engine vector --dimension 128 --index-type IVF64,PQ16

# For fast search with memory efficiency
CREATE-SPACE huge_fast --engine vector --dimension 128 --index-type HNSW128,PQ32
```

#### Index Variant Selection Guidelines

**HNSW Variants:**
- `HNSW2-HNSW16`: Very fast, lower accuracy, good for real-time applications
- `HNSW32-HNSW64`: Balanced speed and accuracy, good for most applications
- `HNSW128-HNSW256`: Higher accuracy, slower, good for precision-critical applications

**IVF Variants:**
- `IVF2-IVF16`: Good for smaller large datasets (1M-5M vectors)
- `IVF32-IVF64`: Good for medium large datasets (5M-20M vectors)
- `IVF128-IVF256`: Good for very large datasets (20M+ vectors)

**PQ Variants:**
- `PQ2-PQ8`: Very memory efficient, lower accuracy
- `PQ16-PQ32`: Balanced memory and accuracy
- `PQ64-PQ256`: Higher accuracy, more memory usage

### Memory Usage Optimization

#### Vector Dimension Impact
```bash
# Lower dimensions = less memory
CREATE-SPACE low_dim --engine vector --dimension 64 --index-type HNSW32

# Higher dimensions = more memory
CREATE-SPACE high_dim --engine vector --dimension 1024 --index-type HNSW32
```

#### Index Type Memory Usage
```bash
# Flat: Highest memory usage
CREATE-SPACE flat_index --engine vector --dimension 128 --index-type Flat

# HNSW: Medium memory usage (varies by neighbor count)
CREATE-SPACE hnsw_small --engine vector --dimension 128 --index-type HNSW16
CREATE-SPACE hnsw_medium --engine vector --dimension 128 --index-type HNSW32
CREATE-SPACE hnsw_large --engine vector --dimension 128 --index-type HNSW64

# IVF: Lower memory usage (varies by cluster count)
CREATE-SPACE ivf_small --engine vector --dimension 128 --index-type IVF16
CREATE-SPACE ivf_medium --engine vector --dimension 128 --index-type IVF32
CREATE-SPACE ivf_large --engine vector --dimension 128 --index-type IVF64

# PQ: Lowest memory usage (varies by quantization bits)
CREATE-SPACE pq_small --engine vector --dimension 128 --index-type PQ4
CREATE-SPACE pq_medium --engine vector --dimension 128 --index-type PQ8
CREATE-SPACE pq_large --engine vector --dimension 128 --index-type PQ16

# Composite indices: Memory usage depends on components
CREATE-SPACE composite_accurate --engine vector --dimension 128 --index-type IVF32,Flat
CREATE-SPACE composite_efficient --engine vector --dimension 128 --index-type HNSW64,PQ16
```

### Search Performance Tuning

#### K Value Impact
```bash
# Smaller k = faster search
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 1

# Larger k = slower search
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 100
```

#### Radius Impact
```bash
# Smaller radius = fewer results, faster
RANGE-SEARCH 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 0.1

# Larger radius = more results, slower
RANGE-SEARCH 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 10.0
```

## Best Practices

### 1. Vector Normalization

#### For InnerProduct Metric
```bash
# Normalize vectors before insertion for cosine similarity
# Example: [0.1, 0.2, 0.3] -> [0.267, 0.534, 0.801]
INSERT-VECTOR 1 0.267,0.534,0.801
INSERT-VECTOR 2 0.577,0.577,0.577
```

#### For L2 Metric
```bash
# L2 works well with raw vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1
```

### 2. Vector ID Management

#### Use Meaningful IDs
```bash
# Good: Descriptive IDs
INSERT-VECTOR user_123_profile 0.1,0.2,0.3,0.4,0.5
INSERT-VECTOR product_456_image 0.6,0.7,0.8,0.9,1.0

# Avoid: Random IDs
INSERT-VECTOR abc123 0.1,0.2,0.3,0.4,0.5
```

#### Consistent ID Patterns
```bash
# Use consistent naming patterns
INSERT-VECTOR user:123:embedding 0.1,0.2,0.3,0.4,0.5
INSERT-VECTOR user:456:embedding 0.6,0.7,0.8,0.9,1.0
INSERT-VECTOR product:789:embedding 1.1,1.2,1.3,1.4,1.5
```

### 3. Batch Operations

#### Efficient Insertion
```bash
# Insert related vectors together
USE user_embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5
INSERT-VECTOR 2 0.6,0.7,0.8,0.9,1.0
INSERT-VECTOR 3 1.1,1.2,1.3,1.4,1.5
```

#### Efficient Search
```bash
# Search multiple related queries
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5 10
SEARCH-TOPK 0.6,0.7,0.8,0.9,1.0 10
SEARCH-TOPK 1.1,1.2,1.3,1.4,1.5 10
```

### 4. Error Handling

#### Check Vector Dimensions
```bash
# Ensure query vector matches space dimension
# Space dimension: 128
# Query vector must have 128 values
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5
# Error: dimension mismatch
```

#### Validate Vector Values
```bash
# Check for NaN or infinite values
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0
# Good: All finite values

INSERT-VECTOR 2 1.0,NaN,3.0,4.0,5.0
# Bad: Contains NaN
```

## Examples and Use Cases

### 1. Recommendation System

```bash
# Create user embedding space with different configurations
# For small user base (fast search)
CREATE-SPACE user_embeddings_fast --engine vector --dimension 128 --index-type HNSW16 --metric InnerProduct

# For medium user base (balanced)
CREATE-SPACE user_embeddings_balanced --engine vector --dimension 128 --index-type HNSW32 --metric InnerProduct

# For large user base (high accuracy)
CREATE-SPACE user_embeddings_accurate --engine vector --dimension 128 --index-type HNSW64,Flat --metric InnerProduct

# For production systems (with WAL enabled for durability)
CREATE-SPACE user_embeddings_production --engine vector --dimension 128 --index-type HNSW32 --metric InnerProduct --enable-wal

# For high-performance caching (WAL disabled for speed)
CREATE-SPACE user_embeddings_cache --engine vector --dimension 128 --index-type HNSW16 --metric InnerProduct --disable-wal

USE user_embeddings_balanced

# Store user embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 3 0.9,0.8,0.7,0.6,0.5,0.4,0.3,0.2

# Find similar users
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 5
```

### 2. Image Search

```bash
# Create image embedding space with different configurations
# For small image collection (balanced)
CREATE-SPACE image_embeddings_balanced --engine vector --dimension 512 --index-type IVF32 --metric L2

# For large image collection (high accuracy)
CREATE-SPACE image_embeddings_accurate --engine vector --dimension 512 --index-type IVF64,Flat --metric L2

# For very large image collection (memory efficient)
CREATE-SPACE image_embeddings_efficient --engine vector --dimension 512 --index-type IVF128,PQ16 --metric L2

USE image_embeddings_balanced

# Store image embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 3 0.15,0.25,0.35,0.45,0.55,0.65,0.75,0.85

# Find similar images
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 10
```

### 3. Text Similarity Search

```bash
# Create text embedding space with different configurations
# For real-time search (very fast)
CREATE-SPACE text_embeddings_fast --engine vector --dimension 768 --index-type HNSW16 --metric InnerProduct

# For balanced performance
CREATE-SPACE text_embeddings_balanced --engine vector --dimension 768 --index-type HNSW32 --metric InnerProduct

# For high accuracy with exact refinement
CREATE-SPACE text_embeddings_accurate --engine vector --dimension 768 --index-type HNSW64,Flat --metric InnerProduct

USE text_embeddings_balanced

# Store document embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 3 0.9,0.8,0.7,0.6,0.5,0.4,0.3,0.2

# Find similar documents
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 5
```

### 4. Anomaly Detection

```bash
# Create anomaly detection space
CREATE-SPACE anomaly_detection --engine vector --dimension 64 --index-type Flat --metric L2
USE anomaly_detection

# Store normal behavior vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1,6.1,7.1,8.1
INSERT-VECTOR 3 1.2,2.2,3.2,4.2,5.2,6.2,7.2,8.2

# Find anomalies (vectors far from normal)
RANGE-SEARCH 10.0,20.0,30.0,40.0,50.0,60.0,70.0,80.0 5.0
```

### 5. Semantic Search

```bash
# Create semantic search space
CREATE-SPACE semantic_search --engine vector --dimension 1024 --index-type HNSW32 --metric InnerProduct
USE semantic_search

# Store semantic embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 3 0.15,0.25,0.35,0.45,0.55,0.65,0.75,0.85

# Semantic search
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 10
```

### 6. Face Recognition

```bash
# Create face embedding space
CREATE-SPACE face_embeddings --engine vector --dimension 128 --index-type IVF32 --metric L2
USE face_embeddings

# Store face embeddings
INSERT-VECTOR person:john:face:1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR person:john:face:2 0.11,0.21,0.31,0.41,0.51,0.61,0.71,0.81
INSERT-VECTOR person:jane:face:1 0.9,0.8,0.7,0.6,0.5,0.4,0.3,0.2

# Face recognition
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 5
```

## Troubleshooting

### Common Issues

#### 1. "Dimension mismatch" Error

**Problem**: Query vector dimension doesn't match space dimension

**Solution**:
```bash
# Check space dimension
# Create space with correct dimension
CREATE-SPACE my_space --engine vector --dimension 128

# Ensure query vector has 128 values
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0 5
# Error: Need 128 values, got 8
```

#### 2. "Invalid vector id" Error

**Problem**: Vector ID format is invalid

**Solution**:
```bash
# Use valid ID format
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0
INSERT-VECTOR 2 1.0,2.0,3.0,4.0,5.0

# Avoid non-numeric IDs
INSERT-VECTOR user@123 1.0,2.0,3.0,4.0,5.0
# May cause issues
```

#### 3. "Operation not supported: not a vector space" Error

**Problem**: Trying to use vector operations in key-value space

**Solution**:
```bash
# Create vector space
CREATE-SPACE my_vectors --engine vector --dimension 128

# Use vector space
USE my_vectors

# Then perform vector operations
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0
```

#### 4. Performance Issues

**Problem**: Slow search or high memory usage

**Solutions**:
```bash
# Choose appropriate index type
CREATE-SPACE fast_search --engine vector --dimension 128 --index-type HNSW32

# Reduce search k value
SEARCH-TOPK 1.0,2.0,3.0,4.0,5.0 5  # Instead of 100

# Use smaller radius for range search
RANGE-SEARCH 1.0,2.0,3.0,4.0,5.0 0.5  # Instead of 10.0
```

### Performance Monitoring

#### Check Index Status

```bash
# Monitor disk usage
du -sh /usr/local/var/lib/shibudb/

# Check specific vector space files
ls -la /usr/local/var/lib/shibudb/
```

#### Monitor Search Performance

```bash
# Watch server logs for performance issues
tail -f /usr/local/var/log/shibudb.log | grep -E "(slow|performance|timeout)"

# Check connection usage
shibudb manager 9090 stats
```

### Data Recovery

#### From WAL (Write-Ahead Log)

Vector data is automatically recovered from WAL on server restart (if WAL is enabled):

```bash
# Restart server (recovery happens automatically)
sudo shibudb stop
sudo shibudb start 9090

# Check logs for recovery messages
tail -f /usr/local/var/log/shibudb.log
```

**Note**: WAL recovery is only available if the space was created with `--enable-wal`. Spaces created with WAL disabled have limited recovery capabilities.

#### Index Rebuilding

If index corruption occurs:

```bash
# Delete and recreate space (data will be lost)
DELETE-SPACE corrupted_space
CREATE-SPACE new_space --engine vector --dimension 128 --index-type HNSW32

# Re-insert vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0,5.0
INSERT-VECTOR 2 1.1,2.1,3.1,4.1,5.1
```

## Next Steps

After mastering the Vector Engine, explore:

- [Key-Value Engine Guide](KEY_VALUE_ENGINE.md) - Learn key-value operations
- [User Management Guide](USER_MANAGEMENT.md) - Set up authentication and permissions
- [Administration Guide](ADMINISTRATION.md) - Server management and monitoring
- [API Reference](API_REFERENCE.md) - Complete command reference 