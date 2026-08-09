package storage

import "math/rand"

func randomVector(dimension int) []float32 {
	vector := make([]float32, dimension)
	for idx := range vector {
		vector[idx] = rand.Float32()
	}
	return vector
}
