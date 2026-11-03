package vars

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	ctx := &Context{
		Vars: map[string]any{"pod_network_cidr": "10.244.0.0/16"},
		Host: map[string]any{"name": "master1", "port": 22},
	}
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"no placeholders", "no placeholders", false},
		// Bare vars now resolve directly — the old `{{ vars.xxx }}`
		// scope is gone.
		{"cidr={{ pod_network_cidr }}", "cidr=10.244.0.0/16", false},
		{"{{ host.name }}:{{ host.port }}", "master1:22", false},
		{"unknown={{ nope }}", "", true},
		{"{{ .", "", true},
	}
	for _, c := range cases {
		got, err := Render(c.in, ctx)
		if c.err {
			if err == nil {
				t.Errorf("Render(%q): want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Render(%q): unexpected error: %v", c.in, got)
			continue
		}
		if got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRender_HostBuiltins pins the bare-name shortcuts that map to
// per-host facts. These let playbooks write {{ hostname }} instead of
// {{ host.name }} so the entry-file's documented built-in env vars
// (hostname / hostip / username / password / sshkey) actually
// resolve. Unknown bare names still error with a regular hint that
// lists the available built-ins.
func TestRender_HostBuiltins(t *testing.T) {
	ctx := &Context{
		Host: map[string]any{
			"name":     "master1",
			"ip":       "192.168.170.48",
			"user":     "root",
			"password": "your_password",
			"ssh_key":  "/root/.ssh/id_rsa",
		},
	}
	cases := []struct {
		in   string
		want string
	}{
		{"{{ hostname }}", "master1"},
		{"{{ hostip }}", "192.168.170.48"},
		{"{{ username }}", "root"},
		{"{{ password }}", "your_password"},
		{"{{ sshkey }}", "/root/.ssh/id_rsa"},
		{"name={{ hostname }} ip={{ hostip }}", "name=master1 ip=192.168.170.48"},
	}
	for _, c := range cases {
		got, err := Render(c.in, ctx)
		if err != nil {
			t.Errorf("Render(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Unknown bare names fall through to the regular not-found error
	// so typos don't silently produce empty strings.
	if _, err := Render("{{ foobar }}", ctx); err == nil {
		t.Errorf("Render({{ foobar }}): want error, got nil")
	}
}

// TestRender_OsArchBuiltin pins the bare-name shortcut for the
// per-host architecture probe (archOf → amd64 | arm64). It maps
// directly to ctx.Host["os_arch"] rather than going through the
// dotted path, mirroring the existing {{ hostname }} / {{ hostip }}
// convention so playbooks can write `uname -m; echo $os_arch` style
// shell snippets without forcing `{{ host.os_arch }}` everywhere.
func TestRender_OsArchBuiltin(t *testing.T) {
	cases := []struct {
		name string
		host map[string]any
		in   string
		want string
	}{
		{"amd64", map[string]any{"os_arch": "amd64"}, "{{ os_arch }}", "amd64"},
		{"arm64", map[string]any{"os_arch": "arm64"}, "{{ os_arch }}", "arm64"},
		{"dotted path also works", map[string]any{"os_arch": "amd64"}, "{{ host.os_arch }}", "amd64"},
		{"interpolated", map[string]any{"os_arch": "arm64"}, "binary-linux-{{ os_arch }}.tar.gz", "binary-linux-arm64.tar.gz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.in, &Context{Host: c.host})
			if err != nil {
				t.Fatalf("Render(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// Missing os_arch in the host map is the regular "no such host
	// field" error, not a panic — same shape as the other builtins.
	if _, err := Render("{{ os_arch }}", &Context{}); err == nil {
		t.Error(`Render("{{ os_arch }}") with no host facts: want error, got nil`)
	}
}

// TestRender_NestedVarsScope pins the dotted-walk that lets a
// playbook reach a nested field of a top-level var (e.g.
// `{{ registryServer.hostName }}` → "master2"). The first segment
// is the user-chosen label from the entry file's top-level `vars:`
// block; the renderer walks the vars map as a nested map[string]any
// to resolve the rest of the path.
func TestRender_NestedVarsScope(t *testing.T) {
	ctx := &Context{
		Vars: map[string]any{
			"registryServer": map[string]any{"hostName": "master2", "alias": "registry.local"},
			"apiserverLB":    map[string]any{"hostName": "master3", "alias": "apiserver.cluster.local"},
		},
	}

	cases := []struct {
		in   string
		want string
	}{
		{"{{ registryServer.hostName }}", "master2"},
		{"{{ registryServer.alias }}", "registry.local"},
		{"{{ apiserverLB.hostName }}", "master3"},
		{"{{ apiserverLB.alias }}", "apiserver.cluster.local"},
		{"hosts={{ registryServer.hostName }}:{{ apiserverLB.hostName }}", "hosts=master2:master3"},
	}
	for _, c := range cases {
		got, err := Render(c.in, ctx)
		if err != nil {
			t.Errorf("Render(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Typo in the label is the regular "not found" error so the typo
	// surfaces with the full expression in the message.
	if _, err := Render("{{ registry.hostName }}", ctx); err == nil {
		t.Error("Render with typo label: want error, got nil")
	} else if !strings.Contains(err.Error(), "registry.hostName") {
		t.Errorf("typo error = %q, want it to mention the full placeholder", err)
	}

	// Asking for a dotted path on a context with no vars block
	// gives a friendly hint instead of a generic map-nil panic.
	if _, err := Render("{{ foo.bar }}", &Context{}); err == nil {
		t.Error(`Render("{{ foo.bar }}") with no vars: want error, got nil`)
	} else if !strings.Contains(err.Error(), "no vars block") {
		t.Errorf("missing-vars error = %q, want it to mention the missing vars block", err)
	}
}

// TestRender_HostNameDerivedIP pins the install-k8s.yaml
// spec rule that the runner auto-injects an `ip` sibling
// next to any nested map's `hostName` declaration. After
// vars.ExpandHostNameIPs has enriched the vars tree, the
// dotted template `{{ registryServer.ip }}` resolves to the
// IP of the host whose hostname is `registryServer.hostName`.
// The same value is exported to shell scripts as
// `$registryServer_ip` by the prelude's existing map-flattening
// pass — neither template nor env-var path needs any change
// in the renderer / prelude because the field is just another
// key on the same nested map.
func TestRender_HostNameDerivedIP(t *testing.T) {
	// Mimic what runner.Apply builds: a hostname→IP lookup
	// table from the inventory, then enrich the user-declared
	// vars with the auto-derived `ip` field.
	hostNameToIP := map[string]string{
		"master1": "192.168.170.48",
		"master2": "192.168.170.49",
		"master3": "192.168.170.47",
	}
	vars := ExpandHostNameIPs(map[string]any{
		"registryServer": map[string]any{
			"hostName": "master2",
			"alias":    "registry.local",
		},
		"apiserverLB": map[string]any{
			"hostName": "master3",
			"alias":    "apiserver.cluster.local",
		},
	}, hostNameToIP)

	ctx := &Context{Vars: vars}

	cases := []struct {
		in   string
		want string
	}{
		{"{{ registryServer.ip }}", "192.168.170.49"},
		{"{{ apiserverLB.ip }}", "192.168.170.47"},
		{"ip={{ registryServer.ip }} alias={{ registryServer.alias }}",
			"ip=192.168.170.49 alias=registry.local"},
	}
	for _, c := range cases {
		got, err := Render(c.in, ctx)
		if err != nil {
			t.Errorf("Render(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// hostName still resolves to the literal value (the
	// expansion doesn't touch it).
	if got, err := Render("{{ registryServer.hostName }}", ctx); err != nil {
		t.Errorf("Render hostName: %v", err)
	} else if got != "master2" {
		t.Errorf("Render hostName = %q, want master2", got)
	}

	// Unknown hostname stays unresolved (no auto-ip injected),
	// so asking for its ip is the regular "not found" error
	// rather than a silent empty string.
	bareGhost := ExpandHostNameIPs(map[string]any{
		"thing": map[string]any{"hostName": "ghost"},
	}, hostNameToIP)
	if _, err := Render("{{ thing.ip }}", &Context{Vars: bareGhost}); err == nil {
		t.Errorf("Render {{ thing.ip }} on ghost host: want error, got nil")
	}
}

// TestRender_VarsShadowBuiltin pins the precedence rule: a
// user-declared var with the same name as a host builtin (e.g.
// `hostname`) wins, so the operator can override builtins per-play.
func TestRender_VarsShadowBuiltin(t *testing.T) {
	ctx := &Context{
		Vars: map[string]any{"hostname": "from-vars"},
		Host: map[string]any{"name": "from-host"},
	}
	got, err := Render("{{ hostname }}", ctx)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}
	if got != "from-vars" {
		t.Errorf("Render({{ hostname }}) = %q, want from-vars (vars scope wins)", got)
	}
}

// TestRender_AllGroupHostname pins the per-group inventory builtin:
// `{{ all_<group>_hostname }}` resolves to a space-separated list
// of every host's name within the named group, in declaration
// order. Empty when the group is missing or empty, and a
// user-declared var of the same name shadows the builtin (matching
// the precedence rule for the other bare-name builtins).
func TestRender_AllGroupHostname(t *testing.T) {
	cases := []struct {
		name string
		ctx  *Context
		in   string
		want string
	}{
		{
			name: "three masters joined by space",
			ctx: &Context{
				GroupHostNames: map[string][]string{
					"masters":  {"master1", "master2", "master3"},
					"workers":  {"node1", "node2"},
				},
			},
			in:   "{{ all_master_hostname }}",
			want: "master1 master2 master3",
		},
		{
			name: "workers split out separately",
			ctx: &Context{
				GroupHostNames: map[string][]string{
					"masters": {"master1"},
					"workers": {"node1", "node2"},
				},
			},
			in:   "{{ all_worker_hostname }}",
			want: "node1 node2",
		},
		{
			name: "interpolated into a sentence",
			ctx: &Context{
				GroupHostNames: map[string][]string{"masters": {"solo"}},
			},
			in:   "host={{ all_master_hostname }}",
			want: "host=solo",
		},
		{
			name: "missing group yields empty string",
			ctx:  &Context{},
			in:   "[{{ all_master_hostname }}]",
			want: "[]",
		},
		{
			name: "empty group yields empty string",
			ctx: &Context{
				GroupHostNames: map[string][]string{"masters": {}},
			},
			in:   "[{{ all_master_hostname }}]",
			want: "[]",
		},
		{
			name: "user-declared var shadows the builtin",
			ctx: &Context{
				Vars:           map[string]any{"all_master_hostname": "from-vars"},
				GroupHostNames: map[string][]string{"masters": {"master1", "master2"}},
			},
			in:   "{{ all_master_hostname }}",
			want: "from-vars",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.in, c.ctx)
			if err != nil {
				t.Fatalf("Render(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// A typo of the builtin pattern (wrong suffix) still falls
	// through to the regular not-found error — the renderer should
	// never silently produce an empty string for an unknown
	// placeholder.
	if _, err := Render("{{ all_master_names }}", &Context{
		GroupHostNames: map[string][]string{"masters": {"m1"}},
	}); err == nil {
		t.Error("Render with typo suffix: want error, got nil")
	} else if !strings.Contains(err.Error(), "all_master_names") {
		t.Errorf("typo error = %q, want it to mention the full placeholder", err)
	}

	// Synthesized composition groups (currently `allnode`) are
	// not exposed as per-group builtins — the spec documents
	// per-role exports only, so `{{ all_allnode_hostname }}` is a
	// regular not-found error even when the inventory has an
	// `allnode` group populated.
	if _, err := Render("{{ all_allnode_hostname }}", &Context{
		GroupHostNames: map[string][]string{
			"masters": {"m1"},
			"allnode": {"m1", "n1"},
		},
	}); err == nil {
		t.Error("Render({{ all_allnode_hostname }}): want error, got nil")
	}
}

// TestRender_MasterNodeNumber pins the master-count builtin. The
// value is sourced from the host facts map (same lookup path as
// {{ os_arch }} — every host sees the same value, but the lookup
// is per-host so it shares the existing builtin table).
// inventory.New guarantees the value is in {1, 3, 5}; the renderer
// just stringifies whatever it's given.
func TestRender_MasterNodeNumber(t *testing.T) {
	cases := []struct {
		name string
		ctx  *Context
		in   string
		want string
	}{
		{
			name: "three masters",
			ctx:  &Context{Host: map[string]any{"master_node_number": 3}},
			in:   "{{ master_node_number }}",
			want: "3",
		},
		{
			name: "single master (int)",
			ctx:  &Context{Host: map[string]any{"master_node_number": 1}},
			in:   "{{ master_node_number }}",
			want: "1",
		},
		{
			name: "five masters (string form)",
			ctx:  &Context{Host: map[string]any{"master_node_number": "5"}},
			in:   "{{ master_node_number }}",
			want: "5",
		},
		{
			name: "interpolated into a sentence",
			ctx:  &Context{Host: map[string]any{"master_node_number": 3}},
			in:   "size={{ master_node_number }}",
			want: "size=3",
		},
		{
			name: "dotted path also works",
			ctx:  &Context{Host: map[string]any{"master_node_number": 3}},
			in:   "{{ host.master_node_number }}",
			want: "3",
		},
		{
			name: "user-declared var shadows the builtin",
			ctx: &Context{
				Vars: map[string]any{"master_node_number": "from-vars"},
				Host: map[string]any{"master_node_number": 3},
			},
			in:   "{{ master_node_number }}",
			want: "from-vars",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.in, c.ctx)
			if err != nil {
				t.Fatalf("Render(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// Missing master_node_number in the host map is the regular
	// "no such host field" error, not a panic. This is the case
	// for a worker-only inventory (no masters group), where the
	// shell prelude also skips the export.
	if _, err := Render("{{ master_node_number }}", &Context{}); err == nil {
		t.Error(`Render("{{ master_node_number }}") with no host facts: want error, got nil`)
	}
}
