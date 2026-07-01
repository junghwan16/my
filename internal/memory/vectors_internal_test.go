package memory

import (
	"math"
	"testing"
)

func TestEncodeDecodeVectorRoundTrips(t *testing.T) {
	vec := []float32{0, 1, -1, 3.14159, 1e-7}
	got := decodeVector(encodeVector(vec))
	if len(got) != len(vec) {
		t.Fatalf("decoded len = %d, want %d", len(got), len(vec))
	}
	for i := range vec {
		if got[i] != vec[i] {
			t.Fatalf("decoded[%d] = %v, want %v", i, got[i], vec[i])
		}
	}
}

func TestDecodeVectorRejectsMisalignedBlob(t *testing.T) {
	if got := decodeVector([]byte{1, 2, 3}); got != nil {
		t.Fatalf("misaligned blob decoded to %v, want nil", got)
	}
}

func TestCosineSimilarityOrdersByAngle(t *testing.T) {
	query := []float32{1, 0, 0}
	near := cosineSimilarity(query, []float32{0.9, 0.1, 0})
	far := cosineSimilarity(query, []float32{0, 1, 0})
	if !(near > far) {
		t.Fatalf("cosine near=%v not greater than far=%v", near, far)
	}
	if math.Abs(cosineSimilarity(query, []float32{2, 0, 0})-1) > 1e-6 {
		t.Fatalf("cosine of parallel vectors = %v, want 1", cosineSimilarity(query, []float32{2, 0, 0}))
	}
}

func TestCosineSimilarityGuardsDegenerateInputs(t *testing.T) {
	if got := cosineSimilarity([]float32{1, 2}, []float32{1}); got != 0 {
		t.Fatalf("mismatched-length cosine = %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{0, 0}, []float32{1, 1}); got != 0 {
		t.Fatalf("zero-magnitude cosine = %v, want 0", got)
	}
}
