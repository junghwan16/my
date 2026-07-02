package sources

import "testing"

func TestWorkspaceKey(t *testing.T) {
	aliases := DefaultScopeAliases()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain path unchanged",
			raw:  "/Users/jeff.cho/Projects/adserver",
			want: "/Users/jeff.cho/Projects/adserver",
		},
		{
			name: "worktree collapses to repo root",
			raw:  "/Users/jeff.cho/personal/gieok/.claude/worktrees/modular-percolating-tulip",
			want: "/Users/jeff.cho/personal/gieok",
		},
		{
			name: "worktree of another repo",
			raw:  "/Users/jeff.cho/Projects/adserver/.claude/worktrees/velvety-yawning-meteor",
			want: "/Users/jeff.cho/Projects/adserver",
		},
		{
			name: "rename alias applied",
			raw:  "/Users/jeff.cho/personal/my",
			want: "/Users/jeff.cho/personal/gieok",
		},
		{
			name: "worktree under renamed root still unifies via alias",
			raw:  "/Users/jeff.cho/personal/my/.claude/worktrees/x",
			want: "/Users/jeff.cho/personal/gieok",
		},
		{
			name: "conductor instance collapses to project",
			raw:  "/Users/jeff.cho/conductor/workspaces/adserver/vienna",
			want: "/Users/jeff.cho/conductor/workspaces/adserver",
		},
		{
			name: "conductor sibling instance shares the key",
			raw:  "/Users/jeff.cho/conductor/workspaces/adserver/adelaide",
			want: "/Users/jeff.cho/conductor/workspaces/adserver",
		},
		{
			name: "conductor deeper path still collapses to project",
			raw:  "/Users/jeff.cho/conductor/workspaces/gitploy/louisville/sub/dir",
			want: "/Users/jeff.cho/conductor/workspaces/gitploy",
		},
		{
			name: "trailing slash trimmed",
			raw:  "/Users/jeff.cho/Projects/adserver/",
			want: "/Users/jeff.cho/Projects/adserver",
		},
		{
			name: "empty stays empty",
			raw:  "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkspaceKey(tc.raw, aliases); got != tc.want {
				t.Fatalf("WorkspaceKey(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestWorkspaceKeyUnifiesRenameAndWorktree is the regression guard for the
// concrete fragmentation that made Recall miss this project's own memory: the
// canonical path, the pre-rename path, and an agent worktree must all map to one
// key so their memory is recalled together.
func TestWorkspaceKeyUnifiesRenameAndWorktree(t *testing.T) {
	aliases := DefaultScopeAliases()
	canonical := WorkspaceKey("/Users/jeff.cho/personal/gieok", aliases)
	renamed := WorkspaceKey("/Users/jeff.cho/personal/my", aliases)
	worktree := WorkspaceKey("/Users/jeff.cho/personal/gieok/.claude/worktrees/abc", aliases)

	if canonical != renamed || canonical != worktree {
		t.Fatalf("keys diverged: canonical=%q renamed=%q worktree=%q", canonical, renamed, worktree)
	}
}
