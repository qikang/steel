package runner

import (
	"strings"

	"steel/internal/inventory"
)

// derivePluginVars walks every host's `plugin:` field and builds a
// fresh vars map that, after the existing ExpandHostNameIPs /
// ExpandTopLevelHostnameIPs passes run, produces the same env
// exports as the legacy "vars:" block declarations. Two value
// shapes are supported (and the legacy shapes are still supported
// when the user keeps declaring them in the top-level vars):
//
//   - Boolean (e.g. `traefikServer: true` on master1): the helper
//     emits a top-level `<key>: <hostName>` scalar. The
//     ExpandTopLevelHostnameIPs pass then derives the
//     `<key>_ip: <hostIP>` companion, matching the spec rule
//     "traefikServer 字段可以依据hostName生成内置变量
//     traefikServer_ip" — but driven by the per-host `plugin:`
//     field rather than a top-level `traefikServer: master1`
//     vars entry. The flat-shape expansion is a strict superset
//     of the same rule for the legacy declaration form, so the
//     output is byte-identical to the previous behavior.
//
//   - Map (e.g. `registryServer: { alias: registry.local }` on
//     master2): the helper emits a top-level `<key>: { hostName:
//     <hostName>, ip: <hostIP>, <original fields> }` map. The
//     `hostName` and `ip` siblings are what the existing
//     `vars.ExpandHostNameIPs` pass uses to auto-derive
//     `registryServer_ip`; the original fields (here `alias`)
//     pass through untouched and surface as e.g.
//     `registryServer_alias` in the shell prelude's
//     map-flattening pass. This matches the spec rule
//     "registryServer 字段可以依据hostName生成内置变量
//     registryServer_hostName=master2 /
//     registryServer_ip=192.168.170.49" when the declaration is
//     made under the host's `plugin:` field.
//
//   - Any other value shape (string, int, list, nil) is passed
//     through verbatim into `<key>: <value>` so future plugin
//     kinds can be added without a Go-side decode bump.
//
// "User-declared vars win" semantics: this helper NEVER overwrites
// an existing entry in `existing` — if the operator already
// declared `traefikServer: master2` in the top-level `vars:`, the
// host's plugin field is ignored for that key. This is the same
// override policy the auto-derived `k8s_all_ip` / `noapiserverips`
// / `k8s_all_hostname` injections follow (see Apply in apply.go),
// and it keeps a user-declared var — which they likely chose
// deliberately — from being silently clobbered when a host's
// plugin field happens to also name the same role.
//
// Same-key conflicts across hosts (e.g. two hosts both declaring
// `traefikServer: true`): the second host wins in inventory.All()
// iteration order (alphabetical group, then declaration order
// within a group). The first host's value is silently dropped.
// The runner does NOT error on duplicates — the inventory's
// host-de-dup step at apply time already guarantees the same
// `Name` is not declared twice, but two distinct hosts are
// allowed to claim the same role (the spec doesn't say what to
// do, and the user can resolve it by editing one out). Loudly
// failing the apply would surface a configuration style that
// has no spec-defined resolution.
//
// Returns nil when the inventory has no hosts or no host
// declares a plugin field, so the caller can skip the merge when
// there's nothing to inject. The returned map is a fresh
// allocation; the input `existing` is not mutated.
func derivePluginVars(inv *inventory.Inventory, existing map[string]any) map[string]any {
	if inv == nil {
		return nil
	}
	all := inv.All()
	if len(all) == 0 {
		return nil
	}
	var derived map[string]any
	for _, h := range all {
		if h == nil || len(h.Plugin) == 0 {
			continue
		}
		for key, val := range h.Plugin {
			if existing != nil {
				if _, already := existing[key]; already {
					// User-declared vars win; never overwrite.
					continue
				}
			}
			switch v := val.(type) {
			case bool:
				if !v {
					// `traefikServer: false` is treated as
					// "this host is not the <key> server" —
					// skip silently rather than emitting
					// `<key>=false`, which would later be
					// looked up against hostIPs and silently
					// not match (no host is named "false").
					continue
				}
				if derived == nil {
					derived = make(map[string]any, len(h.Plugin))
				}
				derived[key] = h.Name
			case map[string]any:
				// Inject `hostName: <h.Name>` and `ip: <h.IP>`
				// as siblings to whatever the user put in the
				// plugin map. The existing
				// vars.ExpandHostNameIPs pass will then derive
				// `<key>_ip: <h.IP>` from the `hostName`
				// sibling — and the `ip` we put in here is
				// also preserved (ExpandHostNameIPs only sets
				// `ip` if it isn't already there), so the
				// pre-existing behavior of "explicit ip wins"
				// still applies at this level too.
				out := make(map[string]any, len(v)+2)
				for k, x := range v {
					out[k] = x
				}
				out["hostName"] = h.Name
				out["ip"] = h.IP
				if derived == nil {
					derived = make(map[string]any, len(h.Plugin))
				}
				derived[key] = out
			default:
				// Pass through (string, int, list, etc.)
				// untouched. The shell prelude's
				// writeEnvFromAny flattens any value the
				// spec throws at it; this branch is the
				// forward-compat hook.
				if derived == nil {
					derived = make(map[string]any, len(h.Plugin))
				}
				derived[key] = val
			}
		}
	}
	return derived
}

