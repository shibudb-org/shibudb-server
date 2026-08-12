# ShibuDb Performance Benchmarking

Performance benchmark results for **ShibuDb v1.2.0**, covering the key-value engine, the vector search engine (all supported index types), and vector search with metadata filtering.

- **Benchmark date:** August 11–12, 2026
- **ShibuDb version:** v1.2.0
- **Benchmark tool:** [shibudb-server-benchmarking](https://github.com/shibudb-org/shibudb-server-benchmarking) (commit `22b30f0351acb1f56cdbe77742f47c0e93a54067`)

---

## 1. Test Environment

The benchmark client and the ShibuDb server ran **on the same machine** (client connected to `localhost:4444`), so results exclude real network latency but include full protocol/serialization overhead. Client and server share CPU and RAM, which slightly understates what a dedicated server could achieve at high concurrency.

| Component | Detail                                                    |
|---|-----------------------------------------------------------|
| Host | (single node, server + client co-located)                 |
| CPU | AMD EPYC 7B12, 8 cores, 16 vCPUs                                 |
| RAM | 23.5 GB                                                   |
| Disk | 10 GB total (`/`)                                         |
| OS | Linux 6.12.100 (Debian 13 cloud image, amd64, glibc 2.41) |
| Client runtime | Python 3.13.5, process-based parallelism                  |

## 2. Methodology

All results were produced with the [shibudb-server-benchmarking](https://github.com/shibudb-org/shibudb-server-benchmarking) suite, a Python-based harness that ingests data over the ShibuDb wire protocol, runs concurrency-swept query workloads, verifies recall against exact ground truth, and emits per-run CSV/JSON reports and charts.

- **Dataset:** SIFT1M (128-dimensional float vectors, L2 metric). Vector suites use real SIFT base/query vectors.
- **Key-value payloads:** sequential ASCII keys of the form `key_0` … `key_999999` (5–10 bytes) with fixed 100-byte synthetic values — roughly 108 bytes per entry (~108 MB total at 1M keys).
- **Data scales:** 50,000 / 100,000 / 500,000 / 1,000,000 records per suite.
- **Queries:** 10,000 search queries per configuration, `k = 10`.
- **Concurrency:** searches/reads swept at 1, 2, 4, 8, and 16 concurrent clients; ingest (writes) always ran at concurrency 16.
- **WAL:** every suite was run twice — with the write-ahead log disabled and enabled — to quantify durability cost.
- **Recall:** measured as recall@10 against exact ground truth computed on the client.
- **Hygiene:** spaces were dropped and recreated per run, with a settle period after ingest before queries were issued. No failed operations were recorded in any run (0 errors across 760 result rows).

Latency figures below are per-operation client-observed latencies in milliseconds. "Peak QPS" is the best throughput across the concurrency sweep (the concurrency at which it occurred is shown in parentheses).

---

## 3. Key-Value Engine

### 3.1 Writes and Deletes (concurrency 16)

WAL disabled:

| Keys | PUT ops/s | PUT p50 (ms) | PUT p99 (ms) | DELETE ops/s | DELETE p50 (ms) | DELETE p99 (ms) |
|---:|---:|---:|---:|---:|---:|---:|
| 50,000 | 61,332 | 0.192 | 0.571 | 50,698 | 0.204 | 0.761 |
| 100,000 | 65,582 | 0.198 | 0.562 | 53,764 | 0.208 | 0.632 |
| 500,000 | 65,122 | 0.207 | 0.621 | 46,627 | 0.226 | 0.634 |
| 1,000,000 | 61,691 | 0.216 | 0.693 | 43,446 | 0.240 | 0.699 |

WAL enabled:

| Keys | PUT ops/s | PUT p50 (ms) | PUT p99 (ms) | DELETE ops/s | DELETE p50 (ms) | DELETE p99 (ms) |
|---:|---:|---:|---:|---:|---:|---:|
| 50,000 | 666 | 23.85 | 31.29 | 664 | 23.93 | 31.10 |
| 100,000 | 653 | 24.29 | 32.30 | 658 | 24.15 | 32.15 |
| 500,000 | 647 | 24.25 | 33.87 | 645 | 24.54 | 32.01 |
| 1,000,000 | 600 | 26.25 | 35.86 | 614 | 25.72 | 34.31 |

### 3.2 Reads (GET) — concurrency scaling, WAL off

| Keys | c=1 | c=2 | c=4 | c=8 | c=16 | p50 @ c=1 (ms) | p99 @ c=16 (ms) |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 50,000 | 12,215 | 20,401 | 24,012 | 43,879 | 57,899 | 0.069 | 0.652 |
| 100,000 | 12,393 | 21,590 | 24,924 | 44,691 | 61,894 | 0.071 | 0.604 |
| 500,000 | 12,791 | 20,981 | 23,810 | 39,497 | 63,744 | 0.071 | 0.577 |
| 1,000,000 | 12,214 | 20,346 | 23,362 | 38,744 | 62,594 | 0.073 | 0.614 |

GET performance with WAL enabled is effectively identical (e.g. 61,611 ops/s at 1M keys, c=16), since the WAL only affects the write path.

### 3.3 Key-value takeaways

- **Read and write throughput is flat across dataset size** — going from 50k to 1M keys costs almost nothing (~60k+ ops/s writes, ~62k ops/s reads at c=16). Point lookups stay at ~0.07 ms p50 single-threaded.
- **Reads scale near-linearly with concurrency** (12k → 62k ops/s from 1 to 16 clients on 16 vCPUs).
- **WAL costs ~100× on writes** (65k → ~650 ops/s, ~26 ms p50), consistent with a synchronous fsync per operation on this disk. Reads are unaffected.

---

## 4. Vector Search Engine

All vector runs use SIFT vectors (128-dim, L2), k=10, 10,000 queries. Ingest ran at concurrency 16.

### 4.1 Ingest throughput (vectors/s, WAL off / WAL on)

| Index | 50k | 100k | 500k | 1M |
|---|---:|---:|---:|---:|
| Flat | 25,725 / 640 | 25,452 / 552 | 31,063 / 591 | 27,054 / 597 |
| HNSW8 | 20,666 / 603 | 21,356 / 607 | 24,391 / 557 | 21,506 / 543 |
| HNSW16 | 20,626 / 602 | 21,823 / 604 | 23,705 / 568 | 20,869 / 536 |
| HNSW32 | 19,302 / 587 | 20,226 / 585 | 19,961 / 558 | 18,021 / 512 |
| HNSW64 | 19,064 / 519 | 19,313 / 572 | 17,497 / 516 | 16,263 / 514 |
| IVF64,Flat | 21,337 / 626 | 24,467 / 606 | 31,441 / 622 | 27,399 / 632 |
| IVF128,Flat | 21,905 / 639 | 24,360 / 575 | 30,950 / 643 | 27,845 / 638 |
| IVF256,Flat | 21,097 / 650 | 24,813 / 628 | 31,164 / 607 | 27,404 / 614 |
| IVF256,PQ8 | 21,681 / 638 | 23,985 / 635 | 30,986 / 652 | 26,218 / 617 |
| IVF256,PQ16 | 22,162 / 631 | 24,270 / 628 | 31,005 / 593 | 21,862 / 585 |
| PQ8 | 21,915 / 598 | 24,941 / 600 | 30,454 / 605 | 25,148 / 586 |
| PQ16 | 21,934 / 578 | 23,888 / 593 | 17,559 / 641 | 22,014 / 597 |

Ingest is dominated by the request path rather than the index build for most index types (~20–31k vectors/s across the board, WAL off). HNSW graph construction cost is visible at higher `M` values: HNSW64 drops to ~16k vectors/s at 1M. With WAL enabled, ingest converges to ~500–650 vectors/s for every index type — the same fsync bound seen in the KV suite.

### 4.2 Search — 1,000,000 vectors (WAL off)

| Index | Recall@10 | QPS @ c=1 | Peak QPS | p50 @ c=1 (ms) | p95 @ c=1 (ms) | p99 @ c=1 (ms) |
|---|---:|---:|---:|---:|---:|---:|
| Flat | 0.999 | 26 | 153 (c16) | 37.80 | 39.75 | 40.95 |
| HNSW8 | 0.647 | 1,610 | 4,676 (c8) | 0.566 | 0.798 | 0.983 |
| HNSW16 | 0.721 | 1,578 | 4,758 (c8) | 0.580 | 0.814 | 1.000 |
| **HNSW32** | **0.855** | **1,363** | **4,592 (c8)** | **0.671** | **0.962** | **1.126** |
| HNSW64 | 0.900 | 1,382 | 4,462 (c8) | 0.669 | 0.908 | 1.086 |
| IVF64,Flat | 0.612 | 725 | 2,928 (c8) | 1.329 | 1.835 | 2.147 |
| IVF128,Flat | 0.545 | 1,005 | 3,462 (c8) | 0.929 | 1.347 | 1.572 |
| IVF256,Flat | 0.478 | 1,369 | 4,200 (c8) | 0.673 | 0.958 | 1.149 |
| IVF256,PQ8 | 0.253 | 1,832 | 5,129 (c8) | 0.493 | 0.688 | 0.817 |
| IVF256,PQ16 | 0.354 | 1,650 | 4,833 (c8) | 0.547 | 0.789 | 0.953 |
| PQ8 | 0.304 | 166 | 927 (c16) | 5.921 | 6.648 | 7.163 |
| PQ16 | 0.540 | 157 | 1,014 (c16) | 6.208 | 7.091 | 7.635 |

### 4.3 Search — 500,000 vectors (WAL off)

| Index | Recall@10 | QPS @ c=1 | Peak QPS | p50 @ c=1 (ms) | p99 @ c=1 (ms) |
|---|---:|---:|---:|---:|---:|
| Flat | 0.999 | 56 | 292 (c16) | 17.79 | 19.89 |
| HNSW8 | 0.663 | 1,866 | 6,615 (c8) | 0.493 | 0.831 |
| HNSW16 | 0.749 | 1,907 | 6,611 (c8) | 0.485 | 0.798 |
| HNSW32 | 0.873 | 1,739 | 6,243 (c8) | 0.536 | 0.862 |
| HNSW64 | 0.914 | 1,606 | 5,855 (c8) | 0.585 | 0.934 |
| IVF64,Flat | 0.600 | 1,251 | 5,112 (c8) | 0.760 | 1.214 |
| IVF128,Flat | 0.533 | 1,487 | 5,681 (c8) | 0.633 | 0.992 |
| IVF256,Flat | 0.461 | 1,684 | 5,811 (c8) | 0.542 | 0.870 |
| IVF256,PQ8 | 0.264 | 1,971 | 6,518 (c8) | 0.469 | 0.750 |
| IVF256,PQ16 | 0.353 | 1,926 | 6,108 (c8) | 0.466 | 0.761 |
| PQ8 | 0.328 | 234 | 1,171 (c16) | 4.166 | 5.504 |
| PQ16 | 0.558 | 287 | 1,899 (c16) | 3.349 | 5.266 |

### 4.4 Search — 100,000 vectors (WAL off)

| Index | Recall@10 | QPS @ c=1 | Peak QPS | p50 @ c=1 (ms) | p99 @ c=1 (ms) |
|---|---:|---:|---:|---:|---:|
| Flat | 1.000 | 228 | 1,903 (c16) | 4.362 | 5.063 |
| HNSW8 | 0.757 | 2,035 | 8,718 (c16) | 0.456 | 0.800 |
| HNSW16 | 0.689 | 2,000 | 8,840 (c16) | 0.467 | 0.798 |
| HNSW32 | 0.920 | 1,970 | 8,807 (c16) | 0.475 | 0.776 |
| HNSW64 | 0.952 | 1,790 | 8,530 (c16) | 0.524 | 0.855 |
| IVF64,Flat | 0.562 | 1,541 | 8,180 (c16) | 0.608 | 1.019 |
| IVF128,Flat | 0.481 | 1,857 | 8,769 (c16) | 0.502 | 0.880 |
| IVF256,Flat | 0.409 | 2,040 | 9,081 (c16) | 0.457 | 0.800 |
| IVF256,PQ8 | 0.284 | 2,055 | 8,900 (c16) | 0.449 | 0.805 |
| IVF256,PQ16 | 0.345 | 2,022 | 8,988 (c16) | 0.461 | 0.777 |
| PQ8 | 0.400 | 345 | 1,663 (c16) | 2.836 | 3.828 |
| PQ16 | 0.612 | 840 | 5,383 (c16) | 1.097 | 1.932 |

### 4.5 Search — 50,000 vectors (WAL off)

| Index | Recall@10 | QPS @ c=1 | Peak QPS | p50 @ c=1 (ms) | p99 @ c=1 (ms) |
|---|---:|---:|---:|---:|---:|
| Flat | 1.000 | 432 | 3,690 (c16) | 2.279 | 2.891 |
| HNSW8 | 0.752 | 2,085 | 8,293 (c16) | 0.449 | 0.791 |
| HNSW16 | 0.823 | 2,007 | 8,144 (c16) | 0.461 | 0.852 |
| HNSW32 | 0.936 | 1,909 | 7,913 (c16) | 0.489 | 0.880 |
| HNSW64 | 0.966 | 1,617 | 6,942 (c16) | 0.581 | 1.018 |
| IVF64,Flat | 0.538 | 1,917 | 8,080 (c16) | 0.487 | 0.880 |
| IVF128,Flat | 0.464 | 2,107 | 8,166 (c16) | 0.442 | 0.817 |
| IVF256,Flat | 0.391 | 2,126 | 8,455 (c16) | 0.433 | 0.809 |
| IVF256,PQ8 | 0.291 | 2,127 | 8,312 (c16) | 0.436 | 0.808 |
| IVF256,PQ16 | 0.340 | 2,048 | 8,236 (c16) | 0.452 | 0.832 |
| PQ8 | 0.438 | 358 | 1,765 (c16) | 2.716 | 3.974 |
| PQ16 | 0.635 | 1,162 | 6,112 (c16) | 0.788 | 1.462 |

### 4.6 Vector search takeaways

- **HNSW is the best default for approximate search.** At 1M vectors, HNSW32 delivers ~0.85 recall@10 at sub-millisecond p50 latency and ~4,600 QPS peak; HNSW64 pushes recall to 0.90 for a modest throughput cost. HNSW latency grows only mildly with dataset size (0.49 ms → 0.67 ms p50 from 50k to 1M).
- **Flat gives exact results (recall ≈ 1.0) but scales linearly:** 2.3 ms p50 at 50k grows to 37.8 ms at 1M, capping peak throughput at ~150 QPS on 1M vectors. Use it only for small collections or when exact results are mandatory.
- **IVF variants trade recall for speed** and, at these settings (default nprobe), recall drops as `nlist` grows (IVF64 ≈ 0.61 vs IVF256 ≈ 0.48 at 1M). They are fast but need nprobe tuning to be competitive with HNSW on recall.
- **PQ-only indexes (PQ8/PQ16) are the slowest of the ANN options here** (~157–166 QPS single-client at 1M) with low recall (0.30–0.54); their main benefit is memory compression. IVF256,PQ8/PQ16 recover the speed (5,000+ peak QPS) but recall falls to 0.25–0.35.
- **Search throughput peaks at concurrency 8 on larger datasets** (c16 shows contention on this 16-vCPU box where the client competes with the server for cores) and at c16 on the smaller ones.
- **WAL has no measurable effect on search** (e.g. HNSW32 @ 1M: 1,363 QPS off vs 1,394 QPS on) — it only taxes the ingest path, where all index types drop to ~500–650 vectors/s.

---

## 5. Vector Search with Metadata Filtering

Filtered k-NN search over a Flat index with per-vector metadata, 3 filter scenarios of varying selectivity (fraction of the dataset matching the filter). Recall is ≥ 0.999 in all configurations, so the comparison is purely throughput/latency.

### 5.1 Ingest (vectors + metadata, concurrency 16)

| Records | Ops/s (WAL off) | Ops/s (WAL on) |
|---:|---:|---:|
| 50,000 | 25,054 | 483 |
| 100,000 | 26,713 | 468 |
| 500,000 | 24,009 | 461 |
| 1,000,000 | 24,655 | 443 |

### 5.2 Filtered search (WAL off)

| Records | Scenario (selectivity) | QPS @ c=1 | Peak QPS (c16) | p50 @ c=1 (ms) | p99 @ c=1 (ms) |
|---:|---|---:|---:|---:|---:|
| 50,000 | `category_eq` (10%) | 347.0 | 4,041 | 2.65 | 4.56 |
| 50,000 | `year_between` (20%) | 167.4 | 2,032 | 5.82 | 7.94 |
| 50,000 | `price_lt_500_and_cat_in` (15%) | 200.8 | 2,413 | 4.99 | 6.85 |
| 100,000 | `category_eq` (10%) | 149.5 | 1,865 | 6.62 | 8.66 |
| 100,000 | `year_between` (20%) | 74.1 | 871 | 13.47 | 15.81 |
| 100,000 | `price_lt_500_and_cat_in` (15%) | 93.4 | 1,020 | 10.47 | 13.17 |
| 500,000 | `category_eq` (10%) | 23.7 | 277 | 42.04 | 46.60 |
| 500,000 | `year_between` (20%) | 12.5 | 138 | 78.62 | 128.37 |
| 500,000 | `price_lt_500_and_cat_in` (15%) | 15.9 | 169 | 62.51 | 68.69 |
| 1,000,000 | `category_eq` (10%) | 10.9 | 124 | 87.86 | 136.82 |
| 1,000,000 | `year_between` (20%) | 5.8 | 62 | 169.46 | 209.98 |
| 1,000,000 | `price_lt_500_and_cat_in` (15%) | 7.3 | 82 | 136.14 | 163.79 |

### 5.3 Metadata filtering takeaways

- **Accuracy is essentially exact** (recall@10 ≥ 0.999 everywhere), as expected for filtered search on a Flat index.
- **Latency scales linearly with dataset size** — as with unfiltered Flat search, the scan dominates: `category_eq` p50 goes 2.65 ms (50k) → 87.9 ms (1M).
- **Filter complexity and selectivity matter:** the broader `year_between` (20% match) is consistently ~2× slower than `category_eq` (10% match) at every scale.
- **Concurrency scales well** (~11–12× throughput gain from c=1 to c=16), so aggregate QPS is much healthier than single-client numbers suggest.
- For large filtered workloads at 1M+ records, latency in the 100+ ms range makes an ANN-backed filtered search (or pre-partitioning by the filter attribute into separate spaces) the recommended direction.

---

## 6. Durability (WAL) Cost Summary

| Path | WAL off | WAL on | Impact |
|---|---:|---:|---|
| KV PUT (1M keys, c16) | 61,691 ops/s | 600 ops/s | ~100× slower, p50 0.22 ms → 26.2 ms |
| KV GET (1M keys, c16) | 62,594 ops/s | 61,611 ops/s | No impact |
| Vector ingest (HNSW32, 1M) | 18,021 vec/s | 512 vec/s | ~35× slower |
| Vector search (HNSW32, 1M, c=1) | 1,363 QPS | 1,394 QPS | No impact |
| Metadata ingest (1M) | 24,655 ops/s | 443 ops/s | ~55× slower |

WAL throughput (~450–660 ops/s at ~25 ms p50 regardless of engine or scale) is bounded by synchronous disk flushes on this host's disk. Read/search paths are unaffected. For bulk loads, ingest with WAL disabled (or batch commits) and enable WAL for steady-state operation.

---

## 7. Recommendations

1. **Vector search default:** HNSW32 (recall ≈ 0.85–0.94 across scales, sub-ms p50, 4,500–8,800 peak QPS). Choose HNSW64 when recall ≥ 0.9 is required at 1M scale.
2. **Exact search:** Flat is practical up to ~100k vectors (≤ ~4.4 ms p50); beyond that, expect linear degradation.
3. **Memory-constrained deployments:** IVF256,PQ8/PQ16 give the highest raw QPS but recall of 0.25–0.35 at these settings — validate recall against your workload before adopting.
4. **Key-value workloads:** throughput is scale-independent up to at least 1M keys; size concurrency to core count for near-linear read scaling.
5. **Durability:** keep WAL on for production writes but batch bulk loads or ingest with WAL off; expect ~500–650 durable writes/s on comparable disks.
6. **Sizing note:** these numbers were obtained with client and server sharing one 16-vCPU host. Peak concurrent throughput (especially the c=16 dips in vector search) should improve with a dedicated client machine.

---

## Appendix: Raw Data

Raw reports were generated by the [shibudb-server-benchmarking](https://github.com/shibudb-org/shibudb-server-benchmarking) tool at commit `22b30f0`. Each configuration produced a CSV, JSON, and log file plus charts (throughput/latency vs concurrency, recall vs QPS), named `<suite>_<index>_<num_base>_<num_queries>.*`:

- `kv_<keys>_<queries>.*` — key-value suite
- `vector_<index>_<vectors>_<queries>.*` — vector search suite (12 index types × 4 scales)
- `metadata_<records>_<queries>.*` — metadata filtering suite

All runs completed with **0 failed operations**.
