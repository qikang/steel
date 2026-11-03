package shell

import (
	"strings"
	"testing"

	"steel/internal/inventory"
	"steel/internal/vars"
)

// TestBuildEnvPrelude_InstallK8sFixture mimics what the
// install-k8s.yaml bootstrap flow expects: a 3-master inventory,
// `registryServer.hostName = master2`, `apiserverLB.hostName =
// master3`, and the prelude must export the auto-derived IPs
// alongside the master_node_number / k8s_master1_ip /
// k8s_hosts_alias / noapiserverips.
//
// This is the end-to-end pin for the spec rule
// "registryServer 字段可以依据hostName生成内置变量
//
//	registryServer_ip=192.168.170.49" plus the other new
//
// variables the spec gained. After Apply has called
// vars.ExpandHostNameIPs on the playbook vars, the prelude
// builder sees `registryServer.ip` and `apiserverLB.ip` as
// ordinary nested fields and flattens them to env vars via
// writeEnvFromAny — no change needed in the shell prelude
// itself.
func TestBuildEnvPrelude_InstallK8sFixture(t *testing.T) {
	allHosts := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
	}
	hostNameToIP := map[string]string{
		"master1": "192.168.170.48",
		"master2": "192.168.170.49",
		"master3": "192.168.170.47",
	}

	// Mirror what Apply() builds: enrich the playbook vars
	// with auto-derived `ip` siblings before constructing the
	// per-host vars.Context.
	enrichedVars := vars.ExpandHostNameIPs(map[string]any{
		"data_root": "/data/install-k8s",
		"registryServer": map[string]any{
			"hostName": "master2",
			"alias":    "registry.local",
		},
		"apiserverLB": map[string]any{
			"hostName": "master3",
			"alias":    "apiserver.cluster.local",
		},
	}, hostNameToIP)

	ctx := &vars.Context{
		Vars: enrichedVars,
		Host: map[string]any{
			"os_arch":            "amd64",
			"master_node_number": 3,
		},
		GroupHostNames: map[string][]string{
			"masters": {"master1", "master2", "master3"},
		},
	}

	got := buildEnvPrelude("amd64", ctx, allHosts)

	// Pin the auto-derived IPs. These come from the
	// vars.ExpandHostNameIPs enrichment, NOT from anything the
	// user declared. They're the headline feature of this
	// change.
	for _, want := range []string{
		"export registryServer_ip='192.168.170.49'",
		"export apiserverLB_ip='192.168.170.47'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prelude missing %q\n  got: %q", want, got)
		}
	}

	// The hostName and alias fields still export via the
	// existing flattening — those should be unchanged.
	for _, want := range []string{
		"export registryServer_hostName='master2'",
		"export registryServer_alias='registry.local'",
		"export apiserverLB_hostName='master3'",
		"export apiserverLB_alias='apiserver.cluster.local'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prelude missing %q\n  got: %q", want, got)
		}
	}

	// Per-host cross-references: only k8s_master1_ip survives.
	// k8s_master2_ip, k8s_master3_ip and the per-host _hostname
	// exports were intentionally dropped — scripts iterate via
	// $k8s_hosts_alias instead.
	for _, want := range []string{
		"export k8s_master1_ip='192.168.170.48'",
		"k8s_hosts_alias=" + shellQuote("192.168.170.48 master1\\n 192.168.170.49 master2\\n 192.168.170.47 master3"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prelude missing %q (regression)\n  got: %q", want, got)
		}
	}

	// And explicitly confirm the per-group / extra per-host
	// exports are gone — without this pin a regression that
	// re-introduces them would silently slip through the
	// substring checks above.
	for _, unwanted := range []string{
		"k8s_master2_ip",
		"k8s_master3_ip",
		"k8s_master1_hostname",
		"all_master_hostname",
		"all_worker_hostname",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("prelude unexpectedly contains dropped export %q\n  got: %q", unwanted, got)
		}
	}
}

