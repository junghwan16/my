package memory

import (
	"fmt"
	"strings"
)

// DefaultAgents returns the local ingest agents used when the CLI is given no
// explicit --agent flags: Claude Code, Codex, and pi, each run read-only.
func DefaultAgents() []Agent {
	return []Agent{
		NewCommandAgent("claude", "claude", "-p", "--no-session-persistence", "--disallowedTools=Bash,Edit,Write"),
		NewCommandAgent(
			"codex",
			"codex",
			"--ask-for-approval",
			"never",
			"exec",
			"--sandbox",
			"read-only",
			"--skip-git-repo-check",
		),
		NewCommandAgent("pi", "pi", "-p", "--no-tools", "--no-session"),
	}
}

// ParseAgentSpec parses a CLI agent spec of the form name=command[,arg...] into
// a CommandAgent.
func ParseAgentSpec(spec string) (CommandAgent, error) {
	name, commandSpec, ok := strings.Cut(spec, "=")
	if !ok || name == "" || commandSpec == "" {
		return CommandAgent{}, fmt.Errorf("invalid agent spec %q", spec)
	}

	parts := strings.Split(commandSpec, ",")
	for _, part := range parts {
		if part == "" {
			return CommandAgent{}, fmt.Errorf("invalid agent spec %q", spec)
		}
	}
	return NewCommandAgent(name, parts[0], parts[1:]...), nil
}
