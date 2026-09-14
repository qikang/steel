package playbook

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"steel/internal/inventory"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPlainPlaybook(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "p.yaml", `
- name: alpha
  hosts: all
  shell:
    cmd: echo a
- hosts: all
  shell:
    cmd: echo b
`)

	entry, err := LoadEntry(p)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	plays := entry.Plays
	if len(plays) != 2 {
		t.Fatalf("len(plays) = %d, want 2", len(plays))
	}
	if plays[0].Name != "alpha" {
		t.Errorf("plays[0].Name = %q, want alpha", plays[0].Name)
	}
	// Auto-generated name for the unnamed play.
	if plays[1].Name != "play-2" {
		t.Errorf("plays[1].Name = %q, want play-2", plays[1].Name)
	}
	// Plain playbook has no place to declare vars — Vars stays nil.
	if entry.Vars != nil {
		t.Errorf("entry.Vars = %v, want nil for plain playbook", entry.Vars)
	}
}

func TestLoadRunlist_OrderAndNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "01-first.yaml", `
- name: first-shell
  hosts: all
  shell: { cmd: echo first }
- name: first-copy
  hosts: all
  copy: { src: /a, dest: /b }
`)
	writeFile(t, dir, "02-second.yaml", `
- name: second-shell
  hosts: all
  shell: { cmd: echo second }
`)
	// Runlist paths are written relative to the runlist file's dir.
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - 01-first.yaml
  - 02-second.yaml
`)

	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	plays := out.Plays
	if len(plays) != 3 {
		t.Fatalf("len(plays) = %d, want 3", len(plays))
	}
	wantOrder := []string{
		"01-first.yaml:first-shell",
		"01-first.yaml:first-copy",
		"02-second.yaml:second-shell",
	}
	for i, w := range wantOrder {
		if plays[i].Name != w {
			t.Errorf("plays[%d].Name = %q, want %q", i, plays[i].Name, w)
		}
	}
	// No vars block in this runlist — Vars stays nil.
	if out.Vars != nil {
		t.Errorf("out.Vars = %v, want nil", out.Vars)
	}
}

func TestLoadRunlist_AutoNamePrefix(t *testing.T) {
	dir := t.TempDir()
	// First file: a play without a name (Load() auto-generates "play-1").
	writeFile(t, dir, "a.yaml", `
- hosts: all
  shell: { cmd: echo a }
`)
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - a.yaml
`)

	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	plays := out.Plays
	if len(plays) != 1 {
		t.Fatalf("len(plays) = %d, want 1", len(plays))
	}
	if plays[0].Name != "a.yaml:play-1" {
		t.Errorf("plays[0].Name = %q, want a.yaml:play-1", plays[0].Name)
	}
}

func TestLoadRunlist_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	absPlay := writeFile(t, other, "abs.yaml", `
- name: standalone
  hosts: all
  shell: { cmd: echo abs }
`)
	entry := writeFile(t, dir, "site.yaml", "runlist:\n  - "+absPlay+"\n")

	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	plays := out.Plays
	// Prefix is filepath.Base of the as-written runlist entry — for an
	// absolute path that means just the file name. The play is still
	// loaded from the absolute path on disk.
	if len(plays) != 1 {
		t.Fatalf("len(plays) = %d, want 1", len(plays))
	}
	if plays[0].Name != "abs.yaml:standalone" {
		t.Errorf("plays[0].Name = %q, want abs.yaml:standalone", plays[0].Name)
	}
}

func TestLoadRunlist_MissingEntry(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - nope.yaml
`)
	_, err := LoadEntry(entry)
	if err == nil {
		t.Fatalf("LoadEntry: want error for missing entry")
	}
}

func TestLoadRunlist_ValidationPropagates(t *testing.T) {
	dir := t.TempDir()
	// Sub-file is missing required 'hosts' field.
	writeFile(t, dir, "bad.yaml", `
- name: missing-hosts
  shell: { cmd: echo x }
`)
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - bad.yaml
`)
	_, err := LoadEntry(entry)
	if err == nil {
		t.Fatalf("LoadEntry: want validation error")
	}
}

