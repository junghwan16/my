package memories

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

const (
	maxPromptBytes  = 16_000
	maxPromptEvents = 40
	// maxRelatedQueryBytes bounds the text used to recall existing memory
	// related to a source, before that source is ingested.
	maxRelatedQueryBytes = 4_000
	// maxRelatedMemoryBytes bounds how much of the ingest prompt the related-
	// memory section may take, leaving the rest of maxPromptBytes for the
	// source's own sampled text.
	maxRelatedMemoryBytes = 4_000
	// maxRelatedItemBytes truncates any single related memory shown in the
	// prompt, so one long memory can't crowd out the others.
	maxRelatedItemBytes = 800
)

// runner sends a prompt to an external process and returns its stdout. It is the
// transport boundary of CommandAgent: it owns process spawning, stderr capture, and
// error wrapping, so CommandAgent's prompt building and output parsing can be
// tested without spawning a real process.
type runner interface {
	Run(ctx context.Context, prompt string) ([]byte, error)
}

// CommandAgent runs an external command to produce memories.
type CommandAgent struct {
	name   string
	runner runner
}

var _ Agent = CommandAgent{}

// NewCommandAgent returns an agent that appends the generated prompt to args.
func NewCommandAgent(name string, command string, args ...string) CommandAgent {
	return CommandAgent{
		name: name,
		runner: execRunner{
			name:    name,
			command: command,
			args:    append([]string(nil), args...),
		},
	}
}

// Name returns the configured agent name.
func (a CommandAgent) Name() string {
	return a.name
}

// Ingest builds the prompt, runs it through the transport, and parses stdout
// into memories.
func (a CommandAgent) Ingest(ctx context.Context, input AgentInput) (AgentOutput, error) {
	if a.name == "" {
		return AgentOutput{}, errors.New("agent name is empty")
	}
	if a.runner == nil {
		return AgentOutput{}, fmt.Errorf("agent %q has no runner", a.name)
	}

	stdout, err := a.runner.Run(ctx, buildIngestPrompt(input))
	if err != nil {
		return AgentOutput{}, err
	}

	text := strings.TrimSpace(string(stdout))
	if text == "" {
		return AgentOutput{}, nil
	}
	return AgentOutput{Memories: parseAgentMemories(text)}, nil
}

// execRunner is the default transport: it spawns the configured command with the
// prompt appended to its args and returns stdout, surfacing stderr on failure.
type execRunner struct {
	name    string
	command string
	args    []string
}

var _ runner = execRunner{}

