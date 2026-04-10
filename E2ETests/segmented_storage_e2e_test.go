package E2ETests

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

func TestSegmentedKeyValueE2E(t *testing.T) {
	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Fatalf("TCP connection error: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	if !Login("admin", "admin", conn, reader) {
		fmt.Println("Setup failed: Login failed")
		os.Exit(1)
	}

	space := "kv_segmented_e2e"
	CleanSpace(space, conn, reader)

	if !CreateSpaceWithSettings(models.Query{
		Type:                   models.TypeCreateSpace,
		Space:                  space,
		EngineType:             "key-value",
		EnableWAL:              true,
		SegmentRolloverBytes:   96,
		MaxSegmentsBeforeMerge: 5,
	}, conn, reader) {
		t.Fatalf("failed to create segmented key-value space")
	}

	for _, item := range []struct {
		key   string
		value string
	}{
		{"alpha", strings.Repeat("a", 128)},
		{"beta", strings.Repeat("b", 128)},
		{"gamma", strings.Repeat("c", 128)},
		{"delta", strings.Repeat("d", 128)},
	} {
		resp := sendQueryAndGetResponse(models.Query{
			Type:  models.TypePut,
			Space: space,
			Key:   item.key,
			Value: item.value,
		}, conn, reader)
		if !strings.Contains(resp, "OK") {
			t.Fatalf("PUT %s failed: %s", item.key, resp)
		}
	}

	time.Sleep(3 * time.Second)

	for _, item := range []struct {
		key   string
		value string
	}{
		{"alpha", strings.Repeat("a", 128)},
		{"beta", strings.Repeat("b", 128)},
		{"gamma", strings.Repeat("c", 128)},
		{"delta", strings.Repeat("d", 128)},
	} {
		resp := sendQueryAndGetResponse(models.Query{
			Type:  models.TypeGet,
			Space: space,
			Key:   item.key,
		}, conn, reader)
		if !strings.Contains(resp, item.value) {
			t.Fatalf("GET %s did not return expected value: %s", item.key, resp)
		}
	}

	if !UpdateSpaceSettings(space, 80, 5, conn, reader) {
		t.Fatalf("failed to update segmented key-value settings")
	}

	resp := sendQueryAndGetResponse(models.Query{
		Type:  models.TypePut,
		Space: space,
		Key:   "epsilon",
		Value: strings.Repeat("e", 128),
	}, conn, reader)
	if !strings.Contains(resp, "OK") {
		t.Fatalf("PUT epsilon failed: %s", resp)
	}

	time.Sleep(3 * time.Second)

	resp = sendQueryAndGetResponse(models.Query{
		Type:  models.TypeGet,
		Space: space,
		Key:   "epsilon",
	}, conn, reader)
	if !strings.Contains(resp, strings.Repeat("e", 128)) {
		t.Fatalf("GET epsilon did not return expected value after settings update: %s", resp)
	}
}

func TestSegmentedVectorE2E(t *testing.T) {
	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Fatalf("TCP connection error: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	if !Login("admin", "admin", conn, reader) {
		fmt.Println("Setup failed: Login failed")
		os.Exit(1)
	}

	space := "vec_segmented_e2e"
	CleanSpace(space, conn, reader)

	if !CreateSpaceWithSettings(models.Query{
		Type:                   models.TypeCreateSpace,
		Space:                  space,
		EngineType:             "vector",
		Dimension:              4,
		IndexType:              "Flat",
		Metric:                 "L2",
		EnableWAL:              true,
		SegmentRolloverBytes:   48,
		MaxSegmentsBeforeMerge: 5,
	}, conn, reader) {
		t.Fatalf("failed to create segmented vector space")
	}

	vectors := []struct {
		id  string
		vec string
	}{
		{"1001", "1,0,0,0"},
		{"1002", "0,1,0,0"},
		{"1003", "0,0,1,0"},
		{"1004", "0,0,0,1"},
	}
	for _, item := range vectors {
		resp := sendQueryAndGetResponse(models.Query{
			Type:  models.TypeInsertVector,
			Space: space,
			Key:   item.id,
			Value: item.vec,
		}, conn, reader)
		if !strings.Contains(resp, "VECTOR_INSERTED") && !strings.Contains(resp, `"status":"OK"`) {
			t.Fatalf("INSERT_VECTOR %s failed: %s", item.id, resp)
		}
	}

	time.Sleep(3 * time.Second)

	for _, item := range vectors {
		resp := sendQueryAndGetResponse(models.Query{
			Type:      models.TypeSearchTopK,
			Space:     space,
			Value:     item.vec,
			Dimension: 1,
		}, conn, reader)
		if !strings.Contains(resp, item.id) {
			t.Fatalf("SEARCH_TOPK for %s did not return expected id: %s", item.id, resp)
		}
	}

	if !UpdateSpaceSettings(space, 40, 5, conn, reader) {
		t.Fatalf("failed to update segmented vector settings")
	}

	resp := sendQueryAndGetResponse(models.Query{
		Type:  models.TypeInsertVector,
		Space: space,
		Key:   "1005",
		Value: "1,1,0,0",
	}, conn, reader)
	if !strings.Contains(resp, "VECTOR_INSERTED") && !strings.Contains(resp, `"status":"OK"`) {
		t.Fatalf("INSERT_VECTOR 1005 failed: %s", resp)
	}

	time.Sleep(3 * time.Second)

	for _, item := range append(vectors, struct {
		id  string
		vec string
	}{"1005", "1,1,0,0"}) {
		resp := sendQueryAndGetResponse(models.Query{
			Type:      models.TypeSearchTopK,
			Space:     space,
			Value:     item.vec,
			Dimension: 1,
		}, conn, reader)
		if !strings.Contains(resp, item.id) {
			t.Fatalf("SEARCH_TOPK after settings update for %s did not return expected id: %s", item.id, resp)
		}
	}
}
