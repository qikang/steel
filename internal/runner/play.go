package runner

import (
	"fmt"
	"sync"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/runner/copy"
	"steel/internal/runner/logstream"
	"steel/internal/runner/shell"
	"steel/internal/vars"
)

// runPlay is the per-play dispatch loop. It opens the
// run-wide log streamer once per host (copy modules don't have
// a stream so the log is still opened for the result row) and
// closes it after the call returns — writing the final exit
// code to the log's footer. The play header is mirrored into
// each host's section of the run-wide log so the file is
// self-describing when opened cold.
//
// Dispatch mode is governed by `sequential`, sourced from
// playbook.Play.ExecutionMode (only the literal "serial"
// flips it on):
//
//   - sequential=true: targets run one after the other in
//     declaration order. This is the opt-in path for plays
//     that must not race their peers (e.g. cluster-bootstrap
//     steps where one host's state depends on the previous
//     host's). Selected by `execution_mode: serial`.
//   - sequential=false: targets fan out concurrently via
//     goroutines. This is the DEFAULT — including for
//     `hosts:` resolving to a single host (the fan-out is a
//     no-op for one target but keeps the code paths uniform)
//     and for `hosts:` resolving to a group (e.g.
//     `hosts: allnode`).
//
// Either way, the run-wide log file, terminal result row,
// and `firstErr` capture are written under the shared mutex,
// so the on-disk log and the terminal stream remain a
// faithful mirror of each other regardless of mode.
func runPlay(play playbook.Play, targets []*inventory.Host, playbookVars map[string]any, allHosts []*inventory.Host, allHostNames []string, groupHostNames map[string][]string, masterNodeNumber int, logBaseDir string, sequential bool) error {
	playHeader := headerLine(play, targets)
	fmt.Print(playHeader)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	dispatch := func(h *inventory.Host) {
		// hostFacts probes the host's arch (one SSH dial +
		// one uname -m on first call per host, then cached).
		// Doing this before opening the per-play log keeps
		// the log honest: a fact-gathering failure
		// shouldn't produce a header+logfile when nothing
		// actually ran.
		hf, err := hostFacts(h, masterNodeNumber)
		if err != nil {
			failHost(h, play, logBaseDir, playHeader, err, &firstErr, &mu)
			return
		}
		ctx := &vars.Context{
			Vars:           playbookVars,
			Host:           hf,
			AllHostNames:   allHostNames,
			GroupHostNames: groupHostNames,
		}

		var (
			res shell.Result
			log *logstream.Streamer
		)
		// Open the run-wide log for every module type so
		// the file is a faithful mirror of what was shown
		// on the terminal — header, the per-host result
		// row, and (for shell) the live stdout/stderr
		// stream with a "host=" tag.
		//
		// The flat layout is logs/<ts>/run.log — a single
		// file for the whole run; every play and every host
		// append to the same file in execution order. The
		// play name and host name are recorded INSIDE the
		// file (as a `play=...` header per play and a
		// `host=...` marker on shell stream lines) so the
		// operator greps one file for the entire run
		// timeline rather than chasing per-host files.
		if logBaseDir != "" {
			l, lerr := logstream.Open(logBaseDir, play.Name, h.Name)
			if lerr != nil {
				// No log file is open for this host —
				// write the warn to stderr only. We can't
				// mirror it because the file we wanted to
				// mirror into doesn't exist.
				fmt.Fprintf(stderrSink(), "warn: open log for %s: %v\n", h.Name, lerr)
			} else {
				log = l
				log.WriteLine(playHeader)
			}
		}
		switch {
		case play.Copy != nil:
			err = copy.Run(h, play.Copy, ctx)
			res = shell.Result{Host: h.Name, OK: err == nil, Err: err}
			// Copy has no remote exit code — synthesize
			// 0/1 for the log footer so the file's
			// self-description is consistent with shell
			// logs.
			if res.OK {
				res.ExitCode = 0
			} else {
				res.ExitCode = 1
			}
		case play.Shell != nil:
			res = shell.Run(h, play.Shell, ctx, log, allHosts)
		}

		if log != nil {
			// Defer via closure so we read res after the
			// module returns. (Defer arg evaluation would
			// otherwise snapshot the zero-value Result.)
			func(l *logstream.Streamer, r shell.Result) {
				defer l.Close(r.ExitCode, errMsgForLog(r.Err))
			}(log, res)
		}

		mu.Lock()
		defer mu.Unlock()
		printResultLog(play, res, log)
		if !res.OK && firstErr == nil {
			firstErr = res.Err
		}
	}

	if sequential {
		for _, h := range targets {
			dispatch(h)
		}
		return firstErr
	}

	for _, h := range targets {
		wg.Add(1)
		go func(h *inventory.Host) {
			defer wg.Done()
			dispatch(h)
		}(h)
	}
	wg.Wait()
	return firstErr
}

// errMsgForLog returns the error message for a log footer,
// or "" if the error is nil. Centralized so the run-wide
// log footer always sees the same stringification rules.
func errMsgForLog(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// headerLine returns the play header line WITHOUT a trailing
// newline stripped — callers append "\n" via Print/WriteLine
// so the result can be mirrored verbatim into the run-wide
// log file.
func headerLine(play playbook.Play, targets []*inventory.Host) string {
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.Name)
	}
	return fmt.Sprintf("\n- %s  hosts=%v\n", play.Name, names)
}
