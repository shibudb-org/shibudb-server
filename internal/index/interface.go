package index

type KeyValueIndex interface {
	Add(key string, pos int64) error
	Get(key string) (int64, bool)
	SnapshotEntries() map[string]int64
	Remove(key string) error
	Close() error
}

func NewKeyValueIndex(filename, indexType string) (KeyValueIndex, error) {
	if indexType == "hashmap" {
		return NewHashMapIndex(filename)
	}
	return NewBTreeIndex(filename)
}
