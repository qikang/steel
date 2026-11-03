// The shell-env prelude that Shell() prepends to every remote
// command. The prelude is a `;`-separated list of `export k=v`
// statements that inject per-host facts, inventory-wide builtins,
// and the playbook's vars into the remote login shell — so a
// shell script on master1 can reference `$k8s_master1_ip` (the
// first master node's IP), `$k8s_hosts_alias` (every host as a
// `'ip hostname'` pair, ready for `for pair in $k8s_hosts_alias`),
// `$master_node_number` (size of the validated `masters` group,
// used by etcd/kubeadm bootstrap scripts), or
// `$registryServer_hostName` (underscore-flattened nested var)
// without any extra plumbing.
//
// Split out of shell.go so the prelude shape and the ordering
// rules can be tested and evolved independently of the SSH/PTY
// dance in shell.go proper.
package shell

import (
	"fmt"
	"sort"
	"strings"

	"steel/internal/inventory"
	"steel/internal/vars"
)

// buildEnvPrelude assembles the `export k=v; export ...;` prelude
// prepended to the user's command so shell scripts can reference:
//
//   - `os_arch` (probe result, e.g. amd64 | arm64) — always first.
//   - `master_node_number` (size of the inventory's `masters`
//     group, validated by inventory.New to be exactly 1, 3, or 5).
//     Emitted immediately after `os_arch` because both are
//     topology-level facts rather than per-host fields. The
//     prelude always has a value here: inventory.New rejects any
//     inventory without a masters group of the right size, so
//     `hostFacts` has populated this field by the time the
//     prelude runs.
//   - `k8s_master1_ip` — the IP of the host whose hosts-map key is
//     `k8s_master1` (the first master, in declaration order). The
//     spec keeps this one per-host export because the bootstrap
//     scripts always need to know the first master's address —
//     all other `k8s_<key>_ip` / `k8s_<key>_hostname` pairs and
//     the per-group `all_<group>_hostname` lists are intentionally
//     NOT emitted. Scripts that need to iterate hosts should use
//     `k8s_hosts_alias` instead (see below).
//   - `k8s_hosts_alias` — every host in declaration order
//     (inventory.All() order: alphabetical group, then hosts
//     within the group), formatted as a single shell value where
//     each host is one `ip hostname` pair separated from the next
//     by a literal `\n ` (three chars: backslash, n, space — the
//     trailing space lets a downstream `IFS=$'\n '` split recover
//     clean pairs without leading whitespace on non-first lines).
//     Scripts can append the result to /etc/hosts with a single
//     `echo -e "$k8s_hosts_alias" >> /etc/hosts`, or iterate line
//     by line with `while read -r pair; do ...; done
//     <<<"$k8s_hosts_alias"`. Empty when the inventory has no
//     hosts.
//   - Cluster-wide mutable shell vars. The current spec has
//     none — the kubeadm bootstrap dance now reads its inputs
//     from a separate `/etc/hosts`-style side channel written
//     by the orchestrator, not from orchestrator-managed state
//     — so no extra `export NAME=…` lines are emitted here.
//   - Top-level vars exported under their names; nested maps
//     flattened to underscore-separated names (e.g.
//     `registryServer.hostName` → `registryServer_hostName`).
//
// Returns "" only if arch is empty AND there's nothing else to
// export — which never happens in practice because os_arch is
// always populated by the orchestrator before Shell() is called.
func buildEnvPrelude(arch string, ctx *vars.Context, allHosts []*inventory.Host) string {
	var b strings.Builder
	// os_arch first — most scripts depend on it, and it always has
	// a value once hostFacts succeeded.
	b.WriteString("export os_arch=")
	b.WriteString(shellQuote(arch))

	if ctx == nil {
		ctx = &vars.Context{}
	}

	// master_node_number is the inventory-wide size of the
	// `masters` group (validated by inventory.New to be 1, 3, or 5).
	// It is sourced from the host facts map rather than passed in
	// separately because every host sees the same value and
	// vars.Context.Host is already the canonical per-host fact
	// store. When the host map is nil (e.g. unit tests) or the
	// field is missing (worker-only inventories with no masters
	// group), we omit the export rather than writing an empty
	// value — a script that depends on $master_node_number is by
	// definition operating against a masters-bearing inventory.
	if n, ok := masterNodeNumberFromCtx(ctx); ok {
		b.WriteString("; export master_node_number=")
		b.WriteString(shellQuote(n))
	}

	// k8s_master1_ip — the first master's IP. Find the host whose
	// hosts-map key (normalized with the `k8s_` prefix) is
	// `k8s_master1`. The legacy list-form inventory falls back to
	// the host Name for the key (see inventory.New), so
	// peer.Key=="master1" still matches after normalization.
	if master1 := findHostByKey(allHosts, "k8s_master1"); master1 != nil {
		b.WriteString("; export k8s_master1_ip=")
		b.WriteString(shellQuote(master1.IP))
	}

	// k8s_hosts_alias — one `ip hostname` pair per host in
	// declaration order, separated by a literal `\n` (backslash-n
	// so `echo -e` expands it into a real newline before appending
	// to /etc/hosts). The whole value is single-quoted so the
	// backslash survives intact and the assignment is a single
	// shell token — a script can then write
	// `echo -e "$k8s_hosts_alias" >> /etc/hosts` directly.
	if pairs := buildHostsAlias(allHosts); pairs != "" {
		b.WriteString("; export k8s_hosts_alias=")
		b.WriteString(shellQuote(pairs))
	}

	// Top-level vars (with nested fields flattened). Sorted by
	// key for stable output so the prelude is byte-stable across
	// runs and easier to diff in logs.
	keys := make([]string, 0, len(ctx.Vars))
	for k := range ctx.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeEnvFromAny(&b, k, ctx.Vars[k])
	}
	return b.String()
}

