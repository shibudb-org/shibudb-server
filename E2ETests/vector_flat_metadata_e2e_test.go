package E2ETests

import (
	"bufio"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/models"
	"github.com/shibudb.org/shibudb-server/internal/storage"
)

// resultIDRe matches the per-result id token in a SEARCH_TOPK / RANGE_SEARCH
// response. The result list is embedded in the JSON response message, so the
// inner quotes may be escaped (\"id\": 7) or not ("id": 7); the optional
// backslash handles both forms.
var resultIDRe = regexp.MustCompile(`id\\?":\s*(\d+)`)

func extractResultIDs(resp string) map[int64]bool {
	ids := make(map[int64]bool)
	for _, m := range resultIDRe.FindAllStringSubmatch(resp, -1) {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			ids[n] = true
		}
	}
	return ids
}

func sortedIDs(ids map[int64]bool) []int64 {
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func assertHasIDs(t *testing.T, name, resp string, want ...int64) {
	t.Helper()
	got := extractResultIDs(resp)
	for _, w := range want {
		if !got[w] {
			t.Fatalf("%s: expected id %d in results, got %v (resp=%s)", name, w, sortedIDs(got), strings.TrimSpace(resp))
		}
	}
}

func assertMissingIDs(t *testing.T, name, resp string, notWant ...int64) {
	t.Helper()
	got := extractResultIDs(resp)
	for _, w := range notWant {
		if got[w] {
			t.Fatalf("%s: did not expect id %d in results, got %v (resp=%s)", name, w, sortedIDs(got), strings.TrimSpace(resp))
		}
	}
}

func assertExactIDs(t *testing.T, name, resp string, want ...int64) {
	t.Helper()
	got := extractResultIDs(resp)
	if len(got) != len(want) {
		t.Fatalf("%s: expected exactly %v, got %v (resp=%s)", name, want, sortedIDs(got), strings.TrimSpace(resp))
	}
	assertHasIDs(t, name, resp, want...)
}

func insertVectorWithMeta(space, id, vec string, meta map[string]any, conn net.Conn, reader *bufio.Reader) {
	q := models.Query{
		Type:     models.TypeInsertVector,
		Space:    space,
		Key:      id,
		Value:    vec,
		Metadata: meta,
	}
	SendQuery(q, conn, reader)
}

func searchTopKFiltered(space, vec string, k int, filter *storage.MetadataFilter, conn net.Conn, reader *bufio.Reader) string {
	q := models.Query{
		Type:      models.TypeSearchTopK,
		Space:     space,
		Value:     vec,
		Dimension: k,
		Filter:    filter,
	}
	return sendQueryAndGetResponse(q, conn, reader)
}

func rangeSearchFiltered(space, vec string, radius float32, filter *storage.MetadataFilter, conn net.Conn, reader *bufio.Reader) string {
	q := models.Query{
		Type:   models.TypeRangeSearch,
		Space:  space,
		Value:  vec,
		Radius: radius,
		Filter: filter,
	}
	return sendQueryAndGetResponse(q, conn, reader)
}

func dialAndLogin(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Fatalf("TCP connection error: %v", err)
	}
	reader := bufio.NewReader(conn)
	if !Login("admin", "admin", conn, reader) {
		conn.Close()
		t.Fatalf("Login failed (expected admin:admin server on :4444)")
	}
	return conn, reader
}

// productFields is the indexed metadata schema used by the filterable Flat space.
func productFields() []storage.MetadataFieldSpec {
	return []storage.MetadataFieldSpec{
		{Name: "user_id", Type: storage.MetadataTypeString},
		{Name: "category", Type: storage.MetadataTypeString},
		{Name: "price", Type: storage.MetadataTypeFloat},
		{Name: "year", Type: storage.MetadataTypeInt},
	}
}

// seedProducts creates the filterable space and inserts the standard 3-vector
// fixture used across the filter assertions.
func seedProducts(t *testing.T, space string, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	CleanSpace(space, conn, reader)
	createQ := models.Query{
		Type:                  models.TypeCreateSpace,
		Space:                 space,
		EngineType:            "vector",
		Dimension:             4,
		IndexType:             "Flat",
		Metric:                "L2",
		EnableWAL:             true,
		IndexedMetadataFields: productFields(),
	}
	if !CreateSpaceWithSettings(createQ, conn, reader) {
		t.Fatalf("failed to create filterable Flat space %q", space)
	}

	insertVectorWithMeta(space, "1", "0.1,0.1,0.1,0.1",
		map[string]any{"user_id": "alice", "category": "books", "price": 12.5, "year": 2020}, conn, reader)
	insertVectorWithMeta(space, "2", "0.2,0.2,0.2,0.2",
		map[string]any{"user_id": "bob", "category": "books", "price": 40, "year": 2022}, conn, reader)
	insertVectorWithMeta(space, "3", "0.15,0.15,0.15,0.15",
		map[string]any{"user_id": "alice", "category": "toys", "price": 5, "year": 2023}, conn, reader)

	// Small margin for any async persistence; in-memory indexes are updated synchronously.
	time.Sleep(300 * time.Millisecond)
}

