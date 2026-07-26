package E2ETests

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

func TestHashMapIndexE2E(t *testing.T) {
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

	space := "hashmap_e2e_test"
	CleanSpace(space, conn, reader)

	if !CreateSpaceWithSettings(models.Query{
		Type:       models.TypeCreateSpace,
		Space:      space,
		EngineType: "key-value",
		IndexType:  "hashmap",
		EnableWAL:  false,
	}, conn, reader) {
		t.Fatalf("failed to create key-value space with hashmap index")
	}

	// Insert data
	resp := sendQueryAndGetResponse(models.Query{
		Type:  models.TypePut,
		Space: space,
		Key:   "hello",
		Value: "world",
	}, conn, reader)
	if !strings.Contains(resp, "OK") {
		t.Fatalf("PUT failed: %s", resp)
	}

	// Retrieve data
	resp = sendQueryAndGetResponse(models.Query{
		Type:  models.TypeGet,
		Space: space,
		Key:   "hello",
	}, conn, reader)
	if !strings.Contains(resp, "world") {
		t.Fatalf("GET did not return expected value: %s", resp)
	}

	// Delete data
	resp = sendQueryAndGetResponse(models.Query{
		Type:  models.TypeDelete,
		Space: space,
		Key:   "hello",
	}, conn, reader)
	if !strings.Contains(resp, "DELETED") {
		t.Fatalf("DELETE failed: %s", resp)
	}
	
	// Verify delete
	resp = sendQueryAndGetResponse(models.Query{
		Type:  models.TypeGet,
		Space: space,
		Key:   "hello",
	}, conn, reader)
	if !strings.Contains(resp, "key is deleted") {
		t.Fatalf("GET after DELETE did not return key is deleted: %s", resp)
	}

	CleanSpace(space, conn, reader)
}
