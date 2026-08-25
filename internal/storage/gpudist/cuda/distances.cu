#include "distances.h"

#include <cuda_runtime.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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

static char g_last_error[512] = "";

static void set_last_error(const char* msg) {
    if (!msg) {
        g_last_error[0] = '\0';
        return;
    }
    snprintf(g_last_error, sizeof(g_last_error), "%s", msg);
}

static void set_cuda_error(const char* what, cudaError_t err) {
    snprintf(
        g_last_error,
        sizeof(g_last_error),
        "%s: %s (%d)",
        what,
        cudaGetErrorString(err),
        (int)err);
}

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

extern "C" int shibudb_gpudist_last_error(char* buf, int buflen) {
    if (!buf || buflen < 1) {
        return -1;
    }
    snprintf(buf, (size_t)buflen, "%s", g_last_error);
    return (int)strlen(buf);
}

extern "C" int shibudb_gpudist_available(void) {
    set_last_error("");
    int count = 0;
    cudaError_t err = cudaGetDeviceCount(&count);
    if (err != cudaSuccess) {
        set_cuda_error("cudaGetDeviceCount", err);
        return 0;
    }
    if (count <= 0) {
        set_last_error("cudaGetDeviceCount returned 0 devices");
        return 0;
    }
    err = cudaSetDevice(0);
    if (err != cudaSuccess) {
        set_cuda_error("cudaSetDevice(0)", err);
        return 0;
    }
    /* Touch the runtime to surface insufficient-driver / bad-cudart early. */
    err = cudaFree(0);
    if (err != cudaSuccess) {
        set_cuda_error("cudaFree(0)", err);
        return 0;
    }
    return 1;
}

extern "C" int shibudb_gpudist_batch(
    int metric,
    const float* query,
    const float* matrix,
    int n,
    int dim,
    float* out) {
    set_last_error("");
    if (!query || !matrix || !out || n <= 0 || dim <= 0) {
        set_last_error("invalid arguments to shibudb_gpudist_batch");
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
        set_cuda_error("cudaMalloc(query)", err);
        return 2;
    }
    err = cudaMalloc((void**)&d_matrix, m_bytes);
    if (err != cudaSuccess) {
        set_cuda_error("cudaMalloc(matrix)", err);
        cudaFree(d_query);
        return 2;
    }
    err = cudaMalloc((void**)&d_out, o_bytes);
    if (err != cudaSuccess) {
        set_cuda_error("cudaMalloc(out)", err);
        cudaFree(d_query);
        cudaFree(d_matrix);
        return 2;
    }

    int rc = 0;
    err = cudaMemcpy(d_query, query, q_bytes, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        set_cuda_error("cudaMemcpy(query H2D)", err);
        rc = 3;
        goto cleanup;
    }
    err = cudaMemcpy(d_matrix, matrix, m_bytes, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        set_cuda_error("cudaMemcpy(matrix H2D)", err);
        rc = 3;
        goto cleanup;
    }

    {
        int threads = 256;
        int blocks = (n + threads - 1) / threads;
        batch_distances_kernel<<<blocks, threads>>>(metric, d_query, d_matrix, n, dim, d_out);
        err = cudaGetLastError();
        if (err != cudaSuccess) {
            set_cuda_error("batch_distances_kernel launch", err);
            rc = 4;
            goto cleanup;
        }
        err = cudaDeviceSynchronize();
        if (err != cudaSuccess) {
            set_cuda_error("cudaDeviceSynchronize", err);
            rc = 4;
            goto cleanup;
        }
    }

    err = cudaMemcpy(out, d_out, o_bytes, cudaMemcpyDeviceToHost);
    if (err != cudaSuccess) {
        set_cuda_error("cudaMemcpy(out D2H)", err);
        rc = 5;
    }

cleanup:
    cudaFree(d_query);
    cudaFree(d_matrix);
    cudaFree(d_out);
    return rc;
}
