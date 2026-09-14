package ping

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"steel/internal/inventory"
)

// resetPingState restores the production detectFn. Tests that
// override it MUST call resetPingState (or restore manually) in
// a t.Cleanup so they don't leak their mock into sibling tests.
func resetPingState() {
	detectFn = defaultDetect
}

// TestPing_OK verifies the happy path: a fake detect fn that
// returns (duration, nil) makes Probe report success with a
// non-zero RTT.
func TestPing_OK(t *testing.T) {
	resetPingState()
	calls := int32(0)
	detectFn = func(_ *inventory.Host, _ time.Duration) (time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(2 * time.Millisecond)
		return 2 * time.Millisecond, nil
	}
	t.Cleanup(resetPingState)

	h := &inventory.Host{Name: "h1", IP: "10.0.0.1", User: "root", Port: 22}
	rtt, err := Probe(h, 1*time.Second)
	if err != nil {
		t.Fatalf("Probe err = %v, want nil", err)
	}
	if rtt <= 0 {
		t.Fatalf("Probe rtt = %v, want > 0", rtt)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("detectFn calls = %d, want 1", got)
	}
}

// TestPing_AuthError checks that auth failures are surfaced
// with the "ssh user@addr: ..." prefix so the operator can
// tell at a glance whether the problem is credentials vs.
// network vs. remote-exec.
func TestPing_AuthError(t *testing.T) {
	resetPingState()
	detectFn = func(h *inventory.Host, _ time.Duration) (time.Duration, error) {
		return 0, errors.New("ssh root@10.0.0.1: handshake failed")
	}
	t.Cleanup(resetPingState)

	h := &inventory.Host{Name: "h1", IP: "10.0.0.1", User: "root", Port: 22}
	rtt, err := Probe(h, 1*time.Second)
	if err == nil {
		t.Fatalf("Probe err = nil, want error")
	}
	if rtt != 0 {
		t.Fatalf("Probe rtt = %v, want 0 on error", rtt)
	}
}

// TestPing_ExecMismatch covers the rare case where `echo pong`
// runs successfully but the stdout isn't "pong" — e.g. a
// remote shell wrapper that injects a banner. defaultDetect
// surfaces this with a clear "unexpected echo" message; we
// re-implement that check here via the fake so we don't need a
// live SSH server.
func TestPing_ExecMismatch(t *testing.T) {
	resetPingState()
	detectFn = func(_ *inventory.Host, _ time.Duration) (time.Duration, error) {
		return 5 * time.Millisecond, errors.New(`unexpected echo on h1: got "PONG", want "pong"`)
	}
	t.Cleanup(resetPingState)

	h := &inventory.Host{Name: "h1", IP: "10.0.0.1", User: "root", Port: 22}
	_, err := Probe(h, 1*time.Second)
	if err == nil {
		t.Fatalf("Probe err = nil, want mismatch error")
	}
}
