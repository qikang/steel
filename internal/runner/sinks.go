package runner

import (
	"io"
	"os"
)

// stderrSink is split out so tests can override it. In
// production it points at os.Stderr; tests redirect it to a
// buffer.
var stderrSink = func() io.Writer { return os.Stderr }

// stdoutSink mirrors stderrSink but for the terminal output
// stream. Splitting it out lets tests capture the on-screen
// lines that printResultLog emits, so they can assert that the
// same lines also reach the run-wide log file.
var stdoutSink = func() io.Writer { return os.Stdout }
