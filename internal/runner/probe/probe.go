// Package probe runs the SSH connectivity check used both by
// `steel ping` and (as a preflight) by `steel apply`. The probe
// is the same one described in runner/ping: a real SSH handshake
// plus an `echo pong` exec, with the round-trip time captured for
// the rendered table.
//
// The package exists so `apply` can run the same check the
// operator would have run with `steel ping` first, without
// duplicating the table-rendering and probe-loop code from
// cmd/ping.go. All is the per-host entry point; RenderTable
// writes the same human-friendly summary `steel ping` uses.
package probe

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"

	"steel/internal/inventory"
)

// Result is the outcome of one host's probe.
type Result struct {
	Host *inventory.Host
	OK   bool
	Err  error
	RTT  time.Duration
}

// ProbeFn is the SSH-side probe. Production code wires this to
// ping.Probe (which SSHes in and runs `echo pong`); tests inject
// a fake so the table-rendering and concurrent-fan-out code can
// be exercised without a real sshd.
var ProbeFn = func(h *inventory.Host, timeout time.Duration) error {
	_, err := pingProbe(h, timeout)
	return err
}

// All runs ProbeFn against every host in parallel and returns
// the per-host results in the same order as the input slice.
// Each host is probed concurrently — the whole check is bounded
// by the slowest single-host SSH dial plus exec, not the sum.
func All(hosts []*inventory.Host, timeout time.Duration) []Result {
	results := make([]Result, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, h *inventory.Host) {
			defer wg.Done()
			start := time.Now()
			err := ProbeFn(h, timeout)
			rtt := time.Since(start)
			if err != nil {
				results[i] = Result{Host: h, OK: false, Err: err, RTT: rtt}
				return
			}
			results[i] = Result{Host: h, OK: true, RTT: rtt}
		}(i, h)
	}
	wg.Wait()
	return results
}

// AllOK reports whether every result in rs is OK. The apply
// preflight uses this to decide whether to proceed; the standalone
// `steel ping` command uses it for the exit code.
func AllOK(rs []Result) bool {
	for _, r := range rs {
		if !r.OK {
			return false
		}
	}
	return true
}

// RenderTable writes a human-friendly summary of rs to w. The
// output matches what `steel ping` prints so the apply preflight
// and the standalone command look identical.
//
// Results are sorted by host name so the table is stable across
// runs (the input slice's order is the inventory.All() order
// — masters before workers alphabetically — but the table reads
// better when sorted alphabetically by name).
func RenderTable(w io.Writer, rs []Result) {
	sorted := make([]Result, len(rs))
	copy(sorted, rs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Host.Name < sorted[j].Host.Name })

	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.AppendHeader(table.Row{"Host", "Address", "Port", "Status", "RTT", "Detail"})
	for _, r := range sorted {
		status := "REACHABLE"
		detail := ""
		if !r.OK {
			status = "UNREACHABLE"
			detail = trimErr(r.Err)
		}
		t.AppendRow(table.Row{
			r.Host.Name,
			r.Host.IP,
			r.Host.Port,
			status,
			r.RTT.Round(time.Millisecond),
			detail,
		})
	}
	fmt.Fprintln(w, t.Render())
}

func trimErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
