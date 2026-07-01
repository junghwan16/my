package memory

import (
	"time"

	"github.com/junghwan16/my/internal/source"
)

// SourceRef is the Source context carried alongside a recalled Memory: enough to
// explain where the Memory came from and to feed a later read, without loading
// the whole Source. A Memory can derive from more than one Source, so a
// Recollection holds a slice of these.
type SourceRef struct {
	ID    source.SourceID `json:"id"`
	URI   string          `json:"uri"`
	Scope source.Scope    `json:"scope"`
}

// Recollection is one result of a Recall: a Memory plus the Source context it
// was linked from. It is a plain domain result so the CLI, and a later MCP
// memory.recall tool, can share the same shape. Sources is ordered by Source ID
// for stable output.
type Recollection struct {
	MemoryID  MemoryID    `json:"memory_id"`
	Agent     string      `json:"agent"`
	Kind      MemoryKind  `json:"kind"`
	Text      string      `json:"text"`
	CreatedAt time.Time   `json:"created_at"`
	Sources   []SourceRef `json:"sources"`
}
