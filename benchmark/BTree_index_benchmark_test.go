package benchmark

import (
	"fmt"
	"github.com/Podcopic-Labs/ShibuDb/internal/index"
	"os"
	"sync"
	"testing"
	"time"
)

func BenchmarkConcurrentIndexOps(b *testing.B) {
	_ = os.Remove("benchmark_index.dat")

	idx, err := index.NewBTreeIndex("benchmark_index.dat")
	if err != nil {
		b.Fatalf("Failed to create index: %v", err)
	}
	defer idx.Close()

	const numGoroutines = 16
	const opsPerGoroutine = 10000

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", gid, j)
				pos := int64(gid*opsPerGoroutine + j)
				_ = idx.Add(key, pos)
			}
		}(i)
	}
	wg.Wait()
	duration := time.Since(start)

	totalOps := numGoroutines * opsPerGoroutine
	fmt.Printf("\n\n[Benchmark] Total ops: %d\n", totalOps)
	fmt.Printf("[Benchmark] Total time: %v\n", duration)
	fmt.Printf("[Benchmark] Throughput: %.2f ops/sec\n", float64(totalOps)/duration.Seconds())

	// Now verify correctness
	t := &testing.T{}
	for i := 0; i < numGoroutines; i++ {
		for j := 0; j < opsPerGoroutine; j++ {
			key := fmt.Sprintf("key-%d-%d", i, j)
			expectedPos := int64(i*opsPerGoroutine + j)
			actualPos, found := idx.Get(key)
			if !found || actualPos != expectedPos {
				t.Errorf("Key %s: expected pos %d, got %d (found=%v)", key, expectedPos, actualPos, found)
			}
		}
	}
	if t.Failed() {
		b.Fatalf("Correctness check failed.")
	}
}
