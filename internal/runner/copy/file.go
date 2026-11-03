// Single-file copy: open the local file, ensure the remote
// parent directory exists, stream the file to SFTP, and apply
// the destination mode. The recursive walk that calls this lives
// in dir.go; this file is the leaf node of the copy tree.
package copy

import (
	"fmt"
	"os"
	"path/filepath"

	"steel/internal/inventory"
	"steel/internal/runner/connect"
)

// copyFile uploads a single file src to dest on host h, creating
// the parent directory on the remote if needed and applying the
// destination mode (either explicit from the playbook or
// preserved from the source).
func copyFile(h *inventory.Host, src, dest, mode string) error {
	conn, err := connect.Connect(h)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := ensureRemoteDir(conn.SFTP, filepath.Dir(dest)); err != nil {
		return err
	}

	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	rf, err := conn.SFTP.Create(dest)
	if err != nil {
		return fmt.Errorf("sftp create %q: %w", dest, err)
	}
	defer rf.Close()

	if err := connect.Stream(rf, f, stat.Size(), nil); err != nil {
		return fmt.Errorf("sftp write %q: %w", dest, err)
	}

	// Always chmod — even when the resolved mode happens to
	// match the SFTP default — so the on-disk mode exactly
	// mirrors what the user (or the source) said. resolveChmodMode
	// picks the explicit value when set, otherwise preserves the
	// source's perms.
	if err := conn.SFTP.Chmod(dest, resolveChmodMode(mode, stat.Mode().Perm())); err != nil {
		return fmt.Errorf("chmod %q: %w", dest, err)
	}
	return nil
}
