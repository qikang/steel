package inventory

import (
	"os"
	"path/filepath"
	"strings"
)

// expandHome expands a leading "~" in a path to the current user's
// home directory. Returns the input unchanged when it doesn't start
// with "~" or when the home directory can't be determined.
func expandHome(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
