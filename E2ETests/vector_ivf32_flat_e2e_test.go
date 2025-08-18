package E2ETests

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Podcopic-Labs/ShibuDb/internal/models"
)

func TestVectorIVF32FlatE2E(t *testing.T) {
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

	space := "vec_ivf32_flat_e2e"
	dim := 4
	indexType := "IVF32,Flat"

	CleanSpace(space, conn, reader)
	ok := CreateVectorSpace(space, dim, indexType, "L2", conn, reader)
	if !ok {
		t.Fatalf("Failed to create vector space: %s", space)
	}

	// Insert 100 vectors
	for i := 0; i < 100; i++ {
		vec := []float32{float32(i), float32(i + 1), float32(i + 2), float32(i + 3)}
		id := int64(1000 + i)
		vecStr := formatVec(vec)
		q := models.Query{Type: models.TypeInsertVector, Space: space, Key: formatID(id), Value: vecStr}
		SendQuery(q, conn, reader)
	}

	// wait for autoflush data
	time.Sleep(2000 * time.Millisecond)

	// Search for [50,51,52,53] (should match id 1050)
	searchVec := "50,51,52,53"
	q := models.Query{Type: models.TypeSearchTopK, Space: space, Value: searchVec, Dimension: 1}
	resp := sendQueryAndGetResponse(q, conn, reader)
	expectedID := "1050"
	if !strings.Contains(resp, expectedID) {
		t.Fatalf("Expected top-1 result to be %s, got: %s", expectedID, resp)
	}
}
