package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadAndResolve(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 10.0.0.1
      user: root
      password: your_password
  workers:
    k8s_node01:
      hostname: node01
      ip: 10.0.0.2
      user: root
      password: your_password
    k8s_node02:
      hostname: node02
      ip: 10.0.0.3
      user: root
      password: your_password
allnode:
  - masters
  - workers
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	masters, err := got.Hosts("masters")
	if err != nil || len(masters) != 1 || masters[0].Name != "master1" {
		t.Errorf("Hosts(masters) = %v, %v; want [master1]", masters, err)
	}
	workers, err := got.Hosts("workers")
	if err != nil || len(workers) != 2 {
		t.Errorf("Hosts(workers) = %v, %v; want 2 hosts", workers, err)
	}
	single, err := got.Hosts("node01")
	if err != nil || len(single) != 1 || single[0].IP != "10.0.0.2" {
		t.Errorf("Hosts(node01) = %v, %v", single, err)
	}
	all, err := got.Hosts("allnode")
	if err != nil || len(all) != 3 {
		t.Errorf("Hosts(allnode) = %v, %v; want 3 hosts", all, err)
	}
	if _, err := got.Hosts("ghost"); err == nil {
		t.Errorf("Hosts(ghost): want error")
	}
}

func TestGroupOfGroups(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_m1: {hostname: m1, ip: 10.0.0.1, user: root, password: your_password}
    k8s_m2: {hostname: m2, ip: 10.0.0.2, user: root, password: your_password}
    k8s_m3: {hostname: m3, ip: 10.0.0.3, user: root, password: your_password}
  workers:
    k8s_w1: {hostname: w1, ip: 10.0.0.10, user: root, password: your_password}
allnode:
  - masters
  - workers
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	all, err := got.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	if len(all) != 4 {
		names := make([]string, 0, len(all))
		for _, h := range all {
			names = append(names, h.Name)
		}
		t.Errorf("Hosts(allnode) = %v, want 4 hosts (3 masters + 1 worker)", names)
	}
	// De-duplication: a group that transitively references the same
	// host twice (via two child groups that both contain it) should
	// only appear once in the resolved list. (Hosts may only be
	// declared once in `hosts:`, but they can appear in multiple
	// group-of-groups via sibling `groups:` keys — that's the
	// dedup path being tested here.) The `masters` group is added
	// to satisfy the inventory's masters-count validation, but the
	// dedup-under-test uses the unrelated `a` / `b` / `c` groups.
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_m0: {hostname: m0, ip: 10.0.0.10, user: root, password: your_password}
  a:
    k8s_m1: {hostname: m1, ip: 10.0.0.1, user: root, password: your_password}
  b:
    k8s_m2: {hostname: m2, ip: 10.0.0.2, user: root, password: your_password}
a_group: [m1]
b_group: [m1, m2]
c:
  - a_group
  - b_group
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(inv)
	if err != nil {
		t.Fatalf("Load (dedup): %v", err)
	}
	c, err := got.Hosts("c")
	if err != nil {
		t.Fatalf("Hosts(c): %v", err)
	}
	if len(c) != 2 {
		names := make([]string, 0, len(c))
		for _, h := range c {
			names = append(names, h.Name)
		}
		t.Errorf("Hosts(c) = %v, want 2 unique hosts", names)
	}
}

func TestGroupCycle(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  a:
    k8s_m1: {hostname: m1, ip: 10.0.0.1, user: root, password: your_password}
a:
  - b
b:
  - a
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(inv); err == nil {
		t.Errorf("Load: want cycle error, got nil")
	}
}

func TestUnknownGroupMember(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  a:
    k8s_m1: {hostname: m1, ip: 10.0.0.1, user: root, password: your_password}
a:
  - ghost
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(inv); err == nil {
		t.Errorf("Load: want unknown-member error, got nil")
	}
}

