package shell

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"steel/internal/inventory"
	"steel/internal/runner/logstream"
	"steel/internal/vars"
)

// TestStartPump_LinePrefix verifies that each complete line
// emitted by the remote side is rendered to the writer with a
// "host=<name> <stream>> " prefix, and that a trailing partial
// line is flushed on EOF.
func TestStartPump_LinePrefix(t *testing.T) {
	inR, inW := io.Pipe()
	var out bytes.Buffer
	var buf strings.Builder
	done := make(chan struct{})

	go startPump(inR, &out, nil, "node1", "stdout", &buf, nil, done)

	// Two complete lines plus a partial tail.
	inW.Write([]byte("hello\nworld\nno-newline"))
	_ = inW.Close()
	<-done

	want := "host=node1 stdout> hello\nhost=node1 stdout> world\nhost=node1 stdout> no-newline"
	if got := out.String(); got != want {
		t.Errorf("out = %q\nwant %q", got, want)
	}
	if buf.String() != "hello\nworld\nno-newline" {
		t.Errorf("buf captured = %q", buf.String())
	}
}

// TestStartPump_ChunkedLine verifies that a logical line split
// across two Reads is still rendered as a single prefixed
// output line.
func TestStartPump_ChunkedLine(t *testing.T) {
	inR, inW := io.Pipe()
	var out bytes.Buffer
	var buf strings.Builder
	done := make(chan struct{})

	go startPump(inR, &out, nil, "node1", "stdout", &buf, nil, done)
	inW.Write([]byte("hello "))
	inW.Write([]byte("world\n"))
	_ = inW.Close()
	<-done

	want := "host=node1 stdout> hello world\n"
	if got := out.String(); got != want {
		t.Errorf("out = %q\nwant %q", got, want)
	}
}

// TestStartPump_MirrorsToLog verifies that every line the pump
// writes is mirrored verbatim into the run-wide log file — the
// on-disk file should be a faithful copy of the stream for that
// host. (The pump's terminal-side writer is io.Discard in
// production, so the test only asserts the log path.)
func TestStartPump_MirrorsToLog(t *testing.T) {
	dir := t.TempDir()
	s, err := logstream.Open(dir, "p", "node1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close(0, "")

	inR, inW := io.Pipe()
	var buf strings.Builder
	done := make(chan struct{})

	go startPump(inR, io.Discard, s, "node1", "stdout", &buf, nil, done)
	inW.Write([]byte("hello\nworld\n"))
	_ = inW.Close()
	<-done

	data, _ := readFile(dir + "/run.log")
	for _, want := range []string{
		"host=node1 stdout> hello",
		"host=node1 stdout> world",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("log missing %q\n--- full ---\n%s", want, data)
		}
	}
}

