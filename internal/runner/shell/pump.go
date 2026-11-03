// The pump goroutines that drain a remote shell's stdout/stderr.
// They prefix each line with `host=<name> <stream>>` so the
// per-host log file is a faithful mirror of the line-prefixed
// terminal output the operator would see — and they buffer the
// raw bytes in an in-memory strings.Builder so programmatic
// callers (tests, future automation) can read the captured
// output as a single string.
//
// Splitting pumps out of the main shell.go file keeps the entry
// point small and gives the line-buffering logic its own
// home — it has enough complexity (partial-line carry across
// reads, EOF flush) to deserve a focused file.
package shell

import (
	"fmt"
	"io"
	"strings"

	"steel/internal/runner/logstream"
)

// lineFilter is the optional callback that startPump consults
// for every complete line. It returns true when the line
// should be suppressed from both the terminal writer and the
// per-host log file. The buf (in-memory mirror) still gets
// the unfiltered bytes — programmatic callers reading the
// Result.Stdout/Stderr buffer want to see everything the
// remote side produced.
//
// Today no caller passes a non-nil filter; the type is kept
// around for future use cases where a shell module emits a
// marker line (e.g. a checksum trailer) that should reach
// the in-memory buf but not the operator-facing log.
type lineFilter func(line string) bool

// noLineFilter is the identity (don't suppress anything).
// Used as the default when the caller doesn't pass a filter.
func noLineFilter(string) bool { return false }

// startPump reads from r and, for every complete line, emits
// `host=<name> <stream>> <line>` to both the terminal writer and
// the per-host log file. The trailing partial line (no newline
// yet) is buffered and emitted at EOF so we don't lose the last
// line of a command that didn't print a newline.
//
// filter is consulted for each complete line; returning true
// suppresses the line from both the writer and the log file.
// The unfiltered bytes still land in buf because programmatic
// callers want to see them.
//
// buf is the in-memory mirror; the same bytes that go to the
// terminal/log are also accumulated there, which is what
// programmatic callers (e.g. tests) read back. `done` is signaled
// once at EOF so the caller's wait group can complete.
func startPump(r io.Reader, w io.Writer, log *logstream.Streamer, host, stream string, buf *strings.Builder, filter lineFilter, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	if filter == nil {
		filter = noLineFilter
	}
	tag := fmt.Sprintf("host=%s %s> ", host, stream)
	carry := []byte{}
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			chunk := tmp[:n]
			buf.Write(chunk)
			carry = emitLines(w, tag, carry, chunk, log, filter)
		}
		if err == io.EOF {
			if len(carry) > 0 {
				// Partial line at EOF — no filter check
				// because real-world producers terminate
				// every line with \n; a partial line here
				// means the producer didn't, and the
				// operator still wants to see it.
				w.Write(append([]byte(tag), carry...))
				if log != nil {
					log.WriteLine(string(append([]byte(tag), carry...)))
				}
			}
			return
		}
		if err != nil {
			return
		}
	}
}

// emitLines writes complete newline-terminated lines to w
// (prefixed with tag) and — when log is non-nil — mirrors each
// line into the per-host log file. Lines for which filter
// returns true are suppressed from both destinations but the
// buf still receives the raw bytes (see startPump's doc).
// Returns any trailing partial line. Caller passes the
// returned carry back on the next chunk so a logical line
// split across SSH reads still renders as one prefixed
// output line.
func emitLines(w io.Writer, tag string, carry, chunk []byte, log *logstream.Streamer, filter lineFilter) []byte {
	combined := append(append([]byte{}, carry...), chunk...)
	start := 0
	for i := 0; i < len(combined); i++ {
		if combined[i] != '\n' {
			continue
		}
		line := combined[start : i+1]
		// Filter sees the line content WITHOUT the trailing
		// newline — same shape the orchestrator's parser will
		// receive when it scans the buf.
		body := combined[start:i]
		if filter(string(body)) {
			start = i + 1
			continue
		}
		w.Write(append([]byte(tag), line...))
		if log != nil {
			log.WriteLine(string(append([]byte(tag), line...)))
		}
		start = i + 1
	}
	if start == len(combined) {
		return nil
	}
	out := make([]byte, len(combined)-start)
	copy(out, combined[start:])
	return out
}