func TestLoadEntry_RejectsUnknownMapping(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "site.yaml", `
something_else:
  - x.yaml
`)
	_, err := LoadEntry(entry)
	if err == nil {
		t.Fatalf("LoadEntry: want error for non-runlist mapping")
	}
}

func TestRunlistOrder_Stability(t *testing.T) {
	dir := t.TempDir()
	// Three sub-files; confirm concatenated order is runlist order, not
	// alphabetical or any other scheme.
	for _, name := range []string{"c.yaml", "a.yaml", "b.yaml"} {
		writeFile(t, dir, name, "- name: from-"+name+"\n  hosts: all\n  shell: { cmd: echo "+name+" }\n")
	}
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - c.yaml
  - a.yaml
  - b.yaml
`)
	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	plays := out.Plays
	got := make([]string, 0, len(plays))
	for _, p := range plays {
		got = append(got, p.Name)
	}
	want := []string{"c.yaml:from-c.yaml", "a.yaml:from-a.yaml", "b.yaml:from-b.yaml"}
	if !sliceEq(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadPlainPlaybook_RejectsMultipleModules(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "p.yaml", `
- name: both
  hosts: all
  copy:
    src: /a
    dest: /b
  shell:
    cmd: echo x
`)
	_, err := LoadEntry(p)
	if err == nil {
		t.Fatalf("LoadEntry: want multiple-modules error, got nil")
	}
	if !strings.Contains(err.Error(), "multiple modules") {
		t.Errorf("err = %q, want it to mention 'multiple modules'", err)
	}
}

// TestLoadPlainPlaybook_ExecutionMode pins the wire shape of the
// per-play `execution_mode` field. Only the literal "serial" is
// meaningful to the runner; everything else (empty, "parallel",
// typos) is treated as the default (concurrent fan-out) so a
// misspelled value doesn't silently turn a play into a serial
// one.
func TestLoadPlainPlaybook_ExecutionMode(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "serial_sets_field",
			yaml: `
- name: ordered
  hosts: masters
  execution_mode: serial
  shell:
    cmd: echo a
`,
			want: "serial",
		},
		{
			name: "absent_defaults_to_empty",
			yaml: `
- name: concurrent
  hosts: masters
  shell:
    cmd: echo a
`,
			want: "",
		},
		{
			name: "parallel_value_passes_through_verbatim",
			yaml: `
- name: explicit_parallel
  hosts: masters
  execution_mode: parallel
  shell:
    cmd: echo a
`,
			want: "parallel",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeFile(t, dir, "p.yaml", tc.yaml)
			entry, err := LoadEntry(p)
			if err != nil {
				t.Fatalf("LoadEntry: %v", err)
			}
			if len(entry.Plays) != 1 {
				t.Fatalf("len(plays) = %d, want 1", len(entry.Plays))
			}
			if got := entry.Plays[0].ExecutionMode; got != tc.want {
				t.Errorf("ExecutionMode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLoadRunlist_TopLevelVars verifies that a `vars:` block sibling
// to `runlist:` is captured and returned in Entry.Vars, available to
// every play under that runlist as {{ xxx }} (bare-name lookup).
func TestLoadRunlist_TopLevelVars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "p.yaml", `
- name: use-var
  hosts: all
  shell:
    cmd: "echo {{ data_root }}"
    bash: /bin/bash
`)
	entry := writeFile(t, dir, "site.yaml", `
vars:
  data_root: /data/install-k8s
  pod_network_cidr: 10.244.0.0/16
  registryServer:
    hostName: master2
    alias: registry.local
runlist:
  - p.yaml
`)

	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	if out.Vars == nil {
		t.Fatalf("out.Vars = nil, want populated map")
	}
	if out.Vars["data_root"] != "/data/install-k8s" {
		t.Errorf("Vars[data_root] = %v, want /data/install-k8s", out.Vars["data_root"])
	}
	if out.Vars["pod_network_cidr"] != "10.244.0.0/16" {
		t.Errorf("Vars[pod_network_cidr] = %v, want 10.244.0.0/16", out.Vars["pod_network_cidr"])
	}
	rs, ok := out.Vars["registryServer"].(map[string]any)
	if !ok {
		t.Fatalf("Vars[registryServer] = %T, want map[string]any", out.Vars["registryServer"])
	}
	if rs["hostName"] != "master2" {
		t.Errorf("registryServer.hostName = %v, want master2", rs["hostName"])
	}
	if len(out.Plays) != 1 {
		t.Errorf("len(Plays) = %d, want 1", len(out.Plays))
	}
}

// TestLoadRunlist_NoVarsBlock ensures a runlist without a `vars:` key
// returns Entry.Vars == nil (so the runner passes an empty map into
// the vars.Context).
func TestLoadRunlist_NoVarsBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "p.yaml", `
- name: a
  hosts: all
  shell: { cmd: echo a }
`)
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - p.yaml
`)
	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	if out.Vars != nil {
		t.Errorf("out.Vars = %v, want nil", out.Vars)
	}
}

