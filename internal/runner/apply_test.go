package runner

import (
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/runner/logstream"
	"steel/internal/runner/shell"
	"steel/internal/vars"
)

// TestRenderHostsSpec covers the host-agnostic render pass
// that Apply runs on every play's `hosts:` field. The contract
// is:
//
//   - plain strings (no `{{`) pass through untouched
//   - host-independent vars resolve (`{{ xxx }}`,
//     `{{ label.field }}`)
//   - per-host placeholders (`{{ hostname }}`, `{{ host.x }}`)
//     error out with a clear message, since at this point we
//     don't know which host the play targets yet
//
// All sub-cases go through the same renderHostsSpec entry
// point so the assertions describe the contract in one place,
// rather than duplicating it across Apply-level integration
// tests that would have to fake out SSH to run at all.
func TestRenderHostsSpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		ctx     *vars.Context
		want    string
		wantErr string // substring; empty means expect success
	}{
		{
			name: "plain_string_passes_through",
			spec: "allnode",
			ctx:  &vars.Context{},
			want: "allnode",
		},
		{
			name: "nested_var_resolves",
			spec: "{{ registryServer.hostName }}",
			ctx: &vars.Context{
				Vars: map[string]any{
					"registryServer": map[string]any{"hostName": "master2"},
				},
			},
			want: "master2",
		},
		{
			name: "bare_var_resolves",
			spec: "{{ data_root }}",
			ctx: &vars.Context{
				Vars: map[string]any{"data_root": "/data/install-k8s"},
			},
			want: "/data/install-k8s",
		},
		{
			name: "comma_separated_template_targets",
			spec: "{{ registryServer.hostName }},{{ apiserverLB.hostName }}",
			ctx: &vars.Context{
				Vars: map[string]any{
					"registryServer": map[string]any{"hostName": "master2"},
					"apiserverLB":    map[string]any{"hostName": "master3"},
				},
			},
			want: "master2,master3",
		},
		{
			name:    "host_builtin_in_hosts_errors",
			spec:    "{{ hostname }}",
			ctx:     &vars.Context{},
			wantErr: `placeholder "hostname"`,
		},
		{
			name:    "host_scope_in_hosts_errors",
			spec:    "{{ host.name }}",
			ctx:     &vars.Context{},
			wantErr: `placeholder "host.name"`,
		},
		{
			name:    "unknown_dotted_in_hosts_errors",
			spec:    "{{ nope.foo }}",
			ctx:     &vars.Context{},
			wantErr: "no vars block",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderHostsSpec(tc.spec, tc.ctx)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("renderHostsSpec(%q) = %q, want error containing %q", tc.spec, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("renderHostsSpec(%q) error = %v, want substring %q", tc.spec, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("renderHostsSpec(%q) error = %v, want %q", tc.spec, err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("renderHostsSpec(%q) = %q, want %q", tc.spec, got, tc.want)
			}
		})
	}
}

// TestHeaderLine verifies the play header line used both on
// the terminal and (mirrored) at the top of every per-host log
// file.
func TestHeaderLine(t *testing.T) {
	play := playbook.Play{Name: "os-init.yaml:本地时间"}
	targets := []*inventory.Host{{Name: "master1"}, {Name: "node1"}}
	got := headerLine(play, targets)
	want := "\n- os-init.yaml:本地时间  hosts=[master1 node1]\n"
	if got != want {
		t.Errorf("headerLine = %q, want %q", got, want)
	}
}

