# TurboQuant Vector Engine â€” Proposal

**Status:** Draft Â· **Author:** Giriraj Bidwai Â· **Date:** 2026-06-13

## Abstract

Add a pure Go implementation of the [TurboQuant](https://arxiv.org/abs/2504.19874)
(ICLR 2026) vector compression algorithm as a new storage engine in ShibuDB,
achieving 8â€“16Ã— compression with ~95 % recall@10 and zero training overhead,
while avoiding the CGo dependency required by the existing FAISS engine.

## Background

### ShibuDB Vector Storage Today

ShibuDB supports two vector engines via the `VectorEngine` interface
(`internal/storage/engine.go`):

| Engine | File | Tech | Pros | Cons |
|---|---|---|---|---|
| `VectorEngineImpl` | `vector_storage.go` | FAISS via CGo | Mature, fast | CGo build pain, no Windows |
| `FlatMetaVectorEngine` | `flat_meta_vector_storage.go` | In-memory FP32 | Pure Go, metadata filter | ~256 MB/1M vectors |

Both engines store full-precision (FP32) vectors. A production cluster with
1 billion 768-dim vectors needs **~2.8 TB** of RAM for dense storage.

### TurboQuant Algorithm

TurboQuant is a **data-oblivious** quantization scheme:

1. **Random Rotation** â€“ Apply a fixed random orthogonal matrix to the input vector.
2. **Lloyd-Max Quantization** â€“ Each dimension independently quantized to a small
   number of levels using precomputed centroids from Beta(0.5, 0.5).
3. **Bit Packing** â€“ Pack quantized codes into dense byte arrays.
4. **Asymmetric Distance Computation (ADC)** â€“ Precompute dot-products for each
   codebook entry, then look up and accumulate across dimensions.

Key property: **no training data required** â€” codebook derived analytically.

### Performance (from paper)

| Bits | Compression | Recall@10 (SIFT1M) | Recall@10 (Deep1M) |
|------|-------------|-------------------|--------------------|
| 2    | 16Ã—         | 76.3 %            | 64.1 %             |
| 4    | 8Ã—          | 86.2 %            | 81.5 %             |
| 6    | 5.3Ã—        | 95.1 %            | 94.3 %             |
| 8    | 4Ã—          | 97.8 %            | 97.2 %             |

## Design

```
internal/storage/
â”œâ”€â”€ turboquant/
â”‚   â”œâ”€â”€ rotation.go          # Random orthogonal matrix
â”‚   â”œâ”€â”€ codebook.go          # Precomputed Lloyd-Max centroids
â”‚   â”œâ”€â”€ quantize.go          # Encode / decode pipeline
â”‚   â””â”€â”€ adc.go               # Asymmetric distance computation
â”œâ”€â”€ tq_vector_storage.go     # TurboQuantVectorEngine
```

### Index Types

| Type | Bits | Compression | Suffix |
|------|------|-------------|--------|
| TQ2  | 2    | 16Ã—         | .tq2   |
| TQ4  | 4    | 8Ã—          | .tq4   |
| TQ6  | 6    | 5.3Ã—        | .tq6   |
| TQ8  | 8    | 4Ã—          | .tq8   |

## Implementation Plan

### Phase 1 â€” Core Library (~400 lines)
rotation.go, codebook.go, quantize.go, adc.go

### Phase 2 â€” VectorEngine Impl (~400 lines)
tq_vector_storage.go â€” InsertVector, RemoveVector, SearchTopK, etc.

### Phase 3 â€” Space Manager Integration (~20 lines)
space_manager.go: route `TQ2`â€“`TQ8` to NewTurboQuantVectorEngine.

### Phase 4 â€” Rebuild & Compaction (~100 lines)
rebuild.go: enumerate .tq* files, rebuild on compaction.

## Open Questions

1. Default bit-width: 4 or 6? Recommend **6-bit** for production.
2. L2 metric: v1 InnerProduct only; L2 via norm + ADC.
3. Metadata filtering: follow-up.
4. Naming: bit in type name (TQ6) or `--bits` param?

## Migration Path

No migration â€” old indexes untouched. Users choose TQ via `indexType: "TQ6"`.
