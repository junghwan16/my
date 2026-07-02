package memories_test

import (
	"context"
	"path/filepath"
	"testing"

	memoriespkg "github.com/junghwan16/gieok/internal/memory"
)

// A human Override changes the effective text a Memory returns while preserving
// its original agent text, and clearing it restores the original (ADR-0010).
func TestEditMemoryOverridesEffectiveTextAndPreservesOriginal(t *testing.T) {
	ctx := context.Background()
	sources, memories, closeStores := openStores(ctx, t, filepath.Join(t.TempDir(), "m.db"))
	defer closeStores()

	recordMemory(ctx, t, sources, memories, scopedSource("codex_session:a", "/work/a"), "memory:a", "원본 기억 텍스트")
	recaller := memoriespkg.NewRecaller(memories)

	res, found, err := recaller.EditMemory(ctx, "memory:a", "사람이 고친 텍스트")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("edit reported not-found for a saved memory, want found")
	}
	if res.Text != "사람이 고친 텍스트" {
		t.Fatalf("effective text = %q, want the override", res.Text)
	}
	if !res.Edited {
		t.Fatal("edited = false, want true after an override")
	}
	if res.OriginalText != "원본 기억 텍스트" {
		t.Fatalf("original_text = %q, want the agent's text", res.OriginalText)
	}

	// A plain recall reflects the override too.
	got, _, err := recaller.Get(ctx, "memory:a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "사람이 고친 텍스트" || !got.Edited {
		t.Fatalf("recall text/edited = %q/%v, want the override", got.Text, got.Edited)
	}

	// Clearing the override restores the agent's original text.
	res, _, err = recaller.EditMemory(ctx, "memory:a", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "원본 기억 텍스트" {
		t.Fatalf("after clear text = %q, want the original", res.Text)
	}
	if res.Edited {
		t.Fatal("edited = true after clearing, want false")
	}
	if res.OriginalText != "" {
		t.Fatalf("original_text = %q after clear, want empty", res.OriginalText)
	}

	// Editing an unknown Memory reports not-found rather than creating one.
	_, found, err = recaller.EditMemory(ctx, "memory:missing", "x")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("edit reported found for an unknown id, want not-found")
	}
}
