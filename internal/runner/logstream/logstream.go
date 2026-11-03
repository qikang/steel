// Package logstream maintains the single per-run log file that
// mirrors the lines the operator sees on the terminal. One file
// per run; every play and every host append to the same file in
// execution order, separated by per-play headers and footers.
//
// Files live at <baseDir>/run.log (one level below the timestamp
// directory chosen by cmd/steel). The orchestrator writes a
// `run started` header on first Open, threads streaming lines
// through WriteLine as each module runs (shell lines already
// carry their `host=<name>` prefix), and writes a
// `play=<name> exit=<n>` footer on Close. A blank line between
// plays gives a clear visual boundary when the file is opened
// cold.
//
// The host and play identities live INSIDE the file (as
// `host=` and `play=` markers on each line and section) rather
// than in the file path, so the operator greps one file for the
// whole run rather than chasing per-host files across
// subdirectories.
package logstream

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// runLogName is the single fixed log file name used for every
// run. The whole run — every play, every host — lands here, in
// execution order, with `host=` / `play=` markers preserving the
// per-line context.
const runLogName = "run.log"

// Streamer tees remote I/O into the single per-run log file
// and records the per-play exit code in the file's footer when
// Close is called.
//
// The zero value is not usable; always construct via Open. A
// nil *Streamer is safe to pass to WriteLine / Close — the
// helper functions turn into no-ops, which is the contract
// copy modules and the pre-log-open warn path rely on.
type Streamer struct {
	mu        sync.Mutex
	file      *os.File
	host      string
	hasHeader bool // true once the per-run "run started" header has been emitted
	hasPlay   bool // true once a per-play header has been emitted for the current play
}

// Open opens the run-wide log file under baseDir for append.
// The first Open in a run writes a `run started` header;
// subsequent Opens (one per play) append a per-play header
// after a blank-line separator.
//
// `host` and `play` are recorded in the per-play header so the
// file reads as a chronological log when opened cold; they are
// informational only and do not influence the file path (the
// entire run shares one file under `runLogName`).
//
// baseDir is required; passing "" returns an error so a
// forgotten `logs/` path surfaces at the call site rather than
// silently dropping log output.
//
// Passing "" for host still errors today (it would render an
// empty `host=` marker in the header) — the runner always
// knows which host a dispatch targets, so an empty host is
// always a caller bug.
func Open(baseDir, play, host string) (*Streamer, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("log base dir is empty")
	}
	if host == "" {
		return nil, fmt.Errorf("log host is empty")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	path := baseDir + "/" + runLogName

	// Detect first-touch: a fresh file (or one with just our
	// own header from a previous run that was somehow left
	// behind) gets the per-run header. An existing non-empty
	// file means a previous play already wrote to it this run
	// — we just append a separator + per-play header.
	fresh := true
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		fresh = false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	s := &Streamer{file: f, host: host}
	if fresh {
		fmt.Fprintf(f, "run started\n")
		s.hasHeader = true
	}
	if play != "" {
		// Two blank lines between plays give a clear visual
		// boundary when the file is opened cold (matching
		// the precedent the operator's eye is used to from
		// standard log conventions).
		if !s.hasHeader {
			fmt.Fprintf(f, "\n")
		}
		fmt.Fprintf(f, "\nhost=%s play=%q started\n", host, play)
		s.hasPlay = true
	}
	return s, nil
}

// Close writes the per-play footer (exit code and optional
// error message) and closes the file. Safe to call on a nil
// receiver. After Close, the file remains on disk so the next
// play's Open can append to it.
func (s *Streamer) Close(exitCode int, errMsg string) {
	if s == nil || s.file == nil {
		return
	}
	if s.hasPlay {
		fmt.Fprintf(s.file, "---\nexit=%d\n", exitCode)
	}
	if errMsg != "" {
		fmt.Fprintf(s.file, "error=%s\n", errMsg)
	}
	_ = s.file.Close()
	s.file = nil
}

// WriteLine appends a single complete control line (header,
// warn, result row, etc.) to the run-wide log file so the file
// mirrors the lines the user sees on the terminal. It is safe
// to call from multiple goroutines.
//
// A trailing newline is added automatically if the line does
// not end with one; lines that already end with "\n" are
// written as-is.
func (s *Streamer) WriteLine(line string) {
	if s == nil || s.file == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.file.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		_, _ = s.file.WriteString("\n")
	}
}
