package benchmark

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

// Using models.Query instead of local Query type

const (
	serverAddr         = "localhost:4444"
	totalSpaces        = 5
	totalClients       = 100
	firstPhaseOps      = 20
	secondPhaseOps     = 20
	sleepBetweenPhases = 2 * time.Second
)

func TestMultiSpace(t *testing.T) {
	runMultiSpace()
}

func runMultiSpace() {
	fmt.Println("Starting multi-table space concurrency test...")

	// Step 1: Create table spaces
	for i := 0; i < totalSpaces; i++ {
		space := fmt.Sprintf("space_%d", i)
		if err := createSpace(space); err != nil {
			fmt.Printf("Failed to create space %s: %v\n", space, err)
			return
		}
	}

	type metrics struct {
		putOps   int
		getOps   int
		putTime  float64
		getTime  float64
		failures int
	}

	var wg sync.WaitGroup
	metricsCh := make(chan metrics, totalClients)

	startWall := time.Now()

	for i := 0; i < totalClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			space := fmt.Sprintf("space_%d", clientID%totalSpaces)

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

			// PUT Phase
			putStart := time.Now()
			for j := 0; j < firstPhaseOps; j++ {
				key := fmt.Sprintf("c%d-f1-%d", clientID, j)
				val := fmt.Sprintf("v%d-f1-%d", clientID, j)
				if err := sendQuery(models.Query{Type: "PUT", Key: key, Value: val, Space: space}, conn, reader); err == nil {
					m.putOps++
					localExpected[key] = val
				} else {
					m.failures++
				}
			}
			time.Sleep(sleepBetweenPhases)
			for j := 0; j < secondPhaseOps; j++ {
				key := fmt.Sprintf("c%d-f2-%d", clientID, j)
				val := fmt.Sprintf("v%d-f2-%d", clientID, j)
				if err := sendQuery(models.Query{Type: "PUT", Key: key, Value: val, Space: space}, conn, reader); err == nil {
					m.putOps++
					localExpected[key] = val
				} else {
					m.failures++
				}
			}
			m.putTime = time.Since(putStart).Seconds()

			// GET Phase
			getStart := time.Now()
			for key, expected := range localExpected {
				query := models.Query{Type: "GET", Key: key, Space: space}
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
				var respObj map[string]string
				if err := json.Unmarshal(resp, &respObj); err != nil {
					m.failures++
					continue
				}
				if respObj["status"] != "OK" || respObj["value"] != expected {
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
	var totalPut, totalGet, totalFails int
	var totalClientPutTh, totalClientGetTh float64
	var totalClientPutTime, totalClientGetTime float64
	var clientCount int

	for m := range metricsCh {
		if m.putTime > 0 {
			totalClientPutTh += float64(m.putOps) / m.putTime
			totalPut += m.putOps
			totalClientPutTime += m.putTime
		}
		if m.getTime > 0 {
			totalClientGetTh += float64(m.getOps) / m.getTime
			totalGet += m.getOps
			totalClientGetTime += m.getTime
		}
		totalFails += m.failures
		clientCount++
	}

	totalOps := totalPut + totalGet
	systemThroughput := float64(totalOps) / duration.Seconds()
	clientAvgPutTh := totalClientPutTh / float64(clientCount)
	clientAvgGetTh := totalClientGetTh / float64(clientCount)

	// Final output
	fmt.Println("\n📊 Multi-Space Benchmark Results:")
	fmt.Printf("Wall clock time: %v\n", duration)
	fmt.Printf("Total Ops: %d (PUTs: %d, GETs: %d)\n", totalOps, totalPut, totalGet)
	fmt.Printf("Failures: %d\n", totalFails)
	fmt.Println()

	fmt.Printf("✅ System throughput: %.2f ops/sec (based on wall time)\n", systemThroughput)
	fmt.Printf("📈 Avg per-client PUT throughput: %.2f ops/sec\n", clientAvgPutTh)
	fmt.Printf("📈 Avg per-client GET throughput: %.2f ops/sec\n", clientAvgGetTh)
	fmt.Printf("📊 Avg per-client combined throughput: %.2f ops/sec\n", (totalClientPutTh+totalClientGetTh)/float64(clientCount))
}

func createSpace(space string) error {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	login(conn, reader)

	query := models.Query{Type: "CREATE_SPACE", Space: space, EnableWAL: true}
	data, _ := json.Marshal(query)
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}

	_, err = reader.ReadBytes('\n')
	return err
}

func sendQuery(q models.Query, conn net.Conn, reader *bufio.Reader) error {
	data, _ := json.Marshal(q)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = reader.ReadBytes('\n')
	return err
}

func login(conn net.Conn, reader *bufio.Reader) {
	login := models.LoginRequest{Username: "admin", Password: "admin"}
	data, _ := json.Marshal(login)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, `"status":"OK"`) {
		fmt.Println("Authentication failed. Server response:", strings.TrimSpace(resp))
	}
}