// TestStartPump_FilterDropsFromLogNotBuf pins the line-filter
// contract: when a filter returns true for a line, the line is
// suppressed from BOTH the writer and the run-wide log file,
// but the in-memory buf still accumulates the raw bytes. The
// filter is a generic predicate (today the orchestrator doesn't
// need one, but the abstraction is in place for future use
// cases such as trailer markers that an in-memory parser reads
// but the operator-facing log does not).
func TestStartPump_FilterDropsFromLogNotBuf(t *testing.T) {
	dir := t.TempDir()
	s, err := logstream.Open(dir, "p", "node1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close(0, "")

	inR, inW := io.Pipe()
	var out bytes.Buffer
	var buf strings.Builder
	done := make(chan struct{})

	// Drop any line beginning with "MAGIC:" — stands in for
	// whatever production predicate a future caller needs.
	filter := func(line string) bool { return strings.HasPrefix(line, "MAGIC:") }
	go startPump(inR, &out, s, "node1", "stdout", &buf, filter, done)
	inW.Write([]byte("regular line\n"))
	inW.Write([]byte("MAGIC: payload\n"))
	inW.Write([]byte("another regular line\n"))
	_ = inW.Close()
	<-done

	// The writer (terminal mirror) drops the filtered line.
	if strings.Contains(out.String(), "MAGIC:") {
		t.Errorf("writer leaked filtered line: %q", out.String())
	}
	if !strings.Contains(out.String(), "regular line") {
		t.Errorf("writer missing regular line: %q", out.String())
	}
	if !strings.Contains(out.String(), "another regular line") {
		t.Errorf("writer missing later regular line: %q", out.String())
	}

	// The log file mirrors the writer (so the on-disk view
	// matches what the operator would have seen on the
	// terminal).
	data, _ := readFile(dir + "/run.log")
	if strings.Contains(data, "MAGIC:") {
		t.Errorf("log file leaked filtered line: %q", data)
	}

	// The buf still has the filtered line — removing it would
	// silently break any future parser that relies on seeing
	// every byte the remote side produced.
	if !strings.Contains(buf.String(), "MAGIC:") {
		t.Errorf("buf dropped filtered line: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "regular line") {
		t.Errorf("buf missing regular line: %q", buf.String())
	}
}

// TestStartPump_NilFilterPassesEverything pins that a nil
// filter is treated as no-op (every line passes through), so
// existing call sites that don't care about filtering don't
// need to set up an identity function.
func TestStartPump_NilFilterPassesEverything(t *testing.T) {
	inR, inW := io.Pipe()
	var out bytes.Buffer
	var buf strings.Builder
	done := make(chan struct{})

	go startPump(inR, &out, nil, "node1", "stdout", &buf, nil, done)
	inW.Write([]byte("STEEL_STATE_DUMP should-pass-through\nregular\n"))
	_ = inW.Close()
	<-done

	if !strings.Contains(out.String(), "STEEL_STATE_DUMP should-pass-through") {
		t.Errorf("nil filter dropped sentinel: %q", out.String())
	}
}

// TestBuildEnvPrelude pins the prelude that Run prepends to
// every remote command. The contract is:
//
//   - `export os_arch=...` is always first (most scripts depend
//     on it, and it always has a value once hostFacts succeeded).
//   - `export master_node_number=...` follows immediately when
//     the inventory has a `masters` group (size validated by
//     inventory.New to be 1, 3, or 5). Worker-only inventories
//     skip this line.
//   - `export k8s_master1_ip=...` is emitted when an inventory
//     host has the key `k8s_master1` (the first master, in
//     declaration order). All other per-host
//     `k8s_<key>_ip`/`k8s_<key>_hostname` pairs are NOT emitted;
//     scripts that need to iterate hosts should use
//     `$k8s_hosts_alias`.
//   - `export k8s_hosts_alias=...` is one `ip hostname` pair per
//     host in declaration order, separated by a literal `\n `
//     (backslash + n + space — three chars) so
//     `echo -e "$k8s_hosts_alias" >> /etc/hosts` drops each
//     host onto its own line. The trailing space lets a
//     downstream `IFS=$'\n '` split or a `strings.Split` on
//     the same three-char separator recover clean pairs
//     without leading whitespace on every non-first line.
//     Empty inventory skips the export entirely.
//   - Top-level vars exported under their names; nested maps
//     flattened to underscore-separated names (e.g.
//     `registryServer.hostName` → `registryServer_hostName`).
//   - Stable order (vars sorted by key) so the prelude is
//     byte-stable and easy to diff in logs.
func TestBuildEnvPrelude(t *testing.T) {
	allHosts := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.47"},
	}
	ctx := &vars.Context{
		Vars: map[string]any{
			"data_root":         "/data/install-k8s",
			"k8s_network_stack": "ipv4",
			"registryServer": map[string]any{
				"hostName": "master2",
				"alias":    "registry.local",
			},
		},
		Host: map[string]any{
			"os_arch":            "amd64",
			"master_node_number": 3,
		},
		AllHostNames: []string{"master1", "master2"},
		GroupHostNames: map[string][]string{
			"masters": {"master1", "master2"},
		},
	}

	got := buildEnvPrelude("amd64", ctx, allHosts)

	// Pin the exact expected prelude so any change to ordering,
	// quoting, or key flattening surfaces as a test failure.
	// Per-group exports (all_master_hostname etc.) and per-host
	// pairs beyond k8s_master1_ip are intentionally absent.
	// k8s_hosts_alias is built via shellQuote so the test doesn't
	// have to hand-count escape sequences.
	want := "export os_arch='amd64'" +
		"; export master_node_number='3'" +
		"; export k8s_master1_ip='192.168.170.48'" +
		"; export k8s_hosts_alias=" + shellQuote("192.168.170.48 master1\\n 192.168.170.47 master2") +
		"; export data_root='/data/install-k8s'" +
		"; export k8s_network_stack='ipv4'" +
		"; export registryServer_alias='registry.local'" +
		"; export registryServer_hostName='master2'"
	if got != want {
		t.Errorf("buildEnvPrelude =\n  %q\nwant\n  %q", got, want)
	}
}

// TestBuildEnvPrelude_QuotesValueWithSingleQuote pins that
// values containing single quotes survive the shell-quoting
// step in the prelude — a registry password like `your_password`
// has no quote but the operator might pass anything, so the
// test exercises the `'\”` escape by injecting a quote in a
// var value.
func TestBuildEnvPrelude_QuotesValueWithSingleQuote(t *testing.T) {
	ctx := &vars.Context{
		Vars: map[string]any{"tricky": "it's-a-value"},
	}
	got := buildEnvPrelude("amd64", ctx, nil)
	// shellQuote('it's-a-value') = "'" + "it's-a-value" with '
	// replaced by '\'' + "'" = "'it'\\''s-a-value'"
	if !strings.Contains(got, "export tricky='it'\\''s-a-value'") {
		t.Errorf("buildEnvPrelude did not escape embedded single quote:\n  %q", got)
	}
}

// TestBuildEnvPrelude_EmptyHosts is a degenerate case: an empty
// inventory still yields a valid prelude (just
// `export os_arch=...`) and no panic. A nil allHosts is treated
// the same.
func TestBuildEnvPrelude_EmptyHosts(t *testing.T) {
	ctx := &vars.Context{}
	if got := buildEnvPrelude("arm64", ctx, nil); !strings.HasPrefix(got, "export os_arch='arm64'") {
		t.Errorf("buildEnvPrelude with nil hosts = %q, want os_arch export at the start", got)
	}
	if got := buildEnvPrelude("amd64", ctx, []*inventory.Host{}); !strings.HasPrefix(got, "export os_arch='amd64'") {
		t.Errorf("buildEnvPrelude with empty hosts = %q, want os_arch export at the start", got)
	}
}

// TestBuildEnvPrelude_NoTrailingSemicolon pins that
// buildEnvPrelude does NOT end with a `;` — the Run caller is
// responsible for joining prelude and the user's command with
// `; `, and we want one source of truth for the separator. A
// trailing `;` here would also still parse, but a `;` at the
// very end of the prelude in the Run join would produce a
// doubled separator.
func TestBuildEnvPrelude_NoTrailingSemicolon(t *testing.T) {
	ctx := &vars.Context{
		Vars:         map[string]any{"x": "y"},
		AllHostNames: []string{"m1"},
	}
	got := buildEnvPrelude("amd64", ctx, []*inventory.Host{{Name: "m1", IP: "10.0.0.1"}})
	if strings.HasSuffix(got, ";") {
		t.Errorf("buildEnvPrelude should not end with %q, got: %q", ";", got)
	}
	if strings.HasSuffix(strings.TrimSpace(got), ";") {
		t.Errorf("buildEnvPrelude should not end with %q (after trim), got: %q", ";", got)
	}
}

// TestBuildEnvPrelude_MasterNodeNumber pins the inventory-wide
// `master_node_number` export. The value comes from the host
// facts map (every host sees the same value, but the lookup is
// per-host). A nil/empty host map (e.g. worker-only inventory)
// omits the export entirely rather than writing an empty value.
func TestBuildEnvPrelude_MasterNodeNumber(t *testing.T) {
	cases := []struct {
		name        string
		ctx         *vars.Context
		wantSub     []string
		wantMissing []string
	}{
		{
			name: "three masters",
			ctx: &vars.Context{
				Host: map[string]any{"master_node_number": 3},
			},
			wantSub: []string{"export master_node_number='3'"},
		},
		{
			name: "single master (string form)",
			ctx: &vars.Context{
				Host: map[string]any{"master_node_number": "1"},
			},
			wantSub: []string{"export master_node_number='1'"},
		},
		{
			name: "five masters",
			ctx: &vars.Context{
				Host: map[string]any{"master_node_number": 5},
			},
			wantSub: []string{"export master_node_number='5'"},
		},
		{
			name:        "nil host map omits the export",
			ctx:         &vars.Context{},
			wantMissing: []string{"master_node_number"},
		},
		{
			name: "missing field omits the export",
			ctx: &vars.Context{
				Host: map[string]any{"os_arch": "amd64"},
			},
			wantMissing: []string{"master_node_number"},
		},
		{
			name: "zero value omits the export (worker-only inventory)",
			ctx: &vars.Context{
				Host: map[string]any{"master_node_number": 0},
			},
			wantSub:     []string{"export master_node_number='0'"},
			wantMissing: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildEnvPrelude("amd64", c.ctx, nil)
			for _, want := range c.wantSub {
				if !strings.Contains(got, want) {
					t.Errorf("buildEnvPrelude =\n  %q\nwant substring %q", got, want)
				}
			}
			for _, missing := range c.wantMissing {
				if strings.Contains(got, missing) {
					t.Errorf("buildEnvPrelude = %q, want NO substring %q", got, missing)
				}
			}
		})
	}
}

