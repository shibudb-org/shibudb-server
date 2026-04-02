package benchmark

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

const (
	totalVectorSpaces = 5
	vectorDimension   = 128
)

func TestVectorMultiSpace(t *testing.T) {
	runVectorMultiSpace(t, true)
}

func TestVectorMultiSpaceNoWAL(t *testing.T) {
	runVectorMultiSpace(t, false)
}

func runVectorMultiSpace(t *testing.T, enableWAL bool) {
	fmt.Println("Starting vector engine multi-space concurrency test...")
	runID := time.Now().UnixNano()

	// Step 1: Create vector spaces
	for i := 0; i < totalVectorSpaces; i++ {
		space := fmt.Sprintf("vector_space_%d_%d", i, runID)
		if err := createVectorSpace(space, enableWAL); err != nil {
			t.Fatalf("Failed to create vector space %s: %v", space, err)
		}
	}

	type metrics struct {
		insertOps      int
		searchOps      int
		getOps         int
		insertTime     float64
		searchTime     float64
		getTime        float64
		failures       int
		insertFailures int
		searchFailures int
		getFailures    int
	}

	var wg sync.WaitGroup
	metricsCh := make(chan metrics, totalClients)

	startWall := time.Now()

	for i := 0; i < totalClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			space := fmt.Sprintf("vector_space_%d_%d", clientID%totalVectorSpaces, runID)

			conn, err := net.Dial("tcp", serverAddr)
			if err != nil {
				fmt.Printf("Client %d: Connection error: %v\n", clientID, err)
				metricsCh <- metrics{}
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)

			login(conn, reader)

			localExpected := make(map[string]string)
			var m metrics

			// INSERT Phase
			insertStart := time.Now()
			for j := 0; j < firstPhaseOps; j++ {
				vectorID := fmt.Sprintf("%d", clientID*1000+j)
				vectorData := generateRandomVector(vectorDimension)
				if _, err := sendCheckedVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: space}, conn, reader); err == nil {
					m.insertOps++
					localExpected[vectorID] = vectorData
				} else {
					m.failures++
					m.insertFailures++
				}
			}
			time.Sleep(sleepBetweenPhases)
			for j := 0; j < secondPhaseOps; j++ {
				vectorID := fmt.Sprintf("%d", clientID*1000+j+firstPhaseOps)
				vectorData := generateRandomVector(vectorDimension)
				if _, err := sendCheckedVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: space}, conn, reader); err == nil {
					m.insertOps++
					localExpected[vectorID] = vectorData
				} else {
					m.failures++
					m.insertFailures++
				}
			}
			m.insertTime = time.Since(insertStart).Seconds()

			// SEARCH Phase
			searchStart := time.Now()
			for j := 0; j < 5; j++ {
				queryVector := generateRandomVector(vectorDimension)
				if _, err := sendCheckedVectorQuery(models.Query{Type: "SEARCH_TOPK", Value: queryVector, Space: space, Dimension: 10}, conn, reader); err == nil {
					m.searchOps++
				} else {
					m.failures++
					m.searchFailures++
				}
			}
			m.searchTime = time.Since(searchStart).Seconds()

			// GET Phase
			getStart := time.Now()
			for vectorID := range localExpected {
				if _, err := getVectorEventually(models.Query{Type: "GET_VECTOR", Key: vectorID, Space: space}, conn, reader); err != nil {
					m.failures++
					m.getFailures++
				} else {
					m.getOps++
				}
			}
			m.getTime = time.Since(getStart).Seconds()

			metricsCh <- m
		}(i)
	}

	wg.Wait()
	close(metricsCh)

	duration := time.Since(startWall)

	// Aggregate stats
	var totalInsert, totalSearch, totalGet, totalFails int
	var totalInsertFails, totalSearchFails, totalGetFails int
	var totalClientInsertTh, totalClientSearchTh, totalClientGetTh float64
	var totalClientInsertTime, totalClientSearchTime, totalClientGetTime float64
	var clientCount int

	for m := range metricsCh {
		if m.insertTime > 0 {
			totalClientInsertTh += float64(m.insertOps) / m.insertTime
			totalInsert += m.insertOps
			totalClientInsertTime += m.insertTime
		}
		if m.searchTime > 0 {
			totalClientSearchTh += float64(m.searchOps) / m.searchTime
			totalSearch += m.searchOps
			totalClientSearchTime += m.searchTime
		}
		if m.getTime > 0 {
			totalClientGetTh += float64(m.getOps) / m.getTime
			totalGet += m.getOps
			totalClientGetTime += m.getTime
		}
		totalFails += m.failures
		totalInsertFails += m.insertFailures
		totalSearchFails += m.searchFailures
		totalGetFails += m.getFailures
		clientCount++
	}

	totalOps := totalInsert + totalSearch + totalGet
	systemThroughput := float64(totalOps) / duration.Seconds()
	clientAvgInsertTh := totalClientInsertTh / float64(clientCount)
	clientAvgSearchTh := totalClientSearchTh / float64(clientCount)
	clientAvgGetTh := totalClientGetTh / float64(clientCount)

	// Final output
	fmt.Println("\n📊 Vector Engine Multi-Space Benchmark Results:")
	fmt.Printf("Wall clock time: %v\n", duration)
	fmt.Printf("Total Ops: %d (INSERTs: %d, SEARCHs: %d, GETs: %d)\n", totalOps, totalInsert, totalSearch, totalGet)
	fmt.Printf("Failures: %d (INSERTs: %d, SEARCHs: %d, GETs: %d)\n", totalFails, totalInsertFails, totalSearchFails, totalGetFails)
	fmt.Printf("WAL enabled: %t\n", enableWAL)
	fmt.Println()

	fmt.Printf("✅ System throughput: %.2f ops/sec (based on wall time)\n", systemThroughput)
	fmt.Printf("📥 Aggregate INSERT throughput: %.2f ops/sec\n", float64(totalInsert)/duration.Seconds())
	fmt.Printf("🔎 Aggregate SEARCH throughput: %.2f ops/sec\n", float64(totalSearch)/duration.Seconds())
	fmt.Printf("📦 Aggregate GET throughput: %.2f ops/sec\n", float64(totalGet)/duration.Seconds())
	fmt.Printf("📈 Avg per-client INSERT throughput: %.2f ops/sec\n", clientAvgInsertTh)
	fmt.Printf("📈 Avg per-client SEARCH throughput: %.2f ops/sec\n", clientAvgSearchTh)
	fmt.Printf("📈 Avg per-client GET throughput: %.2f ops/sec\n", clientAvgGetTh)
	fmt.Printf("📊 Avg per-client combined throughput: %.2f ops/sec\n", (totalClientInsertTh+totalClientSearchTh+totalClientGetTh)/float64(clientCount))

	if totalFails > 0 {
		t.Fatalf("vector multi-space benchmark recorded %d failed operations", totalFails)
	}
}

func createVectorSpace(space string, enableWAL bool) error {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	login(conn, reader)

	_, err = sendCheckedVectorQuery(models.Query{
		Type:       "CREATE_SPACE",
		Space:      space,
		EngineType: "vector",
		Dimension:  vectorDimension,
		IndexType:  "Flat",
		Metric:     "L2",
		EnableWAL:  enableWAL,
	}, conn, reader)
	return err
}
