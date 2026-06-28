package storage

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

type insertPhaseSample struct {
	total      time.Duration
	walWrite   time.Duration
	indexWrite time.Duration
	walCommit  time.Duration
}

func benchmarkInsertWithBreakdown(ve *VectorEngineImpl, id int64, vector []float32) (insertPhaseSample, error) {
	var sample insertPhaseSample
	start := time.Now()

	if len(vector) != ve.maxVectorSize {
		return sample, fmt.Errorf("vector length mismatch: expected %d", ve.maxVectorSize)
	}

	if ve.wal != nil {
		key := make([]byte, 8)
		binary.LittleEndian.PutUint64(key, uint64(id))

		walStart := time.Now()
		if err := ve.wal.WriteEntry(string(key), string(float32ArrayToBytes(vector))); err != nil {
			return sample, err
		}
		sample.walWrite = time.Since(walStart)
	}

	indexStart := time.Now()
	if err := ve.insertAfterWAL(id, vector); err != nil {
		return sample, err
	}
	sample.indexWrite = time.Since(indexStart)

	if ve.wal != nil {
		commitStart := time.Now()
		if err := ve.wal.MarkCommitted(); err != nil {
			return sample, err
		}
		sample.walCommit = time.Since(commitStart)
	}

	sample.total = time.Since(start)
	return sample, nil
}

func TestVectorEngineMultiClientInsertBenchmark(t *testing.T) {
	runVectorEngineMultiClientInsertBenchmark(t, true)
}

func TestVectorEngineMultiClientInsertBenchmarkNoWAL(t *testing.T) {
	runVectorEngineMultiClientInsertBenchmark(t, false)
}

