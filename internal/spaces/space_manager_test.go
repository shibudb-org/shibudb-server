package spaces

import (
	"testing"
)

func TestIsAllowedIndexType(t *testing.T) {
	tests := []struct {
		name     string
		indexType string
		expected bool
	}{
		// Single index types
		{"Flat", "Flat", true},
		{"Flat with number", "Flat32", false}, // Flat should not have number suffix
		
		// HNSW variants (powers of 2 from 2 to 256)
		{"HNSW2", "HNSW2", true},
		{"HNSW4", "HNSW4", true},
		{"HNSW8", "HNSW8", true},
		{"HNSW16", "HNSW16", true},
		{"HNSW32", "HNSW32", true},
		{"HNSW64", "HNSW64", true},
		{"HNSW128", "HNSW128", true},
		{"HNSW256", "HNSW256", true},
		{"HNSW without number", "HNSW", false}, // HNSW requires number suffix
		{"HNSW512", "HNSW512", false}, // Out of range
		{"HNSW1", "HNSW1", false}, // Out of range
		{"HNSW3", "HNSW3", false}, // Not power of 2
		{"HNSW7", "HNSW7", false}, // Not power of 2
		
		// IVF variants (powers of 2 from 2 to 256)
		{"IVF2", "IVF2", true},
		{"IVF4", "IVF4", true},
		{"IVF8", "IVF8", true},
		{"IVF16", "IVF16", true},
		{"IVF32", "IVF32", true},
		{"IVF64", "IVF64", true},
		{"IVF128", "IVF128", true},
		{"IVF256", "IVF256", true},
		{"IVF without number", "IVF", false}, // IVF requires number suffix
		{"IVF512", "IVF512", false}, // Out of range
		{"IVF1", "IVF1", false}, // Out of range
		{"IVF3", "IVF3", false}, // Not power of 2
		{"IVF7", "IVF7", false}, // Not power of 2
		
		// PQ variants (powers of 2 from 2 to 256)
		{"PQ2", "PQ2", true},
		{"PQ4", "PQ4", true},
		{"PQ8", "PQ8", true},
		{"PQ16", "PQ16", true},
		{"PQ32", "PQ32", true},
		{"PQ64", "PQ64", true},
		{"PQ128", "PQ128", true},
		{"PQ256", "PQ256", true},
		{"PQ without number", "PQ", false}, // PQ requires number suffix
		{"PQ512", "PQ512", false}, // Out of range
		{"PQ1", "PQ1", false}, // Out of range
		{"PQ3", "PQ3", false}, // Not power of 2
		{"PQ7", "PQ7", false}, // Not power of 2
		
		// Composite indices
		{"IVF32,Flat", "IVF32,Flat", true},
		{"HNSW64,Flat", "HNSW64,Flat", true},
		{"PQ8,Flat", "PQ8,Flat", true},
		{"IVF64,PQ16", "IVF64,PQ16", true},
		{"HNSW128,PQ32", "HNSW128,PQ32", true},
		
		// Invalid composite indices
		{"Invalid composite 1", "IVF32,Invalid", false},
		{"Invalid composite 2", "Invalid,Flat", false},
		{"Invalid composite 3", "HNSW,Flat", false}, // HNSW without number
		{"Invalid composite 4", "IVF,Flat", false}, // IVF without number
		{"Invalid composite 5", "PQ,Flat", false}, // PQ without number
		{"Invalid composite 6", "Flat32,Flat", false}, // Flat with number
		
		// Edge cases
		{"Empty string", "", false},
		{"Only comma", ",", false},
		{"Multiple commas", "HNSW32,,Flat", false},
		{"Whitespace", " HNSW32 ", true}, // Should trim whitespace
		{"Whitespace composite", " HNSW32 , Flat ", true}, // Should trim whitespace
		
		// Invalid index types
		{"Invalid type", "Invalid", false},
		{"Unknown type", "Unknown32", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAllowedIndexType(tt.indexType)
			if result != tt.expected {
				t.Errorf("isAllowedIndexType(%q) = %v, want %v", tt.indexType, result, tt.expected)
			}
		})
	}
}

func TestIsPowerOf2InRange(t *testing.T) {
	tests := []struct {
		name     string
		n        int
		expected bool
	}{
		// Valid powers of 2 in range 2-256
		{"2", 2, true},
		{"4", 4, true},
		{"8", 8, true},
		{"16", 16, true},
		{"32", 32, true},
		{"64", 64, true},
		{"128", 128, true},
		{"256", 256, true},
		
		// Invalid: out of range
		{"1", 1, false},
		{"512", 512, false},
		{"1024", 1024, false},
		{"0", 0, false},
		{"-1", -1, false},
		
		// Invalid: not power of 2
		{"3", 3, false},
		{"5", 5, false},
		{"6", 6, false},
		{"7", 7, false},
		{"9", 9, false},
		{"10", 10, false},
		{"15", 15, false},
		{"17", 17, false},
		{"31", 31, false},
		{"33", 33, false},
		{"63", 63, false},
		{"65", 65, false},
		{"127", 127, false},
		{"129", 129, false},
		{"255", 255, false},
		{"257", 257, false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPowerOf2InRange(tt.n)
			if result != tt.expected {
				t.Errorf("isPowerOf2InRange(%d) = %v, want %v", tt.n, result, tt.expected)
			}
		})
	}
}
