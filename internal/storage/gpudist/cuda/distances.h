#ifndef SHIBUDB_GPUDIST_DISTANCES_H
#define SHIBUDB_GPUDIST_DISTANCES_H

#ifdef __cplusplus
extern "C" {
#endif

/* Returns 1 if at least one CUDA device is usable. */
int shibudb_gpudist_available(void);

/*
 * Copies the last CUDA/library diagnostic message into buf (NUL-terminated).
 * Returns bytes written (excluding NUL), or -1 if buf is NULL / buflen < 1.
 */
int shibudb_gpudist_last_error(char* buf, int buflen);

/*
 * Compute distances between query[dim] and each of n row-major vectors in
 * matrix[n * dim]. Writes n float distances into out.
 *
 * metric values match faiss MetricType:
 *   0 InnerProduct, 1 L2 (squared), 2 L1, 3 Linf, 4 Lp (p=2),
 *   20 Canberra, 21 BrayCurtis, 22 JensenShannon
 *
 * Returns 0 on success, non-zero on failure.
 */
int shibudb_gpudist_batch(
    int metric,
    const float* query,
    const float* matrix,
    int n,
    int dim,
    float* out);

#ifdef __cplusplus
}
#endif

#endif /* SHIBUDB_GPUDIST_DISTANCES_H */
