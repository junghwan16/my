package memories

import "context"

// Embedder turns memory text into a dense vector for semantic recall. Like the
// Tokenizer, the concrete model lives behind this interface so it can be
// swapped (a different local model, or a future cgo/remote embedder) without
// changing the recall contract. The same Embedder must embed both the stored
// memory text and the query, so a memory indexed one way is not missed because
// the query was embedded by a different model.
//
// The dependency is optional: a store may hold a nil Embedder, in which case
// semantic recall is disabled and lexical (morpheme + FTS5) recall keeps
// working. The default adapter (internal/embed) is Ollama-backed and returns an
// error when Ollama is unreachable, so construction can keep lexical recall.
type Embedder interface {
	// Embed returns the dense vector for text. Implementations should return a
	// stable dimension for a given model; callers persist the model id and dim
	// so a model change is detectable and old vectors can be re-embedded.
	Embed(ctx context.Context, text string) ([]float32, error)

	// Model identifies the embedding model (e.g. "bge-m3"). It is stored beside
	// each vector so a model swap invalidates old vectors instead of silently
	// mixing incomparable embeddings.
	Model() string
}