func runVectorEngineMultiClientInsertBenchmark(t *testing.T, enableWAL bool) {
	dir := t.TempDir()
	dataPath := dir + "/vector_data.db"
	indexPath := dir + "/vector_index.faiss"
	walPath := dir + "/vector_wal.db"

	const (
		dimension        = 32
		insertWorkers    = 16
		insertsPerWorker = 150
		searchWorkers    = 4
		getWorkers       = 4
		checkpointEvery  = 75 * time.Millisecond
		seedCount        = 128
	)

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, dimension, "Flat", faiss.MetricL2, enableWAL)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer ve.Close()

	type seedSample struct {
		id  int64
		vec []float32
	}

	seedVectors := make([]seedSample, 0, seedCount)
	for i := 0; i < seedCount; i++ {
		id := int64(i + 1)
		vec := randomVector(dimension)
		if err := ve.InsertVector(id, vec); err != nil {
			t.Fatalf("seed insert failed for id=%d: %v", id, err)
		}
		seedVectors = append(seedVectors, seedSample{id: id, vec: vec})
	}
	ve.flushData(true)

	done := make(chan struct{})
	resultsCh := make(chan insertPhaseSample, insertWorkers*insertsPerWorker)
	errCh := make(chan error, 16)
	reportErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	var searchOps atomic.Int64
	var getOps atomic.Int64
	var checkpointOps atomic.Int64

	var auxWG sync.WaitGroup

	for worker := 0; worker < searchWorkers; worker++ {
		auxWG.Add(1)
		go func(offset int) {
			defer auxWG.Done()
			i := offset
			for {
				select {
				case <-done:
					return
				default:
				}

				vec := seedVectors[i%len(seedVectors)].vec
				ids, _, err := ve.SearchTopK(vec, 1)
				if err != nil {
					reportErr(fmt.Errorf("search failed: %w", err))
					return
				}
				if len(ids) == 0 {
					reportErr(fmt.Errorf("search returned no results"))
					return
				}
				searchOps.Add(1)
				i++
			}
		}(worker)
	}

	for worker := 0; worker < getWorkers; worker++ {
		auxWG.Add(1)
		go func(offset int) {
			defer auxWG.Done()
			i := offset
			for {
				select {
				case <-done:
					return
				default:
				}

				s := seedVectors[i%len(seedVectors)]
				vec, err := ve.GetVectorByID(s.id)
				if err != nil {
					reportErr(fmt.Errorf("get failed: %w", err))
					return
				}
				if len(vec) != len(s.vec) {
					reportErr(fmt.Errorf("get returned vector length %d for id=%d, want=%d", len(vec), s.id, len(s.vec)))
					return
				}
				getOps.Add(1)
				i++
			}
		}(worker)
	}

	auxWG.Add(1)
	go func() {
		defer auxWG.Done()
		ticker := time.NewTicker(checkpointEvery)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := ve.checkpoint(); err != nil {
					reportErr(fmt.Errorf("checkpoint failed: %w", err))
					return
				}
				checkpointOps.Add(1)
			}
		}
	}()

	var insertWG sync.WaitGroup
	startWall := time.Now()

	for worker := 0; worker < insertWorkers; worker++ {
		insertWG.Add(1)
		go func(workerID int) {
			defer insertWG.Done()
			for j := 0; j < insertsPerWorker; j++ {
				id := int64(100_000 + workerID*10_000 + j)
				sample, err := benchmarkInsertWithBreakdown(ve, id, randomVector(dimension))
				if err != nil {
					reportErr(fmt.Errorf("insert failed for id=%d: %w", id, err))
					return
				}
				resultsCh <- sample
			}
		}(worker)
	}

	insertWG.Wait()
	wallDuration := time.Since(startWall)
	close(done)
	auxWG.Wait()
	close(resultsCh)
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("multi-client insert benchmark failed: %v", err)
		}
	}

	var (
		insertCount        int
		totalInsertTime    time.Duration
		totalWALWriteTime  time.Duration
		totalIndexTime     time.Duration
		totalWALCommitTime time.Duration
		latencies          []time.Duration
	)

	for sample := range resultsCh {
		insertCount++
		totalInsertTime += sample.total
		totalWALWriteTime += sample.walWrite
		totalIndexTime += sample.indexWrite
		totalWALCommitTime += sample.walCommit
		latencies = append(latencies, sample.total)
	}

	if insertCount != insertWorkers*insertsPerWorker {
		t.Fatalf("insert count mismatch: got=%d want=%d", insertCount, insertWorkers*insertsPerWorker)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)/2]
	p95 := latencies[(len(latencies)*95)/100]
	avgInsert := totalInsertTime / time.Duration(insertCount)
	avgWALWrite := totalWALWriteTime / time.Duration(insertCount)
	avgIndex := totalIndexTime / time.Duration(insertCount)
	avgWALCommit := totalWALCommitTime / time.Duration(insertCount)
	avgExplicitSync := (totalWALWriteTime + totalWALCommitTime) / time.Duration(insertCount)
	syncShare := 100 * float64((totalWALWriteTime + totalWALCommitTime).Nanoseconds()) / float64(totalInsertTime.Nanoseconds())

	mode := "WAL enabled"
	if !enableWAL {
		mode = "WAL disabled"
	}

	fmt.Printf("\nVector Engine Multi-Client Insert Benchmark Results (%s):\n", mode)
	fmt.Printf("Wall clock time: %v\n", wallDuration)
	fmt.Printf("Insert workers: %d, inserts per worker: %d\n", insertWorkers, insertsPerWorker)
	fmt.Printf("Concurrent search workers: %d, concurrent get workers: %d\n", searchWorkers, getWorkers)
	fmt.Printf("Completed inserts: %d\n", insertCount)
	fmt.Printf("Aggregate INSERT throughput: %.2f ops/sec\n", float64(insertCount)/wallDuration.Seconds())
	fmt.Printf("Search ops completed during insert load: %d\n", searchOps.Load())
	fmt.Printf("GET ops completed during insert load: %d\n", getOps.Load())
	fmt.Printf("Checkpoints completed during insert load: %d\n", checkpointOps.Load())
	fmt.Println()
	fmt.Printf("Avg insert latency: %v\n", avgInsert)
	fmt.Printf("P50 insert latency: %v\n", p50)
	fmt.Printf("P95 insert latency: %v\n", p95)
	fmt.Printf("Avg WAL write+sync time: %v\n", avgWALWrite)
	fmt.Printf("Avg index mutation/enqueue time: %v\n", avgIndex)
	fmt.Printf("Avg WAL commit+sync time: %v\n", avgWALCommit)
	fmt.Printf("Avg explicit disk-sync time on insert path: %v\n", avgExplicitSync)
	fmt.Printf("Explicit disk-sync share of total insert latency: %.2f%%\n", syncShare)
}
