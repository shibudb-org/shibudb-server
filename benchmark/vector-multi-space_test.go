package benchmark

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Podcopic-Labs/ShibuDb/internal/models"
)

const (
	totalVectorSpaces = 5
	vectorDimension   = 128
)

func TestVectorMultiSpace(t *testing.T) {
	runVectorMultiSpace()
}

func runVectorMultiSpace() {
	fmt.Println("Starting vector engine multi-space concurrency test...")

	// Step 1: Create vector spaces
	for i := 0; i < totalVectorSpaces; i++ {
		space := fmt.Sprintf("vector_space_%d", i)
		if err := createVectorSpace(space); err != nil {
			fmt.Printf("Failed to create vector space %s: %v\n", space, err)
			return
		}
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
	metricsCh := make(chan metrics, totalClients)

	startWall := time.Now()

	for i := 0; i < totalClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			space := fmt.Sprintf("vector_space_%d", clientID%totalVectorSpaces)

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
				if err := sendVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: space}, conn, reader); err == nil {
					m.insertOps++
					localExpected[vectorID] = vectorData
				} else {
					m.failures++
				}
			}
			time.Sleep(sleepBetweenPhases)
			for j := 0; j < secondPhaseOps; j++ {
				vectorID := fmt.Sprintf("%d", clientID*1000+j+firstPhaseOps)
				vectorData := generateRandomVector(vectorDimension)
				if err := sendVectorQuery(models.Query{Type: "INSERT_VECTOR", Key: vectorID, Value: vectorData, Space: space}, conn, reader); err == nil {
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
				queryVector := generateRandomVector(vectorDimension)
				if err := sendVectorQuery(models.Query{Type: "SEARCH_TOPK", Value: queryVector, Space: space, Dimension: 10}, conn, reader); err == nil {
					m.searchOps++
				} else {
					m.failures++
				}
			}
			m.searchTime = time.Since(searchStart).Seconds()

			// GET Phase
			getStart := time.Now()
			for vectorID := range localExpected {
				query := models.Query{Type: "GET_VECTOR", Key: vectorID, Space: space}
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
	close(metricsCh)

	duration := time.Since(startWall)

	// Aggregate stats
	var totalInsert, totalSearch, totalGet, totalFails int
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
	fmt.Printf("Failures: %d\n", totalFails)
	fmt.Println()

	fmt.Printf("✅ System throughput: %.2f ops/sec (based on wall time)\n", systemThroughput)
	fmt.Printf("📈 Avg per-client INSERT throughput: %.2f ops/sec\n", clientAvgInsertTh)
	fmt.Printf("📈 Avg per-client SEARCH throughput: %.2f ops/sec\n", clientAvgSearchTh)
	fmt.Printf("📈 Avg per-client GET throughput: %.2f ops/sec\n", clientAvgGetTh)
	fmt.Printf("📊 Avg per-client combined throughput: %.2f ops/sec\n", (totalClientInsertTh+totalClientSearchTh+totalClientGetTh)/float64(clientCount))
}

func createVectorSpace(space string) error {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	login(conn, reader)

	query := models.Query{Type: "CREATE_SPACE", Space: space, EngineType: "vector", Dimension: vectorDimension, EnableWAL: true}
	data, _ := json.Marshal(query)
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}

	_, err = reader.ReadBytes('\n')
	return err
}

func sendVectorQuery(q models.Query, conn net.Conn, reader *bufio.Reader) error {
	data, _ := json.Marshal(q)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = reader.ReadBytes('\n')
	return err
}
