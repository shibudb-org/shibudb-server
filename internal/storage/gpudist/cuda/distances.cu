#include "distances.h"

#include <cuda_runtime.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>

enum {
    METRIC_INNER_PRODUCT = 0,
    METRIC_L2 = 1,
    METRIC_L1 = 2,
    METRIC_LINF = 3,
    METRIC_LP = 4,
    METRIC_CANBERRA = 20,
    METRIC_BRAYCURTIS = 21,
    METRIC_JENSENSHANNON = 22
};

__device__ inline float dist_one(
    int metric,
    const float* __restrict__ query,
    const float* __restrict__ vec,
    int dim) {
    switch (metric) {
    case METRIC_INNER_PRODUCT: {
        float s = 0.f;
        for (int i = 0; i < dim; ++i) {
            s += query[i] * vec[i];
        }
        return s;
    }
    case METRIC_L1: {
        float s = 0.f;
        for (int i = 0; i < dim; ++i) {
            s += fabsf(query[i] - vec[i]);
        }
        return s;
    }
    case METRIC_LINF: {
        float m = 0.f;
        for (int i = 0; i < dim; ++i) {
            float d = fabsf(query[i] - vec[i]);
            if (d > m) {
                m = d;
            }
        }
        return m;
    }
    case METRIC_LP: {
        /* Matches FlatMeta CPU path: p=2 without root. */
        float s = 0.f;
        for (int i = 0; i < dim; ++i) {
            float d = fabsf(query[i] - vec[i]);
            s += d * d;
        }
        return s;
    }
    case METRIC_CANBERRA: {
        float s = 0.f;
        for (int i = 0; i < dim; ++i) {
            float num = fabsf(query[i] - vec[i]);
            float den = fabsf(query[i]) + fabsf(vec[i]);
            if (den != 0.f) {
                s += num / den;
            }
        }
        return s;
    }
    case METRIC_BRAYCURTIS: {
        float num = 0.f;
        float den = 0.f;
        for (int i = 0; i < dim; ++i) {
            num += fabsf(query[i] - vec[i]);
            den += fabsf(query[i] + vec[i]);
        }
        if (den == 0.f) {
            return 0.f;
        }
        return num / den;
    }
    case METRIC_JENSENSHANNON: {
        float js = 0.f;
        for (int i = 0; i < dim; ++i) {
            float ai = query[i];
            float bi = vec[i];
            float mi = 0.5f * (ai + bi);
            if (ai > 0.f && mi > 0.f) {
                js += ai * logf(ai / mi);
            }
            if (bi > 0.f && mi > 0.f) {
                js += bi * logf(bi / mi);
            }
        }
        return 0.5f * js;
    }
    case METRIC_L2:
    default: {
        float s = 0.f;
        for (int i = 0; i < dim; ++i) {
            float d = query[i] - vec[i];
            s += d * d;
        }
        return s;
    }
    }
}

__global__ void batch_distances_kernel(
    int metric,
    const float* __restrict__ query,
    const float* __restrict__ matrix,
    int n,
    int dim,
    float* __restrict__ out) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= n) {
        return;
    }
    out[idx] = dist_one(metric, query, matrix + (size_t)idx * (size_t)dim, dim);
}

extern "C" int shibudb_gpudist_available(void) {
    int count = 0;
    cudaError_t err = cudaGetDeviceCount(&count);
    if (err != cudaSuccess || count <= 0) {
        return 0;
    }
    err = cudaSetDevice(0);
    return err == cudaSuccess ? 1 : 0;
}

extern "C" int shibudb_gpudist_batch(
    int metric,
    const float* query,
    const float* matrix,
    int n,
    int dim,
    float* out) {
    if (!query || !matrix || !out || n <= 0 || dim <= 0) {
        return 1;
    }

    size_t q_bytes = (size_t)dim * sizeof(float);
    size_t m_bytes = (size_t)n * (size_t)dim * sizeof(float);
    size_t o_bytes = (size_t)n * sizeof(float);

    float* d_query = nullptr;
    float* d_matrix = nullptr;
    float* d_out = nullptr;

    cudaError_t err;
    err = cudaMalloc((void**)&d_query, q_bytes);
    if (err != cudaSuccess) {
        return 2;
    }
    err = cudaMalloc((void**)&d_matrix, m_bytes);
    if (err != cudaSuccess) {
        cudaFree(d_query);
        return 2;
    }
    err = cudaMalloc((void**)&d_out, o_bytes);
    if (err != cudaSuccess) {
        cudaFree(d_query);
        cudaFree(d_matrix);
        return 2;
    }

    int rc = 0;
    err = cudaMemcpy(d_query, query, q_bytes, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        rc = 3;
        goto cleanup;
    }
    err = cudaMemcpy(d_matrix, matrix, m_bytes, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        rc = 3;
        goto cleanup;
    }

    {
        int threads = 256;
        int blocks = (n + threads - 1) / threads;
        batch_distances_kernel<<<blocks, threads>>>(metric, d_query, d_matrix, n, dim, d_out);
        err = cudaGetLastError();
        if (err != cudaSuccess) {
            rc = 4;
            goto cleanup;
        }
        err = cudaDeviceSynchronize();
        if (err != cudaSuccess) {
            rc = 4;
            goto cleanup;
        }
    }

    err = cudaMemcpy(out, d_out, o_bytes, cudaMemcpyDeviceToHost);
    if (err != cudaSuccess) {
        rc = 5;
    }

cleanup:
    cudaFree(d_query);
    cudaFree(d_matrix);
    cudaFree(d_out);
    return rc;
}
