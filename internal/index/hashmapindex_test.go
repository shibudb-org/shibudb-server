package index

import (
	"fmt"
	"os"
	"testing"
)

func TestHashMapIndex_AddGet(t *testing.T) {
	filename := "test_hashmap_addget.dat"
	defer os.Remove(filename)

	idx, err := NewHashMapIndex(filename)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}
	defer idx.Close()

	err = idx.Add("key1", 100)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	pos, ok := idx.Get("key1")
	if !ok || pos != 100 {
		t.Fatalf("Get failed, expected 100, got %d, ok: %v", pos, ok)
	}
}

func TestHashMapIndex_Remove(t *testing.T) {
	filename := "test_hashmap_remove.dat"
	defer os.Remove(filename)

	idx, err := NewHashMapIndex(filename)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}
	defer idx.Close()

	idx.Add("key1", 100)
	idx.Remove("key1")

	_, ok := idx.Get("key1")
	if ok {
		t.Fatalf("Remove failed, key1 still present")
	}
}

func TestHashMapIndex_Snapshot(t *testing.T) {
	filename := "test_hashmap_snapshot.dat"
	defer os.Remove(filename)

	idx, err := NewHashMapIndex(filename)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}
	defer idx.Close()

	idx.Add("key1", 100)
	idx.Add("key2", 200)

	snap := idx.SnapshotEntries()
	if len(snap) != 2 || snap["key1"] != 100 || snap["key2"] != 200 {
		t.Fatalf("Snapshot failed, got %v", snap)
	}
}

func TestHashMapIndex_Persistence(t *testing.T) {
	filename := "test_hashmap_persistence.dat"
	defer os.Remove(filename)

	idx, err := NewHashMapIndex(filename)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}
	idx.Add("key1", 100)
	idx.Add("key2", 200)
	idx.Close()

	// Reopen
	idx2, err := NewHashMapIndex(filename)
	if err != nil {
		t.Fatalf("Failed to reopen index: %v", err)
	}
	defer idx2.Close()

	pos, ok := idx2.Get("key1")
	if !ok || pos != 100 {
		t.Fatalf("Persistence failed for key1")
	}

	pos, ok = idx2.Get("key2")
	if !ok || pos != 200 {
		t.Fatalf("Persistence failed for key2")
	}
}

func BenchmarkHashMapIndex_Get(b *testing.B) {
	filename := "bench_hashmap_get.dat"
	defer os.Remove(filename)
	idx, _ := NewHashMapIndex(filename)
	defer idx.Close()

	for i := 0; i < 1000; i++ {
		idx.Add(fmt.Sprintf("key%d", i), int64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Get(fmt.Sprintf("key%d", i%1000))
	}
}

func BenchmarkBTreeIndex_Get(b *testing.B) {
	filename := "bench_btree_get.dat"
	defer os.Remove(filename)
	idx, _ := NewBTreeIndex(filename)
	defer idx.Close()

	for i := 0; i < 1000; i++ {
		idx.Add(fmt.Sprintf("key%d", i), int64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Get(fmt.Sprintf("key%d", i%1000))
	}
}
