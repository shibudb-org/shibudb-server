package queryengine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/models"
	"github.com/shibudb.org/shibudb-server/internal/spaces"
	"github.com/shibudb.org/shibudb-server/internal/storage"
)

func TestQueryEngine_FilterableFlatSpace(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()
	qe := NewQueryEngine(sm, &mockAuth{})

	space := "docs"
	if _, err := qe.Execute(models.Query{
		Type:       models.TypeCreateSpace,
		Space:      space,
		EngineType: "vector",
		Dimension:  2,
		IndexType:  "Flat",
		Metric:     "L2",
		User:       "admin",
		EnableWAL:  true,
		IndexedMetadataFields: []storage.MetadataFieldSpec{
			{Name: "user_id", Type: storage.MetadataTypeString},
			{Name: "age", Type: storage.MetadataTypeInt},
		},
	}); err != nil {
		t.Fatalf("create space: %v", err)
	}

	insert := func(id, user string, age float64, vec string) {
		if _, err := qe.Execute(models.Query{
			Type:     models.TypeInsertVector,
			Space:    space,
			Key:      id,
			Value:    vec,
			Metadata: map[string]any{"user_id": user, "age": age},
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("1", "alice", 30, "0,0")
	insert("2", "bob", 40, "0.1,0")
	insert("3", "alice", 50, "0.2,0")

	t.Run("eq filter", func(t *testing.T) {
		res, err := qe.Execute(models.Query{
			Type:      models.TypeSearchTopK,
			Space:     space,
			Value:     "0,0",
			Dimension: 10, // k is taken from Dimension by the query engine
			Filter:    &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"},
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if !strings.Contains(res, `"id": 1`) || !strings.Contains(res, `"id": 3`) {
			t.Fatalf("expected ids 1 and 3, got %s", res)
		}
		if strings.Contains(res, `"id": 2`) {
			t.Fatalf("did not expect id 2 (bob), got %s", res)
		}
	})

	t.Run("and filter", func(t *testing.T) {
		res, err := qe.Execute(models.Query{
			Type:      models.TypeSearchTopK,
			Space:     space,
			Value:     "0,0",
			Dimension: 10,
			Filter: &storage.MetadataFilter{Op: storage.FilterOpAnd, Filters: []*storage.MetadataFilter{
				{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"},
				{Op: storage.FilterOpGt, Field: "age", Value: 40.0},
			}},
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if !strings.Contains(res, `"id": 3`) || strings.Contains(res, `"id": 1`) {
			t.Fatalf("expected only id 3, got %s", res)
		}
	})

	t.Run("unknown field errors", func(t *testing.T) {
		if _, err := qe.Execute(models.Query{
			Type:      models.TypeSearchTopK,
			Space:     space,
			Value:     "0,0",
			Dimension: 10,
			Filter:    &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "nope", Value: "x"},
		}); err == nil {
			t.Fatalf("expected error for unknown filter field")
		}
	})
}

func TestQueryEngine_MetadataFieldsRequireFlat(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()
	qe := NewQueryEngine(sm, &mockAuth{})

	_, err := qe.Execute(models.Query{
		Type:                  models.TypeCreateSpace,
		Space:                 "bad",
		EngineType:            "vector",
		Dimension:             4,
		IndexType:             "HNSW32",
		Metric:                "L2",
		User:                  "admin",
		IndexedMetadataFields: []storage.MetadataFieldSpec{{Name: "user_id", Type: storage.MetadataTypeString}},
	})
	if err == nil {
		t.Fatalf("expected error: indexed metadata fields only allowed for the Flat index type")
	}
}

func TestQueryEngine_FilterOnPlainVectorSpaceErrors(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()
	qe := NewQueryEngine(sm, &mockAuth{})

	space := "plain"
	if _, err := qe.Execute(models.Query{
		Type: models.TypeCreateSpace, Space: space, EngineType: "vector",
		Dimension: 2, IndexType: "Flat", Metric: "L2", User: "admin",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := qe.Execute(models.Query{
		Type: models.TypeInsertVector, Space: space, Key: "1", Value: "0,0",
		Metadata: map[string]any{"user_id": "alice"},
	}); err == nil {
		t.Fatalf("expected error inserting metadata into a plain vector space")
	}
	if _, err := qe.Execute(models.Query{
		Type: models.TypeSearchTopK, Space: space, Value: "0,0", Dimension: 1,
		Filter: &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"},
	}); err == nil {
		t.Fatalf("expected error filtering on a plain vector space")
	}
}

func TestQueryEngine_TurboQuantFlatSpace(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()
	qe := NewQueryEngine(sm, &mockAuth{})

	space := "tq"
	if _, err := qe.Execute(models.Query{
		Type:        models.TypeCreateSpace,
		Space:       space,
		EngineType:  "vector",
		Dimension:   8,
		IndexType:   "Flat",
		Metric:      "L2",
		User:        "admin",
		Compression: storage.VectorCompressionTurboQuant4Bits,
		IndexedMetadataFields: []storage.MetadataFieldSpec{
			{Name: "user_id", Type: storage.MetadataTypeString},
		},
	}); err != nil {
		t.Fatalf("create compressed space: %v", err)
	}

	if _, err := qe.Execute(models.Query{
		Type:     models.TypeInsertVector,
		Space:    space,
		Key:      "1",
		Value:    "1,0,0,0,0,0,0,0",
		Metadata: map[string]any{"user_id": "alice"},
	}); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := qe.Execute(models.Query{
		Type:     models.TypeInsertVector,
		Space:    space,
		Key:      "2",
		Value:    "0,1,0,0,0,0,0,0",
		Metadata: map[string]any{"user_id": "bob"},
	}); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	res, err := qe.Execute(models.Query{
		Type:      models.TypeSearchTopK,
		Space:     space,
		Value:     "1,0,0,0,0,0,0,0",
		Dimension: 1,
		Filter:    &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(res, `"id": 1`) {
		t.Fatalf("expected id 1, got %s", res)
	}
	if strings.Contains(res, `"id": 2`) {
		t.Fatalf("did not expect id 2, got %s", res)
	}

	if _, err := qe.Execute(models.Query{
		Type: models.TypeCreateSpace, Space: "bad_comp", EngineType: "vector",
		Dimension: 8, IndexType: "Flat", Metric: "L2", User: "admin",
		Compression: storage.VectorCompressionTurboQuant4Bits,
	}); err == nil {
		t.Fatal("expected error creating compressed space without metadata fields")
	}
}

func TestQueryEngine_CompressionRejectedOnNonFlatMeta(t *testing.T) {
	dir := t.TempDir()
	sm := spaces.NewSpaceManager(dir)
	defer sm.CloseAll()
	qe := NewQueryEngine(sm, &mockAuth{})

	fields := []storage.MetadataFieldSpec{{Name: "user_id", Type: storage.MetadataTypeString}}
	cases := []struct {
		name      string
		indexType string
		fields    []storage.MetadataFieldSpec
		wantSub   string
	}{
		{"plain flat", "Flat", nil, "indexed metadata fields"},
		{"hnsw32", "HNSW32", nil, `got index type "HNSW32"`},
		{"ivf32 flat", "IVF32,Flat", nil, `got index type "IVF32,Flat"`},
		{"pq8", "PQ8", nil, `got index type "PQ8"`},
		{"hnsw32 with metadata", "HNSW32", fields, `got index type "HNSW32"`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := qe.Execute(models.Query{
				Type:                  models.TypeCreateSpace,
				Space:                 fmt.Sprintf("rej_%d", i),
				EngineType:            "vector",
				Dimension:             8,
				IndexType:             tc.indexType,
				Metric:                "L2",
				User:                  "admin",
				IndexedMetadataFields: tc.fields,
				Compression:           storage.VectorCompressionTurboQuant4Bits,
			})
			if err == nil {
				t.Fatalf("expected compression error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), "compression is only supported for Flat spaces declared with indexed metadata fields") {
				t.Fatalf("got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("got %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}
