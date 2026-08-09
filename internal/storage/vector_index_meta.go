package storage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	vectorIndexMetaVersion = 1
	vectorIndexMetaSuffix  = ".meta.json"
)

type VectorIndexMode string

const (
	VectorIndexModeFallback VectorIndexMode = "fallback"
	VectorIndexModeTrained  VectorIndexMode = "trained"
)

// VectorIndexMeta records the durable FAISS snapshot state for the single-file
// vector engine. It is deliberately separate from SegmentManifest, which is
// only used by segmented KV and FlatMeta storage.
type VectorIndexMeta struct {
	Version        int             `json:"version"`
	Mode           VectorIndexMode `json:"mode"`
	IndexDataBytes int64           `json:"index_data_bytes"`
	IndexType      string          `json:"index_type"`
	Dimension      int             `json:"dimension"`
	Metric         int             `json:"metric"`
	IndexSHA256    string          `json:"index_sha256,omitempty"`
	DataSHA256     string          `json:"data_sha256,omitempty"`
}

func vectorIndexMetaPath(indexPath string) string {
	return indexPath + vectorIndexMetaSuffix
}

func loadVectorIndexMeta(path string) (VectorIndexMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return VectorIndexMeta{
				Version: vectorIndexMetaVersion,
				Mode:    VectorIndexModeFallback,
			}, nil
		}
		return VectorIndexMeta{}, err
	}
	var meta VectorIndexMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return VectorIndexMeta{}, fmt.Errorf("parse vector index metadata: %w", err)
	}
	if meta.Version == 0 {
		meta.Version = vectorIndexMetaVersion
	}
	if meta.Version != vectorIndexMetaVersion {
		return VectorIndexMeta{}, fmt.Errorf("unsupported vector index metadata version %d", meta.Version)
	}
	if meta.Mode == "" {
		meta.Mode = VectorIndexModeFallback
	}
	if meta.Mode != VectorIndexModeFallback && meta.Mode != VectorIndexModeTrained {
		return VectorIndexMeta{}, fmt.Errorf("invalid vector index mode %q", meta.Mode)
	}
	if meta.IndexDataBytes < 0 {
		return VectorIndexMeta{}, fmt.Errorf("invalid vector index watermark %d", meta.IndexDataBytes)
	}
	return meta, nil
}

func writeVectorIndexMeta(path string, meta VectorIndexMeta) error {
	meta.Version = vectorIndexMetaVersion
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomicFile(path, data, 0644)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func filePrefixSHA256(path string, size int64) (string, error) {
	if size < 0 {
		return "", fmt.Errorf("invalid checksum size %d", size)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyN(hash, file, size); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