// TestBuildEnvPrelude_NoApiserverIps pins the third auto-derived
// apiserverLB variable:
//
//	# 依据 apiserverLB 字段的hostName, 可以生成不包含apiserverLB_ip的所有master主机IP地址的内置变量，比如noapiserverips="192.168.170.48 192.168.170.49"
//
// Unlike `apiserverLB_ip` (which is the single LB host's IP),
// `noapiserverips` is the space-separated list of every OTHER
// master's IP. The shell prelude's writeEnvFromAny flattens a
// top-level scalar var as `export <name>=<quoted value>`, so
// Apply's enrichment just needs to set the value on
// `effectiveVars["noapiserverips"]` and the prelude exports it
// for free — this test pins that contract.
func TestBuildEnvPrelude_NoApiserverIps(t *testing.T) {
	allHosts := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
	}
	ctx := &vars.Context{
		Vars: map[string]any{
			"apiserverLB":    map[string]any{"hostName": "master3"},
			"noapiserverips": "192.168.170.48 192.168.170.49",
			"registryServer": map[string]any{"hostName": "master2", "ip": "192.168.170.49"},
		},
		Host: map[string]any{"os_arch": "amd64", "master_node_number": 3},
		GroupHostNames: map[string][]string{
			"masters": {"master1", "master2", "master3"},
		},
	}
	got := buildEnvPrelude("amd64", ctx, allHosts)
	want := "export noapiserverips='192.168.170.48 192.168.170.49'"
	if !strings.Contains(got, want) {
		t.Errorf("prelude missing %q\n  got: %q", want, got)
	}
}

