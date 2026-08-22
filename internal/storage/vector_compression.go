package storage

import (
	"fmt"
	"strings"

	"github.com/mredencom/turboquant"
)

// Vector compression schemes for the filterable Flat (flat-meta) engine.
// These names are the CREATE-SPACE --compression values.
const (
	VectorCompressionNone            = ""
	VectorCompressionTurboQuant2Bits = "TurboQuant2Bits"
	VectorCompressionTurboQuant3Bits = "TurboQuant3Bits"
	VectorCompressionTurboQuant4Bits = "TurboQuant4Bits"
)

// turboQuantRotationSeed is the orthogonal-rotation seed passed to TurboQuant.
// It must stay constant so persisted quantized payloads dequantize with the same
// rotation that produced them.
const turboQuantRotationSeed int64 = 42

// NormalizeVectorCompression validates a user-supplied --compression value
// (empty means uncompressed). Comparison is case-sensitive to match the
// documented names.
func NormalizeVectorCompression(s string) (string, error) {
	switch s {
	case VectorCompressionNone:
		return VectorCompressionNone, nil
	case VectorCompressionTurboQuant2Bits, VectorCompressionTurboQuant3Bits, VectorCompressionTurboQuant4Bits:
		return s, nil
	default:
		return "", fmt.Errorf("unsupported compression %q; valid values: %s", s, SupportedVectorCompressions())
	}
}

// ValidateVectorCompression checks that compression is valid for the given
// dimension. TurboQuant requires dimension >= 2.
func ValidateVectorCompression(dim int, compression string) error {
	c, err := NormalizeVectorCompression(compression)
	if err != nil {
		return err
	}
	if c == VectorCompressionNone {
		return nil
	}
	if dim < 2 {
		return fmt.Errorf("TurboQuant compression requires dimension >= 2, got %d", dim)
	}
	return nil
}

func turboQuantBitWidth(compression string) (int, error) {
	switch compression {
	case VectorCompressionTurboQuant2Bits:
		return turboquant.Bit2, nil
	case VectorCompressionTurboQuant3Bits:
		return turboquant.Bit3, nil
	case VectorCompressionTurboQuant4Bits:
		return turboquant.Bit4, nil
	default:
		return 0, fmt.Errorf("not a TurboQuant compression: %q", compression)
	}
}

// VectorQuantizer wraps a TurboQuant instance for a space's dimension and bit width.
type VectorQuantizer struct {
	compression string
	bitWidth    int
	payloadSize int
	tq          *turboquant.TurboQuant
}

// NewVectorQuantizer builds a quantizer for dim and compression.
// compression must already be a recognized TurboQuant scheme (not empty).
func NewVectorQuantizer(dim int, compression string) (*VectorQuantizer, error) {
	if err := ValidateVectorCompression(dim, compression); err != nil {
		return nil, err
	}
	c, err := NormalizeVectorCompression(compression)
	if err != nil {
		return nil, err
	}
	if c == VectorCompressionNone {
		return nil, fmt.Errorf("NewVectorQuantizer requires a TurboQuant compression scheme")
	}
	bitWidth, err := turboQuantBitWidth(c)
	if err != nil {
		return nil, err
	}
	tq, err := turboquant.NewTurboQuant(dim, bitWidth, turboQuantRotationSeed)
	if err != nil {
		return nil, fmt.Errorf("init TurboQuant (dim=%d bits=%d): %w", dim, bitWidth, err)
	}
	return &VectorQuantizer{
		compression: c,
		bitWidth:    bitWidth,
		payloadSize: quantizedPayloadSize(dim, bitWidth),
		tq:          tq,
	}, nil
}

// Compression returns the scheme name (e.g. TurboQuant4Bits).
func (q *VectorQuantizer) Compression() string { return q.compression }

// PayloadSize is the on-disk / in-memory byte length of one quantized vector.
func (q *VectorQuantizer) PayloadSize() int { return q.payloadSize }

// Quantize compresses a float32 vector into the compact serialized form.
func (q *VectorQuantizer) Quantize(vec []float32) ([]byte, error) {
	qv, err := q.tq.Quantize(vec)
	if err != nil {
		return nil, err
	}
	return q.tq.Serialize(qv)
}

// Dequantize reconstructs an approximate float32 vector from a serialized payload.
func (q *VectorQuantizer) Dequantize(data []byte) ([]float32, error) {
	qv, err := q.tq.Deserialize(data)
	if err != nil {
		return nil, err
	}
	return q.tq.Dequantize(qv)
}

func quantizedPayloadSize(dimension, bitWidth int) int {
	return 4 + quantizedIndexBytes(dimension, bitWidth)
}

func quantizedIndexBytes(dimension, bitWidth int) int {
	switch bitWidth {
	case turboquant.Bit2:
		return (dimension + 3) / 4
	case turboquant.Bit3:
		return (dimension*3 + 7) / 8
	case turboquant.Bit4:
		return (dimension + 1) / 2
	default:
		return 0
	}
}

// SupportedVectorCompressions is a comma-separated list for CLI/help text.
func SupportedVectorCompressions() string {
	return strings.Join([]string{
		VectorCompressionTurboQuant2Bits,
		VectorCompressionTurboQuant3Bits,
		VectorCompressionTurboQuant4Bits,
	}, ", ")
}
