package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/junghwan16/my/internal/memory"
)

// writeRecallJSON emits the recall result as a stable structured document. The
// shape is derived directly from memory.Recollection so a future MCP
// memory.recall tool can reuse it as structured tool content.
func writeRecallJSON(w io.Writer, recollections []memory.Recollection) error {
	if recollections == nil {
		recollections = []memory.Recollection{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"memories": recollections}); err != nil {
		return fmt.Errorf("write recall json: %w", err)
	}
	return nil
}

// writeRecallText renders a concise, human-readable recall result. Each memory
// shows its identifier and Source context so a reader can inspect evidence
// without opening SQLite. Empty results are stated explicitly.
func writeRecallText(w io.Writer, recollections []memory.Recollection) error {
	if len(recollections) == 0 {
		if _, err := fmt.Fprintln(w, "no memory recalled"); err != nil {
			return fmt.Errorf("write recall summary: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintf(w, "recalled %d memory\n", len(recollections)); err != nil {
		return fmt.Errorf("write recall summary: %w", err)
	}
	for _, rec := range recollections {
		if err := writeRecollection(w, rec); err != nil {
			return err
		}
	}
	return nil
}

// writeRecollection renders one recalled memory: its ID, agent, kind, creation
// time, text, and the Source(s) it came from.
func writeRecollection(w io.Writer, rec memory.Recollection) error {
	if _, err := fmt.Fprintf(
		w,
		"\n%s  [%s/%s]  %s\n%s\n",
		rec.MemoryID,
		rec.Agent,
		rec.Kind,
		rec.CreatedAt.Format("2006-01-02 15:04:05"),
		rec.Text,
	); err != nil {
		return fmt.Errorf("write recalled memory: %w", err)
	}
	for _, src := range rec.Sources {
		if _, err := fmt.Fprintf(w, "  source %s  scope=%s  %s\n", src.ID, src.Scope.Value, src.URI); err != nil {
			return fmt.Errorf("write recalled source: %w", err)
		}
	}
	return nil
}
