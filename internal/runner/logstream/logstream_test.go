package logstream

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestHeaderAndFooter exercises Open + Close: the file must
// contain a "run started" header, a per-play header that names
// the host, and an "exit=<n>" footer. The file is now a single
// <baseDir>/run.log (one file per run, all hosts and all plays
// append to it), so the path is read at that fixed location.
func TestHeaderAndFooter(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "my-play", "node1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Mirror a streamed line as startPump would
	// (terminal-style).
	s.WriteLine("host=node1 stdout> hi")

	// A control line such as the per-host result row.
	s.WriteLine("- my-play  host=node1  OK")

	s.Close(0, "")

	data, err := readFile(dir + "/run.log")
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{
		"run started",
		`host=node1 play="my-play" started`,
		"host=node1 stdout> hi",
		"- my-play  host=node1  OK",
		"exit=0",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("log missing %q\n--- full ---\n%s", want, data)
		}
	}
}

// TestFailureFooter ensures the footer records a non-zero exit
// code and the accompanying error message.
func TestFailureFooter(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "p", "h")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close(127, "command not found")
	data, _ := readFile(dir + "/run.log")
	for _, want := range []string{"exit=127", "error=command not found"} {
		if !strings.Contains(data, want) {
			t.Errorf("log missing %q", want)
		}
	}
}

// TestWriteLine verifies that a control line (header, result
// row, warn) can be appended to the run-wide log via WriteLine,
// with automatic trailing newline if missing.
func TestWriteLine(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "p", "h")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// No trailing newline on purpose — WriteLine should add one.
	s.WriteLine("- p  host=h  OK")
	// Already-terminated line should not get a doubled newline.
	s.WriteLine("---")
	s.Close(0, "")

	data, _ := readFile(dir + "/run.log")
	for _, want := range []string{"- p  host=h  OK\n", "---"} {
		if !strings.Contains(data, want) {
			t.Errorf("log missing %q\n--- full ---\n%s", want, data)
		}
	}
	// Sanity: the line should not appear with two trailing
	// newlines.
	if strings.Contains(data, "OK\n\n") {
		t.Errorf("WriteLine doubled the newline for a line that already had one")
	}
}

// TestWriteLineNilSafe: nil receiver must not panic. This is
// the contract copy modules and any pre-log-open warn path
// rely on.
func TestWriteLineNilSafe(t *testing.T) {
	var s *Streamer
	s.WriteLine("anything") // must not panic
}

// TestRunWideSingleFile pins the new layout: one file per run,
// fixed at <baseDir>/run.log, regardless of how many hosts or
// plays participate. A regression that reintroduced per-host
// files (or any other path layout) would break this test.
func TestRunWideSingleFile(t *testing.T) {
	dir := t.TempDir()

	// Open several times across plays and hosts — every Open
	// must land at <dir>/run.log.
	for _, h := range []string{"node1", "node2", "node3"} {
		s, err := Open(dir, "play-"+h, h)
		if err != nil {
			t.Fatalf("Open %s: %v", h, err)
		}
		s.WriteLine(h + " line")
		s.Close(0, "")
	}

	// The single file MUST exist at <dir>/run.log.
	if _, err := os.Stat(dir + "/run.log"); err != nil {
		t.Errorf("expected log at run.log, stat err = %v", err)
	}
	// Per-host or per-play subdirectories must NOT be used.
	for _, sub := range []string{"node1", "node2", "node3", "play-node1", "play-node2", "play-node3"} {
		if _, err := os.Stat(dir + "/" + sub); err == nil {
			t.Errorf("did not expect per-host or per-play entry %q; the run uses one shared file", sub)
		}
	}
}

// TestMultiPlayAppendsToOneFile pins the contract that the
// second play's Open on the same host appends to the same
// file rather than creating a new one. The file is the
// operator's single timeline for what happened across the run,
// so plays must serialize chronologically.
func TestMultiPlayAppendsToOneFile(t *testing.T) {
	dir := t.TempDir()

	s1, err := Open(dir, "play1", "node1")
	if err != nil {
		t.Fatalf("Open play1: %v", err)
	}
	s1.WriteLine("from play1 stdout line A")
	s1.Close(0, "")

	s2, err := Open(dir, "play2", "node1")
	if err != nil {
		t.Fatalf("Open play2: %v", err)
	}
	s2.WriteLine("from play2 stdout line B")
	s2.Close(0, "")

	data, _ := readFile(dir + "/run.log")
	for _, want := range []string{
		"play1 stdout line A", // first play's content
		"play2 stdout line B", // second play's content
		`host=node1 play="play1" started`,
		`host=node1 play="play2" started`,
	} {
		if !strings.Contains(data, want) {
			t.Errorf("log missing %q\n--- full ---\n%s", want, data)
		}
	}
	// Exactly one per-run header, regardless of how many
	// plays target the host.
	if got := strings.Count(data, "run started"); got != 1 {
		t.Errorf("expected 1 'run started' header, got %d\n%s", got, data)
	}
	// play2 content must come AFTER play1 content in the
	// file (chronological). A regression that wrote them out
	// of order would still have all the substrings but be
	// useless as a timeline.
	i := strings.Index(data, "play1 stdout line A")
	if i < 0 {
		t.Fatalf("play1 content missing")
	}
	j := strings.Index(data, "play2 stdout line B")
	if j < 0 {
		t.Fatalf("play2 content missing")
	}
	if i > j {
		t.Errorf("play2 content appeared before play1; want chronological append\n%s", data)
	}
}

// TestDifferentHostsShareOneFile pins that two hosts in the
// same run land in the SAME file (not two distinct files).
// The flat layout is one file per run, with the host identity
// preserved on each line via the `host=` marker — not by
// splitting the run into per-host files.
func TestDifferentHostsShareOneFile(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir, "play1", "node1")
	if err != nil {
		t.Fatalf("Open node1: %v", err)
	}
	s1.WriteLine("node1 line")
	s1.Close(0, "")

	s2, err := Open(dir, "play1", "node2")
	if err != nil {
		t.Fatalf("Open node2: %v", err)
	}
	s2.WriteLine("node2 line")
	s2.Close(0, "")

	// The single run-wide file MUST exist.
	if _, err := os.Stat(dir + "/run.log"); err != nil {
		t.Errorf("run.log missing: %v", err)
	}
	// Per-host files must NOT exist.
	if _, err := os.Stat(dir + "/node1.log"); err == nil {
		t.Errorf("node1.log should not exist; the run uses one shared file")
	}
	if _, err := os.Stat(dir + "/node2.log"); err == nil {
		t.Errorf("node2.log should not exist; the run uses one shared file")
	}

	data, _ := readFile(dir + "/run.log")
	for _, want := range []string{
		"run started",
		`host=node1 play="play1" started`,
		"node1 line",
		`host=node2 play="play1" started`,
		"node2 line",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("log missing %q\n--- full ---\n%s", want, data)
		}
	}
}

// TestEmptyBaseDirRejected pins that the helper refuses to
// operate without a base dir; otherwise the caller would
// silently write to "" which is meaningless.
func TestEmptyBaseDirRejected(t *testing.T) {
	if _, err := Open("", "p", "h"); err == nil {
		t.Errorf("Open with empty baseDir should error")
	}
}

// TestEmptyHostRejected pins that the host name is required
// — without it the per-play header renders as `host= play=...`
// which loses the host identity that lines in the file rely on
// the operator (and any downstream tooling) being able to grep.
func TestEmptyHostRejected(t *testing.T) {
	if _, err := Open(t.TempDir(), "p", ""); err == nil {
		t.Errorf("Open with empty host should error")
	}
}

func readFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
