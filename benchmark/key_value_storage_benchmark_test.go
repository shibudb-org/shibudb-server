package benchmark

import (
	"github.com/Podcopic-Labs/ShibuDb/internal/storage"
	"os"
	"strconv"
	"sync"
	"testing"
)

func BenchmarkShibuDB(b *testing.B) {
	os.Remove("benchmark_storage.db")
	os.Remove("benchmark_wal.db")
	db, err := storage.OpenDBWithPaths("benchmark_storage.db", "benchmark_wal.db", "benchmark_index.dat")
	if err != nil {
		b.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	b.Run("BenchmarkPut", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			db.PutBatch("key"+strconv.Itoa(i), "value"+strconv.Itoa(i))
		}
		_ = db.FlushBatch()
	})

	b.Run("BenchmarkGet", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			db.Get("key" + strconv.Itoa(i))
		}
	})

	b.Run("BenchmarkConcurrentAccess", func(b *testing.B) {
		var wg sync.WaitGroup
		concurrency := 1000
		b.ResetTimer()

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(threadID int) {
				defer wg.Done()
				for j := 0; j < b.N/concurrency; j++ {
					key := "concurrentKey" + strconv.Itoa(threadID) + "_" + strconv.Itoa(j)
					db.PutBatch(key, "value"+strconv.Itoa(j))
					db.Get(key)
				}
			}(i)
		}
		wg.Wait()
		_ = db.FlushBatch()
	})
}
