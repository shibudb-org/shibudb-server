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

	"github.com/Podcopic-Labs/ShibuDb/internal/models"
)

const (
	ServerAddr         = "localhost:4444"
	tableSpace         = "benchmark_space"
	TotalClients       = 100
	FirstPhaseOps      = 10
	SecondPhaseOps     = 10
	SleepBetweenPhases = 3 * time.Second
)

func TestSingleSpace(t *testing.T) {
	RunSingleSpace(t)
}

func RunSingleSpace(t *testing.T) {
	fmt.Println("Starting concurrency + auto-flush test...")

	if err := createBenchmarkSpace(); err != nil {
		t.Fatalf("Failed to create benchmark space: %v", err)
	}

	type metrics struct {
		putOps   int
		getOps   int
		putTime  float64
		getTime  float64
		failures int
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

			// PUT Phase
			putStart := time.Now()
			for j := 0; j < FirstPhaseOps; j++ {
				key := fmt.Sprintf("key-%d-p1-%d", clientID, j)
				val := fmt.Sprintf("value-%d-p1-%d", clientID, j)
				if err := SendQuery(models.Query{Type: "PUT", Key: key, Value: val, Space: tableSpace}, conn, reader); err == nil {
					m.putOps++
					localExpected[key] = val
				} else {
					m.failures++
				}
			}
			time.Sleep(SleepBetweenPhases)
			for j := 0; j < SecondPhaseOps; j++ {
				key := fmt.Sprintf("key-%d-p2-%d", clientID, j)
				val := fmt.Sprintf("value-%d-p2-%d", clientID, j)
				if err := SendQuery(models.Query{Type: "PUT", Key: key, Value: val, Space: tableSpace}, conn, reader); err == nil {
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
				query := models.Query{Type: "GET", Key: key, Space: tableSpace}
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
	wallDuration := time.Since(startWall)
	close(metricsCh)

	// Aggregation
	var totalPutOps, totalGetOps, totalFails int
	var totalPutTime, totalGetTime float64
	var totalClientPutThroughput, totalClientGetThroughput float64
	var clientCount int

	for m := range metricsCh {
		if m.putTime > 0 {
			totalClientPutThroughput += float64(m.putOps) / m.putTime
			totalPutOps += m.putOps
			totalPutTime += m.putTime
		}
		if m.getTime > 0 {
			totalClientGetThroughput += float64(m.getOps) / m.getTime
			totalGetOps += m.getOps
			totalGetTime += m.getTime
		}
		totalFails += m.failures
		clientCount++
	}

	totalOps := totalPutOps + totalGetOps
	totalClientThroughput := totalClientPutThroughput + totalClientGetThroughput

	fmt.Println("\n📊 Benchmark Results:")
	fmt.Printf("Wall clock time: %v\n", wallDuration)
	fmt.Printf("Total Ops: %d (PUTs: %d, GETs: %d)\n", totalOps, totalPutOps, totalGetOps)
	fmt.Printf("Failures: %d\n", totalFails)
	fmt.Println()

	// Actual system throughput (correct)
	fmt.Printf("System throughput: %.2f ops/sec (based on wall time)\n", float64(totalOps)/wallDuration.Seconds())

	// Diagnostic: avg client throughput
	fmt.Printf("Avg per-client PUT throughput: %.2f ops/sec\n", totalClientPutThroughput/float64(clientCount))
	fmt.Printf("Avg per-client GET throughput: %.2f ops/sec\n", totalClientGetThroughput/float64(clientCount))
	fmt.Printf("Avg per-client combined throughput: %.2f ops/sec\n", totalClientThroughput/float64(clientCount))
}

func createBenchmarkSpace() error {
	conn, err := net.Dial("tcp", ServerAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	Login(conn, reader)

	query := models.Query{Type: "CREATE_SPACE", Space: tableSpace, EnableWAL: true}
	data, _ := json.Marshal(query)
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}

	_, err = reader.ReadBytes('\n')
	return err
}

func SendQuery(q models.Query, conn net.Conn, reader *bufio.Reader) error {
	data, _ := json.Marshal(q)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = reader.ReadBytes('\n')
	return err
}

func Login(conn net.Conn, reader *bufio.Reader) {
	login := models.LoginRequest{Username: "admin", Password: "admin"}
	data, _ := json.Marshal(login)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, `"status":"OK"`) {
		fmt.Println("Authentication failed. Server response:", strings.TrimSpace(resp))
	}
}
