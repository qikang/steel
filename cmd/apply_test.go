package cmd

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"steel/internal/inventory"
)

// makeCmd builds a fresh cobra command whose stdin/stdout/stderr
// are the supplied buffers, so the test can both drive the prompt
// and capture the output without touching the real process stdio.
func makeCmd(in *bytes.Buffer, out, errOut *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{}
	c.SetIn(in)
	c.SetOut(out)
	c.SetErr(errOut)
	return c
}

// makeInv returns the smallest inventory confirmPreflight will
// accept (one group, one host) — confirmPreflight itself only
// iterates group names, so a single host in the masters group is
// enough to drive the group-listing branch and the prompt.
// inventory.New requires a `user` per host; password is optional
// but we set it for symmetry with the install-k8s.yaml fixture.
func makeInv(t *testing.T) *inventory.Inventory {
	t.Helper()
	inv, err := inventory.New(&inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {{
				Key:      "k8s_master1",
				Name:     "master1",
				IP:       "192.168.170.48",
				User:     "root",
				Password: "your_password",
			}},
		},
		Groups: map[string][]string{},
	})
	if err != nil {
		t.Fatalf("inventory.New: %v", err)
	}
	return inv
}

// TestReadYesNo_Defaults covers the four canonical answers and
// the surrounding-whitespace / case tolerance contract.
//
// Note: the "default yes on Enter" case is a `"\n"` line — a
// truly empty stream (`""`) is treated as invalid so a stray
// Ctrl-D / closed pipe doesn't silently proceed with the apply.
func TestReadYesNo_Defaults(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
		valid bool
	}{
		{"Enter defaults yes", "\n", true, true},
		{"lowercase y", "y\n", true, true},
		{"uppercase Y", "Y\n", true, true},
		{"yes full", "yes\n", true, true},
		{"no", "n\n", false, true},
		{"uppercase N", "N\n", false, true},
		{"no full", "no\n", false, true},
		{"leading whitespace yes", "   y\n", true, true},
		{"trailing whitespace yes", "y   \n", true, true},
		{"quoted y", `"y"` + "\n", true, true},
		{"garbage is invalid", "maybe\n", false, false},
		{"Y Y Y is no longer valid (was the old per-question form)", "Y Y Y\n", false, false},
		{"yyny is no longer valid (was the run-length form)", "yyny\n", false, false},
		{"clean partial line yes (no trailing newline)", "y", true, true},
		{"clean partial line no (no trailing newline)", "n", false, true},
		{"truly empty stream is invalid (not a default-yes)", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdr := bufio.NewReader(strings.NewReader(c.input))
			got, valid := readYesNo(rdr)
			if got != c.want || valid != c.valid {
				t.Fatalf("readYesNo(%q) = (%v, %v); want (%v, %v)", c.input, got, valid, c.want, c.valid)
			}
		})
	}
}

// TestReadYesNo_EOF covers the case where the underlying reader
// returns an error before delivering a line. Both fields must
// come back false so the apply aborts rather than silently
// proceeding without a confirmation.
func TestReadYesNo_EOF(t *testing.T) {
	// An empty reader with no trailing newline yields io.EOF on
	// ReadString. That has to be reported as invalid.
	r := strings.NewReader("")
	rdr := bufio.NewReader(r)
	got, valid := readYesNo(rdr)
	if got || valid {
		t.Fatalf("readYesNo(EOF) = (%v, %v); want (false, false)", got, valid)
	}
}

// TestConfirmPreflight_Y covers the operator answering yes
// (single `y`) and the prompt rendering the four hints
// before the Y/N. The output must include all four hint
// strings, the "是否继续? [Y/N]" line, and the group listing
// from the inventory in the canonical order
// (masters → workers → allnode → addworkers).
func TestConfirmPreflight_Y(t *testing.T) {
	in := bytes.NewBufferString("y\n")
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	c := makeCmd(in, out, errOut)

	if !confirmPreflight(c, makeInv(t)) {
		t.Fatalf("confirmPreflight: operator answered y; want proceed")
	}
	got := out.String()
	for _, want := range []string{
		"配置文件已读取到以下组信息",
		"/data",
		"resolv.conf",
		"时区",
		"OS 软件源",
		"k8s节点信息确认",
		"是否继续? [Y/N]",
		`group "masters": 1 host`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n--- got ---\n%s", want, got)
		}
	}
	// Canonical group display order: masters must appear
	// before allnode in the rendered output — this guards the
	// hardcoded display order (vs Go's randomized map iteration
	// or alphabetical sort which would put addworkers first).
	mastersIdx := strings.Index(got, `group "masters"`)
	allnodeIdx := strings.Index(got, `group "allnode"`)
	if mastersIdx < 0 || allnodeIdx < 0 {
		t.Fatalf("stdout missing masters/allnode listing\n%s", got)
	}
	if mastersIdx > allnodeIdx {
		t.Errorf("group display order: masters (%d) must appear before allnode (%d)\n%s", mastersIdx, allnodeIdx, got)
	}
	// The old "answer (e.g. \"Y Y Y\" ...)" prompt line must
	// be gone — that was the per-question Y/N UX.
	if strings.Contains(got, "Y Y Y") {
		t.Errorf("stdout still shows the old per-question prompt\n%s", got)
	}
}

// TestConfirmPreflight_DefaultYesEnter covers hitting Enter
// (empty line) — must be treated as yes per the spec.
func TestConfirmPreflight_DefaultYesEnter(t *testing.T) {
	in := bytes.NewBufferString("\n")
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	if !confirmPreflight(makeCmd(in, out, errOut), makeInv(t)) {
		t.Fatalf("confirmPreflight: empty line should default to yes")
	}
}

// TestConfirmPreflight_N covers the operator declining — the
// function must return false and surface a clear "declined,
// aborting" message on stderr. This is the path that bails
// out of the apply before any SSH probe runs.
func TestConfirmPreflight_N(t *testing.T) {
	in := bytes.NewBufferString("n\n")
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	if confirmPreflight(makeCmd(in, out, errOut), makeInv(t)) {
		t.Fatalf("confirmPreflight: operator answered n; want abort")
	}
	if !strings.Contains(errOut.String(), "declined") {
		t.Errorf("stderr missing decline message; got %q", errOut.String())
	}
}

// TestConfirmPreflight_Invalid covers the case where the
// operator types something that is not Y or N (including the
// legacy "Y Y Y" multi-answer form). The function must treat
// it as invalid and return false; the apply should abort and
// the user can re-run with a clean input.
func TestConfirmPreflight_Invalid(t *testing.T) {
	cases := []string{"maybe\n", "Y Y Y\n", "y n\n", "yyy\n", "abc\n"}
	for _, input := range cases {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			in := bytes.NewBufferString(input)
			out := &bytes.Buffer{}
			errOut := &bytes.Buffer{}
			if confirmPreflight(makeCmd(in, out, errOut), makeInv(t)) {
				t.Fatalf("confirmPreflight(%q): want abort on invalid input", input)
			}
			if !strings.Contains(errOut.String(), "please answer Y or N") {
				t.Errorf("stderr missing invalid-input message for %q; got %q", input, errOut.String())
			}
		})
	}
}
