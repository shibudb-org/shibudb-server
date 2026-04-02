package benchmark

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

const (
	vectorSpace     = "vector_benchmark_space"
	VectorDimension = 128
)

func TestVectorSingleSpace(t *testing.T) {
	RunVectorSingleSpace(t, true)
}

func TestVectorSingleSpaceNoWAL(t *testing.T) {
	RunVectorSingleSpace(t, false)
}

func RunVectorSingleSpace(t *testing.T, enableWAL bool) {
	fmt.Println("Starting vector engine single space concurrency test...")

	spaceName := fmt.Sprintf("%s_%d", vectorSpace, time.Now().UnixNano())
	if err := createVectorBenchmarkSpace(spaceName, enableWAL); err != nil {
		t.Fatalf("Failed to create vector benchmark space: %v", err)
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
	metricsCh := make(chan metrics, TotalClients)

	startWall := time.Now()

	for i := 0; i < TotalClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", ServerAddr)
			if err != nil {
				t.Logf("Client %d: Connection error: %v\n", clientID, err)
				metricsCh <- metrics{}
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)

			Login(conn, reader)

			localExpected := make(map[string]string)
			var m metrics

			// INSERT Phase
			insertStart := time.Now()
			for j := 0; j < FirstPhaseOps; j++ {
				vectorID := fmt.Sprintf("%d", clientID*1000+j)
				vectorData := generateRandomVector(VectorDimension)
				if _, err := sendCheckedVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: spaceName}, conn, reader); err == nil {
					m.insertOps++
					localExpected[vectorID] = vectorData
				} else {
					m.failures++
					m.insertFailures++
				}
			}
			time.Sleep(SleepBetweenPhases)
			for j := 0; j < SecondPhaseOps; j++ {
				vectorID := fmt.Sprintf("%d", clientID*1000+j+FirstPhaseOps)
				vectorData := generateRandomVector(VectorDimension)
				if _, err := sendCheckedVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: spaceName}, conn, reader); err == nil {
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
				queryVector := generateRandomVector(VectorDimension)
				if _, err := sendCheckedVectorQuery(models.Query{Type: "SEARCH_TOPK", Value: queryVector, Space: spaceName, Dimension: 10}, conn, reader); err == nil {
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
				if _, err := getVectorEventually(models.Query{Type: "GET_VECTOR", Key: vectorID, Space: spaceName}, conn, reader); err != nil {
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
	wallDuration := time.Since(startWall)
	close(metricsCh)

	// Aggregation
	var totalInsertOps, totalSearchOps, totalGetOps, totalFails int
	var totalInsertFails, totalSearchFails, totalGetFails int
	var totalInsertTime, totalSearchTime, totalGetTime float64
	var totalClientInsertThroughput, totalClientSearchThroughput, totalClientGetThroughput float64
	var clientCount int

	for m := range metricsCh {
		if m.insertTime > 0 {
			totalClientInsertThroughput += float64(m.insertOps) / m.insertTime
			totalInsertOps += m.insertOps
			totalInsertTime += m.insertTime
		}
		if m.searchTime > 0 {
			totalClientSearchThroughput += float64(m.searchOps) / m.searchTime
			totalSearchOps += m.searchOps
			totalSearchTime += m.searchTime
		}
		if m.getTime > 0 {
			totalClientGetThroughput += float64(m.getOps) / m.getTime
			totalGetOps += m.getOps
			totalGetTime += m.getTime
		}
		totalFails += m.failures
		totalInsertFails += m.insertFailures
		totalSearchFails += m.searchFailures
		totalGetFails += m.getFailures
		clientCount++
	}

	totalOps := totalInsertOps + totalSearchOps + totalGetOps
	totalClientThroughput := totalClientInsertThroughput + totalClientSearchThroughput + totalClientGetThroughput

	fmt.Println("\n📊 Vector Engine Single Space Benchmark Results:")
	fmt.Printf("Wall clock time: %v\n", wallDuration)
	fmt.Printf("Total Ops: %d (INSERTs: %d, SEARCHs: %d, GETs: %d)\n", totalOps, totalInsertOps, totalSearchOps, totalGetOps)
	fmt.Printf("Failures: %d (INSERTs: %d, SEARCHs: %d, GETs: %d)\n", totalFails, totalInsertFails, totalSearchFails, totalGetFails)
	fmt.Printf("WAL enabled: %t\n", enableWAL)
	fmt.Println()

	// Actual system throughput (correct)
	fmt.Printf("System throughput: %.2f ops/sec (based on wall time)\n", float64(totalOps)/wallDuration.Seconds())
	fmt.Printf("Aggregate INSERT throughput: %.2f ops/sec\n", float64(totalInsertOps)/wallDuration.Seconds())
	fmt.Printf("Aggregate SEARCH throughput: %.2f ops/sec\n", float64(totalSearchOps)/wallDuration.Seconds())
	fmt.Printf("Aggregate GET throughput: %.2f ops/sec\n", float64(totalGetOps)/wallDuration.Seconds())

	// Diagnostic: avg client throughput
	fmt.Printf("Avg per-client INSERT throughput: %.2f ops/sec\n", totalClientInsertThroughput/float64(clientCount))
	fmt.Printf("Avg per-client SEARCH throughput: %.2f ops/sec\n", totalClientSearchThroughput/float64(clientCount))
	fmt.Printf("Avg per-client GET throughput: %.2f ops/sec\n", totalClientGetThroughput/float64(clientCount))
	fmt.Printf("Avg per-client combined throughput: %.2f ops/sec\n", totalClientThroughput/float64(clientCount))

	if totalFails > 0 {
		t.Fatalf("vector benchmark recorded %d failed operations", totalFails)
	}
}

func createVectorBenchmarkSpace(space string, enableWAL bool) error {
	conn, err := net.Dial("tcp", ServerAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	Login(conn, reader)

	_, err = sendCheckedVectorQuery(models.Query{
		Type:       "CREATE_SPACE",
		Space:      space,
		EngineType: "vector",
		Dimension:  VectorDimension,
		IndexType:  "Flat",
		Metric:     "L2",
		EnableWAL:  enableWAL,
	}, conn, reader)
	return err
}

func generateRandomVector(dimension int) string {
	components := make([]string, dimension)
	for i := 0; i < dimension; i++ {
		components[i] = fmt.Sprintf("%.6f", rand.Float32())
	}
	return strings.Join(components, ",")
}
