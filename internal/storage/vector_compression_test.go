package storage

import (
	"math"
	"testing"
)

func TestNormalizeVectorCompression(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", VectorCompressionNone, false},
		{VectorCompressionTurboQuant2Bits, VectorCompressionTurboQuant2Bits, false},
		{VectorCompressionTurboQuant3Bits, VectorCompressionTurboQuant3Bits, false},
		{VectorCompressionTurboQuant4Bits, VectorCompressionTurboQuant4Bits, false},
		{"TurboQuant6Bits", "", true},
		{"TurboQuant8Bits", "", true},
		{"turboquant4bits", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeVectorCompression(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("NormalizeVectorCompression(%q) error = nil, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizeVectorCompression(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("NormalizeVectorCompression(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateVectorCompressionDimension(t *testing.T) {
	t.Parallel()
	if err := ValidateVectorCompression(1, VectorCompressionTurboQuant4Bits); err == nil {
		t.Fatal("expected error for dim < 2")
	}
	if err := ValidateVectorCompression(8, VectorCompressionTurboQuant4Bits); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVectorQuantizerRoundTrip(t *testing.T) {
	dim := 8
	vec := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	for _, scheme := range []string{VectorCompressionTurboQuant2Bits, VectorCompressionTurboQuant3Bits, VectorCompressionTurboQuant4Bits} {
		q, err := NewVectorQuantizer(dim, scheme)
		if err != nil {
			t.Fatalf("%s: NewVectorQuantizer: %v", scheme, err)
		}
		if q.PayloadSize() >= 4*dim {
			t.Fatalf("%s: payload size %d is not smaller than float32 (%d)", scheme, q.PayloadSize(), 4*dim)
		}
		payload, err := q.Quantize(vec)
		if err != nil {
			t.Fatalf("%s: Quantize: %v", scheme, err)
		}
		if len(payload) != q.PayloadSize() {
			t.Fatalf("%s: quantized bytes %d, want %d", scheme, len(payload), q.PayloadSize())
		}
		got, err := q.Dequantize(payload)
		if err != nil {
			t.Fatalf("%s: Dequantize: %v", scheme, err)
		}
		if len(got) != dim {
			t.Fatalf("%s: reconstructed dim %d, want %d", scheme, len(got), dim)
		}
		if cosineSimilarity(vec, got) < 0.7 {
			t.Fatalf("%s: cosine similarity too low: %f", scheme, cosineSimilarity(vec, got))
		}
	}
}

func cosineSimilarity(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
