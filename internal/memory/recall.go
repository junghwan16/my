package memories

import (
	"time"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// SourceRef is the Source context carried alongside a recalled Memory: enough to
// explain where the Memory came from and to feed a later read, without loading
// the whole Source. A Memory can derive from more than one Source, so a
// RecallResult holds a slice of these.
type SourceRef struct {
	ID    sourcespkg.SourceID `json:"id"`
	URI   string              `json:"uri"`
	Scope sourcespkg.Scope    `json:"scope"`
}

// RecallResult is one result of a Recall: a Memory plus the Source context it
// was linked from. It is a plain domain result so the CLI and MCP recall tool
// can share the same structure. Sources is ordered by Source ID
// for stable output.
type RecallResult struct {
	MemoryID  MemoryID    `json:"memory_id"`
	Agent     string      `json:"agent"`
	Kind      MemoryKind  `json:"kind"`
	Text      string      `json:"text"`
	CreatedAt time.Time   `json:"created_at"`
	Sources   []SourceRef `json:"sources"`
}