// TestPrintResultLog_WritesToLogFile verifies printResultLog
// writes the verdict line to the supplied per-host log file.
// (The terminal sink is exercised separately by `TestApply`'s
// end-to-end coverage; the result row is intentionally part of
// the "task progress" output the operator sees on the console
// — covered by runPlay's fmt.Fprint call — but this test pins
// the log-side contract that the on-disk file mirrors that
// same row for later inspection.)
func TestPrintResultLog_WritesToLogFile(t *testing.T) {
	dir := t.TempDir()
	s, err := logstream.Open(dir, "p", "h")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close(0, "")

	r := shell.Result{Host: "h", OK: true}
	printResultLog(playbook.Play{Name: "p"}, r, s)

	data, _ := readFile(dir + "/run.log")
	if !strings.Contains(data, "- p  host=h  OK") {
		t.Errorf("log file missing result line: %q", data)
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

// TestComputeNoApiserverIps pins the spec rule:
//
//	# 依据 apiserverLB 字段的hostName, 可以生成不包含apiserverLB_ip的所有master主机IP地址的内置变量，比如noapiserverips="192.168.170.48 192.168.170.49"
//
// i.e. when vars.apiserverLB.hostName is one of the masters,
// the derivation returns every other master's IP, in
// declaration order, space-separated. The cases below cover
// the three valid configurations plus the three guard
// clauses that make the helper refuse to emit a misleading
// value.
func TestComputeNoApiserverIps(t *testing.T) {
	threeMasters := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
		},
	}
	singleMaster := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
			},
		},
	}
	mustInv := func(t *testing.T, f *inventory.File) *inventory.Inventory {
		t.Helper()
		inv, err := inventory.New(f)
		if err != nil {
			t.Fatalf("inventory.New: %v", err)
		}
		return inv
	}

	cases := []struct {
		name string
		inv  *inventory.Inventory
		vars map[string]any
		want string
	}{
		{
			name: "apiserverLB_is_master3_returns_others_in_decl_order",
			inv:  mustInv(t, threeMasters),
			vars: map[string]any{
				"apiserverLB": map[string]any{"hostName": "master3"},
			},
			want: "192.168.170.48 192.168.170.49",
		},
		{
			name: "apiserverLB_is_master1_returns_others_in_decl_order",
			inv:  mustInv(t, threeMasters),
			vars: map[string]any{
				"apiserverLB": map[string]any{"hostName": "master1"},
			},
			want: "192.168.170.49 192.168.170.47",
		},
		{
			name: "single_master_returns_empty_list",
			inv:  mustInv(t, singleMaster),
			vars: map[string]any{
				"apiserverLB": map[string]any{"hostName": "master1"},
			},
			want: "",
		},
		{
			name: "no_apiserverLB_var_returns_empty",
			inv:  mustInv(t, threeMasters),
			vars: map[string]any{},
			want: "",
		},
		{
			name: "apiserverLB_with_no_hostName_returns_empty",
			inv:  mustInv(t, threeMasters),
			vars: map[string]any{
				"apiserverLB": map[string]any{"alias": "apiserver.cluster.local"},
			},
			want: "",
		},
		{
			name: "apiserverLB_hostName_not_in_masters_returns_empty",
			inv:  mustInv(t, threeMasters),
			vars: map[string]any{
				"apiserverLB": map[string]any{"hostName": "ghost"},
			},
			want: "",
		},
		{
			name: "non_string_hostName_returns_empty",
			inv:  mustInv(t, threeMasters),
			vars: map[string]any{
				"apiserverLB": map[string]any{"hostName": 42},
			},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeNoApiserverIps(tc.inv, tc.vars)
			if got != tc.want {
				t.Errorf("computeNoApiserverIps = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestComputeK8sAllIP pins the spec rule:
//
//	# 依据hosts字段，添加k8s_all_ip="192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1"，包含所有masters和workers节点的IP地址。
//
// i.e. when the hosts field declares masters and workers, the
// helper returns every IP concatenated with single spaces, in
// inventory.All() order (alphabetical group: masters before
// workers; declaration order within each group). The order is
// part of the contract — a regression that flipped masters
// and workers would silently change every cluster-id hash
// downstream, so it is pinned below.
//
// The cases cover:
//
//   - masters + workers in the conventional install-k8s
//     layout (3 + 3), mirroring the spec's example;
//   - masters-only inventories (the bootstrap-time case
//     before any worker joins);
//   - single-master (still valid — a developer setup);
//   - order pin: with workers declared BEFORE masters in the
//     same `hosts:` block, masters must still come first in
//     the output (the alphabetical group walk in
//     inventory.All() is what makes this stable — without
//     that, declaration order would leak through).
//
// The "no hosts at all" case is not exercised because
// inventory.New rejects any inventory without a masters
// group of size 1/3/5 — there is no inventory that reaches
// computeK8sAllIP with zero hosts. The helper's early-return
// guard against an empty All() slice is defensive belt-and-
// braces (a nil-safe exit when a test fixture wires up a
// partial Inventory directly).
func TestComputeK8sAllIP(t *testing.T) {
	mastersAndWorkers := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
			"workers": {
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
				{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.43", User: "root", Password: "your_password"},
				{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1", User: "root", Password: "your_password"},
			},
		},
	}
	mastersOnly := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
		},
	}
	singleMaster := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
			},
		},
	}
	// The order pin: workers declared before masters in the
	// same `hosts:` block. inventory.All() walks the groups
	// in alphabetical order via sort.Strings, so masters
	// (M=77) sorts before workers (W=87). If a regression
	// stops sorting, the output would flip and the assertion
	// below fails — that's the point of the case.
	workersBeforeMasters := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"workers": {
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
			},
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
		},
	}
	mustInv := func(t *testing.T, f *inventory.File) *inventory.Inventory {
		t.Helper()
		inv, err := inventory.New(f)
		if err != nil {
			t.Fatalf("inventory.New: %v", err)
		}
		return inv
	}

	cases := []struct {
		name string
		inv  *inventory.Inventory
		want string
	}{
		{
			name: "masters_and_workers_concatenated_in_all_order",
			inv:  mustInv(t, mastersAndWorkers),
			// Exact string from the install-k8s.yaml spec comment.
			want: "192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1",
		},
		{
			name: "masters_only_inventory",
			inv:  mustInv(t, mastersOnly),
			want: "192.168.170.48 192.168.170.49 192.168.170.47",
		},
		{
			name: "single_master_inventory",
			inv:  mustInv(t, singleMaster),
			want: "192.168.170.48",
		},
		{
			name: "order_pin_masters_first_even_when_declared_after_workers",
			inv:  mustInv(t, workersBeforeMasters),
			// masters sort before workers alphabetically, so
			// the worker IP MUST land at the end of the list,
			// not the front — regardless of declaration order
			// in the hosts block. If a regression drops the
			// alphabetical walk (e.g. iterating groupOrder in
			// Go's randomized map order), this assertion flips.
			want: "192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeK8sAllIP(tc.inv)
			if got != tc.want {
				t.Errorf("computeK8sAllIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestComputeK8sAllHostname is the hostname twin of
// TestComputeK8sAllIP. The two helpers share computeAllHostField,
// so the cases mirror the IP test 1:1 with Host.Name substituted
// in. The order pin is identical (masters sort before workers
// alphabetically, so the worker hostname MUST land at the end
// even when the workers group is declared before masters in the
// hosts block) — a regression in computeAllHostField's walk
// would fail both helpers simultaneously, but the per-helper
// pin makes the failure location obvious in CI output.
//
// The exact-string value for the masters+workers case matches
// the install-k8s.yaml spec comment so a spec-doc drift and a
// code drift both surface as a test failure.
func TestComputeK8sAllHostname(t *testing.T) {
	mastersAndWorkers := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
			"workers": {
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
				{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.43", User: "root", Password: "your_password"},
				{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1", User: "root", Password: "your_password"},
			},
		},
	}
	mastersOnly := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
		},
	}
	singleMaster := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
			},
		},
	}
	// Order pin: workers declared BEFORE masters in the
	// same `hosts:` block. Same shape as TestComputeK8sAllIP's
	// order pin — the two helpers share the walk, so this
	// asserts both behaviors simultaneously.
	workersBeforeMasters := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"workers": {
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
			},
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
		},
	}
	mustInv := func(t *testing.T, f *inventory.File) *inventory.Inventory {
		t.Helper()
		inv, err := inventory.New(f)
		if err != nil {
			t.Fatalf("inventory.New: %v", err)
		}
		return inv
	}

	cases := []struct {
		name string
		inv  *inventory.Inventory
		want string
	}{
		{
			name: "masters_and_workers_concatenated_in_all_order",
			inv:  mustInv(t, mastersAndWorkers),
			// Exact string from the install-k8s.yaml spec comment.
			want: "master1 master2 master3 node1 node2 ip-192-168-1-1",
		},
		{
			name: "masters_only_inventory",
			inv:  mustInv(t, mastersOnly),
			want: "master1 master2 master3",
		},
		{
			name: "single_master_inventory",
			inv:  mustInv(t, singleMaster),
			want: "master1",
		},
		{
			name: "order_pin_masters_first_even_when_declared_after_workers",
			inv:  mustInv(t, workersBeforeMasters),
			// masters sort before workers alphabetically, so
			// the worker hostname MUST land at the end of the
			// list, not the front — regardless of declaration
			// order in the hosts block.
			want: "master1 master2 master3 node1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeK8sAllHostname(tc.inv)
			if got != tc.want {
				t.Errorf("computeK8sAllHostname = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestComputeK8sAllIP_ExcludesAddWorkers pins the install-k8s.yaml
// spec rule "addworkers 是一个独立的组" at the runner level. The
// bootstrap-time `k8s_all_ip` aggregate must list only the hosts
// the bootstrap touched (masters + the original workers) — never
// the nodes the operator declares under `addworkers:` for a later
// expansion. A regression here would silently include post-install
// nodes in etcd's --initial-cluster and kubelet peer lists.
//
// The test mirrors the mastersAndWorkers fixture from
// TestComputeK8sAllIP and adds a populated addworkers block; the
// expected value is byte-identical to the no-addworkers case so a
// reviewer can confirm the exclusion is the only difference.
func TestComputeK8sAllIP_ExcludesAddWorkers(t *testing.T) {
	withAddWorkers := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
			"workers": {
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
			},
			"addworkers": {
				{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.42", User: "root", Password: "your_password"},
				{Key: "k8s_worker3", Name: "node3", IP: "192.168.170.43", User: "root", Password: "your_password"},
			},
		},
	}
	inv, err := inventory.New(withAddWorkers)
	if err != nil {
		t.Fatalf("inventory.New: %v", err)
	}
	// Expected: only the 3 masters + 1 worker, identical to the
	// no-addworkers case from TestComputeK8sAllIP.
	want := "192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22"
	got := computeK8sAllIP(inv)
	if got != want {
		t.Errorf("computeK8sAllIP with addworkers = %q, want %q (addworkers must be excluded)", got, want)
	}

	// Empty addworkers: same expected output (the exclusion is
	// a no-op when nothing is there, but the contract must hold
	// end-to-end so the install-k8s.yaml default doesn't drift).
	emptyAddWorkers := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
			},
			"addworkers": {},
		},
	}
	inv, err = inventory.New(emptyAddWorkers)
	if err != nil {
		t.Fatalf("inventory.New (empty addworkers): %v", err)
	}
	if got := computeK8sAllIP(inv); got != "192.168.170.48" {
		t.Errorf("computeK8sAllIP with empty addworkers = %q, want 192.168.170.48", got)
	}
}