// TestLoadRunlist_RejectsNonMappingVars catches typos like
// `vars: /data/install-k8s` (a scalar) so the user sees a clear
// error rather than silently losing the block.
func TestLoadRunlist_RejectsNonMappingVars(t *testing.T) {
	dir := t.TempDir()
	entry := writeFile(t, dir, "site.yaml", `
vars: not-a-map
runlist: []
`)
	_, err := LoadEntry(entry)
	if err == nil {
		t.Fatalf("LoadEntry: want error for non-mapping vars, got nil")
	}
	if !strings.Contains(err.Error(), "vars") {
		t.Errorf("err = %q, want it to mention 'vars'", err)
	}
}

// TestLoadRunlist_TopLevelHosts verifies that the entry file's
// embedded inventory is captured into Entry.Hosts / Entry.Groups. The
// new `hosts:` shape is a mapping of group name → host list; sibling
// group keys (e.g. `allnode:`) compose across groups. Reserved keys
// (runlist / vars) are not treated as groups.
func TestLoadRunlist_TopLevelHosts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "p.yaml", `
- name: a
  hosts: masters
  shell: { cmd: echo hi }
`)
	entry := writeFile(t, dir, "site.yaml", `
hosts:
  masters:
    k8s_master1:
      hostname: master1
      hostip: 192.168.170.48
      username: root
      sshkey: ~/.ssh/id_rsa
    k8s_master2:
      hostname: master2
      hostip: 192.168.170.47
      username: root
      sshkey: ~/.ssh/id_rsa
allnode: [masters]
runlist:
  - p.yaml
`)

	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	if got, ok := out.Hosts["masters"]; !ok || len(got) != 2 {
		t.Fatalf("Hosts[masters] = %v, want 2 entries", got)
	}
	if out.Hosts["masters"][0].Name != "master1" || out.Hosts["masters"][0].IP != "192.168.170.48" {
		t.Errorf("Hosts[masters][0] = %+v, want master1/192.168.170.48", out.Hosts["masters"][0])
	}
	if out.Hosts["masters"][0].Key != "k8s_master1" {
		t.Errorf("Hosts[masters][0].Key = %q, want k8s_master1 (populated from map key)", out.Hosts["masters"][0].Key)
	}
	if out.Hosts["masters"][1].Name != "master2" || out.Hosts["masters"][1].IP != "192.168.170.47" {
		t.Errorf("Hosts[masters][1] = %+v, want master2/192.168.170.47", out.Hosts["masters"][1])
	}
	wantGroups := map[string][]string{
		"allnode": {"masters"},
	}
	if !reflect.DeepEqual(out.Groups, wantGroups) {
		t.Errorf("Groups = %+v, want %+v", out.Groups, wantGroups)
	}
}

// TestLoadRunlist_NoHostsOrGroups ensures a runlist without hosts or
// group keys returns Entry.Hosts == nil and Entry.Groups == nil so
// cmd/apply.go can fall back to the -i flag.
func TestLoadRunlist_NoHostsOrGroups(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "p.yaml", `
- name: a
  hosts: all
  shell: { cmd: echo a }
`)
	entry := writeFile(t, dir, "site.yaml", `
runlist:
  - p.yaml
`)
	out, err := LoadEntry(entry)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	if out.Hosts != nil {
		t.Errorf("out.Hosts = %v, want nil", out.Hosts)
	}
	if out.Groups != nil {
		t.Errorf("out.Groups = %v, want nil", out.Groups)
	}
}

