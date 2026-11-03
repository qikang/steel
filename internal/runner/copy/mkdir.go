// Remote-directory helper used by the copy module. SFTP has no
// "mkdir -p" primitive of its own, so we wrap sftp.MkdirAll and
// treat "already exists" as the success case for an idempotent
// deploy.
//
// Kept in its own file because the error-typing logic is the
// only thing that ties copy together across an idempotent
// re-run, and it deserves a focused home.
package copy

import (
	"fmt"
	"strings"

	"github.com/pkg/sftp"
)

// ensureRemoteDir mkdir -p's the path on the remote host.
//
// The trivial-path short-circuits (empty, ".", "/") avoid spurious
// MkdirAll calls that some SFTP servers reject. "Already exists"
// is treated as success because re-running a copy against a
// target whose parent already exists is the normal idempotent
// deploy case; only true mkdir failures surface to the caller.
func ensureRemoteDir(sftpClient *sftp.Client, path string) error {
	if path == "" || path == "." || path == "/" {
		return nil
	}
	// sftp.MkdirAll creates intermediate directories. We only
	// fail on "already exists" — that's the success case for an
	// idempotent deploy.
	if err := sftpClient.MkdirAll(path); err != nil {
		if !isExistErr(err) {
			return fmt.Errorf("mkdir -p %q: %w", path, err)
		}
	}
	return nil
}

func isExistErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "file exists")
}