// TestShorthandFieldNames verifies that the per-host entry can use the
// shorthand field names (hostname/hostip/username/sshkey) that match
// the built-in env-var shortcuts exposed in templates. A shorthand
// value must land in the corresponding canonical Host field.
func TestShorthandFieldNames(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      hostip: 192.168.1.10
      username: root
      password: your_password
    k8s_master2:
      hostname: master2
      hostip: 192.168.1.11
      username: root
      sshkey: ~/.ssh/id_rsa
    k8s_master3:
      hostname: master3
      hostip: 192.168.1.12
      username: root
      password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	masters, err := got.Hosts("masters")
	if err != nil || len(masters) != 3 {
		t.Fatalf("Hosts(masters) = %v, %v; want 3 hosts", masters, err)
	}
	if masters[0].Name != "master1" || masters[0].IP != "192.168.1.10" || masters[0].User != "root" || masters[0].Password != "your_password" {
		t.Errorf("master1 = %+v, want Name=master1 IP=192.168.1.10 User=root Password=x", masters[0])
	}
	if masters[1].Name != "master2" || masters[1].IP != "192.168.1.11" || masters[1].User != "root" || masters[1].SSHKey == "" {
		t.Errorf("master2 = %+v, want canonical fields populated from shorthand", masters[1])
	}
	// `~` in sshkey should be expanded the same way as for ssh_key.
	if !filepath.IsAbs(masters[1].SSHKey) {
		t.Errorf("SSHKey = %q, want ~-expanded absolute path", masters[1].SSHKey)
	}
}

// TestMixedFieldNames confirms that a host entry mixing canonical and
// shorthand keys (which we don't expect in practice but want to be
// robust to) still loads — canonical wins when both are present.
// A separate `masters` group is included so the inventory satisfies
// the masters-count validation; the test exercises only the
// shorthand-vs-canonical field mapping on the `g` group's host.
func TestMixedFieldNames(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_m0: {hostname: m0, ip: 10.0.0.10, user: root, password: your_password}
  g:
    k8s_g1:
      name: from-canonical
      hostname: from-shorthand
      hostip: 10.0.0.1
      ip: 10.0.0.2
      user: root
      password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	all := got.All()
	if len(all) != 2 {
		t.Fatalf("len(All()) = %d, want 2 (1 master + 1 in g)", len(all))
	}
	// Find the host under test (masters may sort first).
	var target *Host
	for _, h := range all {
		if h.IP == "10.0.0.2" {
			target = h
			break
		}
	}
	if target == nil {
		t.Fatalf("did not find the test host (IP 10.0.0.2) in All(): %+v", all)
	}
	// Canonical takes precedence over shorthand when both are set.
	if target.Name != "from-canonical" {
		t.Errorf("Name = %q, want from-canonical (canonical wins)", target.Name)
	}
	if target.IP != "10.0.0.2" {
		t.Errorf("IP = %q, want 10.0.0.2 (canonical wins)", target.IP)
	}
}

// TestEmptyGroup is a sanity check: an empty group (e.g.
// `workers: []`) is allowed and yields no hosts.
func TestEmptyGroup(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"}},
			"workers": {},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, _ := inv.Hosts("workers"); len(got) != 0 {
		t.Errorf("Hosts(workers) = %v, want empty", got)
	}
	if got, _ := inv.Hosts("masters"); len(got) != 1 {
		t.Errorf("Hosts(masters) = %v, want 1", got)
	}
	// allnode is a built-in group: it spans every host declared under
	// hosts: (masters and workers combined), so we expect 3 group
	// entries including allnode, and allnode should yield exactly the
	// m1 host (workers is empty).
	if got := inv.GroupNames(); len(got) != 3 {
		t.Errorf("GroupNames() = %v, want 3 groups (masters, workers, allnode)", got)
	}
	if got, _ := inv.Hosts("allnode"); len(got) != 1 || got[0].Name != "m1" {
		t.Errorf("Hosts(allnode) = %v, want [m1]", got)
	}
	wantGroups := map[string][]string{
		"masters": {"m1"},
		"workers": {},
		"allnode": {"m1"},
	}
	if !reflect.DeepEqual(inv.groups, wantGroups) {
		t.Errorf("groups = %+v, want %+v", inv.groups, wantGroups)
	}
}

// TestAllnodeBuiltIn verifies that allnode works as a built-in group
// (without any user-declared sibling `allnode:` key) and yields every
// host declared under hosts:.
func TestAllnodeBuiltIn(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
				{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
			},
			"workers": {
				{Name: "w1", IP: "10.0.0.4", User: "root", Password: "your_password"},
			},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all, err := inv.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	want := []string{"m1", "m2", "m3", "w1"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Hosts(allnode) names = %v, want %v", names, want)
	}
}

