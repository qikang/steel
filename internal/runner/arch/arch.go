// Package arch probes each remote host's CPU architecture and
// memoizes the result for the lifetime of a single `steel apply` run.
//
// The probed value feeds {{ os_arch }} (and {{ host.os_arch }}) in
// playbook templates, and is also injected as `$os_arch` into every
// remote shell command. The cache keeps multi-play runs cheap: a
// host's arch is detected once, then re-used across all subsequent
// plays on the same host.
package arch

import (
	"fmt"
	"strings"
	"sync"

	"steel/internal/inventory"
	"steel/internal/runner/connect"
)

// archCache caches the per-host detected architecture for the
// lifetime of the running apply process. Keyed by host.Name (unique
// within the inventory). A failed detection is intentionally NOT
// cached — the next call retries, so a transient SSH blip doesn't
// permanently mask the host's arch for the rest of the run.
var archCache sync.Map // map[host.Name]string (amd64 | arm64)

// archDetectFn is the SSH-side probe behind Of. Tests override this
// variable to inject a fake without standing up a real SSH endpoint;
// production uses defaultArchDetect which SSHes in and runs
// `uname -m`.
var archDetectFn = defaultArchDetect

// Of returns the normalized architecture (amd64 / arm64) for host
// h. The first call per host opens an SSH connection, runs
// `uname -m`, and maps the kernel arch to Go-style naming;
// subsequent calls for the same host.Name hit the cache. Detection
// errors are NOT cached so a flaky probe doesn't poison later plays.
//
// `uname -m` is the canonical way to ask "what arch is this
// machine"; any value other than x86_64 / aarch64 surfaces
// immediately as an error rather than silently flowing through into
// a binary URL that the host can't actually fetch.
func Of(h *inventory.Host) (string, error) {
	if cached, ok := archCache.Load(h.Name); ok {
		return cached.(string), nil
	}
	raw, err := archDetectFn(h)
	if err != nil {
		return "", err
	}
	arch, err := unameToArch(raw)
	if err != nil {
		return "", err
	}
	archCache.Store(h.Name, arch)
	return arch, nil
}

// unameToArch maps the raw output of `uname -m` to Go-style arch
// naming (x86_64 → amd64, aarch64 → arm64). Anything else
// (ppc64le, riscv64, a sandbox-reported empty string, ...) is an
// error so the caller fails fast instead of building an URL for a
// binary the host can't actually run.
//
// Only the first line of raw is consulted — some BusyBox /
// coreutils builds tack extra output after `uname -m` (e.g. under
// `-i`); taking only the first line is the robust thing to do.
func unameToArch(raw string) (string, error) {
	line := strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
	switch line {
	case "x86_64":
		return "amd64", nil
	case "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q (supported: x86_64, aarch64)", line)
	}
}

// defaultArchDetect SSHes into h and runs `uname -m`. The session
// is short-lived (single Output call, no PTY) so the cost is one
// dial + one exec per host per apply. No PTY is requested: uname
// is not a TTY-bound command, and avoiding RequestPty keeps the
// path simple on hardened images that deny it.
func defaultArchDetect(h *inventory.Host) (string, error) {
	conn, err := connect.Connect(h)
	if err != nil {
		return "", fmt.Errorf("arch: connect %s: %w", h.Name, err)
	}
	defer conn.Close()

	sess, err := conn.Client.NewSession()
	if err != nil {
		return "", fmt.Errorf("arch: session %s: %w", h.Name, err)
	}
	defer sess.Close()

	out, err := sess.Output("uname -m")
	if err != nil {
		return "", fmt.Errorf("arch: uname -m on %s: %w", h.Name, err)
	}
	return string(out), nil
}
