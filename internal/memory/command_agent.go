package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

const (
	maxPromptBytes  = 12_000
	maxPromptEvents = 40
)

// CommandAgent runs an external command to produce memory items.
type CommandAgent struct {
	name    string
	command string
	args    []string
}

// NewCommandAgent returns an agent that appends the generated prompt to args.
func NewCommandAgent(name string, command string, args ...string) CommandAgent {
	return CommandAgent{
		name:    name,
		command: command,
		args:    append([]string(nil), args...),
	}
}

// Name returns the configured agent name.
func (a CommandAgent) Name() string {
	return a.name
}

// Ingest runs the command and stores stdout as a summary item.
func (a CommandAgent) Ingest(ctx context.Context, input AgentInput) (AgentOutput, error) {
	if a.name == "" {
		return AgentOutput{}, fmt.Errorf("agent name is empty")
	}
	if a.command == "" {
		return AgentOutput{}, fmt.Errorf("agent %q command is empty", a.name)
	}

	prompt := buildIngestPrompt(input)
	args := append([]string(nil), a.args...)
	args = append(args, prompt)

	//nolint:gosec // The command is explicitly configured by the local CLI user.
	cmd := exec.CommandContext(ctx, a.command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return AgentOutput{}, fmt.Errorf("run command %q: %w: %s", a.name, err, bytes.TrimSpace(stderr.Bytes()))
	}

	text := strings.TrimSpace(string(stdout))
	if text == "" {
		return AgentOutput{}, nil
	}
	return AgentOutput{Items: parseAgentItems(text)}, nil
}

// parseAgentItems interprets command stdout. A JSON array of items lets agents
// emit multiple typed, metadata-tagged memories; any other output is stored as a
// single summary.
func parseAgentItems(text string) []AgentItem {
	if items, ok := decodeAgentItems(text); ok {
		return items
	}
	return []AgentItem{{Kind: ItemKindSummary, Text: text}}
}

func decodeAgentItems(text string) ([]AgentItem, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var raw []AgentItem
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, false
	}
	items := make([]AgentItem, 0, len(raw))
	for _, item := range raw {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		if item.Kind == "" {
			item.Kind = ItemKindSummary
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, false
	}
	return items, true
}

func buildIngestPrompt(input AgentInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ingest this source into durable memory.\n")
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

// truncateUTF8 caps s at max bytes without splitting a multi-byte rune.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	truncated := s[:max]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