// TestComputeK8sAllHostname_ExcludesAddWorkers is the hostname
// twin of TestComputeK8sAllIP_ExcludesAddWorkers. Same fixture
// shape (masters + workers + addworkers), same byte-identical
// expected output to the no-addworkers case. The two helpers
// share computePrimaryHostField under the hood, but each gets
// its own test so a CI failure points at the right field.
func TestComputeK8sAllHostname_ExcludesAddWorkers(t *testing.T) {
	withAddWorkers := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password"},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password"},
			},
			"workers": {
				{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22", User: "root", Password: "your_password"},
			},
			"addworkers": {
				{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.42", User: "root", Password: "your_password"},
				{Key: "k8s_worker3", Name: "node3", IP: "192.168.170.43", User: "root", Password: "your_password"},
			},
		},
	}
	inv, err := inventory.New(withAddWorkers)
	if err != nil {
		t.Fatalf("inventory.New: %v", err)
	}
	want := "master1 master2 master3 node1"
	got := computeK8sAllHostname(inv)
	if got != want {
		t.Errorf("computeK8sAllHostname with addworkers = %q, want %q (addworkers must be excluded)", got, want)
	}
}

// TestDerivePluginVars_BoolPlugins pins the install-k8s.yaml
// "plugin: { traefikServer: true }" -> `traefikServer=<hostName>`
// derivation. The boolean plugin on a host surfaces the host's
// Name as the value of the top-level `<key>` var, which the
// downstream ExpandTopLevelHostnameIPs pass then auto-enriches
// with a `<key>_ip` sibling. Cases cover:
//
//   - single boolean plugin on one host (the canonical case);
//   - multiple boolean plugins across distinct hosts (the
//     install-k8s.yaml test fixture shape);
//   - `plugin: { <key>: false }` is treated as "this host is
//     not the <key> server" and the var is OMITTED (not
//     emitted as `<key>=false`, which would be a hostname
//     lookup miss and a confusing shell var);
//   - hosts without a `plugin:` field contribute nothing.
//
// The "user-declared wins" rule is exercised in
// TestDerivePluginVars_UserDeclaredWins — the cases here use a
// nil / empty `existing` so the plugin field is the only
// source.
func TestDerivePluginVars_BoolPlugins(t *testing.T) {
	singleHost := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password",
					Plugin: map[string]any{"traefikServer": true}},
			},
		},
	}
	multipleBool := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password",
					Plugin: map[string]any{"traefikServer": true}},
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password",
					Plugin: map[string]any{"nfsServer": true}},
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password",
					Plugin: map[string]any{"timeServer": true}},
			},
		},
	}
	falsePlugin := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password",
					Plugin: map[string]any{"traefikServer": false}},
			},
		},
	}
	noPluginField := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password"},
			},
		},
	}
	mustInv := func(t *testing.T, f *inventory.File) *inventory.Inventory {
		t.Helper()
		inv, err := inventory.New(f)
		if err != nil {
			t.Fatalf("inventory.New: %v", err)
		}
		return inv
	}

	cases := []struct {
		name string
		inv  *inventory.Inventory
		have map[string]any
		want map[string]any
	}{
		{
			name: "single_bool_plugin_emits_hostName_as_value",
			inv:  mustInv(t, singleHost),
			want: map[string]any{"traefikServer": "master1"},
		},
		{
			name: "multiple_bool_plugins_collect_one_var_per_host",
			inv:  mustInv(t, multipleBool),
			want: map[string]any{
				"traefikServer": "master1",
				"nfsServer":     "master2",
				"timeServer":    "master3",
			},
		},
		{
			name: "bool_false_is_omitted",
			inv:  mustInv(t, falsePlugin),
			want: map[string]any{},
		},
		{
			name: "host_without_plugin_field_contributes_nothing",
			inv:  mustInv(t, noPluginField),
			want: map[string]any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := derivePluginVars(tc.inv, tc.have)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("derivePluginVars = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDerivePluginVars_MapPlugins pins the install-k8s.yaml
// "plugin: { registryServer: { alias: registry.local } }"
// derivation. The map plugin on a host surfaces a nested map
// with `hostName: <hostName>` and `ip: <hostIP>` injected as
// siblings to the user-supplied fields (`alias` etc.), so the
// downstream vars.ExpandHostNameIPs pass auto-derives
// `<key>_ip` and the shell prelude's map-flattening pass emits
// `<key>_alias`, `<key>_hostName`, `<key>_ip`.
//
// Cases cover:
//
//   - the canonical install-k8s.yaml shape: `registryServer:
//     { alias: registry.local }` on master2 — must produce
//     `{ hostName: master2, ip: 192.168.170.49, alias:
//     registry.local }`;
//   - the apiserverLB counterpart, pinning that the helper
//     doesn't accidentally special-case registryServer;
//   - map value with NO user-supplied fields (just an empty
//     map) — must still produce `{ hostName, ip }` so the
//     downstream pass has something to derive from;
//   - map value that already has its own `hostName` field
//     (forward-compat: a user might set one explicitly) — the
//     helper's `hostName` and `ip` siblings are
//     unconditionally set, so the user-declared `hostName` is
//     overwritten. This matches the spec's authority
//     (the plugin field is the host's role assignment — the
//     host the plugin lives on IS the hostName, by
//     construction).
func TestDerivePluginVars_MapPlugins(t *testing.T) {
	registryHost := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password",
					Plugin: map[string]any{
						"registryServer": map[string]any{"alias": "registry.local"},
					}},
			},
		},
	}
	apiserverHost := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", User: "root", Password: "your_password",
					Plugin: map[string]any{
						"apiserverLB": map[string]any{"alias": "apiserver.cluster.local"},
					}},
			},
		},
	}
	emptyMap := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", User: "root", Password: "your_password",
					Plugin: map[string]any{
						"someRole": map[string]any{},
					}},
			},
		},
	}
	mustInv := func(t *testing.T, f *inventory.File) *inventory.Inventory {
		t.Helper()
		inv, err := inventory.New(f)
		if err != nil {
			t.Fatalf("inventory.New: %v", err)
		}
		return inv
	}

	cases := []struct {
		name string
		inv  *inventory.Inventory
		want map[string]any
	}{
		{
			name: "registryServer_with_alias_injects_hostName_and_ip",
			inv:  mustInv(t, registryHost),
			want: map[string]any{
				"registryServer": map[string]any{
					"hostName": "master2",
					"ip":       "192.168.170.49",
					"alias":    "registry.local",
				},
			},
		},
		{
			name: "apiserverLB_with_alias_injects_hostName_and_ip",
			inv:  mustInv(t, apiserverHost),
			want: map[string]any{
				"apiserverLB": map[string]any{
					"hostName": "master3",
					"ip":       "192.168.170.47",
					"alias":    "apiserver.cluster.local",
				},
			},
		},
		{
			name: "empty_map_value_still_gets_hostName_and_ip_siblings",
			inv:  mustInv(t, emptyMap),
			want: map[string]any{
				"someRole": map[string]any{
					"hostName": "master2",
					"ip":       "192.168.170.49",
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := derivePluginVars(tc.inv, nil)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("derivePluginVars = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDerivePluginVars_UserDeclaredWins pins the "user-declared
// vars win" override policy: when the operator declares a
// `<key>: <value>` in the top-level `vars:` block AND a host
// also has the same key in its `plugin:` field, the user's
// declaration is preserved and the plugin field is ignored for
// that key. This is the same override policy every other
// auto-derived var follows in Apply (see k8s_all_ip,
// noapiserverips, k8s_all_hostname) — silently overwriting
// would surprise users who shared the var deliberately.
func TestDerivePluginVars_UserDeclaredWins(t *testing.T) {
	inv := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password",
					Plugin: map[string]any{"nfsServer": true}},
			},
		},
	}
	parsed, err := inventory.New(inv)
	if err != nil {
		t.Fatalf("inventory.New: %v", err)
	}
	// User-declared: nfsServer is "master2" via top-level vars.
	// The plugin field on master1 also names nfsServer, but
	// the user value must win.
	existing := map[string]any{"nfsServer": "master2"}
	got := derivePluginVars(parsed, existing)
	if got != nil {
		if _, has := got["nfsServer"]; has {
			t.Errorf("derivePluginVars should not overwrite user-declared nfsServer; got %+v", got)
		}
	}
	// The merge pattern used in apply.go: start from existing,
	// then layer derived on top. The user value survives.
	merged := make(map[string]any, len(existing)+len(got))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range got {
		merged[k] = v
	}
	if merged["nfsServer"] != "master2" {
		t.Errorf("merged nfsServer = %v, want master2 (user-declared wins)", merged["nfsServer"])
	}
}

// TestDerivePluginVars_PassThroughUnknownShape pins the
// forward-compat hook: plugin values that aren't bool or
// map[string]any (e.g. a string, an int, a list) are passed
// through untouched into the derived map. The shell prelude's
// writeEnvFromAny flattens any value the spec throws at it,
// so a new plugin kind can be added without bumping the Go
// decoder.
func TestDerivePluginVars_PassThroughUnknownShape(t *testing.T) {
	inv := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password",
					Plugin: map[string]any{
						"label":        "k8s-master",
						"replicas":     3,
						"allowedCIDRs": []any{"10.0.0.0/8", "192.168.0.0/16"},
					}},
			},
		},
	}
	parsed, err := inventory.New(inv)
	if err != nil {
		t.Fatalf("inventory.New: %v", err)
	}
	got := derivePluginVars(parsed, nil)
	if got["label"] != "k8s-master" {
		t.Errorf("derivePluginVars label = %v, want k8s-master", got["label"])
	}
	if got["replicas"] != 3 {
		t.Errorf("derivePluginVars replicas = %v, want 3", got["replicas"])
	}
	if !reflect.DeepEqual(got["allowedCIDRs"], []any{"10.0.0.0/8", "192.168.0.0/16"}) {
		t.Errorf("derivePluginVars allowedCIDRs = %v, want [10.0.0.0/8 192.168.0.0/16]", got["allowedCIDRs"])
	}
}

