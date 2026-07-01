package memories

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
// a CommandAgent. Because "," both separates arguments and could appear inside
// one (e.g. a comma-separated flag value), a spec cannot represent an argument
// containing a comma; use ResolveAgentSpec with a bare default agent name
// instead when that argument is one of DefaultAgents.
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

// ResolveAgentSpec resolves a CLI --agent spec into an Agent. A bare name
// (no "=") that matches one of DefaultAgents — "claude", "codex", or "pi" —
// returns that built-in agent verbatim, args and all. This lets a caller pin
// production ingest to a subset of the defaults (e.g. --agent claude) without
// reconstructing their exact command through ParseAgentSpec's comma-delimited
// syntax, which cannot represent an argument like
// --disallowedTools=Bash,Edit,Write that itself contains commas. Any other
// spec is parsed as name=command[,arg...] via ParseAgentSpec.
func ResolveAgentSpec(spec string) (Agent, error) {
	if !strings.Contains(spec, "=") {
		for _, agent := range DefaultAgents() {
			if agent.Name() == spec {
				return agent, nil
			}
		}
		return nil, fmt.Errorf("agent %q is not a default agent name (claude, codex, pi) "+
			"and is not a name=command[,arg...] spec", spec)
	}
	return ParseAgentSpec(spec)
}