func TestVectorFlatMetadataFilterE2E(t *testing.T) {
	conn, reader := dialAndLogin(t)
	defer conn.Close()

	space := "vec_flat_meta_e2e"
	seedProducts(t, space, conn, reader)
	defer CleanSpace(space, conn, reader)

	queryVec := "0.1,0.1,0.1,0.1"

	t.Run("no filter returns all", func(t *testing.T) {
		resp := searchTopKFiltered(space, queryVec, 10, nil, conn, reader)
		assertExactIDs(t, "no-filter", resp, 1, 2, 3)
	})

	t.Run("eq on string field", func(t *testing.T) {
		filter := &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"}
		resp := searchTopKFiltered(space, queryVec, 10, filter, conn, reader)
		assertExactIDs(t, "user_id=alice", resp, 1, 3)
		assertMissingIDs(t, "user_id=alice", resp, 2)
	})

	t.Run("and with numeric comparison", func(t *testing.T) {
		filter := &storage.MetadataFilter{Op: storage.FilterOpAnd, Filters: []*storage.MetadataFilter{
			{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"},
			{Op: storage.FilterOpLt, Field: "price", Value: 10},
		}}
		resp := searchTopKFiltered(space, queryVec, 10, filter, conn, reader)
		assertExactIDs(t, "alice AND price<10", resp, 3)
	})

	t.Run("nested or within and", func(t *testing.T) {
		filter := &storage.MetadataFilter{Op: storage.FilterOpAnd, Filters: []*storage.MetadataFilter{
			{Op: storage.FilterOpOr, Filters: []*storage.MetadataFilter{
				{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"},
				{Op: storage.FilterOpEq, Field: "user_id", Value: "bob"},
			}},
			{Op: storage.FilterOpEq, Field: "category", Value: "books"},
		}}
		resp := searchTopKFiltered(space, queryVec, 10, filter, conn, reader)
		assertExactIDs(t, "(alice OR bob) AND books", resp, 1, 2)
		assertMissingIDs(t, "(alice OR bob) AND books", resp, 3)
	})

	t.Run("between on int field", func(t *testing.T) {
		filter := &storage.MetadataFilter{Op: storage.FilterOpBetween, Field: "year", Values: []any{2021, 2023}}
		resp := searchTopKFiltered(space, queryVec, 10, filter, conn, reader)
		assertExactIDs(t, "year BETWEEN 2021 AND 2023", resp, 2, 3)
		assertMissingIDs(t, "year BETWEEN 2021 AND 2023", resp, 1)
	})

	t.Run("in with not", func(t *testing.T) {
		filter := &storage.MetadataFilter{Op: storage.FilterOpAnd, Filters: []*storage.MetadataFilter{
			{Op: storage.FilterOpIn, Field: "category", Values: []any{"books", "toys"}},
			{Op: storage.FilterOpNot, Filters: []*storage.MetadataFilter{
				{Op: storage.FilterOpEq, Field: "user_id", Value: "bob"},
			}},
		}}
		resp := searchTopKFiltered(space, queryVec, 10, filter, conn, reader)
		assertExactIDs(t, "category IN (books,toys) AND NOT bob", resp, 1, 3)
	})

	t.Run("range search with filter", func(t *testing.T) {
		filter := &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"}
		resp := rangeSearchFiltered(space, queryVec, 1.0, filter, conn, reader)
		assertHasIDs(t, "range user_id=alice", resp, 1, 3)
		assertMissingIDs(t, "range user_id=alice", resp, 2)
	})
}

func TestVectorFlatMetadataUpsertE2E(t *testing.T) {
	conn, reader := dialAndLogin(t)
	defer conn.Close()

	space := "vec_flat_meta_upsert_e2e"
	CleanSpace(space, conn, reader)
	defer CleanSpace(space, conn, reader)

	createQ := models.Query{
		Type:                  models.TypeCreateSpace,
		Space:                 space,
		EngineType:            "vector",
		Dimension:             4,
		IndexType:             "Flat",
		Metric:                "L2",
		EnableWAL:             true,
		IndexedMetadataFields: productFields(),
	}
	if !CreateSpaceWithSettings(createQ, conn, reader) {
		t.Fatalf("failed to create filterable Flat space %q", space)
	}

	queryVec := "0.1,0.1,0.1,0.1"
	insertVectorWithMeta(space, "1", queryVec,
		map[string]any{"user_id": "alice", "category": "books", "price": 12.5, "year": 2020}, conn, reader)
	time.Sleep(200 * time.Millisecond)

	aliceFilter := &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "alice"}
	resp := searchTopKFiltered(space, queryVec, 10, aliceFilter, conn, reader)
	assertHasIDs(t, "before upsert user_id=alice", resp, 1)

	// Re-insert the same id with different metadata; this should replace metadata.
	insertVectorWithMeta(space, "1", queryVec,
		map[string]any{"user_id": "bob", "category": "games", "price": 99, "year": 2024}, conn, reader)
	time.Sleep(200 * time.Millisecond)

	resp = searchTopKFiltered(space, queryVec, 10, aliceFilter, conn, reader)
	assertMissingIDs(t, "after upsert user_id=alice", resp, 1)

	bobFilter := &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "user_id", Value: "bob"}
	resp = searchTopKFiltered(space, queryVec, 10, bobFilter, conn, reader)
	assertHasIDs(t, "after upsert user_id=bob", resp, 1)
}