func (r execRunner) Run(ctx context.Context, prompt string) ([]byte, error) {
	if r.command == "" {
		return nil, fmt.Errorf("agent %q command is empty", r.name)
	}

	args := append([]string(nil), r.args...)
	args = append(args, prompt)

	//nolint:gosec // The command is explicitly configured by the local CLI user.
	cmd := exec.CommandContext(ctx, r.command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run command %q: %w: %s", r.name, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout, nil
}

// parseAgentMemories interprets command stdout. A JSON array of memories lets
// agents emit multiple typed, metadata-tagged memories — including an empty array
// when the source holds nothing worth keeping (which the ingest prompt asks for),
// yielding zero memories. Only output that is NOT a JSON array falls back to a
// single summary of the raw text.
func parseAgentMemories(text string) []AgentMemory {
	if memories, isJSONArray := decodeAgentMemories(text); isJSONArray {
		return memories
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	return []AgentMemory{{Kind: MemoryKindSummary, Text: trimmed}}
}

// decodeAgentMemories reports whether text is a JSON array of memories and, if so,
// the non-empty ones it contains. isJSONArray is true whenever the text parses as
// a JSON array — even an empty one — so a valid "[]" ("nothing worth keeping")
// yields zero memories rather than being misread as prose and stored verbatim as
// a "[]" memory. It is false only when the text is not a JSON array (no leading
// "[" or a parse error), so genuine prose still falls back to a single summary.
func decodeAgentMemories(text string) (memories []AgentMemory, isJSONArray bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var raw []AgentMemory
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, false
	}
	out := make([]AgentMemory, 0, len(raw))
	for _, memory := range raw {
		if strings.TrimSpace(memory.Text) == "" {
			continue
		}
		if memory.Kind == "" {
			memory.Kind = MemoryKindSummary
		}
		out = append(out, memory)
	}
	return out, true
}

// ingestInstructions is the fixed leading section of every ingest prompt. It
// spells out what makes a memory worth keeping (useful recall: knowledge that
// changes a future agent's decision or action) and the exact JSON output
// contract the parser expects, so agents actually produce typed, multi-memory,
// linkable output instead of one prose blob. See CONTEXT.md for "Useful Recall".
const ingestInstructions = `You are distilling one coding-agent session into durable, reusable memory for a future agent working in this same workspace.

A good memory changes what a future agent decides or does. Capture things like: a decision and why it was made, a non-obvious fact about this codebase, a gotcha or constraint, a convention, or how a component works. Write each memory in your own words — do not transcribe or quote the session. Skip play-by-play narration, transient chatter, secrets, and anything a future agent could trivially re-derive.

Prefer a few focused, self-contained memories over one long dump; each should stand alone and be recallable on its own topic. If the session holds nothing worth reusing, output an empty array: [].

Output ONLY a JSON array of memory objects — no prose before or after:
[
  {
    "kind": "decision | fact | gotcha | convention | summary",
    "text": "the reusable knowledge, written in your own words",
    "relates_to": ["<id of an existing memory listed below>"]
  }
]
- "text" is required. "kind" is one short label from the list (default "summary"). "relates_to" is optional.
- Only put ids into relates_to that appear in brackets in the existing-memory list below; any other id is dropped.
`

func buildIngestPrompt(input AgentInput) string {
	var b strings.Builder
	b.WriteString(ingestInstructions)
	fmt.Fprintf(&b, "\nSource: %s in workspace %s (id %s)\n\n",
		input.Source.Kind, input.Source.Scope.Value, input.Source.ID)

	writeRelatedMemories(&b, input.RelatedMemories)

	b.WriteString("Session excerpt (evenly sampled across the whole session; reference only, do not quote it):\n")
	for _, event := range sampleEvents(substantiveEvents(input.Events), maxPromptEvents) {
		if b.Len() >= maxPromptBytes {
			break
		}
		fmt.Fprintf(&b, "- %s: %s\n", eventLabel(event), event.Text)
	}

	return truncateUTF8(b.String(), maxPromptBytes)
}

// writeRelatedMemories renders the existing memory recalled for this source
// (see Ingester.recallRelatedMemories) so an agent connects new memory to what
// is already known instead of ingesting the source in isolation.
func writeRelatedMemories(b *strings.Builder, related []RecallResult) {
	if len(related) == 0 {
		b.WriteString("Existing related memory: none yet — this is the first memory for this workspace context.\n\n")
		return
	}

	b.WriteString("Existing related memory for this workspace — build on it (confirm, extend, update, or " +
		"contradict it; don't repeat it). Each line starts with the memory id in brackets:\n")
	for _, recallResult := range related {
		if b.Len() >= maxRelatedMemoryBytes {
			break
		}
		text := truncateUTF8(recallResult.Text, maxRelatedItemBytes)
		fmt.Fprintf(b, "- [%s] (%s) %s\n", recallResult.MemoryID, recallResult.Kind, text)
	}
	b.WriteString("When a new memory continues one of these, put that memory's bracketed id in its relates_to.\n\n")
}

// relatedMemoryQuery builds the recall query text for a source before it is
// ingested, from the same substantive, evenly-sampled events buildIngestPrompt
// shows the agent, so the recall reflects the same material the agent will read.
func relatedMemoryQuery(events []sourcespkg.SourceEvent) string {
	var b strings.Builder
	for _, event := range sampleEvents(substantiveEvents(events), maxPromptEvents) {
		if b.Len() >= maxRelatedQueryBytes {
			break
		}
		b.WriteString(event.Text)
		b.WriteString("\n")
	}
	return truncateUTF8(strings.TrimSpace(b.String()), maxRelatedQueryBytes)
}

// substantiveEvents keeps only events carrying real text, dropping structural
// rows (session_meta, file-history-snapshot, empty response items, mode markers)
// that otherwise waste the prompt budget and dilute the evenly-sampled excerpt
// with noise like "response_item : response_item".
func substantiveEvents(events []sourcespkg.SourceEvent) []sourcespkg.SourceEvent {
	kept := make([]sourcespkg.SourceEvent, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Text) == "" {
			continue
		}
		kept = append(kept, event)
	}
	return kept
}

// eventLabel picks the most informative prefix for a sampled event line: the
// speaker role when present (user/assistant), otherwise the event type.
func eventLabel(event sourcespkg.SourceEvent) string {
	if event.Role != "" {
		return event.Role
	}
	return event.Type
}

// sampleEvents selects at most maxEvents events spread evenly across the session,
// from the first event to the last, preserving chronological order. Taking only
// the leading events made memory reflect a session's opening alone; sampling by
// an even stride makes one bounded prompt cover beginning, middle, and end so
// late-session topics are still summarized (and thus recallable).
func sampleEvents(events []sourcespkg.SourceEvent, maxEvents int) []sourcespkg.SourceEvent {
	if maxEvents <= 0 || len(events) == 0 {
		return nil
	}
	if len(events) <= maxEvents {
		return events
	}

	sampled := make([]sourcespkg.SourceEvent, 0, maxEvents)
	last := len(events) - 1
	prev := -1
	for i := range maxEvents {
		// Map slot i in [0, maxEvents-1] onto an index in [0, last] so the first
		// and last events are always included and the rest are evenly strided.
		idx := i * last / (maxEvents - 1)
		if idx == prev {
			continue
		}
		prev = idx
		sampled = append(sampled, events[idx])
	}
	return sampled
}

// truncateUTF8 caps s at limit bytes without splitting a multi-byte rune.
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	truncated := s[:limit]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
