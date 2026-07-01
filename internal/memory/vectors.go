package memories

import (
	"encoding/binary"
	"math"

	"github.com/uptrace/bun"
)

// The memory_vectors table stores one dense embedding per memory as a
// little-endian float32 BLOB, alongside the model id and dimension that
// produced it. Recording the model + dim lets the backfill detect a model or
// dimension change and re-embed, so incomparable vectors are never mixed.

type vectorRow struct {
	bun.BaseModel `bun:"table:memory_vectors,alias:vec"`

	MemoryID string `bun:"memory_id,pk"`
	Model    string `bun:"model,notnull"`
	Dim      int    `bun:"dim,notnull"`
	Vector   []byte `bun:"vector,notnull"`
}

func newVectorRow(memoryID, model string, vector []float32) *vectorRow {
	return &vectorRow{
		MemoryID: memoryID,
		Model:    model,
		Dim:      len(vector),
		Vector:   encodeVector(vector),
	}
}

// encodeVector packs a float32 slice into a little-endian byte blob. Little
// endian is fixed by contract so a database written on one host reads back
// identically on another.
func encodeVector(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector unpacks a little-endian float32 blob back into a slice. A blob
// whose length is not a multiple of 4 is treated as empty, so a corrupt row is
// skipped in ranking rather than crashing recall.
func decodeVector(buf []byte) []float32 {
	if len(buf)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return vec
}

// cosineSimilarity returns the cosine similarity of two equal-length vectors in
// [-1, 1]. Mismatched lengths or a zero-magnitude vector return 0, so such a
// candidate ranks last instead of producing a NaN that corrupts the ordering.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
