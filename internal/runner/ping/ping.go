// Package ping implements the connectivity probe used by the
// `steel ping` command and (transitively) the `ping` test path
// for the runner. Probe is the only public entry point: it
// does a real SSH handshake, runs `echo pong`, and returns the
// round-trip time. The connection is closed before returning —
// we don't keep it open because ping is purely a probe (the
// next module — apply's first play — will reconnect with the
// same credentials).
package ping

import (
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"steel/internal/inventory"
	"steel/internal/runner/connect"
)

// detectFn is the SSH-side probe behind Probe. Tests override
// this variable to inject a fake without standing up a real SSH
// endpoint; production uses defaultDetect which SSHes in and
// runs `echo pong`, then verifies the stdout matches.
var detectFn = defaultDetect

// Probe does a real SSH handshake against h, runs `echo pong`,
// and returns the round-trip time.
//
// The reported error chain is layered for readability:
//   - auth / network failures at the dial stage → "ssh user@addr: ..."
//   - session open failures → "session <host>: ..."
//   - non-zero exit or transport error from `echo pong` → "exec on <host>: ..."
//   - the rare case of `echo pong` succeeding but stdout not
//     matching "pong" → "unexpected echo on <host>: got %q, want %q"
//
// timeout bounds the SSH dial — it's the user's "give up on
// this host" deadline. The exec itself is bounded by the SSH
// session's internal deadlines and is fast in practice.
func Probe(h *inventory.Host, timeout time.Duration) (time.Duration, error) {
	return detectFn(h, timeout)
}

// defaultDetect is the production probe implementation. It
// opens a fresh SSH client with the user-supplied timeout
// (rather than calling the shared Connect helper, which has a
// hardcoded 15s timeout) so a `steel ping --timeout 1s` actually
// fails fast against a host that drops SYNs.
//
// The auth methods come from connect.AuthMethods — the same
// ssh_key > password > ssh-agent priority that Connect uses, so
// the probe is consistent with the apply-side path. We don't
// call connect.Connect itself because that helper pins a 15s
// dial deadline.
func defaultDetect(h *inventory.Host, timeout time.Duration) (time.Duration, error) {
	start := time.Now()

	methods, err := connect.AuthMethods(h)
	if err != nil {
		return time.Since(start), err
	}

	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // steel is an ops tool for trusted fleets
		Timeout:         timeout,
	}

	addr := net.JoinHostPort(h.IP, fmt.Sprintf("%d", h.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return time.Since(start), fmt.Errorf("ssh %s@%s: %w", h.User, addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return time.Since(start), fmt.Errorf("session %s: %w", h.Name, err)
	}
	defer sess.Close()

	// `echo pong` is the canonical sentinel: trivial to type, no
	// shell expansion, no PATH dependency, and matches what the
	// user asked for verbatim. We don't run a login shell
	// (`bash -l -c`) because ping is a connectivity check, not a
	// remote-execution one — bypassing the shell also keeps the
	// probe independent of the remote /etc/passwd / shell
	// configuration.
	out, err := sess.Output("echo pong")
	if err != nil {
		return time.Since(start), fmt.Errorf("exec on %s: %w", h.Name, err)
	}
	got := strings.TrimSpace(string(out))
	if got != "pong" {
		return time.Since(start), fmt.Errorf("unexpected echo on %s: got %q, want %q", h.Name, got, "pong")
	}
	return time.Since(start), nil
}
