# ShibuDb Vector Engine Guide

## Table of Contents

- [Overview](#overview)
- [Vector Space Management](#vector-space-management)
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
```

**Parameters:**
- `--engine vector`: Specifies vector engine type
- `--dimension N`: Vector dimension (required for vector spaces)
- `--index-type TYPE`: FAISS index type (default: Flat)
- `--metric METRIC`: Distance metric (default: L2)

### Supported Index Types

| Index Type | Description | Use Case | Memory | Speed |
|------------|-------------|----------|--------|-------|
| `Flat` | Exact search | Small datasets, high accuracy | High | Slow |
| `HNSW32` | Approximate search | Fast similarity search | Medium | Fast |
| `IVF32` | Inverted file index | Large datasets | Low | Medium |
| `PQ4` | Product quantization | Very large datasets | Very Low | Fast |

### Using a Vector Space

```bash
# Switch to vector space
USE embeddings

# Verify current space (prompt will show current space)
[embeddings]>
```

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

**Characteristics:**
- Very fast search
- Good accuracy
- Medium memory usage
- No training required
- Number suffix (e.g., HNSW32) indicates number of neighbors

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

**Characteristics:**
- Good for large datasets
- Requires training (automatic)
- Lower memory usage
- Number suffix indicates number of clusters

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

**Characteristics:**
- Very low memory usage
- Fast search
- Lower accuracy
- Requires training (automatic)
- Number suffix indicates bits per sub-vector

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
```

#### Medium Datasets (100K - 1M vectors)
```bash
# Use HNSW for fast approximate search
CREATE-SPACE medium_dataset --engine vector --dimension 128 --index-type HNSW32
```

#### Large Datasets (1M - 10M vectors)
```bash
# Use IVF for balanced performance
CREATE-SPACE large_dataset --engine vector --dimension 128 --index-type IVF32
```

#### Very Large Datasets (> 10M vectors)
```bash
# Use PQ for memory efficiency
CREATE-SPACE huge_dataset --engine vector --dimension 128 --index-type PQ4
```

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

# HNSW: Medium memory usage
CREATE-SPACE hnsw_index --engine vector --dimension 128 --index-type HNSW32

# IVF: Lower memory usage
CREATE-SPACE ivf_index --engine vector --dimension 128 --index-type IVF32

# PQ: Lowest memory usage
CREATE-SPACE pq_index --engine vector --dimension 128 --index-type PQ4
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
# Create user embedding space
CREATE-SPACE user_embeddings --engine vector --dimension 128 --index-type HNSW32 --metric InnerProduct
USE user_embeddings

# Store user embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 3 0.9,0.8,0.7,0.6,0.5,0.4,0.3,0.2

# Find similar users
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 5
```

### 2. Image Search

```bash
# Create image embedding space
CREATE-SPACE image_embeddings --engine vector --dimension 512 --index-type IVF32 --metric L2
USE image_embeddings

# Store image embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 3 0.15,0.25,0.35,0.45,0.55,0.65,0.75,0.85

# Find similar images
SEARCH-TOPK 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8 10
```

### 3. Text Similarity Search

```bash
# Create text embedding space
CREATE-SPACE text_embeddings --engine vector --dimension 768 --index-type HNSW32 --metric InnerProduct
USE text_embeddings

# Store document embeddings
INSERT-VECTOR 1 0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8
INSERT-VECTOR 2 0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9
INSERT-VECTOR 2 0.9,0.8,0.7,0.6,0.5,0.4,0.3,0.2

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

Vector data is automatically recovered from WAL on server restart:

```bash
# Restart server (recovery happens automatically)
sudo shibudb stop
sudo shibudb start 9090

# Check logs for recovery messages
tail -f /usr/local/var/log/shibudb.log
```

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