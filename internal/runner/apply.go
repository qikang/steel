package runner

import (
	"fmt"
	"strings"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/vars"
)

// Apply walks a playbook in order, dispatching each play to
// its target hosts. It returns the first host-level error
// encountered, mirroring Ansible's default "fail fast"
// behavior. Plays themselves run sequentially; within a play,
// hosts fan out concurrently by default. A play can opt into
// per-host serial dispatch by setting `execution_mode: serial`
// at the top level (see playbook.Play.ExecutionMode) — useful
// for ordered bootstraps like "etcd member 1 first, then 2,
// then 3" where concurrent execution would race the cluster.
//
// playbookVars populates the {{ xxx }} scope for every play.
// It comes from the playbook entry file's top-level `vars:`
// block (see playbook.LoadEntry). Passing nil is fine — the
// scope just resolves to an empty map and {{ xxx }} references
// will error out the usual way.
//
// allHosts is the full inventory; it's plumbed into Apply so
// shell modules can inject the per-host cross-references
// (master1_ip, master2_hostname, ...) as remote env vars per
// the entry-file spec.
//
// logBaseDir, when non-empty, is the directory under which the
// single run-wide log file is written (logs/<run-ts>/run.log).
// Every play and every host appends to the same file in
// execution order; the `host=` and `play=` markers on each line
// preserve the per-line context. Pass "" to disable logging —
// terminal output is unaffected.
func Apply(inv *inventory.Inventory, plays []playbook.Play, playbookVars map[string]any, logBaseDir string) error {
	// hostAgnosticCtx is reused for every play's `hosts:` field
	// render. We deliberately leave Host nil: `play.Hosts` is
	// the inventory selector, so any per-host placeholder
	// (e.g. `{{ hostname }}` or `{{ host.name }}`) is ambiguous
	// at this point — we don't yet know which host we're
	// targeting. The vars scope still resolves, which is what
	// the user yamls in install-k8s-yamls actually rely on
	// (e.g. `hosts: {{ registryServer.hostName }}`).
	//
	// AllHostNames IS populated here so inventory-wide builtins
	// like `all_master_hostname` resolve when they appear in a
	// `hosts:` expression (e.g. `hosts: {{ all_master_hostname }}`
	// as a way to target every master).
	allHosts := inv.All()
	allHostNames := make([]string, 0, len(allHosts))
	for _, h := range allHosts {
		allHostNames = append(allHostNames, h.Name)
	}
	// Build the hostname → IP lookup table once. The install-k8s
	// yaml spec rule "依据hostName生成内置变量" (implemented in
	// vars.ExpandHostNameIPs) uses this map to inject a sibling
	// `ip` field next to any `hostName: <name>` declaration,
	// which the shell prelude's map-flattening pass then exports
	// as e.g. `$registryServer_ip`.
	hostNameToIP := make(map[string]string, len(allHosts))
	for _, h := range allHosts {
		hostNameToIP[h.Name] = h.IP
	}
	// Enrich the playbook vars with the per-host `plugin:`
	// field declarations. A host may declare role assignments
	// (e.g. `traefikServer: true`, `registryServer: { alias:
	// registry.local }`) directly under its hosts-block entry;
	// this pass walks those and folds the derived
	// `<key>`, `<key>_ip`, `<key>_hostName`, `<key>_alias`
	// env vars back into the effective vars so the rest of the
	// pipeline (the nested-walk ExpandHostNameIPs below, the
	// flat-shape ExpandTopLevelHostnameIPs, and the shell
	// prelude's map-flattening) sees them through the same
	// surface it always has. Implements the install-k8s.yaml
	// spec rule "plugin 字段可以依据hostName生成内置变量
	// <key>_ip" — the legacy top-level `vars:` declarations of
	// the same keys (e.g. `nfsServer: master2`) are still
	// honored: derivePluginVars never overwrites an existing
	// var, matching the override policy on every other
	// auto-derived var below. The merge happens BEFORE the two
	// Expand* passes so the nested-walk pass auto-derives
	// `<key>.ip` from the injected `<key>.hostName` for the
	// map-shape plugins, and the flat-shape pass auto-derives
	// `<key>_ip` for the boolean plugins.
	if pluginVars := derivePluginVars(inv, playbookVars); len(pluginVars) > 0 {
		// Start from a copy of playbookVars so the caller's
		// input is not mutated, then layer plugin-derived
		// values on top.
		merged := make(map[string]any, len(playbookVars)+len(pluginVars))
		for k, v := range playbookVars {
			merged[k] = v
		}
		for k, v := range pluginVars {
			merged[k] = v
		}
		playbookVars = merged
	}

	// Enrich the playbook vars with the auto-derived `<key>_ip`
	// fields. The returned map is a fresh copy; the caller's
	// playbookVars is not mutated.
	effectiveVars := vars.ExpandHostNameIPs(playbookVars, hostNameToIP)
	// Enrich again for the flat-shape var pattern: a top-level
	// string var whose value resolves to a host Name gets a
	// sibling `<key>_ip` entry. Implements the install-k8s.yaml
	// spec rule:
	//
	//   # 依据 nfsServer 的主机名，添加nfsServer_ip，比如nfsServer_ip="192.168.170.49"
	//
	// (e.g. `nfsServer: master2` → `nfsServer_ip: 192.168.170.49`).
	// This runs AFTER ExpandHostNameIPs so the nested-map shape
	// is fully resolved first; the flat-shape pass is a strict
	// superset in the sense that it covers scalar hostnames the
	// nested walker would never see.
	effectiveVars = vars.ExpandTopLevelHostnameIPs(effectiveVars, hostNameToIP)
	// Inject the auto-derived `noapiserverips` top-level var
	// when `vars.apiserverLB.hostName` resolves to a master
	// host. The value is the space-separated list of master
	// IPs in declaration order, with the apiserverLB host
	// removed — this matches the install-k8s.yaml spec rule:
	//
	//   # 依据 apiserverLB 字段的hostName, 可以生成不包含apiserverLB_ip的所有master主机IP地址的内置变量，比如noapiserverips="192.168.170.48 192.168.170.49"
	//
	// User-declared `noapiserverips` wins — we never overwrite
	// an explicit value with a derived one (matches the
	// `ip`-field policy in ExpandHostNameIPs above).
	if _, has := effectiveVars["noapiserverips"]; !has {
		if ips := computeNoApiserverIps(inv, effectiveVars); ips != "" {
			effectiveVars["noapiserverips"] = ips
		}
	}
	// Inject the auto-derived `k8s_all_ip` top-level var: the
	// space-separated list of every host IP in `inv.All()`
	// order — which walks the `hosts:` groups alphabetically
	// (masters before workers given the conventional layout) and
	// walks each group in declaration order. This matches the
	// install-k8s.yaml spec rule:
	//
	//   # 依据hosts字段，添加k8s_all_ip="192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1"，包含所有masters和workers节点的IP地址。
	//
	// The var gives a script a single string it can splice into
	// etcd `--initial-cluster`, kubelet `cluster-cidr` peer
	// lists, or any other "every node in the cluster" use case
	// without having to parse `$k8s_hosts_alias` line by line.
	//
	// User-declared `k8s_all_ip` wins — we never overwrite an
	// explicit value with a derived one (matches the
	// `ip`-field policy in ExpandHostNameIPs above).
	if _, has := effectiveVars["k8s_all_ip"]; !has {
		if ips := computeK8sAllIP(inv); ips != "" {
			effectiveVars["k8s_all_ip"] = ips
		}
	}
	// Inject the auto-derived `k8s_all_hostname` top-level var:
	// the hostname (Host.Name) twin of `k8s_all_ip`, in the
	// same inventory.All() order so a script can zip the two
	// lists back into `ip hostname` pairs. Implements the
	// install-k8s.yaml spec rule:
	//
	//   # 依据hosts字段，添加k8s_all_hostname="master1 master2 master3 node1 node2 ip-192-168-1-1"，包含所有masters和workers节点的hostname。
	//
	// User-declared `k8s_all_hostname` wins — same override
	// policy as `k8s_all_ip` above.
	if _, has := effectiveVars["k8s_all_hostname"]; !has {
		if names := computeK8sAllHostname(inv); names != "" {
			effectiveVars["k8s_all_hostname"] = names
		}
	}
	// groupHostNames maps each declared group (e.g. "masters",
	// "workers") to the list of host names in that group, in
	// declaration order. The runner's per-group inventory builtins
	// (`{{ all_master_hostname }}` etc.) and the shell prelude's
	// `all_<group>_hostname=` exports both read from this map.
	// Names are the host Name (the `hostname:` field value), not
	// the install-k8s.yaml Key — the spec asks for shell-friendly
	// names without the k8s_ prefix.
	groupHostNames := map[string][]string{}
	for _, g := range inv.GroupNames() {
		hs, err := inv.Hosts(g)
		if err != nil {
			// Hosts() errors only on cycles / unknown members;
			// both are caught earlier in inventory.New. Treat
			// them here as an empty group so a regression in
			// validation can't crash the prelude pass.
			continue
		}
		names := make([]string, 0, len(hs))
		for _, h := range hs {
			names = append(names, h.Name)
		}
		groupHostNames[g] = names
	}
	// masterNodeNumber is the inventory-wide count of hosts in the
	// `masters` group (validated by inventory.New to be 1, 3, or 5).
	// Passed through to every host's vars.Context so the
	// {{ master_node_number }} template builtin and the shell
	// prelude's $master_node_number export both resolve.
	masterNodeNumber := inv.MasterCount()
	hostAgnosticCtx := &vars.Context{Vars: effectiveVars, AllHostNames: allHostNames, GroupHostNames: groupHostNames}
	for i, play := range plays {
		target, err := renderHostsSpec(play.Hosts, hostAgnosticCtx)
		if err != nil {
			return fmt.Errorf("play %d %q: %w", i+1, play.Name, err)
		}
		targets, err := inv.Hosts(target)
		if err != nil {
			return fmt.Errorf("play %d %q: %w", i+1, play.Name, err)
		}
		// Dispatch mode is controlled per-play by `execution_mode`:
		// only the literal "serial" string flips the play into
		// one-host-at-a-time mode. Everything else (empty,
		// "parallel", typos) keeps the default concurrent fan-out
		// — the typical case for group plays (e.g. `hosts: allnode`)
		// and the only safe choice for bootstrap steps that don't
		// race against each other. Serial mode exists as an opt-in
		// for ordered steps like the masters/workers join dance
		// where parallel execution would corrupt cluster state.
		sequential := play.ExecutionMode == "serial"
		if err := runPlay(play, targets, effectiveVars, allHosts, allHostNames, groupHostNames, masterNodeNumber, logBaseDir, sequential); err != nil {
			return err
		}
	}
	return nil
}

// renderHostsSpec renders a play's `hosts:` field with a
// host-agnostic context. The result is the literal string
// that gets handed to inventory.Hosts — either a single host
// name, a group name, or a comma-separated mix like
// "master1,master2" after rendering. Strings with no template
// markers are returned unchanged so non-template plays
// (e.g. `hosts: allnode`) pay no cost.
//
// renderHostsSpec lives next to Apply (rather than being
// inlined) so the host-agnostic constraint is documented in
// one place and unit tests can hit it without spinning up an
// inventory.
func renderHostsSpec(spec string, ctx *vars.Context) (string, error) {
	if !strings.Contains(spec, "{{") {
		return spec, nil
	}
	return vars.Render(spec, ctx)
}