// TestAutoQuoteBareTemplates pins the per-line rewrite that lets
// users write `hosts: {{ registryServer.hostName }}` without
// surrounding quotes. YAML 1.2 would otherwise see the leading `{`
// as the start of a flow mapping and try to unmarshal the value as
// a nested map, which fails for typed string fields like Play.Hosts.
// The pre-processor wraps the value in double quotes so YAML parses
// it as a plain string; the renderer handles placeholder
// substitution at execution time.
//
// The same input is then fed to LoadEntry to confirm the auto-quote
// is wired in (not just a standalone helper).
func TestAutoQuoteBareTemplates(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare hosts: {{ ... }}",
			in:   "hosts: {{ registryServer.hostName }}\n",
			want: "hosts: \"{{ registryServer.hostName }}\"\n",
		},
		{
			name: "bare hosts: with leading indent",
			in:   "  hosts: {{ registryServer.hostName }}\n",
			want: "  hosts: \"{{ registryServer.hostName }}\"\n",
		},
		{
			name: "already quoted: untouched",
			in:   "hosts: \"{{ registryServer.hostName }}\"\n",
			want: "hosts: \"{{ registryServer.hostName }}\"\n",
		},
		{
			name: "mixed-content value: untouched",
			in:   "cmd: /bin/bash install.sh {{ k8s_network_stack }}\n",
			want: "cmd: /bin/bash install.sh {{ k8s_network_stack }}\n",
		},
		{
			name: "value with {{ ... }} in middle: untouched",
			in:   "dest: \"{{ data_root }}/master/01-install-registry\"\n",
			want: "dest: \"{{ data_root }}/master/01-install-registry\"\n",
		},
		{
			name: "different key (cmd: {{ ... }})",
			in:   "cmd: {{ install_cmd }}\n",
			want: "cmd: \"{{ install_cmd }}\"\n",
		},
		{
			name: "no template: untouched",
			in:   "cmd: echo hello\n",
			want: "cmd: echo hello\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := autoQuoteBareTemplates(c.in)
			if got != c.want {
				t.Errorf("autoQuoteBareTemplates(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestLoadEntry_BareTemplateValue verifies the auto-quote is wired
// into LoadEntry end-to-end: a play with a bare `hosts: {{ ... }}`
// (no surrounding quotes) parses and round-trips the placeholder
// string into Play.Hosts.
func TestLoadEntry_BareTemplateValue(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "p.yaml", `
- name: bare-template
  hosts: {{ registryServer.hostName }}
  shell:
    cmd: echo hi
`)
	out, err := LoadEntry(p)
	if err != nil {
		t.Fatalf("LoadEntry: %v", err)
	}
	if len(out.Plays) != 1 {
		t.Fatalf("len(Plays) = %d, want 1", len(out.Plays))
	}
	if out.Plays[0].Hosts != "{{ registryServer.hostName }}" {
		t.Errorf("Hosts = %q, want bare placeholder preserved as-is for later render",
			out.Plays[0].Hosts)
	}
}

// TestLoadRunlist_AddWorkers pins the entry-file shape for the
// `addworkers:` field. It is a top-level sibling of `hosts:` and
// uses the same keyed-map shape as the groups under `hosts:`. The
// decoder must:
//
//   - accept `addworkers: {}` (empty, the install-k8s.yaml default)
//     and yield Hosts["addworkers"] == [];
//   - accept a populated `addworkers:` block and yield the entries
//     with Host.Key populated from each map key (so a play can
//     target the group with `hosts: addworkers`).
//
// The auto-built `allnode` / `k8s_all_ip` exclusion is verified
// separately in inventory_test.go (TestAllnodeExcludesAddWorkers
// and TestPrimaryHosts) — this test only pins the YAML decoder.
func TestLoadRunlist_AddWorkers(t *testing.T) {
	t.Run("empty_addworkers_is_valid", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "p.yaml", `
- name: a
  hosts: masters
  shell: { cmd: echo hi }
`)
		entry := writeFile(t, dir, "site.yaml", `
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: your_password
addworkers: {}
runlist:
  - p.yaml
`)
		out, err := LoadEntry(entry)
		if err != nil {
			t.Fatalf("LoadEntry: %v", err)
		}
		got, ok := out.Hosts["addworkers"]
		if !ok {
			t.Fatalf("Hosts[addworkers] missing; want empty slice (the install-k8s.yaml default)")
		}
		if len(got) != 0 {
			t.Errorf("Hosts[addworkers] = %+v, want empty", got)
		}
	})

	t.Run("populated_addworkers_decodes_keyed_map", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "p.yaml", `
- name: join-existing
  hosts: addworkers
  shell: { cmd: echo hi }
`)
		entry := writeFile(t, dir, "site.yaml", `
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: your_password
addworkers:
  k8s_worker1:
    hostname: node1
    ip: 192.168.170.22
    user: root
    password: your_password
  k8s_worker2:
    hostname: node2
    ip: 192.168.170.42
    user: root
    password: your_password
runlist:
  - p.yaml
`)
		out, err := LoadEntry(entry)
		if err != nil {
			t.Fatalf("LoadEntry: %v", err)
		}
		got, ok := out.Hosts["addworkers"]
		if !ok {
			t.Fatalf("Hosts[addworkers] missing")
		}
		if len(got) != 2 {
			t.Fatalf("len(Hosts[addworkers]) = %d, want 2", len(got))
		}
		if got[0].Key != "k8s_worker1" || got[0].Name != "node1" || got[0].IP != "192.168.170.22" {
			t.Errorf("Hosts[addworkers][0] = %+v, want Key=k8s_worker1 Name=node1 IP=192.168.170.22", got[0])
		}
		if got[1].Key != "k8s_worker2" || got[1].Name != "node2" {
			t.Errorf("Hosts[addworkers][1] = %+v, want Key=k8s_worker2 Name=node2", got[1])
		}
	})

	t.Run("addworkers_uses_shorthand_field_aliases", func(t *testing.T) {
		// The keyed-map decoder for `addworkers:` is the same
		// helper used for groups under `hosts:`, so the canonical /
		// shorthand field alias (hostname/hostip/username/sshkey)
		// resolution must work there too. Confirms a future
		// divergence between the two decoder call sites would
		// surface here, not in a debug session.
		//
		// `~` expansion of ssh_key paths is inventory-layer
		// (inventory.New calls expandHome), not playbook-layer,
		// so this subtest runs the parsed Entry through
		// inventory.New before asserting on the SSHKey path.
		dir := t.TempDir()
		writeFile(t, dir, "p.yaml", `
- name: a
  hosts: masters
  shell: { cmd: echo hi }
`)
		entry := writeFile(t, dir, "site.yaml", `
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: your_password
addworkers:
  k8s_worker1:
    hostname: node1
    hostip: 192.168.170.22
    username: root
    sshkey: ~/.ssh/id_rsa
runlist:
  - p.yaml
`)
		out, err := LoadEntry(entry)
		if err != nil {
			t.Fatalf("LoadEntry: %v", err)
		}
		inv, err := inventory.New(&inventory.File{Hosts: out.Hosts, Groups: out.Groups})
		if err != nil {
			t.Fatalf("inventory.New: %v", err)
		}
		aws, err := inv.Hosts("addworkers")
		if err != nil {
			t.Fatalf("Hosts(addworkers): %v", err)
		}
		if len(aws) != 1 {
			t.Fatalf("len(Hosts(addworkers)) = %d, want 1", len(aws))
		}
		w := aws[0]
		if w.Name != "node1" || w.IP != "192.168.170.22" || w.User != "root" || w.SSHKey == "" {
			t.Errorf("addworkers entry = %+v, want shorthand fields resolved to canonical", w)
		}
		if !filepath.IsAbs(w.SSHKey) {
			t.Errorf("SSHKey = %q, want ~-expanded absolute path", w.SSHKey)
		}
	})
}
