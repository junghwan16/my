package sources

import "strings"

// WorkspaceKey derives a stable grouping key from a raw workspace path (a
// session's cwd). The raw path is a good provenance record but a poor grouping
// key: the same project shows up under many paths — git worktrees, Conductor
// workspaces, and directory renames — and exact-path matching then splits one
// project's memory across many Scopes, so Recall in one path finds nothing
// stored under another (see ADR-0009). WorkspaceKey collapses those variants to
// one canonical path so Recall can group by project instead of by literal path.
//
// It is a pure function of the path plus an alias table, so it is fully unit
// testable and can be recomputed if the rules change — the raw scope value is
// never mutated. Rules, applied in order:
//
//  1. strip a git-worktree suffix: "<root>/.claude/worktrees/<name>" -> "<root>"
//  2. collapse a Conductor workspace instance:
//     "<...>/conductor/workspaces/<proj>/<instance>[/...]" -> "<...>/conductor/workspaces/<proj>"
//  3. apply an exact-match rename alias (e.g. a renamed project directory).
//
// Trailing slashes are trimmed first. A path matching no rule is returned as-is
// (minus trailing slashes), so unaffected workspaces keep their own key.
func WorkspaceKey(raw string, aliases map[string]string) string {
	key := strings.TrimRight(strings.TrimSpace(raw), "/")
	key = stripWorktreeSuffix(key)
	key = collapseConductorInstance(key)
	if aliased, ok := aliases[key]; ok {
		key = aliased
	}
	return key
}

// stripWorktreeSuffix removes a "/.claude/worktrees/<name>[/...]" segment, so a
// repo's worktree shares the repo's key. Claude Code creates agent worktrees
// under "<repo>/.claude/worktrees/<name>".
func stripWorktreeSuffix(path string) string {
	const marker = "/.claude/worktrees/"
	if i := strings.Index(path, marker); i >= 0 {
		return path[:i]
	}
	return path
}

// collapseConductorInstance reduces a Conductor workspace path to its project
// level, dropping the per-instance leaf (and anything deeper) so that different
// instances of one project ("vienna", "adelaide", ...) share a key. Layout:
// ".../conductor/workspaces/<project>/<instance>[/...]".
func collapseConductorInstance(path string) string {
	const marker = "/conductor/workspaces/"
	i := strings.Index(path, marker)
	if i < 0 {
		return path
	}
	rest := path[i+len(marker):] // "<project>/<instance>[/...]"
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[0] == "" {
		return path // only a project, or malformed: leave as-is
	}
	return path[:i+len(marker)] + parts[0]
}

// DefaultScopeAliases maps renamed workspace paths to their canonical form, so a
// directory rename does not fragment a project's memory. The "my" -> "gieok"
// entry reflects this project's own rename (ADR-0009 / issue #21).
func DefaultScopeAliases() map[string]string {
	return map[string]string{
		"/Users/jeff.cho/personal/my": "/Users/jeff.cho/personal/gieok",
	}
}