// TestAll_OrderMastersBeforeWorkers pins the deterministic ordering
// of inv.All(): host groups are walked in alphabetical order, with
// masters coming before workers (the conventional layout in
// install-k8s.yaml). This is the order inventory-wide builtins like
// `all_node_hostname` see, so locking it down here means a future
// change to the inventory iteration can't silently drop or shuffle
// the masters/workers split. A previous iteration of the code used
// `for _, h := range i.hosts` and got whatever Go's map iteration
// happened to yield — a stable contract, not a coincidence.
func TestAll_OrderMastersBeforeWorkers(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
				{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
			},
			"workers": {
				{Name: "w1", IP: "10.0.0.10", User: "root", Password: "your_password"},
				{Name: "w2", IP: "10.0.0.11", User: "root", Password: "your_password"},
			},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all := inv.All()
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	want := []string{"m1", "m2", "m3", "w1", "w2"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("All() names = %v, want %v (masters before workers)", names, want)
	}

	// The same order must surface through the implicit `allnode`
	// group (which is what {{ all_node_hostname }} and the
	// `hosts: allnode` selector both go through).
	allNode, err := inv.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	names = names[:0]
	for _, h := range allNode {
		names = append(names, h.Name)
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Hosts(allnode) names = %v, want %v", names, want)
	}
}

// TestAll_EmptyWorkersStillIncludesMasters exercises the install-k8s.yaml
// shape (3 masters + workers: []) — All() and allnode should yield
// just the masters. This is the case the bundled playbook actually
// runs against today, so the regression here is the regression that
// would silently shrink `all_node_hostname` in the operator's
// shell scripts.
func TestAll_EmptyWorkersStillIncludesMasters(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Name: "master1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				{Name: "master2", IP: "10.0.0.2", User: "root", Password: "your_password"},
				{Name: "master3", IP: "10.0.0.3", User: "root", Password: "your_password"},
			},
			"workers": {},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all := inv.All()
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	want := []string{"master1", "master2", "master3"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("All() names = %v, want %v", names, want)
	}
}

// TestAllnodeExplicitOverride verifies that an explicit user-declared
// `allnode:` sibling key overrides the built-in (e.g. the user can
// pick a strict subset or an explicit ordering).
func TestAllnodeExplicitOverride(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
				{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
			},
			"workers": {
				{Name: "w1", IP: "10.0.0.4", User: "root", Password: "your_password"},
			},
		},
		Groups: map[string][]string{
			"allnode": {"m1"},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all, err := inv.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	if len(all) != 1 || all[0].Name != "m1" {
		t.Errorf("Hosts(allnode) = %v, want [m1] (explicit override)", all)
	}
}

// TestLoadKeyedMapForm exercises the install-k8s.yaml canonical
// shape: `hosts: { masters: { k8s_master1: {hostname: master1, ip: ...}}}`.
// The map key populates Host.Key (used to build `k8s_<key>_ip` env
// vars); the `hostname:` field populates Host.Name (used for SSH
// and the `{{ hostname }}` template builtin). Declaration order
// is preserved so per-host env vars appear in the same order as
// the YAML file.
func TestLoadKeyedMapForm(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: your_password
    k8s_master2:
      hostname: master2
      ip: 192.168.170.47
      user: root
      password: your_password
    k8s_master3:
      hostname: master3
      ip: 192.168.170.46
      user: root
      password: your_password
  workers:
    k8s_worker1:
      hostname: node1
      ip: 192.168.170.22
      user: root
      password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	masters, err := got.Hosts("masters")
	if err != nil || len(masters) != 3 {
		t.Fatalf("Hosts(masters) = %v, %v; want 3 hosts", masters, err)
	}
	if masters[0].Key != "k8s_master1" || masters[0].Name != "master1" || masters[0].IP != "192.168.170.48" {
		t.Errorf("master1 = %+v, want Key=k8s_master1 Name=master1 IP=192.168.170.48", masters[0])
	}
	if masters[1].Key != "k8s_master2" || masters[1].Name != "master2" {
		t.Errorf("master2 = %+v, want Key=k8s_master2 Name=master2", masters[1])
	}
	if masters[2].Key != "k8s_master3" || masters[2].Name != "master3" {
		t.Errorf("master3 = %+v, want Key=k8s_master3 Name=master3", masters[2])
	}
	workers, err := got.Hosts("workers")
	if err != nil || len(workers) != 1 || workers[0].Key != "k8s_worker1" || workers[0].Name != "node1" {
		t.Errorf("workers = %+v, want Key=k8s_worker1 Name=node1", workers)
	}
}