// TestBuildEnvPrelude_K8sAllIP pins the auto-derived
// inventory-wide IP list:
//
//	# 依据hosts字段，添加k8s_all_ip="192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1"，包含所有masters和workers节点的IP地址。
//
// Apply enriches playbookVars with `k8s_all_ip` (every host IP
// in inv.All() order, single-space separated) and the prelude's
// writeEnvFromAny flattens it as `export k8s_all_ip=<quoted>` —
// no change needed in buildEnvPrelude itself, but this test
// pins the end-to-end contract: a 3-master + 3-worker fixture
// surfaces the exact spec-example value, while a 3-master-only
// fixture surfaces just the master IPs. The order is part of
// the contract — masters before workers — so a regression that
// flipped the order would silently change every cluster-id
// hash downstream.
func TestBuildEnvPrelude_K8sAllIP(t *testing.T) {
	mastersAndWorkers := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
		{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22"},
		{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.43"},
		{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1"},
	}
	mastersOnly := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
	}

	cases := []struct {
		name     string
		hosts    []*inventory.Host
		playVars map[string]any // simulates the effectiveVars Apply would build
		wantSub  []string
		wantNot  []string
	}{
		{
			name:     "masters_and_workers_full_spec_example",
			hosts:    mastersAndWorkers,
			playVars: map[string]any{"k8s_all_ip": "192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1"},
			wantSub: []string{
				"export k8s_all_ip='192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1'",
			},
		},
		{
			name:     "masters_only_inventory",
			hosts:    mastersOnly,
			playVars: map[string]any{"k8s_all_ip": "192.168.170.48 192.168.170.49 192.168.170.47"},
			wantSub: []string{
				"export k8s_all_ip='192.168.170.48 192.168.170.49 192.168.170.47'",
			},
		},
		{
			name:     "user_declared_k8s_all_ip_passes_through_unchanged",
			hosts:    mastersOnly,
			playVars: map[string]any{"k8s_all_ip": "10.0.0.1 10.0.0.2 10.0.0.3"},
			// The user's override is preserved verbatim. Apply's
			// "user-declared wins" rule means the derived value
			// never reaches the prelude here — the user's
			// intent (e.g. a placeholder they want to substitute
			// in CI) wins.
			wantSub: []string{
				"export k8s_all_ip='10.0.0.1 10.0.0.2 10.0.0.3'",
			},
		},
		{
			name:     "missing_k8s_all_ip_var_omits_export",
			hosts:    mastersOnly,
			playVars: nil,
			wantNot:  []string{"k8s_all_ip"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := &vars.Context{Vars: c.playVars}
			got := buildEnvPrelude("amd64", ctx, c.hosts)
			for _, want := range c.wantSub {
				if !strings.Contains(got, want) {
					t.Errorf("prelude missing %q\n  got: %q", want, got)
				}
			}
			for _, unwanted := range c.wantNot {
				if strings.Contains(got, unwanted) {
					t.Errorf("prelude unexpectedly contains %q\n  got: %q", unwanted, got)
				}
			}
		})
	}
}

// TestBuildEnvPrelude_K8sAllHostname is the hostname twin of
// TestBuildEnvPrelude_K8sAllIP. Apply enriches playbookVars with
// `k8s_all_hostname` (every host Name in inv.All() order, single-
// space separated) and the prelude's writeEnvFromAny flattens it
// as `export k8s_all_hostname=<quoted>` — same flatten pipeline
// as k8s_all_ip, so the test shape is identical, just with the
// Name values in place of IPs.
//
// The masters-and-workers case pins the spec's exact hostname
// string. The user-override and missing-var cases mirror the
// k8s_all_ip contract: explicit user declarations pass through
// verbatim, and a missing var omits the export entirely.
func TestBuildEnvPrelude_K8sAllHostname(t *testing.T) {
	mastersAndWorkers := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
		{Key: "k8s_worker1", Name: "node1", IP: "192.168.170.22"},
		{Key: "k8s_worker2", Name: "node2", IP: "192.168.170.43"},
		{Key: "k8s_worker3", Name: "ip-192-168-1-1", IP: "192.168.170.1"},
	}
	mastersOnly := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48"},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49"},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47"},
	}

	cases := []struct {
		name     string
		hosts    []*inventory.Host
		playVars map[string]any
		wantSub  []string
		wantNot  []string
	}{
		{
			name:     "masters_and_workers_full_spec_example",
			hosts:    mastersAndWorkers,
			playVars: map[string]any{"k8s_all_hostname": "master1 master2 master3 node1 node2 ip-192-168-1-1"},
			wantSub: []string{
				"export k8s_all_hostname='master1 master2 master3 node1 node2 ip-192-168-1-1'",
			},
		},
		{
			name:     "masters_only_inventory",
			hosts:    mastersOnly,
			playVars: map[string]any{"k8s_all_hostname": "master1 master2 master3"},
			wantSub: []string{
				"export k8s_all_hostname='master1 master2 master3'",
			},
		},
		{
			name:     "user_declared_k8s_all_hostname_passes_through_unchanged",
			hosts:    mastersOnly,
			playVars: map[string]any{"k8s_all_hostname": "alpha beta gamma"},
			// Apply's "user-declared wins" rule means the derived
			// value never reaches the prelude — the user's
			// intent (e.g. a CI matrix placeholder) wins.
			wantSub: []string{
				"export k8s_all_hostname='alpha beta gamma'",
			},
		},
		{
			name:     "missing_k8s_all_hostname_var_omits_export",
			hosts:    mastersOnly,
			playVars: nil,
			wantNot:  []string{"k8s_all_hostname"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := &vars.Context{Vars: c.playVars}
			got := buildEnvPrelude("amd64", ctx, c.hosts)
			for _, want := range c.wantSub {
				if !strings.Contains(got, want) {
					t.Errorf("prelude missing %q\n  got: %q", want, got)
				}
			}
			for _, unwanted := range c.wantNot {
				if strings.Contains(got, unwanted) {
					t.Errorf("prelude unexpectedly contains %q\n  got: %q", unwanted, got)
				}
			}
		})
	}
}