// computeNoApiserverIps returns the space-separated list of
// master IPs whose host Name does NOT match
// playbookVars["apiserverLB"]["hostName"]. Implements the
// install-k8s.yaml spec rule:
//
//	# 依据 apiserverLB 字段的hostName, 可以生成不包含apiserverLB_ip的所有master主机IP地址的内置变量，比如noapiserverips="192.168.170.48 192.168.170.49"
//
// i.e. the IPs of all masters minus the apiserver-LB host —
// the rest of the control-plane endpoints a script needs
// alongside `$apiserverLB_ip`. The list is in declaration
// order (matching the inventory's masters-group ordering),
// which gives a deterministic, byte-stable env-var value for
// log diffing.
//
// Returns "" when:
//   - `apiserverLB.hostName` is missing, empty, or non-string
//     (no exclusion possible — caller skips injection);
//   - the inventory has no `masters` group (defensive — New
//     already rejects this, but the helper must not panic);
//   - the apiserverLB host isn't in the masters group (typo
//     or misconfiguration; we don't silently emit a list
//     that doesn't reflect the user's intent).
func computeNoApiserverIps(inv *inventory.Inventory, playbookVars map[string]any) string {
	lb, _ := playbookVars["apiserverLB"].(map[string]any)
	hostName, _ := lb["hostName"].(string)
	if hostName == "" {
		return ""
	}
	masters, err := inv.Hosts("masters")
	if err != nil || len(masters) == 0 {
		return ""
	}
	// Verify the apiserverLB host actually IS a master before
	// excluding. If it's not — e.g. the user typo'd "master2" as
	// "mater2", or pointed at a worker — silently emitting a
	// "non-LB masters" list would be misleading. Return "" so
	// the caller doesn't inject the var at all; the user gets
	// a clear `apiserverLB_ip` empty value in the prelude that
	// hints at the same root cause.
	var found bool
	var ips []string
	for _, h := range masters {
		if h.Name == hostName {
			found = true
			continue
		}
		ips = append(ips, h.IP)
	}
	if !found {
		return ""
	}
	return strings.Join(ips, " ")
}

