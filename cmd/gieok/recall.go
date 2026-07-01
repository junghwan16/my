package main

import (
	"encoding/json"
	"fmt"
	"io"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
)

// writeRecallJSON emits the recall result as a stable structured document. The
// structure is derived directly from memories.RecallResult so the MCP recall tool
// can reuse it as structured tool content.
func writeRecallJSON(w io.Writer, recallResults []memoriespkg.RecallResult) error {
	if recallResults == nil {
		recallResults = []memoriespkg.RecallResult{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"memories": recallResults}); err != nil {
		return fmt.Errorf("write recall json: %w", err)
	}
	return nil
}

// writeRecallText renders a concise, human-readable recall result. Each memory
// shows its identifier and Source context so a reader can inspect evidence
// without opening SQLite. Empty results are stated explicitly.
func writeRecallText(w io.Writer, recallResults []memoriespkg.RecallResult) error {
	if len(recallResults) == 0 {
		if _, err := fmt.Fprintln(w, "no matching memory"); err != nil {
			return fmt.Errorf("write recall summary: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintf(w, "found %d memory\n", len(recallResults)); err != nil {
		return fmt.Errorf("write recall summary: %w", err)
	}
	for _, rec := range recallResults {
		if err := writeRecallResult(w, rec); err != nil {
			return err
		}
	}
	return nil
}

// writeRecallResult renders one recalled memory: its ID, agent, kind, creation
// time, text, and the Source(s) it came from.
func writeRecallResult(w io.Writer, rec memoriespkg.RecallResult) error {
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
		if _, err := fmt.Fprintf(w, "  from source %s  scope=%s  %s\n", src.ID, src.Scope.Value, src.URI); err != nil {
			return fmt.Errorf("write recalled source: %w", err)
		}
	}
	return nil
}
