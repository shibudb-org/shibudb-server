package storage

type KeyValueEngine interface {
	Close() error
	Put(key, value string) error
	Get(key string) (string, error)
	Delete(key string) error
}

type VectorEngine interface {
	Close() error
	InsertVector(id int64, vector []float32) error
	SearchTopK(query []float32, k int) ([]int64, []float32, error)
	GetVectorByID(id int64) ([]float32, error)
	RangeSearch(query []float32, radius float32) ([]int64, []float32, error)
}
