// Quote helpers used by the shell module: shellQuote turns an
// arbitrary string into a single-quoted shell literal, and
// applyChdir wraps a user command in `cd <dir> && ...` so the
// remote shell is in the right working directory before the user's
// command runs.
//
// Kept in its own file because the quoting rules are the easiest
// part of the module to test in isolation (no SSH involved) and
// the rest of the shell module leans on these primitives.
package shell

import "strings"

// shellQuote wraps s in single quotes and escapes any embedded
// single quotes, producing a string that survives all three of
// shell word-split, variable expansion, and command substitution.
//
// An empty s becomes "”" rather than "" — the empty literal in
// shell is an unquoted token that disappears after word-split, so
// the explicit pair of quotes is the only way to keep the
// assignment alive (`export x=` would set x to nothing, but
// `export x=”` sets x to the empty string).
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Single-quote everything; an embedded ' becomes '\''.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// applyChdir prepends `cd <dir> && ` to cmd, shell-quoting dir so
// paths with spaces or other shell-special characters survive. The
// chdir happens inside the same login shell that runs cmd, so
// profile-sourced aliases and $PATH are already in scope. If the
// cd itself fails, the && short-circuits and the user's command
// never runs — which is the expected ansible-style behavior.
func applyChdir(cmd, dir string) string {
	return "cd " + shellQuote(dir) + " && " + cmd
}