// TestBuildEnvPrelude_HostsAlias pins the k8s_hosts_alias export:
// one `ip hostname` pair per host, in declaration order,
// separated by a literal `\n ` (backslash + n + space — three
// chars) so a downstream `echo -e "$k8s_hosts_alias" >>/etc/hosts`
// drops each host onto its own line of /etc/hosts. The trailing
// space lets a downstream `IFS=$'\n '` split or a `strings.Split`
// on the same three-char separator recover clean pairs without
// leading whitespace on every non-first line. The list mixes masters and
// workers in the order inventory.All() emits (alphabetical
// group, then declaration within group). Empty / nil host
// slices skip the export entirely rather than emitting
// `export k8s_hosts_alias=”`. Hosts with empty IP or Name are
// dropped (a half-empty pair would corrupt the hosts file).
//
// Completeness is checked per host: every host passed in must
// show up as a `ip hostname` line in the prelude. A regression
// that silently drops a host (e.g. by iterating only one group)
// would fail the per-host substring check even if the overall
// k8s_hosts_alias=... substring matches by coincidence.
//
// Note: the prelude no longer emits per-group
// `all_<group>_hostname` exports — scripts that used to iterate
// `$all_master_hostname` should iterate `$k8s_hosts_alias`
// instead.
func TestBuildEnvPrelude_HostsAlias(t *testing.T) {
	cases := []struct {
		name        string
		hosts       []*inventory.Host
		wantSub     []string
		wantMissing []string
	}{
		{
			name: "masters and workers in declaration order",
			hosts: []*inventory.Host{
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22"},
				{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.42"},
				{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1"},
			},
			wantSub: []string{
				"k8s_hosts_alias=" + shellQuote(
					"192.168.170.48 master1\\n 192.168.170.49 master2\\n 192.168.170.47 master3\\n 192.168.170.22 node1\\n 192.168.170.42 node2\\n 192.168.170.1 ip-192-168-1-1",
				),
			},
			wantMissing: []string{
				"all_master_hostname",
				"all_worker_hostname",
			},
		},
		{
			name: "single master exports k8s_master1_ip and one-pair alias",
			hosts: []*inventory.Host{
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
			},
			wantSub: []string{
				"export k8s_master1_ip='192.168.170.48'",
				"k8s_hosts_alias=" + shellQuote("192.168.170.48 master1"),
			},
		},
		{
			name: "every host from hosts field appears as a pair — workers-only inventory",
			hosts: []*inventory.Host{
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22"},
				{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.42"},
				{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1"},
			},
			wantSub: []string{
				"192.168.170.22 node1",
				"192.168.170.42 node2",
				"192.168.170.1 ip-192-168-1-1",
			},
			wantMissing: []string{
				// workers-only inventory must NOT emit k8s_master1_ip —
				// there's no host with key k8s_master1 to source the IP.
				"k8s_master1_ip",
			},
		},
		{
			name:  "empty hosts slice omits k8s_hosts_alias export",
			hosts: []*inventory.Host{},
		},
		{
			name:  "nil hosts slice omits k8s_hosts_alias export",
			hosts: nil,
		},
		{
			name: "host with empty IP is skipped from alias",
			hosts: []*inventory.Host{
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
				{Key: "k8s_master2", Name: "master2", IP: ""},
			},
			wantSub: []string{
				"k8s_hosts_alias=" + shellQuote("192.168.170.48 master1"),
			},
		},
		{
			name: "per-host pair exports beyond k8s_master1_ip are absent",
			hosts: []*inventory.Host{
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
			},
			wantMissing: []string{
				"k8s_master2_ip",
				"k8s_master2_hostname",
				"k8s_master1_hostname",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildEnvPrelude("amd64", &vars.Context{}, c.hosts)
			for _, want := range c.wantSub {
				if !strings.Contains(got, want) {
					t.Errorf("buildEnvPrelude =\n  %q\nwant substring %q", got, want)
				}
			}
			for _, missing := range c.wantMissing {
				if strings.Contains(got, missing) {
					t.Errorf("buildEnvPrelude = %q, want NO substring %q", got, missing)
				}
			}
			// Empty/nil hosts must NOT emit a k8s_hosts_alias export
			// — the absence check is gated on the input shape, not on
			// whether the case happened to leave wantSub empty (other
			// cases legitimately want the export absent for different
			// reasons and would not match this predicate).
			if len(c.hosts) == 0 {
				if strings.Contains(got, "k8s_hosts_alias") {
					t.Errorf("buildEnvPrelude = %q, want no k8s_hosts_alias export", got)
				}
			}
		})
	}
}

// TestBuildHostsAlias_AllHostsIncluded is the dedicated
// completeness pin for k8s_hosts_alias: every host passed to
// buildHostsAlias must appear as a `ip hostname` line in the
// returned value, in declaration order, with no duplicates and
// each pair separated by a literal `\n ` (backslash + n +
// space — three chars) so a downstream
// `echo -e "$k8s_hosts_alias" >> /etc/hosts` drops each host
// onto its own line.
//
// This is stronger than the substring check inside
// TestBuildEnvPrelude_HostsAlias because it asserts each input
// host is present *individually* and that the result has exactly
// the expected sequence — a regression that drops one host
// (e.g. by iterating only one group) would fail here even if
// the overall k8s_hosts_alias=… substring still appeared to
// match by coincidence.
//
// The test calls buildHostsAlias directly (rather than going
// through buildEnvPrelude + shellQuote) so it can inspect the
// un-escaped inner value without having to round-trip the
// `'\”` shell-quote escape sequences.
func TestBuildHostsAlias_AllHostsIncluded(t *testing.T) {
	// Realistic inventory: 3 masters + 3 workers, mirroring the
	// install-k8s.yaml hosts block (with workers uncommented).
	hosts := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
		{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
		{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.42", User: "root", Password: "your_password"},
		{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1", User: "root", Password: "your_password"},
	}

	// (1) The returned value equals the exact, ordered
	// concatenation of `ip hostname` pairs separated by
	// `\n ` (backslash, n, space — three characters). This is
	// the strongest single check: any missing host, any wrong
	// order, any extra pair (duplicate / misformat) flips this
	// from "PASS" to "FAIL".
	wantSeq := "192.168.170.48 master1\\n 192.168.170.49 master2\\n 192.168.170.47 master3\\n 192.168.170.22 node1\\n 192.168.170.42 node2\\n 192.168.170.1 ip-192-168-1-1"
	if got := buildHostsAlias(hosts); got != wantSeq {
		t.Errorf("buildHostsAlias =\n  %q\nwant\n  %q", got, wantSeq)
	}

	// (2) Per-host completeness: every input host appears in the
	// result. This is redundant with (1) for the success case
	// but produces a clearer error message when it fails — the
	// operator sees which host was missing rather than having
	// to diff two long strings.
	got := buildHostsAlias(hosts)
	for _, h := range hosts {
		pair := fmt.Sprintf("%s %s\\n ", h.IP, h.Name)
		// For the last host there's no trailing `\n ` (it's the
		// last line of the value), so accept either the
		// trailing-separator form above or the bare pair at end.
		if !strings.Contains(got, pair) && !strings.HasSuffix(got, fmt.Sprintf("%s %s", h.IP, h.Name)) {
			t.Errorf("alias missing pair %q (host %+v)\n  alias: %q", pair, h, got)
		}
	}

	// (3) No duplicates: each `ip hostname` line appears
	// exactly once. A regression that walks both `masters` and
	// `allnode` without dedup would slip past (1) if the order
	// happened to remain stable, but (3) catches it.
	//
	// buildHostsAlias emits `pair1\n pair2\n pair3\n ...pairN`
	// — every pair but the last is followed by `\n ` (the
	// separator itself). Splitting on that separator gives
	// back clean `ip hostname` lines without leading spaces;
	// splitting on `\n` alone would leave a leading space on
	// every non-first line and break the equality check below.
	lines := strings.Split(got, `\n `)
	for _, h := range hosts {
		pair := fmt.Sprintf("%s %s", h.IP, h.Name)
		n := 0
		for _, line := range lines {
			if line == pair {
				n++
			}
		}
		if n != 1 {
			t.Errorf("pair %q appears %d times in alias, want exactly 1\n  alias: %q", pair, n, got)
		}
	}
}

// TestApplyChdir covers the pure helper that Run uses to wrap a
// command in `cd <dir> && ...`. The wrapping must:
//   - quote the directory so spaces and special chars survive;
//   - keep the user's command text byte-for-byte unchanged
//     after the prepended cd, so quoting inside the command
//     isn't disturbed;
//   - short-circuit on cd failure (verified by the `&&`
//     operator).
func TestApplyChdir(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		dir  string
		want string
	}{
		{
			name: "simple dir",
			cmd:  "pwd",
			dir:  "/tmp",
			want: "cd '/tmp' && pwd",
		},
		{
			name: "dir with space",
			cmd:  "ls",
			dir:  "/tmp/my dir",
			want: "cd '/tmp/my dir' && ls",
		},
		{
			name: "dir with single quote",
			cmd:  "echo hi",
			dir:  "/tmp/it's",
			// shellQuote escapes an embedded ' as '\''.
			want: "cd '/tmp/it'\\''s' && echo hi",
		},
		{
			name: "template-rendered dir",
			cmd:  "set-os-repo.sh",
			dir:  "/data/install-k8s/common/02-set-local-repo",
			want: "cd '/data/install-k8s/common/02-set-local-repo' && set-os-repo.sh",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := applyChdir(c.cmd, c.dir); got != c.want {
				t.Errorf("applyChdir(%q, %q)\n  = %q\nwant %q", c.cmd, c.dir, got, c.want)
			}
		})
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