func TestVectorFlatMetadataErrorsE2E(t *testing.T) {
	conn, reader := dialAndLogin(t)
	defer conn.Close()

	t.Run("filter on undeclared field is rejected", func(t *testing.T) {
		space := "vec_flat_meta_err_field_e2e"
		seedProducts(t, space, conn, reader)
		defer CleanSpace(space, conn, reader)

		filter := &storage.MetadataFilter{Op: storage.FilterOpEq, Field: "region", Value: "us"}
		resp := searchTopKFiltered(space, "0.1,0.1,0.1,0.1", 10, filter, conn, reader)
		if !strings.Contains(resp, "ERROR") {
			t.Fatalf("expected ERROR for undeclared filter field, got: %s", strings.TrimSpace(resp))
		}
	})

	t.Run("range op on string value is rejected", func(t *testing.T) {
		space := "vec_flat_meta_err_type_e2e"
		seedProducts(t, space, conn, reader)
		defer CleanSpace(space, conn, reader)

		filter := &storage.MetadataFilter{Op: storage.FilterOpGt, Field: "price", Value: "cheap"}
		resp := searchTopKFiltered(space, "0.1,0.1,0.1,0.1", 10, filter, conn, reader)
		if !strings.Contains(resp, "ERROR") {
			t.Fatalf("expected ERROR for non-numeric range value, got: %s", strings.TrimSpace(resp))
		}
	})

	t.Run("metadata on a non-filterable space is rejected", func(t *testing.T) {
		space := "vec_flat_plain_e2e"
		CleanSpace(space, conn, reader)
		defer CleanSpace(space, conn, reader)
		// Plain Flat space WITHOUT indexed metadata fields -> not filterable.
		if !CreateVectorSpace(space, 4, "Flat", "L2", conn, reader) {
			t.Fatalf("failed to create plain Flat space %q", space)
		}
		q := models.Query{
			Type:     models.TypeInsertVector,
			Space:    space,
			Key:      "1",
			Value:    "0.1,0.1,0.1,0.1",
			Metadata: map[string]any{"user_id": "alice"},
		}
		resp := sendQueryAndGetResponse(q, conn, reader)
		if !strings.Contains(resp, "ERROR") {
			t.Fatalf("expected ERROR inserting metadata on non-filterable space, got: %s", strings.TrimSpace(resp))
		}
	})

	t.Run("indexed metadata fields on non-Flat index is rejected", func(t *testing.T) {
		space := "vec_hnsw_meta_e2e"
		CleanSpace(space, conn, reader)
		defer CleanSpace(space, conn, reader)
		createQ := models.Query{
			Type:                  models.TypeCreateSpace,
			Space:                 space,
			EngineType:            "vector",
			Dimension:             4,
			IndexType:             "HNSW32",
			Metric:                "L2",
			IndexedMetadataFields: productFields(),
		}
		resp := sendQueryAndGetResponse(createQ, conn, reader)
		if !strings.Contains(resp, "ERROR") {
			t.Fatalf("expected ERROR creating non-Flat space with metadata fields, got: %s", strings.TrimSpace(resp))
		}
	})
}
