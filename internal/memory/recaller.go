package memory

import (
	"context"

	"github.com/junghwan16/my/internal/source"
)

// MemoryReader loads recorded memories and links. *Store satisfies it.
type MemoryReader interface {
	SourceMemories(context.Context, source.SourceID) ([]Memory, error)
	SourceLinks(context.Context, source.SourceID) ([]Link, error)
}

// Recaller finds recorded memory. Today it recalls by source; graph-wide recall
// across memories is on the roadmap (see README).
type Recaller struct {
	store MemoryReader
}

// NewRecaller returns a Recaller backed by a memory reader.
func NewRecaller(store MemoryReader) *Recaller {
	return &Recaller{store: store}
}

// Recall returns the memories linked to a source.
func (r *Recaller) Recall(ctx context.Context, id source.SourceID) ([]Memory, error) {
	return r.store.SourceMemories(ctx, id)
}

// Links returns the source-to-memory links for a source.
func (r *Recaller) Links(ctx context.Context, id source.SourceID) ([]Link, error) {
	return r.store.SourceLinks(ctx, id)
}
