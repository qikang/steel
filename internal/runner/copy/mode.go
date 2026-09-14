// Permission-bit helpers used by the copy module.
//
// `mode:` from the playbook always wins; otherwise the source's
// perms are preserved so a +x script keeps +x on the remote (the
// `cp -p` behavior). parseMode accepts both "0644" and "0o644"
// prefixes so the playbook can be written in either form.
package copy

import (
	"os"
	"strings"
)

// resolveChmodMode picks the chmod target for a file or subdir.
// Explicit `mode:` from the playbook always wins; otherwise the
// source entry's permission bits are preserved so a +x script
// keeps +x on the remote.
func resolveChmodMode(explicit string, sourcePerm os.FileMode) os.FileMode {
	if explicit != "" {
		return parseMode(explicit)
	}
	return sourcePerm
}

// resolveDirChmodMode is a tiny convenience over resolveChmodMode
// that stats the source dir itself; used for the root dir of a
// copyDir call where we don't yet have a DirEntry in hand.
func resolveDirChmodMode(src, explicit string) os.FileMode {
	si, err := os.Stat(src)
	if err != nil {
		// Fall back to 0755 (the SFTP default) on stat failure
		// rather than failing the whole copy. The previous
		// behavior also silently ignored chmod errors here.
		return resolveChmodMode(explicit, 0o755)
	}
	return resolveChmodMode(explicit, si.Mode().Perm())
}

// parseMode accepts "0644" and "0o644" — strip the leading 0/o
// and parse octal. Garbage input parses to 0, which the caller
// can then re-decide to ignore or surface; we don't error out
// here because the explicit mode is best-effort.
func parseMode(s string) os.FileMode {
	// Accept "0644" and "0o644" — strip the leading 0/o and parse
	// octal.
	s = strings.TrimPrefix(s, "0o")
	s = strings.TrimPrefix(s, "0O")
	s = strings.TrimPrefix(s, "0")
	if s == "" {
		return 0
	}
	var m uint64
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0
		}
		m = m*8 + uint64(c-'0')
	}
	return os.FileMode(m)
}
