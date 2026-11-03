package runner

import (
	"fmt"

	"steel/internal/playbook"
	"steel/internal/runner/logstream"
	"steel/internal/runner/shell"
)

// printResultLog writes the per-host result line to the
// terminal and, when log is non-nil, mirrors it into the
// host's log file so the log reflects exactly what the
// operator saw on the terminal for that host.
func printResultLog(play playbook.Play, r shell.Result, log *logstream.Streamer) {
	status := "OK"
	if !r.OK {
		status = "FAILED"
	}
	// Shell stdout/stderr have already been streamed to the
	// terminal in real time, so this row only carries the
	// final verdict. For failed commands we tack on the
	// one-line error (the live stream already shows the full
	// body — no need to repeat it here).
	var line string
	if !r.OK && r.Err != nil {
		line = fmt.Sprintf("- %s  host=%s  %s  %s\n", play.Name, r.Host, status, r.Err.Error())
	} else {
		line = fmt.Sprintf("- %s  host=%s  %s\n", play.Name, r.Host, status)
	}
	fmt.Fprint(stdoutSink(), line)
	if log != nil {
		log.WriteLine(line)
	}
}
