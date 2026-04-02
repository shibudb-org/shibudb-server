package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DataIntelligenceCrew/go-faiss"
)

func TestVectorEngineCheckpointParallelLoad(t *testing.T) {
	dir := t.TempDir()
	dataPath := dir + "/vector_data.db"
	indexPath := dir + "/vector_index.faiss"
	walPath := dir + "/vector_wal.db"

	ve, err := NewVectorEngine(dataPath, indexPath, walPath, 16, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer ve.Close()

	type sample struct {
		id  int64
		vec []float32
	}

	const seedCount = 64
	seedVectors := make([]sample, 0, seedCount)
	for i := 0; i < seedCount; i++ {
		vec := randomVector(16)
		id := int64(i + 1)
		if err := ve.InsertVector(id, vec); err != nil {
			t.Fatalf("seed insert failed for id=%d: %v", id, err)
		}
		seedVectors = append(seedVectors, sample{id: id, vec: vec})
	}
	ve.flushData(true)

	errCh := make(chan error, 16)
	reportErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			id := int64(10_000 + i)
			if err := ve.InsertVector(id, randomVector(16)); err != nil {
				reportErr(err)
				return
			}
			if i%25 == 0 {
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			vec := seedVectors[i%len(seedVectors)].vec
			ids, _, err := ve.SearchTopK(vec, 1)
			if err != nil {
				reportErr(err)
				return
			}
			if len(ids) == 0 {
				reportErr(fmt.Errorf("search returned no results for seeded vector"))
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			s := seedVectors[i%len(seedVectors)]
			vec, err := ve.GetVectorByID(s.id)
			if err != nil {
				reportErr(err)
				return
			}
			if len(vec) != len(s.vec) {
				reportErr(fmt.Errorf("unexpected vector length for id=%d: got=%d want=%d", s.id, len(vec), len(s.vec)))
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 75; i++ {
			if err := ve.checkpoint(); err != nil {
				reportErr(err)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("parallel checkpoint load failed: %v", err)
		}
	}

	ve.flushData(true)
	if err := ve.checkpoint(); err != nil {
		t.Fatalf("final checkpoint failed: %v", err)
	}

	reopened, err := NewVectorEngine(dataPath, indexPath, walPath, 16, "Flat", faiss.MetricL2, true)
	if err != nil {
		t.Fatalf("failed to reopen engine: %v", err)
	}
	defer reopened.Close()

	ids, _, err := reopened.SearchTopK(seedVectors[0].vec, 1)
	if err != nil {
		t.Fatalf("search after reopen failed: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("expected checkpointed index to return at least one result after reopen")
	}
	if _, err := reopened.GetVectorByID(seedVectors[0].id); err != nil {
		t.Fatalf("get after reopen failed: %v", err)
	}
}