// TestNewKeyFromMapKey confirms that a host loaded via the
// keyed-map form ends up with Host.Key populated from the map
// key. The list-form fallback no longer exists — every host
// must come in through a keyed map so the `k8s_<key>_ip` env
// var has a stable identifier to draw from.
func TestNewKeyFromMapKey(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Key: "k8s_master1", Name: "master1", IP: "10.0.0.1", User: "root", Password: "your_password"},
			},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	hs, err := inv.Hosts("masters")
	if err != nil || len(hs) != 1 {
		t.Fatalf("Hosts(masters) = %v, %v", hs, err)
	}
	if hs[0].Key != "k8s_master1" {
		t.Errorf("Key = %q, want k8s_master1 (populated from map key)", hs[0].Key)
	}
}

// TestRejectsListForm pins the spec's "keyed-map form only"
// rule at the YAML decode boundary. A group whose value is a
// YAML sequence (the legacy list form) must be rejected
// outright with a clear, group-pointed error message — the
// spec dropped that form because the per-host env-var naming
// needs a stable identifier to draw from, and the keyed-map
// form is what supplies that.
func TestRejectsListForm(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    - hostname: master1
      ip: 10.0.0.1
      user: root
      password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(inv)
	if err == nil {
		t.Fatalf("Load: want error rejecting list-form group, got nil")
	}
	if !strings.Contains(err.Error(), "masters") {
		t.Errorf("err = %q, want it to mention the offending group", err)
	}
}

