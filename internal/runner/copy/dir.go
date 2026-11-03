// Recursive directory copy. Walks the local source tree and
// recreates it on the remote, calling copyFile for each leaf and
// ensuring + chmoding each intermediate directory.
package copy

import (
	"fmt"
	"os"
	"path/filepath"

	"steel/internal/inventory"
	"steel/internal/runner/connect"
)

// copyDir walks src and uploads every file and subdir to dest on
// host h, preserving the source's perms (or applying the
// explicit `mode:` from the playbook). The root destination is
// created and chmod-ed before the walk so a partial copy that
// fails halfway still leaves the user with a recognizable
// directory on the remote.
func copyDir(h *inventory.Host, src, dest, mode string) error {
	conn, err := connect.Connect(h)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := ensureRemoteDir(conn.SFTP, dest); err != nil {
		return err
	}

	// chmod the root directory to the source's perms (or the
	// explicit override), so the destination's dir perms match
	// what `cp -p` would produce instead of the SFTP default
	// 0755.
	if err := conn.SFTP.Chmod(dest, resolveDirChmodMode(src, mode)); err != nil {
		return fmt.Errorf("chmod %q: %w", dest, err)
	}

	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		remote := filepath.ToSlash(filepath.Join(dest, rel))

		if d.IsDir() {
			if err := ensureRemoteDir(conn.SFTP, remote); err != nil {
				return err
			}
			// Per-subdir chmod: also preserve source perms. We
			// pull the DirInfo to read the source dir's mode bits
			// here.
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := conn.SFTP.Chmod(remote, resolveChmodMode(mode, info.Mode().Perm())); err != nil {
				return fmt.Errorf("chmod %q: %w", remote, err)
			}
			return nil
		}
		return copyFile(h, path, remote, mode)
	})
}
