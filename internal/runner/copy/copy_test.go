package copy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveChmodMode covers the helper that picks a destination
// file/dir's permission bits: an explicit `mode:` from the
// playbook always wins; otherwise the source's perms are
// preserved (the `cp -p` behavior that keeps a +x script
// executable across the upload).
func TestResolveChmodMode(t *testing.T) {
	cases := []struct {
		name       string
		explicit   string
		sourcePerm os.FileMode
		want       os.FileMode
	}{
		{"explicit overrides source", "0600", 0o755, 0o600},
		{"empty explicit + executable source → preserve", "", 0o755, 0o755},
		{"empty explicit + non-executable source → preserve", "", 0o644, 0o644},
		{"explicit with 0o prefix", "0o755", 0o644, 0o755},
		{"empty explicit + zero source → 0 (caller decides fallback)", "", 0, 0},
		{"explicit garbage falls back to 0", "not-octal", 0o755, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveChmodMode(c.explicit, c.sourcePerm); got != c.want {
				t.Errorf("resolveChmodMode(%q, %o) = %o, want %o",
					c.explicit, c.sourcePerm, got, c.want)
			}
		})
	}
}

// TestResolveDirChmodMode exercises the small wrapper used at
// the root of a copyDir call, which has to stat the source
// itself because it has no DirEntry in hand yet.
func TestResolveDirChmodMode(t *testing.T) {
	dir := t.TempDir()
	// A source dir that lands as 0750 — different from the SFTP
	// default 0755 — so the test is meaningful.
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o750); err != nil {
		t.Fatal(err)
	}
	// No explicit mode → preserve the source 0750.
	if got := resolveDirChmodMode(src, ""); got != 0o750 {
		t.Errorf("resolveDirChmodMode(src, \"\") = %o, want 0750", got)
	}
	// Explicit mode wins over the source's 0750.
	if got := resolveDirChmodMode(src, "0700"); got != 0o700 {
		t.Errorf("resolveDirChmodMode(src, \"0700\") = %o, want 0700", got)
	}
	// Missing source: falls back to 0755 rather than erroring —
	// the previous chmod was best-effort, and we want to keep
	// that behavior.
	missing := filepath.Join(dir, "does-not-exist")
	if got := resolveDirChmodMode(missing, ""); got != 0o755 {
		t.Errorf("resolveDirChmodMode(missing, \"\") = %o, want 0755 (fallback)", got)
	}
}