// TestMasterCount_Acceptance pins the supported etcd control-plane
// quorum sizes (1 / 3 / 5). Any of those three inventory shapes
// must load successfully and MasterCount() must report the right
// number — that's the value the shell prelude exports as
// $master_node_number and that {{ master_node_number }} resolves
// to in templates.
func TestMasterCount_Acceptance(t *testing.T) {
	cases := []struct {
		name string
		hs   []Host
		want int
	}{
		{"single master", []Host{
			{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
		}, 1},
		{"three masters", []Host{
			{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
			{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
			{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
		}, 3},
		{"five masters", []Host{
			{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
			{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
			{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
			{Name: "m4", IP: "10.0.0.4", User: "root", Password: "your_password"},
			{Name: "m5", IP: "10.0.0.5", User: "root", Password: "your_password"},
		}, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &File{Hosts: map[string][]Host{"masters": c.hs}}
			inv, err := New(f)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := inv.MasterCount(); got != c.want {
				t.Errorf("MasterCount() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestMasterCount_Rejection pins the spec's "only 1, 3, or 5"
// rule literally. Every other masters-group size — 0, 2, 4, 6, 7,
// and beyond — must be rejected by New with a message that names
// the offending count and the supported values, so an operator
// hitting it gets a clear hint rather than a half-bootstrapped
// cluster later.
func TestMasterCount_Rejection(t *testing.T) {
	mk := func(n int) []Host {
		hs := make([]Host, n)
		for i := 0; i < n; i++ {
			hs[i] = Host{Name: fmt.Sprintf("m%d", i+1), IP: fmt.Sprintf("10.0.0.%d", i+1), User: "root", Password: "your_password"}
		}
		return hs
	}
	for _, n := range []int{0, 2, 4, 6, 7} {
		t.Run(fmt.Sprintf("%d_masters", n), func(t *testing.T) {
			f := &File{Hosts: map[string][]Host{"masters": mk(n)}}
			inv, err := New(f)
			if err == nil {
				t.Errorf("New with %d masters: want error, got nil (inv=%+v)", n, inv)
				return
			}
			if inv != nil {
				t.Errorf("New with %d masters: want nil inv on error, got %+v", n, inv)
			}
			// Error must mention the count and the supported
			// values so the operator doesn't have to grep the
			// source to understand what's accepted.
			want := fmt.Sprintf("%d", n)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q, want it to mention count %s", err, want)
			}
			if !strings.Contains(err.Error(), "1, 3, or 5") {
				t.Errorf("error %q, want it to mention supported sizes", err)
			}
		})
	}
}

// TestMasterCount_NoMastersGroup pins the spec's strict "masters
// group is mandatory" rule. steel exports the masters-group size
// as `master_node_number`, so an inventory without a masters
// group has no value to export — and the bootstrapping playbooks
// only target masters anyway, so a worker-only inventory is out
// of scope. New must reject with a message that mentions both
// the missing group and the supported sizes.
func TestMasterCount_NoMastersGroup(t *testing.T) {
	f := &File{Hosts: map[string][]Host{
		"workers": {
			{Name: "n1", IP: "10.0.0.1", User: "root", Password: "your_password"},
			{Name: "n2", IP: "10.0.0.2", User: "root", Password: "your_password"},
		},
	}}
	inv, err := New(f)
	if err == nil {
		t.Fatalf("New with no masters group: want error, got nil (inv=%+v)", inv)
	}
	if inv != nil {
		t.Errorf("New with no masters group: want nil inv on error, got %+v", inv)
	}
	if !strings.Contains(err.Error(), "masters") {
		t.Errorf("error %q, want it to mention 'masters'", err)
	}
	if !strings.Contains(err.Error(), "1, 3, or 5") {
		t.Errorf("error %q, want it to mention supported sizes", err)
	}
}

// TestMasterCount_LoadShape confirms the validation fires on
// the on-disk YAML load path too (not just the in-memory New
// path). The install-k8s.yaml file uses the keyed-map form for
// hosts, so this test exercises that shape end-to-end: parse
// hosts/masters, validate the count, expose MasterCount().
func TestMasterCount_LoadShape(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 10.0.0.1
      user: root
      password: your_password
    k8s_master2:
      hostname: master2
      ip: 10.0.0.2
      user: root
      password: your_password
    k8s_master3:
      hostname: master3
      ip: 10.0.0.3
      user: root
      password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c := got.MasterCount(); c != 3 {
		t.Errorf("MasterCount() = %d, want 3", c)
	}

	// And the rejection case via Load (2 masters — even, not a
	// valid etcd quorum).
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: m1
      ip: 10.0.0.1
      user: root
      password: your_password
    k8s_master2:
      hostname: m2
      ip: 10.0.0.2
      user: root
      password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(inv); err == nil {
		t.Errorf("Load with 2 masters: want error, got nil")
	}
}

// TestIsGroup pins the IsGroup predicate contract — used by
// inventory inspection / UI hints to distinguish group names
// from single-host names. The runner's per-play dispatch
// decision now lives on playbook.Play.ExecutionMode (see
// runner/apply.go), so this test only pins the predicate's
// shape, not its effect on dispatch:
//
//   - declared host-group names (both under `hosts:` and as
//     sibling group keys like `allnode:`) report true;
//   - single host names report false — even when the user has
//     also declared a group with the same name (which `New`
//     rejects anyway, but the predicate resolves the ambiguity
//     in favor of "host wins");
//   - unknown names report false so callers fall through to
//     `Hosts()` for the proper "unknown target" error path.
func TestIsGroup(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1: {hostname: master1, ip: 10.0.0.1, user: root, password: your_password}
    k8s_master2: {hostname: master2, ip: 10.0.0.2, user: root, password: your_password}
    k8s_master3: {hostname: master3, ip: 10.0.0.3, user: root, password: your_password}
  workers:
    k8s_node1: {hostname: node1, ip: 10.0.0.10, user: root, password: your_password}
allnode:
  - masters
  - workers
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"sibling_group_key", "allnode", true},
		{"hosts_group_masters", "masters", true},
		{"hosts_group_workers", "workers", true},
		{"single_host_master1", "master1", false},
		{"single_host_node1", "node1", false},
		{"unknown_name", "ghost", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if g := got.IsGroup(tc.in); g != tc.want {
				t.Errorf("IsGroup(%q) = %v, want %v", tc.in, g, tc.want)
			}
		})
	}
}

// TestLoadPluginField pins the YAML decoder contract for the
// per-host `plugin:` field added in the install-k8s.yaml
// format change. Two value shapes must round-trip: a boolean
// (the `traefikServer: true` form) and a map (the
// `registryServer: { alias: registry.local }` form). A host
// without a `plugin:` field must have a nil Plugin map (no
// zero-value allocation, no empty map to confuse the runner
// into "every host has a plugin"). A regression in the
// decoder would surface as derivePluginVars seeing a nil/empty
// Plugin and silently dropping the env-var exports the spec
// promises.
func TestLoadPluginField(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: your_password
      plugin:
        traefikServer: true
    k8s_master2:
      hostname: master2
      ip: 192.168.170.49
      user: root
      password: your_password
      plugin:
        registryServer:
          alias: registry.local
        nfsServer: true
    k8s_master3:
      hostname: master3
      ip: 192.168.170.47
      user: root
      password: your_password
  workers: {}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	masters, err := got.Hosts("masters")
	if err != nil || len(masters) != 3 {
		t.Fatalf("Hosts(masters) = %v, %v; want 3 hosts", masters, err)
	}

	// master1 — boolean plugin
	m1 := masters[0]
	if m1.Plugin == nil {
		t.Fatalf("master1.Plugin = nil, want non-nil map")
	}
	if v, ok := m1.Plugin["traefikServer"].(bool); !ok || !v {
		t.Errorf("master1.Plugin[traefikServer] = %v, want true", m1.Plugin["traefikServer"])
	}
	if len(m1.Plugin) != 1 {
		t.Errorf("master1.Plugin = %+v, want exactly 1 entry", m1.Plugin)
	}

	// master2 — map plugin + boolean plugin
	m2 := masters[1]
	if m2.Plugin == nil {
		t.Fatalf("master2.Plugin = nil, want non-nil map")
	}
	rs, ok := m2.Plugin["registryServer"].(map[string]any)
	if !ok {
		t.Fatalf("master2.Plugin[registryServer] = %T, want map[string]any", m2.Plugin["registryServer"])
	}
	if rs["alias"] != "registry.local" {
		t.Errorf("master2.Plugin[registryServer].alias = %v, want registry.local", rs["alias"])
	}
	if v, ok := m2.Plugin["nfsServer"].(bool); !ok || !v {
		t.Errorf("master2.Plugin[nfsServer] = %v, want true", m2.Plugin["nfsServer"])
	}

	// master3 — no plugin field at all
	m3 := masters[2]
	if m3.Plugin != nil {
		t.Errorf("master3.Plugin = %+v, want nil (no plugin: declared)", m3.Plugin)
	}
}

// TestLoadPluginField_NonMapPluginValue pins the
// forward-compat hook: a `plugin:` value that isn't a bool or
// a map (e.g. a string) is preserved verbatim. The runner
// then passes it through to the prelude's map-flattening
// pass unchanged, so adding a new plugin kind doesn't require
// a Go-side decoder bump.
func TestLoadPluginField_NonMapPluginValue(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 10.0.0.1
      user: root
      password: your_password
      plugin:
        label: k8s-master
        replicas: 3
        allowedCIDRs:
          - 10.0.0.0/8
          - 192.168.0.0/16
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	hs, err := got.Hosts("masters")
	if err != nil || len(hs) != 1 {
		t.Fatalf("Hosts(masters) = %v, %v; want 1 host", hs, err)
	}
	p := hs[0].Plugin
	if p["label"] != "k8s-master" {
		t.Errorf("Plugin[label] = %v, want k8s-master", p["label"])
	}
	if p["replicas"] != 3 {
		t.Errorf("Plugin[replicas] = %v, want 3", p["replicas"])
	}
	cidrs, ok := p["allowedCIDRs"].([]any)
	if !ok || len(cidrs) != 2 {
		t.Errorf("Plugin[allowedCIDRs] = %v, want [10.0.0.0/8 192.168.0.0/16]", p["allowedCIDRs"])
	}
}

// TestAllnodeExcludesAddWorkers pins the install-k8s.yaml spec
// rule "执行yaml文件时，addworkers 是一个独立的组". When the
// user does NOT declare an explicit `allnode:` sibling key, the
// auto-built `allnode` must include every host under
// `hosts: { masters, workers, ... }` EXCEPT those declared
// under `addworkers:`. A regression that pulled addworkers in
// would silently include post-install nodes in bootstrap-time
// /etc/hosts and etcd peer lists — see TestPrimaryHosts for the
// k8s_all_ip / k8s_all_hostname half of the same contract.
//
// The test uses the in-memory *File path (rather than Load) so
// it pins the inventory-layer exclusion logic in isolation from
// the YAML decoder's interpretation of `addworkers:`.
func TestAllnodeExcludesAddWorkers(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
				{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
			},
			"workers": {
				{Name: "w1", IP: "10.0.0.10", User: "root", Password: "your_password"},
			},
			"addworkers": {
				{Name: "a1", IP: "10.0.0.20", User: "root", Password: "your_password"},
				{Name: "a2", IP: "10.0.0.21", User: "root", Password: "your_password"},
			},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all, err := inv.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	want := []string{"m1", "m2", "m3", "w1"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Hosts(allnode) names = %v, want %v (addworkers must be excluded)", names, want)
	}
	// Sanity: addworkers itself still resolves (and yields both
	// entries) — the exclusion is from allnode / k8s_all_ip,
	// not from the addworkers group itself.
	aws, err := inv.Hosts("addworkers")
	if err != nil {
		t.Fatalf("Hosts(addworkers): %v", err)
	}
	if len(aws) != 2 {
		t.Errorf("Hosts(addworkers) = %d, want 2 (the group itself must still resolve)", len(aws))
	}
}

// TestAllnodeExplicitOverride_IncludesAddWorkers covers the
// "operator deliberately opts back in" path: an explicit
// `allnode: [masters, workers, addworkers]` sibling key must
// still beat the auto-built exclusion (same override policy
// the rest of the code follows). Confirms we didn't accidentally
// harden the exclusion into a hard wall.
func TestAllnodeExplicitOverride_IncludesAddWorkers(t *testing.T) {
	f := &File{
		Hosts: map[string][]Host{
			"masters": {
				{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
			},
			"addworkers": {
				{Name: "a1", IP: "10.0.0.20", User: "root", Password: "your_password"},
			},
		},
		Groups: map[string][]string{
			"allnode": {"masters", "addworkers"},
		},
	}
	inv, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all, err := inv.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	want := []string{"m1", "a1"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Hosts(allnode) = %v, want %v (explicit override must beat the auto exclusion)", names, want)
	}
}

// TestPrimaryHosts verifies that Inventory.PrimaryHosts() returns
// every host declared under hosts: { masters, workers, ... }
// EXCEPT those in addworkers — same contract as the auto-built
// allnode exclusion, but exposed as a public method so the runner's
// computeK8sAllIP / computeK8sAllHostname helpers (and any future
// caller) can reuse it. Order matches All(): alphabetical group,
// declaration order within each group — except addworkers is
// skipped entirely.
func TestPrimaryHosts(t *testing.T) {
	t.Run("excludes_addworkers", func(t *testing.T) {
		f := &File{
			Hosts: map[string][]Host{
				"masters": {
					{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
					{Name: "m2", IP: "10.0.0.2", User: "root", Password: "your_password"},
					{Name: "m3", IP: "10.0.0.3", User: "root", Password: "your_password"},
				},
				"workers": {
					{Name: "w1", IP: "10.0.0.10", User: "root", Password: "your_password"},
				},
				"addworkers": {
					{Name: "a1", IP: "10.0.0.20", User: "root", Password: "your_password"},
					{Name: "a2", IP: "10.0.0.21", User: "root", Password: "your_password"},
				},
			},
		}
		inv, err := New(f)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		primary := inv.PrimaryHosts()
		names := make([]string, 0, len(primary))
		for _, h := range primary {
			names = append(names, h.Name)
		}
		want := []string{"m1", "m2", "m3", "w1"}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("PrimaryHosts = %v, want %v (addworkers excluded)", names, want)
		}
	})

	t.Run("all_still_includes_addworkers", func(t *testing.T) {
		// Sanity: All() keeps addworkers (it walks every group
		// in alphabetical group order — "addworkers" sorts before
		// "masters", so the order is [a1, m1] not [m1, a1]).
		// PrimaryHosts() is the addworkers-excluding sibling, not
		// a replacement.
		f := &File{
			Hosts: map[string][]Host{
				"masters": {
					{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				},
				"addworkers": {
					{Name: "a1", IP: "10.0.0.20", User: "root", Password: "your_password"},
				},
			},
		}
		inv, err := New(f)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		all := inv.All()
		names := make([]string, 0, len(all))
		for _, h := range all {
			names = append(names, h.Name)
		}
		// addworkers sorts BEFORE masters alphabetically, so a1
		// comes first in All(). The point of the test is that
		// addworkers is INCLUDED at all — the exact order is
		// pinned in TestAll_OrderMastersBeforeWorkers / its
		// addworkers-aware equivalent.
		want := []string{"a1", "m1"}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("All() = %v, want %v (must still include addworkers)", names, want)
		}
	})

	t.Run("empty_addworkers_is_a_noop", func(t *testing.T) {
		// The install-k8s.yaml default is `addworkers: {}`. This
		// must NOT cause PrimaryHosts() to skip a `workers: {}`
		// group by accident — it must walk every non-addworkers
		// group normally.
		f := &File{
			Hosts: map[string][]Host{
				"masters": {
					{Name: "m1", IP: "10.0.0.1", User: "root", Password: "your_password"},
				},
				"workers":  {},
				"addworkers": {},
			},
		}
		inv, err := New(f)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		primary := inv.PrimaryHosts()
		if len(primary) != 1 || primary[0].Name != "m1" {
			names := make([]string, 0, len(primary))
			for _, h := range primary {
				names = append(names, h.Name)
			}
			t.Errorf("PrimaryHosts = %v, want [m1] (empty workers/addworkers must not affect the masters walk)", names)
		}
	})
}

// TestLoadAddWorkersField_KeyedMapForm exercises the on-disk
// YAML load path for the `addworkers:` field. Mirrors
// TestLoadKeyedMapForm for the groups under `hosts:` — keyed
// map with internal host ids as keys, Host.Key populated from
// each key. A regression here would surface as a play's
// `hosts: addworkers` selector resolving an empty list.
func TestLoadAddWorkersField_KeyedMapForm(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: your_password
  workers:
    k8s_worker1:
      hostname: node1
      ip: 192.168.170.22
      user: root
      password: your_password
addworkers:
  k8s_worker2:
    hostname: node2
    ip: 192.168.170.42
    user: root
    password: your_password
  k8s_worker3:
    hostname: node3
    ip: 192.168.170.43
    user: root
    password: your_password
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	aws, err := got.Hosts("addworkers")
	if err != nil {
		t.Fatalf("Hosts(addworkers): %v", err)
	}
	if len(aws) != 2 {
		t.Fatalf("len(Hosts(addworkers)) = %d, want 2", len(aws))
	}
	if aws[0].Key != "k8s_worker2" || aws[0].Name != "node2" || aws[0].IP != "192.168.170.42" {
		t.Errorf("addworkers[0] = %+v, want Key=k8s_worker2 Name=node2 IP=192.168.170.42", aws[0])
	}
	if aws[1].Key != "k8s_worker3" || aws[1].Name != "node3" {
		t.Errorf("addworkers[1] = %+v, want Key=k8s_worker3 Name=node3", aws[1])
	}

	// allnode still resolves to masters + workers only — the
	// addworkers hosts MUST NOT appear here. This is the
	// bootstrap-time host list scripts splice into /etc/hosts
	// and etcd peer lists.
	all, err := got.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	want := []string{"master1", "node1"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Hosts(allnode) = %v, want %v (addworkers must be excluded)", names, want)
	}
}

// TestLoadAddWorkersField_Empty verifies the install-k8s.yaml
// default shape (`addworkers: {}`) loads cleanly — the auto-built
// allnode resolves to masters + workers, and Hosts("addworkers")
// returns an empty slice. A regression that rejected the empty
// keyed map (e.g. by treating empty mapping as "no field") would
// surface here.
func TestLoadAddWorkersField_Empty(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(inv, []byte(`
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 10.0.0.1
      user: root
      password: your_password
addworkers: {}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(inv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	aws, err := got.Hosts("addworkers")
	if err != nil {
		t.Fatalf("Hosts(addworkers): %v", err)
	}
	if len(aws) != 0 {
		t.Errorf("Hosts(addworkers) = %v, want empty", aws)
	}
	// And the empty addworkers must not break allnode — it's
	// the canonical install-k8s.yaml default.
	all, err := got.Hosts("allnode")
	if err != nil {
		t.Fatalf("Hosts(allnode): %v", err)
	}
	if len(all) != 1 || all[0].Name != "master1" {
		t.Errorf("Hosts(allnode) = %v, want [master1] (empty addworkers is a no-op)", all)
	}
}