// masterNodeNumberFromCtx pulls the master-count string out of the
// host facts map. It returns ("", false) when the host map is nil
// or the field is missing so the prelude can decide whether to
// emit an export. In production this only returns ok=false when a
// unit test built the ctx without going through Apply; Apply
// guarantees the field is populated because inventory.New now
// rejects any inventory without a masters group of size 1/3/5.
func masterNodeNumberFromCtx(ctx *vars.Context) (string, bool) {
	if ctx == nil || ctx.Host == nil {
		return "", false
	}
	v, ok := ctx.Host["master_node_number"]
	if !ok {
		return "", false
	}
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		if x == "" {
			return "", false
		}
		return x, true
	case int:
		return fmt.Sprintf("%d", x), true
	case int64:
		return fmt.Sprintf("%d", x), true
	case float64:
		return fmt.Sprintf("%d", int(x)), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

// findHostByKey returns the first host in allHosts whose
// hosts-map key (after the same `k8s_`-prefix normalization the
// per-host exports used to apply) matches want. Returns nil when
// no match is found. The match is a strict equality on the
// normalized key — there's no role/group lookup here, just a
// one-shot scan of the inventory in declaration order.
//
// The normalization mirrors the legacy export loop so a list-form
// inventory where peer.Key=="master1" (the fallback when there's
// no per-host key, set by inventory.New from peer.Name) still
// resolves under the prefix-canonical name `k8s_master1`.
func findHostByKey(allHosts []*inventory.Host, want string) *inventory.Host {
	for _, h := range allHosts {
		if h == nil || h.Key == "" {
			continue
		}
		key := h.Key
		if !strings.HasPrefix(key, "k8s_") {
			key = "k8s_" + key
		}
		if key == want {
			return h
		}
	}
	return nil
}

// buildHostsAlias formats allHosts as a single shell-quoted
// string suitable for `echo -e "$k8s_hosts_alias" >> /etc/hosts`.
// Each host becomes one `ip hostname` pair, and pairs are
// separated by a literal `\n` followed by a single space
// (three characters total: backslash, n, space) so that when
// the value is later passed to `echo -e`, each pair lands on
// its own line of /etc/hosts. The trailing space exists so a
// downstream script can split on `\n ` (the separator itself)
// and get back clean `ip hostname` strings without leading
// whitespace on every line except the first — that's the
// natural input shape for both `for pair in $k8s_hosts_alias`
// (which word-splits on IFS whitespace and would otherwise eat
// a leading space off each pair) and for `read -r pair` in a
// while-loop driven off the alias.
//
// The backslash is preserved through single-quoted export so a
// plain `cat <<<"$k8s_hosts_alias"` in the script still shows
// the `\n` literally. Hosts with empty IP or Name are skipped
// — emitting a half-empty pair would corrupt the hosts file.
// Order matches the input slice (inventory.All() order:
// alphabetical group, then declaration order within the group).
// Returns "" when there is nothing to iterate, in which case
// the prelude omits the export rather than emitting
// `export k8s_hosts_alias=”`.
//
// Completeness contract: every host in the hosts field ends up
// in the alias. The caller (apply.go) sources allHosts from
// inventory.All(), which walks every group under `hosts:` in
// sorted order and concatenates the members in declaration
// order with name-based dedup — so any host declared under
// masters, workers, or any other role-bearing group appears
// in the output exactly once, in masters-before-workers order.
// inventory.New validates that every host has both IP and Name,
// so the empty-field skip is purely defensive (against test
// fixtures or partial inventories) and never silently drops a
// production host.
func buildHostsAlias(allHosts []*inventory.Host) string {
	parts := make([]string, 0, len(allHosts))
	for _, h := range allHosts {
		if h == nil || h.IP == "" || h.Name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", h.IP, h.Name))
	}
	return strings.Join(parts, `\n `)
}

// writeEnvFromAny flattens v into one or more `export NAME=VALUE;`
// chunks appended to b. NAME is the top-level key; nested maps
// walk down with underscores joining the segments (so
// `registryServer.hostName` becomes `registryServer_hostName`).
// Scalars become a single export; nil becomes an empty string;
// maps/arrays that can't be flattened (mixed-shape, etc.) fall
// back to a best-effort fmt.Sprintf of the value so the user
// still gets something rather than a silent drop.
//
// envName is the dot-free name already accumulated for the
// environment variable; on each nested step it gets an underscore
// and the next segment appended.
func writeEnvFromAny(b *strings.Builder, envName string, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		b.WriteString(shellQuote(""))
	case string:
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		b.WriteString(shellQuote(x))
	case bool:
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		if x {
			b.WriteString(shellQuote("true"))
		} else {
			b.WriteString(shellQuote("false"))
		}
	case int:
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		b.WriteString(shellQuote(fmt.Sprintf("%d", x)))
	case int64:
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		b.WriteString(shellQuote(fmt.Sprintf("%d", x)))
	case float64:
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		b.WriteString(shellQuote(strconvFloat(x)))
	case map[string]any:
		// Sorted output keeps the prelude byte-stable for diffing.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeEnvFromAny(b, envName+"_"+k, x[k])
		}
	default:
		// Fall back to fmt for any type we don't recognize so the
		// operator still gets something exported rather than a
		// silent drop.
		b.WriteString("; export ")
		b.WriteString(envName)
		b.WriteString("=")
		b.WriteString(shellQuote(fmt.Sprintf("%v", v)))
	}
}

// strconvFloat is a tiny helper that formats a float without
// trailing zeros so `10.0` (a YAML number that decoded to float64)
// renders as "10" rather than "10.000000".
func strconvFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}