// computeAllHostField walks inv.All() (alphabetical group, then
// declaration order within each group — masters before workers
// for the conventional install-k8s layout) and joins pick(h)
// for every non-empty value with a single space. Hosts whose
// pick returns "" are skipped — emitting a half-empty token
// would corrupt the list (a downstream `for x in $var` loop
// can't tell where the gap is when split on whitespace).
//
// Returns "" when the inventory has no hosts at all or every
// pick returns empty, so the caller can skip the prelude
// export instead of writing `export NAME=”`.
//
// pick is the per-host field selector. Two callers today:
//
//   - IP form:        pick = func(h *Host) string { return h.IP }
//   - hostname form:  pick = func(h *Host) string { return h.Name }
//
// Both fields are required by inventory.New, so under normal
// flow the empty-value filter is purely defensive — only test
// fixtures or partial inventories hit it. The single-string
// output is what scripts splice into etcd's --initial-cluster,
// kubelet peer lists, or `/etc/hosts` line generators without
// having to parse `$k8s_hosts_alias` line by line.
func computeAllHostField(inv *inventory.Inventory, pick func(*inventory.Host) string) string {
	all := inv.All()
	if len(all) == 0 {
		return ""
	}
	out := make([]string, 0, len(all))
	for _, h := range all {
		if h == nil {
			continue
		}
		v := pick(h)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ")
}

// computePrimaryHostField is the addworkers-excluding twin of
// computeAllHostField. It walks inv.PrimaryHosts() instead of
// inv.All() so the inventory-wide aggregates (`k8s_all_ip` /
// `k8s_all_hostname`) reflect only the hosts the initial
// bootstrap touched — never nodes a later `addworkers:` block
// filled in. Used by the bootstrap-time aggregates; the
// per-host `k8s_<key>_ip` exports and the `$k8s_hosts_alias`
// list keep using All() so they keep seeing every host the
// inventory declares. The skip is the install-k8s.yaml spec
// rule "addworkers 是一个独立的组".
func computePrimaryHostField(inv *inventory.Inventory, pick func(*inventory.Host) string) string {
	primary := inv.PrimaryHosts()
	if len(primary) == 0 {
		return ""
	}
	out := make([]string, 0, len(primary))
	for _, h := range primary {
		if h == nil {
			continue
		}
		v := pick(h)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ")
}

// computeK8sAllIP returns the space-separated list of every
// host IP in the inventory, in inventory.PrimaryHosts() order
// (alphabetical group, then declaration order within the group —
// so masters before workers for the conventional install-k8s
// layout, with hosts in the `addworkers` group excluded).
// Implements the install-k8s.yaml spec rule:
//
//	# 依据hosts字段，添加k8s_all_ip="192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22 192.168.170.43 192.168.170.1"，包含所有masters和workers节点的IP地址。
//
// i.e. every masters + workers IP concatenated with single
// spaces — a single string a script can hand to etcd's
// `--initial-cluster`, kubelet's TLS peer list, or any other
// "every node in the cluster" use case without having to parse
// `$k8s_hosts_alias` line by line.
//
// The order matches inventory.PrimaryHosts(): masters before
// workers (the conventional install-k8s layout has those two
// group names, and `sort.Strings` puts `masters` before
// `workers`). A regression that flipped the order would
// silently change every cluster-id hash downstream, so the
// order is part of the contract.
//
// The `addworkers` group is excluded — those hosts are joined
// to the cluster AFTER the initial bootstrap, so writing them
// into the bootstrap-time IP list would silently change every
// downstream etcd / kubelet peer hash. Operators that
// deliberately want addworkers included can declare an
// explicit `k8s_all_ip` in the `vars:` block (it wins over the
// derived value, matching the override policy on every other
// auto-derived var in Apply).
//
// Returns "" when the inventory has no primary hosts at all
// (no masters, no workers), in which case the caller skips
// injection and the prelude omits the export entirely (no
// `export k8s_all_ip=”`).
func computeK8sAllIP(inv *inventory.Inventory) string {
	return computePrimaryHostField(inv, func(h *inventory.Host) string { return h.IP })
}

// computeK8sAllHostname returns the space-separated list of
// every host hostname (Name field) in the inventory, in
// inventory.PrimaryHosts() order — same ordering contract as
// computeK8sAllIP (masters before workers, declaration order
// within each group, addworkers excluded). Implements the
// install-k8s.yaml spec rule:
//
//	# 依据hosts字段，添加k8s_all_hostname="master1 master2 master3 node1 node2 ip-192-168-1-1"，包含所有masters和workers节点的hostname。
//
// i.e. the hostname twin of `k8s_all_ip` — a single string a
// script can splice into `/etc/hosts`-style generators (e.g.
// an `ansible --limit` selector, a `for h in $k8s_all_hostname;
// do ssh root@$h ...; done` loop, or a CI matrix) without
// having to parse `$k8s_hosts_alias` line by line.
//
// The IP and hostname lists have the same length and the same
// index-to-index correspondence (both come from
// inv.PrimaryHosts() in the same order), so a script can zip
// them with a single `paste -d' ' <(echo $k8s_all_hostname)
// <(echo $k8s_all_ip)` to reconstruct the `ip hostname` pairs
// that `$k8s_hosts_alias` already provides. That redundancy is
// intentional: most bootstrap scripts only want one of the two
// (etcd only needs IPs; an SSH loop only needs hostnames) and
// emitting a pre-split list avoids re-parsing
// `$k8s_hosts_alias`.
//
// Returns "" when the inventory has no primary hosts at all,
// in which case the caller skips injection and the prelude
// omits the export entirely (no `export k8s_all_hostname=”`).
func computeK8sAllHostname(inv *inventory.Inventory) string {
	return computePrimaryHostField(inv, func(h *inventory.Host) string { return h.Name })
}
