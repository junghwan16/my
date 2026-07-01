package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

const (
	maxPromptBytes  = 12_000
	maxPromptEvents = 40
)

// runner sends a prompt to an external process and returns its stdout. It is the
// transport seam of CommandAgent: it owns process spawning, stderr capture, and
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
	b.WriteString("Ingest this source into durable memory.\n")
	fmt.Fprintf(&b, "Source ID: %s\n", input.Source.ID)
	fmt.Fprintf(&b, "Source kind: %s\n", input.Source.Kind)
	fmt.Fprintf(&b, "Source URI: %s\n", input.Source.URI)
	fmt.Fprintf(&b, "Workspace: %s\n\n", input.Source.Scope.Value)
	b.WriteString("Relevant events:\n")

	for i, event := range input.Events {
		if i >= maxPromptEvents || b.Len() >= maxPromptBytes {
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
