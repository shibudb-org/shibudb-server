package benchmark

import (
	"bufio"
	"encoding/json"
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

type VectorQuery struct {
	Type       string `json:"type"`
	Key        string `json:"key,omitempty"`
	Value      string `json:"value,omitempty"`
	Space      string `json:"space"`
	Dimension  int    `json:"dimension,omitempty"`
	EngineType string `json:"engine_type,omitempty"`
}

func TestVectorSingleSpace(t *testing.T) {
	RunVectorSingleSpace(t)
}

func RunVectorSingleSpace(t *testing.T) {
	fmt.Println("Starting vector engine single space concurrency test...")

	if err := createVectorBenchmarkSpace(); err != nil {
		t.Fatalf("Failed to create vector benchmark space: %v", err)
	}

	type metrics struct {
		insertOps  int
		searchOps  int
		getOps     int
		insertTime float64
		searchTime float64
		getTime    float64
		failures   int
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
				if err := SendVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: vectorSpace}, conn, reader); err == nil {
					m.insertOps++
					localExpected[vectorID] = vectorData
				} else {
					m.failures++
				}
			}
			time.Sleep(SleepBetweenPhases)
			for j := 0; j < SecondPhaseOps; j++ {
				vectorID := fmt.Sprintf("%d", clientID*1000+j+FirstPhaseOps)
				vectorData := generateRandomVector(VectorDimension)
				if err := SendVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: vectorSpace}, conn, reader); err == nil {
					m.insertOps++
					localExpected[vectorID] = vectorData
				} else {
					m.failures++
				}
			}
			m.insertTime = time.Since(insertStart).Seconds()

			// SEARCH Phase
			searchStart := time.Now()
			for j := 0; j < 5; j++ {
				queryVector := generateRandomVector(VectorDimension)
				if err := SendVectorQuery(models.Query{Type: "SEARCH_TOPK", Value: queryVector, Space: vectorSpace, Dimension: 10}, conn, reader); err == nil {
					m.searchOps++
				} else {
					m.failures++
				}
			}
			m.searchTime = time.Since(searchStart).Seconds()

			// GET Phase
			getStart := time.Now()
			for vectorID := range localExpected {
				query := models.Query{Type: "GET_VECTOR", Key: vectorID, Space: vectorSpace}
				data, _ := json.Marshal(query)

				if _, err := conn.Write(append(data, '\n')); err != nil {
					m.failures++
					continue
				}

				resp, err := reader.ReadBytes('\n')
				if err != nil {
					m.failures++
					continue
				}

				var respObj map[string]interface{}
				if err := json.Unmarshal(resp, &respObj); err != nil {
					m.failures++
					continue
				}

				if respObj["status"] != "OK" {
					m.failures++
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
		clientCount++
	}

	totalOps := totalInsertOps + totalSearchOps + totalGetOps
	totalClientThroughput := totalClientInsertThroughput + totalClientSearchThroughput + totalClientGetThroughput

	fmt.Println("\n📊 Vector Engine Single Space Benchmark Results:")
	fmt.Printf("Wall clock time: %v\n", wallDuration)
	fmt.Printf("Total Ops: %d (INSERTs: %d, SEARCHs: %d, GETs: %d)\n", totalOps, totalInsertOps, totalSearchOps, totalGetOps)
	fmt.Printf("Failures: %d\n", totalFails)
	fmt.Println()

	// Actual system throughput (correct)
	fmt.Printf("System throughput: %.2f ops/sec (based on wall time)\n", float64(totalOps)/wallDuration.Seconds())

	// Diagnostic: avg client throughput
	fmt.Printf("Avg per-client INSERT throughput: %.2f ops/sec\n", totalClientInsertThroughput/float64(clientCount))
	fmt.Printf("Avg per-client SEARCH throughput: %.2f ops/sec\n", totalClientSearchThroughput/float64(clientCount))
	fmt.Printf("Avg per-client GET throughput: %.2f ops/sec\n", totalClientGetThroughput/float64(clientCount))
	fmt.Printf("Avg per-client combined throughput: %.2f ops/sec\n", totalClientThroughput/float64(clientCount))
}

func createVectorBenchmarkSpace() error {
	conn, err := net.Dial("tcp", ServerAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	Login(conn, reader)

	query := models.Query{Type: "CREATE_SPACE", Space: vectorSpace, EngineType: "vector", Dimension: VectorDimension, EnableWAL: true}
	data, _ := json.Marshal(query)
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}

	_, err = reader.ReadBytes('\n')
	return err
}

func SendVectorQuery(q models.Query, conn net.Conn, reader *bufio.Reader) error {
	data, _ := json.Marshal(q)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = reader.ReadBytes('\n')
	return err
}

func generateRandomVector(dimension int) string {
	rand.Seed(time.Now().UnixNano())
	components := make([]string, dimension)
	for i := 0; i < dimension; i++ {
		components[i] = fmt.Sprintf("%.6f", rand.Float32())
	}
	return strings.Join(components, ",")
}
