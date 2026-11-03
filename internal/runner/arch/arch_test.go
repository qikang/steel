package arch

import (
	"errors"
	"sync/atomic"
	"testing"

	"steel/internal/inventory"
)

// resetArchState wipes the per-process arch cache and the
// archDetectFn mock between tests so each one starts from a
// known empty state. Tests that want to override archDetectFn
// must restore the production default (or call resetArchState)
// so they don't leak their mock into sibling tests.
func resetArchState() {
	archCache.Range(func(k, _ any) bool {
		archCache.Delete(k)
		return true
	})
	archDetectFn = defaultArchDetect
}

// TestUnameToArch is a pure-function table for the kernel-arch
// → Go-arch mapping. No SSH involved — just exercises the
// string switch and the first-line trimming for multi-line
// outputs.
func TestUnameToArch(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"x86_64\n", "amd64", false},
		{"x86_64", "amd64", false},
		{"  x86_64  \n", "amd64", false},
		{"aarch64\n", "arm64", false},
		{"aarch64", "arm64", false},
		// Multi-line (some BusyBox / coreutils configs print
		// extra after uname -m): only the first line is
		// consulted.
		{"x86_64\nvendor junk", "amd64", false},
		// Exotic arches are an explicit error rather than a
		// silent empty string, so a misconfigured inventory
		// surfaces immediately.
		{"ppc64le", "", true},
		{"riscv64", "", true},
		{"", "", true},
		{"unknown\n", "", true},
	}
	for _, c := range cases {
		got, err := unameToArch(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("unameToArch(%q): want error, got %q", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("unameToArch(%q): unexpected error: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("unameToArch(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestArchOf_CachesPerHost verifies that arch.Of only invokes
// the detection function once per host.Name — the second call
// must hit the cache and the detect counter must not advance.
// This is the invariant the production code relies on to keep
// SSH cost bounded across multi-play runs.
func TestArchOf_CachesPerHost(t *testing.T) {
	resetArchState()
	t.Cleanup(resetArchState)

	var calls atomic.Int32
	archDetectFn = func(h *inventory.Host) (string, error) {
		calls.Add(1)
		return "x86_64\n", nil
	}

	h := &inventory.Host{Name: "master1", IP: "10.0.0.1"}

	for i := 0; i < 5; i++ {
		got, err := Of(h)
		if err != nil {
			t.Fatalf("Of iter %d: unexpected error: %v", i, err)
		}
		if got != "amd64" {
			t.Errorf("Of iter %d = %q, want %q", i, got, "amd64")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("archDetectFn calls = %d, want 1 (subsequent calls should hit cache)", n)
	}
}

// TestArchOf_DoesNotCacheErrors verifies that a failing
// detection is NOT cached — the next call must retry. This
// prevents a single transient SSH blip from permanently
// masking a host's arch for the rest of the apply run.
func TestArchOf_DoesNotCacheErrors(t *testing.T) {
	resetArchState()
	t.Cleanup(resetArchState)

	var calls atomic.Int32
	archDetectFn = func(h *inventory.Host) (string, error) {
		n := calls.Add(1)
		if n == 1 {
			return "", errors.New("transient ssh blip")
		}
		return "aarch64\n", nil
	}

	h := &inventory.Host{Name: "master1", IP: "10.0.0.1"}

	if _, err := Of(h); err == nil {
		t.Fatal("first Of: want error, got nil")
	}
	got, err := Of(h)
	if err != nil {
		t.Fatalf("second Of: unexpected error: %v", err)
	}
	if got != "arm64" {
		t.Errorf("second Of = %q, want %q", got, "arm64")
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("archDetectFn calls = %d, want 2 (failed detect must not cache)", n)
	}
}

// TestArchOf_PerHostIsolation verifies that the cache is keyed
// by host.Name — different hosts get independent detections.
// Without per-host keying, the second host in a multi-host play
// would see the first host's arch by accident.
func TestArchOf_PerHostIsolation(t *testing.T) {
	resetArchState()
	t.Cleanup(resetArchState)

	var calls atomic.Int32
	archDetectFn = func(h *inventory.Host) (string, error) {
		calls.Add(1)
		switch h.Name {
		case "master1":
			return "x86_64\n", nil
		case "master2":
			return "aarch64\n", nil
		default:
			return "", errors.New("unexpected host")
		}
	}

	a, err := Of(&inventory.Host{Name: "master1", IP: "10.0.0.1"})
	if err != nil {
		t.Fatalf("master1 Of: %v", err)
	}
	b, err := Of(&inventory.Host{Name: "master2", IP: "10.0.0.2"})
	if err != nil {
		t.Fatalf("master2 Of: %v", err)
	}
	if a != "amd64" {
		t.Errorf("master1 arch = %q, want amd64", a)
	}
	if b != "arm64" {
		t.Errorf("master2 arch = %q, want arm64", b)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("archDetectFn calls = %d, want 2 (one per host)", n)
	}
}