// TestDerivePluginVars_NilInventory pins the nil-safe
// contract: a nil inventory yields a nil map (no work to do,
// no panic) so the caller's `if len(...) > 0` guard skips the
// merge cleanly.
func TestDerivePluginVars_NilInventory(t *testing.T) {
	if got := derivePluginVars(nil, nil); got != nil {
		t.Errorf("derivePluginVars(nil) = %+v, want nil", got)
	}
}

// TestDerivePluginVars_DoesNotMutateExisting pins the
// non-mutation contract: the user's playbookVars map (passed
// as `existing`) is not modified. A regression here would
// surface as the second call into Apply seeing a vars map
// polluted with derived values, and would be hard to debug.
func TestDerivePluginVars_DoesNotMutateExisting(t *testing.T) {
	inv := &inventory.File{
		Hosts: map[string][]inventory.Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", User: "root", Password: "your_password",
					Plugin: map[string]any{"traefikServer": true}},
			},
		},
	}
	parsed, err := inventory.New(inv)
	if err != nil {
		t.Fatalf("inventory.New: %v", err)
	}
	existing := map[string]any{"data_root": "/data/install-k8s"}
	snapshot := map[string]any{"data_root": "/data/install-k8s"}
	_ = derivePluginVars(parsed, existing)
	if !reflect.DeepEqual(existing, snapshot) {
		t.Errorf("derivePluginVars mutated existing\n  got:  %+v\n  want: %+v", existing, snapshot)
	}
}