// TestBuildEnvPrelude_PluginDerivedVars is the end-to-end pin
// for the install-k8s.yaml per-host `plugin:` field. The
// `apply` step feeds derivePluginVars → ExpandHostNameIPs →
// ExpandTopLevelHostnameIPs into the prelude, so this test
// builds the same chain by hand and asserts the env exports.
//
// The fixture is the exact install-k8s.yaml shape from the
// spec's example inventory: 3 masters, each with a `plugin:`
// field — master1 hosts `traefikServer: true`, master2 hosts
// `registryServer: { alias: registry.local }` AND
// `nfsServer: true`, master3 hosts `apiserverLB: { alias:
// apiserver.cluster.local }` AND `timeServer: true`.
//
// Expected env exports:
//
//   - traefikServer=master1
//   - traefikServer_ip=192.168.170.48
//   - nfsServer=master2
//   - nfsServer_ip=192.168.170.49
//   - timeServer=master3
//   - timeServer_ip=192.168.170.47
//   - apiserverLB_hostName=master3
//   - apiserverLB_alias=apiserver.cluster.local
//   - apiserverLB_ip=192.168.170.47
//   - registryServer_hostName=master2
//   - registryServer_alias=registry.local
//   - registryServer_ip=192.168.170.49
//
// This is the contract the `env` test (test-env.yaml running
// `env` on allnode) needs to satisfy — see test-env.txt for
// the on-the-wire example. A regression in derivePluginVars,
// ExpandHostNameIPs, or ExpandTopLevelHostnameIPs flips one of
// these substring checks and points the operator at the right
// layer.
func TestBuildEnvPrelude_PluginDerivedVars(t *testing.T) {
	allHosts := []*inventory.Host{
		{Key: "k8s_master1", Name: "master1", IP: "192.168.170.48", Plugin: map[string]any{"traefikServer": true}},
		{Key: "k8s_master2", Name: "master2", IP: "192.168.170.49", Plugin: map[string]any{
			"registryServer": map[string]any{"alias": "registry.local"},
			"nfsServer":      true,
		}},
		{Key: "k8s_master3", Name: "master3", IP: "192.168.170.47", Plugin: map[string]any{
			"apiserverLB": map[string]any{"alias": "apiserver.cluster.local"},
			"timeServer":  true,
		}},
	}
	hostNameToIP := map[string]string{
		"master1": "192.168.170.48",
		"master2": "192.168.170.49",
		"master3": "192.168.170.47",
	}

	// Mirror what Apply() builds. derivePluginVars is in
	// the runner package; we reproduce its work here so the
	// shell prelude test stays independent of the runner
	// package and can be run in isolation.
	pluginDerived := map[string]any{}
	for _, h := range allHosts {
		for k, v := range h.Plugin {
			switch x := v.(type) {
			case bool:
				if x {
					pluginDerived[k] = h.Name
				}
			case map[string]any:
				m := map[string]any{}
				for kk, vv := range x {
					m[kk] = vv
				}
				m["hostName"] = h.Name
				m["ip"] = h.IP
				pluginDerived[k] = m
			}
		}
	}
	enriched := vars.ExpandHostNameIPs(pluginDerived, hostNameToIP)
	enriched = vars.ExpandTopLevelHostnameIPs(enriched, hostNameToIP)

	ctx := &vars.Context{
		Vars: enriched,
		Host: map[string]any{
			"os_arch":            "amd64",
			"master_node_number": 3,
		},
		GroupHostNames: map[string][]string{
			"masters": {"master1", "master2", "master3"},
		},
	}

	got := buildEnvPrelude("amd64", ctx, allHosts)

	// Boolean plugin outputs (top-level scalar + auto-derived _ip).
	for _, want := range []string{
		"export traefikServer='master1'",
		"export traefikServer_ip='192.168.170.48'",
		"export nfsServer='master2'",
		"export nfsServer_ip='192.168.170.49'",
		"export timeServer='master3'",
		"export timeServer_ip='192.168.170.47'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prelude missing %q\n  got: %q", want, got)
		}
	}

	// Map plugin outputs (nested map flattened by the prelude).
	for _, want := range []string{
		"export apiserverLB_hostName='master3'",
		"export apiserverLB_alias='apiserver.cluster.local'",
		"export apiserverLB_ip='192.168.170.47'",
		"export registryServer_hostName='master2'",
		"export registryServer_alias='registry.local'",
		"export registryServer_ip='192.168.170.49'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prelude missing %q\n  got: %q", want, got)
		}
	}
}
