package memories

import (
	"slices"
	"testing"
)

// TestParseAgentSpecSplitsEveryCommaIncludingInsideAFlagValue documents a real
// limitation: ParseAgentSpec's name=command[,arg...] syntax uses "," as the
// only argument separator, so it cannot represent an argument that itself
// contains a comma (such as a comma-separated flag value). This is why
// ResolveAgentSpec exists for reusing a DefaultAgents entry verbatim.
func TestParseAgentSpecSplitsEveryCommaIncludingInsideAFlagValue(t *testing.T) {
	agent, err := ParseAgentSpec("claude=claude,-p,--disallowedTools=Bash,Edit,Write")
	if err != nil {
		t.Fatal(err)
	}
	runner, ok := agent.runner.(execRunner)
	if !ok {
		t.Fatalf("runner type = %T, want execRunner", agent.runner)
	}
	got := runner.args
	want := []string{"-p", "--disallowedTools=Bash", "Edit", "Write"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v (the comma inside --disallowedTools's value is wrongly split)", got, want)
	}
}

func TestResolveAgentSpecBareNameReturnsMatchingDefaultVerbatim(t *testing.T) {
	agent, err := ResolveAgentSpec("claude")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name() != "claude" {
		t.Fatalf("name = %q, want claude", agent.Name())
	}

	cmdAgent, ok := agent.(CommandAgent)
	if !ok {
		t.Fatalf("agent type = %T, want CommandAgent", agent)
	}
	runner, ok := cmdAgent.runner.(execRunner)
	if !ok {
		t.Fatalf("runner type = %T, want execRunner", cmdAgent.runner)
	}

	got := runner.args
	want := []string{"-p", "--no-session-persistence", "--disallowedTools=Bash,Edit,Write"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want the default claude agent's exact args %#v (unsplit)", got, want)
	}
}

func TestResolveAgentSpecUnknownBareNameErrors(t *testing.T) {
	if _, err := ResolveAgentSpec("nope"); err == nil {
		t.Fatal("want error for a bare name that matches no default agent")
	}
}

func TestResolveAgentSpecWithCommandDelegatesToParseAgentSpec(t *testing.T) {
	agent, err := ResolveAgentSpec("x=/bin/echo,hello")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name() != "x" {
		t.Fatalf("name = %q, want x", agent.Name())
	}
}
