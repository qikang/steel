package runner

import (
	"fmt"
	"sync"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/runner/arch"
	"steel/internal/runner/logstream"
	"steel/internal/runner/shell"
)

// hostFacts is the per-host variable scope available as
// {{ host.* }}. It also probes the host's architecture (via
// arch.Of — one SSH dial + one uname -m on first call per
// host, then cached) so {{ os_arch }} and {{ host.os_arch }}
// resolve correctly. Probe errors bubble up here (rather than
// later, inside Shell()) so a flaky arch detection fails the
// play at fact-gathering time with a clear error chain.
//
// masterNodeNumber is the inventory-wide count of hosts in the
// `masters` group (validated by inventory.New to be 1, 3, or 5).
// It lives on the per-host facts map so the hostBuiltin table in
// vars can pick it up via the same lookup path as `os_arch` —
// every host sees the same value, but the lookup is per-host and
// pulling it from inv.MasterCount() here is the cheapest way to
// thread it through.
func hostFacts(h *inventory.Host, masterNodeNumber int) (map[string]any, error) {
	a, err := arch.Of(h)
	if err != nil {
		return nil, fmt.Errorf("hostFacts for %s: %w", h.Name, err)
	}
	return map[string]any{
		"name":               h.Name,
		"ip":                 h.IP,
		"user":               h.User,
		"port":               h.Port,
		"ssh_key":            h.SSHKey,
		"password":           h.Password,
		"os_arch":            a,
		"master_node_number": masterNodeNumber,
	}, nil
}

// failHost records a per-host failure that happened before the
// module ran (currently only: arch probe inside hostFacts). It
// mirrors what the post-module block does on error — open the
// run-wide log (so the failure row still has a file), close
// it with exit=-1, print the result row under the shared lock,
// and stash the error in *firstErr if no earlier host has
// claimed it. The lock is released before this function
// returns, matching the post-module path's locking discipline
// (terminal writes and *firstErr updates are serialized, file
// I/O is not).
func failHost(h *inventory.Host, play playbook.Play, logBaseDir, playHeader string, cause error, firstErr *error, mu *sync.Mutex) {
	var log *logstream.Streamer
	if logBaseDir != "" {
		l, lerr := logstream.Open(logBaseDir, play.Name, h.Name)
		if lerr != nil {
			fmt.Fprintf(stderrSink(), "warn: open log for %s: %v\n", h.Name, lerr)
		} else {
			log = l
			log.WriteLine(playHeader)
		}
	}
	res := shell.Result{Host: h.Name, OK: false, ExitCode: -1, Err: cause}
	if log != nil {
		log.Close(res.ExitCode, errMsgForLog(res.Err))
	}
	mu.Lock()
	defer mu.Unlock()
	printResultLog(play, res, log)
	if *firstErr == nil {
		*firstErr = res.Err
	}
}
