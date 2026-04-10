package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const currentSegmentManifestVersion = 1

type SegmentState string

const (
	SegmentStateHot      SegmentState = "hot"
	SegmentStateSealed   SegmentState = "sealed"
	SegmentStateIndexing SegmentState = "indexing"
	SegmentStateCold     SegmentState = "cold"
	SegmentStateMerging  SegmentState = "merging"
)

type SegmentMeta struct {
	ID              int64        `json:"id"`
	State           SegmentState `json:"state"`
	DataFile        string       `json:"data_file"`
	IndexFile       string       `json:"index_file,omitempty"`
	SizeBytes       int64        `json:"size_bytes,omitempty"`
	CreatedAtUnixNs int64        `json:"created_at_unix_ns,omitempty"`
	SealedAtUnixNs  int64        `json:"sealed_at_unix_ns,omitempty"`
}

type SegmentManifest struct {
	Version         int           `json:"version"`
	NextSegmentID   int64         `json:"next_segment_id"`
	ActiveSegmentID int64         `json:"active_segment_id"`
	Segments        []SegmentMeta `json:"segments"`
}

type SegmentDescriptor struct {
	Meta          SegmentMeta
	DataPath      string
	IndexPath     string
	DataSizeBytes int64
}

type SegmentLayout struct {
	SpaceDir       string
	FilePrefix     string
	DataExtension  string
	IndexExtension string
	ManifestName   string
}

func NewSegmentLayout(spaceDir, filePrefix, dataExtension, indexExtension string) SegmentLayout {
	return SegmentLayout{
		SpaceDir:       spaceDir,
		FilePrefix:     filePrefix,
		DataExtension:  dataExtension,
		IndexExtension: indexExtension,
		ManifestName:   filePrefix + "_segments.manifest.json",
	}
}

func (l SegmentLayout) ManifestPath() string {
	return filepath.Join(l.SpaceDir, l.ManifestName)
}

func (l SegmentLayout) DataFileName(id int64) string {
	return fmt.Sprintf("%s_segment_%06d%s", l.FilePrefix, id, l.DataExtension)
}

func (l SegmentLayout) IndexFileName(id int64) string {
	return fmt.Sprintf("%s_segment_%06d%s", l.FilePrefix, id, l.IndexExtension)
}

func (l SegmentLayout) DataPath(id int64) string {
	return filepath.Join(l.SpaceDir, l.DataFileName(id))
}

func (l SegmentLayout) IndexPath(id int64) string {
	return filepath.Join(l.SpaceDir, l.IndexFileName(id))
}

func (l SegmentLayout) Descriptor(meta SegmentMeta) SegmentDescriptor {
	desc := SegmentDescriptor{
		Meta:      meta,
		DataPath:  filepath.Join(l.SpaceDir, meta.DataFile),
		IndexPath: filepath.Join(l.SpaceDir, meta.IndexFile),
	}
	if info, err := os.Stat(desc.DataPath); err == nil {
		desc.DataSizeBytes = info.Size()
	}
	return desc
}

func LoadOrCreateSegmentManifest(layout SegmentLayout) (*SegmentManifest, error) {
	if err := os.MkdirAll(layout.SpaceDir, 0755); err != nil {
		return nil, err
	}

	path := layout.ManifestPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		manifest := newSegmentManifest(layout)
		if err := WriteSegmentManifest(layout, manifest); err != nil {
			return nil, err
		}
		return manifest, nil
	}

	var manifest SegmentManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse segment manifest: %w", err)
	}
	normalizeSegmentManifest(layout, &manifest)
	return &manifest, nil
}

func WriteSegmentManifest(layout SegmentLayout, manifest *SegmentManifest) error {
	normalizeSegmentManifest(layout, manifest)

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomicFile(filepath.Join(layout.SpaceDir, layout.ManifestName), data, 0644)
}

func (m *SegmentManifest) SortedSegments() []SegmentMeta {
	return append([]SegmentMeta(nil), m.Segments...)
}

func (m *SegmentManifest) ActiveSegment() *SegmentMeta {
	for idx := range m.Segments {
		if m.Segments[idx].ID == m.ActiveSegmentID {
			return &m.Segments[idx]
		}
	}
	return nil
}

func (m *SegmentManifest) UpsertSegment(meta SegmentMeta) {
	for idx := range m.Segments {
		if m.Segments[idx].ID == meta.ID {
			m.Segments[idx] = meta
			return
		}
	}
	m.Segments = append(m.Segments, meta)
}

func (m *SegmentManifest) RemoveSegments(ids ...int64) {
	if len(ids) == 0 {
		return
	}
	remove := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		remove[id] = struct{}{}
	}
	filtered := m.Segments[:0]
	for _, segment := range m.Segments {
		if _, ok := remove[segment.ID]; ok {
			continue
		}
		filtered = append(filtered, segment)
	}
	m.Segments = filtered
}

func newSegmentManifest(layout SegmentLayout) *SegmentManifest {
	now := time.Now().UnixNano()
	first := SegmentMeta{
		ID:              1,
		State:           SegmentStateHot,
		DataFile:        layout.DataFileName(1),
		IndexFile:       layout.IndexFileName(1),
		CreatedAtUnixNs: now,
	}
	return &SegmentManifest{
		Version:         currentSegmentManifestVersion,
		NextSegmentID:   2,
		ActiveSegmentID: first.ID,
		Segments:        []SegmentMeta{first},
	}
}

func normalizeSegmentManifest(layout SegmentLayout, manifest *SegmentManifest) {
	if manifest.Version == 0 {
		manifest.Version = currentSegmentManifestVersion
	}
	if manifest.NextSegmentID <= 0 {
		manifest.NextSegmentID = 1
	}

	for idx := range manifest.Segments {
		if manifest.Segments[idx].DataFile == "" {
			manifest.Segments[idx].DataFile = layout.DataFileName(manifest.Segments[idx].ID)
		}
		if manifest.Segments[idx].IndexFile == "" {
			manifest.Segments[idx].IndexFile = layout.IndexFileName(manifest.Segments[idx].ID)
		}
		if manifest.Segments[idx].CreatedAtUnixNs == 0 {
			manifest.Segments[idx].CreatedAtUnixNs = time.Now().UnixNano()
		}
	}

	var maxID int64
	for _, segment := range manifest.Segments {
		if segment.ID > maxID {
			maxID = segment.ID
		}
	}
	if len(manifest.Segments) == 0 {
		created := newSegmentManifest(layout)
		*manifest = *created
		return
	}
	if manifest.ActiveSegmentID == 0 {
		manifest.ActiveSegmentID = manifest.Segments[len(manifest.Segments)-1].ID
	}
	if manifest.NextSegmentID <= maxID {
		manifest.NextSegmentID = maxID + 1
	}
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
