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

// parseAgentMemories interprets command stdout. A JSON array of memories lets agents
// emit multiple typed, metadata-tagged memories; any other output is stored as a
// single summary.
func parseAgentMemories(text string) []AgentMemory {
	if memories, ok := decodeAgentMemories(text); ok {
		return memories
	}
	return []AgentMemory{{Kind: MemoryKindSummary, Text: text}}
}

func decodeAgentMemories(text string) ([]AgentMemory, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var raw []AgentMemory
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, false
	}
	memories := make([]AgentMemory, 0, len(raw))
	for _, memory := range raw {
		if strings.TrimSpace(memory.Text) == "" {
			continue
		}
		if memory.Kind == "" {
			memory.Kind = MemoryKindSummary
		}
		memories = append(memories, memory)
	}
	if len(memories) == 0 {
		return nil, false
	}
	return memories, true
}

func buildIngestPrompt(input AgentInput) string {
	var b strings.Builder
	b.WriteString("Turn this source into durable memory.\n")
	b.WriteString("A Memory is a short, reusable note a future coding agent can act on. Do not copy " +
		"the session text; explain the useful lesson in your own words.\n")
	fmt.Fprintf(&b, "Source ID: %s\n", input.Source.ID)
	fmt.Fprintf(&b, "Source kind: %s\n", input.Source.Kind)
	fmt.Fprintf(&b, "Source URI: %s\n", input.Source.URI)
	fmt.Fprintf(&b, "Workspace: %s\n\n", input.Source.Scope.Value)

	writeRelatedMemories(&b, input.RelatedMemories)

	b.WriteString("Source sample (reference only; do not quote it verbatim):\n")
	for _, event := range sampleEvents(input.Events, maxPromptEvents) {
		if b.Len() >= maxPromptBytes {
			break
		}
		text := event.Text
		if text == "" {
			text = event.Type
		}
		fmt.Fprintf(&b, "- %s %s: %s\n", event.Type, event.Role, text)
	}

	return truncateUTF8(b.String(), maxPromptBytes)
}

// writeRelatedMemories renders the existing memory recalled for this source
// (see Ingester.recallRelatedMemories) so an agent connects new memory to what
// is already known instead of ingesting the source in isolation.
func writeRelatedMemories(b *strings.Builder, related []RecallResult) {
	if len(related) == 0 {
		b.WriteString("Existing related memory: none yet; this is the first memory for this context.\n\n")
		return
	}

	b.WriteString("Existing related memory (already known; connect this source to it, don't repeat it):\n")
	for _, recallResult := range related {
		if b.Len() >= maxRelatedMemoryBytes {
			break
		}
		text := truncateUTF8(recallResult.Text, maxRelatedItemBytes)
		fmt.Fprintf(b, "- [%s] (%s, %s) %s\n", recallResult.MemoryID, recallResult.Agent, recallResult.Kind, text)
	}
	b.WriteString("Confirm, extend, update, or contradict the above where this source bears on it; " +
		"otherwise call out only what is genuinely new.\n\n")
}

// relatedMemoryQuery builds the recall query text for a source before it is
// ingested, from the same evenly-sampled events buildIngestPrompt shows the
// agent, so the recall reflects the same material the agent will read.
func relatedMemoryQuery(events []sourcespkg.SourceEvent) string {
	var b strings.Builder
	for _, event := range sampleEvents(events, maxPromptEvents) {
		if b.Len() >= maxRelatedQueryBytes {
			break
		}
		text := event.Text
		if text == "" {
			text = event.Type
		}
		b.WriteString(text)
		b.WriteString("\n")
	}
	return truncateUTF8(strings.TrimSpace(b.String()), maxRelatedQueryBytes)
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
