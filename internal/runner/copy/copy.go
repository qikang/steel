// Package copy implements the `copy` module: upload a local file
// or directory tree to a remote host via SFTP, preserving
// permission bits (`cp -p` semantics) unless the playbook sets
// an explicit `mode:`.
//
// Run is the only public entry point. It renders the playbook's
// src/dest strings through the vars template engine, stats the
// local path, and dispatches to copyFile (single file) or
// copyDir (recursive walk). The non-trivial helpers live in
// sibling files in this package — file.go, dir.go, mode.go,
// mkdir.go — so copy.go itself stays focused on the dispatch
// and the template rendering.
package copy

import (
	"fmt"
	"os"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/vars"
)

// Run uploads src on the local filesystem to dest on a remote
// host. Directories are walked recursively; the destination is
// created if missing. When the playbook sets `mode:`, that mode
// is applied to every file and directory; otherwise the source
// file/dir's permission bits are preserved so the upload behaves
// like `cp -p` — a script with +x in the source lands executable
// on the remote without needing an explicit mode in the playbook.
func Run(h *inventory.Host, spec *playbook.CopySpec, ctx *vars.Context) error {
	src, err := vars.Render(spec.Src, ctx)
	if err != nil {
		return err
	}
	dest, err := vars.Render(spec.Dest, ctx)
	if err != nil {
		return err
	}

	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("copy %q: %w", src, err)
	}
	if info.IsDir() {
		return copyDir(h, src, dest, spec.Mode)
	}
	return copyFile(h, src, dest, spec.Mode)
}
