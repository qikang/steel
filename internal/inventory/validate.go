package inventory

import (
	"fmt"
	"sort"
)

// addWorkersGroup is the conventional group name for hosts that are
// joined to the cluster AFTER the initial install. The
// install-k8s.yaml entry file ships with `addworkers: {}` and the
// operator fills it in only when they need to grow the cluster.
// Hosts in this group are intentionally excluded from the auto-built
// `allnode` (see New below) and from the inventory-wide aggregates
// `k8s_all_ip` / `k8s_all_hostname` (see runner.computeK8sAllIP)
// — the spec treats `addworkers` as an independent opt-in group that
// plays must target explicitly (`hosts: addworkers`). Centralizing
// the literal here means the auto-build skip and the PrimaryHosts
// filter agree on the spelling.
const addWorkersGroup = "addworkers"

// New builds an *Inventory from an already-decoded *File, validating
// hosts (required fields, default port, ~ expansion) and groups
// (member references, no cycles). Used by Load (after parsing from
// disk) and by the playbook entry-file path when hosts are embedded
// directly in install-k8s.yaml — in that case the callers pass a
// *File built from the entry's `hosts:` block and sibling group keys.
func New(f *File) (*Inventory, error) {
	if f == nil {
		return nil, fmt.Errorf("inventory file is nil")
	}
	if f.Hosts == nil {
		f.Hosts = map[string][]Host{}
	}
	if f.Groups == nil {
		f.Groups = map[string][]string{}
	}

	// The group table is the union of every key under `hosts:` and
	// every sibling group key — the entry file's `allnode: [masters,
	// workers]` shortcut is a group-of-groups, but so is the implicit
	// `masters:` group that `hosts: { masters: [...] }` declared.
	groups := map[string][]string{}
	for g, hs := range f.Hosts {
		names := make([]string, 0, len(hs))
		for _, h := range hs {
			if h.Name == "" {
				return nil, fmt.Errorf("host in group %q is missing 'hostname'/'name'", g)
			}
			names = append(names, h.Name)
		}
		groups[g] = names
	}
	// Sorted list of host-group names. Used by All() and the implicit
	// allnode group below to walk groups in a stable order
	// (alphabetical — which puts "masters" before "workers" given
	// the conventional install-k8s.yaml layout) instead of Go's
	// randomized map iteration. This pins the order of inventory-wide
	// builtins like `all_node_hostname`. `addworkers` is sorted in
	// its natural position (`a` < `m` < `w`) so All() still includes
	// it — callers that want the "original cluster members" list use
	// PrimaryHosts(), which skips it explicitly.
	groupOrder := make([]string, 0, len(f.Hosts))
	for g := range f.Hosts {
		groupOrder = append(groupOrder, g)
	}
	sort.Strings(groupOrder)
	// `allnode` is a built-in group: it implicitly means "every host
	// declared under hosts: that was part of the original install".
	// The `addworkers` group is excluded — those hosts are joined
	// AFTER the initial bootstrap, so scripts that write to every
	// cluster node (etcd --initial-cluster, kubelet peer lists,
	// /etc/hosts generators) must not see them. An explicit
	// user-declared `allnode: [masters, workers, addworkers]`
	// sibling key still wins — operators that genuinely want
	// addworkers in the aggregate can opt back in. Iterate via
	// groupOrder so the built-in allnode has a deterministic order
	// (masters before workers, then any other host groups in
	// alphabetical order).
	if _, explicit := f.Groups["allnode"]; !explicit {
		all := make([]string, 0)
		for _, g := range groupOrder {
			if g == addWorkersGroup {
				continue
			}
			all = append(all, groups[g]...)
		}
		groups["allnode"] = all
	}
	for g, m := range f.Groups {
		if _, exists := groups[g]; exists && g != "allnode" && g != addWorkersGroup {
			return nil, fmt.Errorf("group %q is declared both under hosts: and as a sibling group key", g)
		}
		groups[g] = m
	}

	inv := &Inventory{
		hosts:      make(map[string]*Host),
		groups:     groups,
		groupOrder: groupOrder,
	}

	for _, hs := range f.Hosts {
		for i := range hs {
			h := hs[i]
			if h.Name == "" {
				return nil, fmt.Errorf("host at index %d is missing 'hostname'/'name'", i)
			}
			if h.IP == "" {
				return nil, fmt.Errorf("host %q is missing 'ip'", h.Name)
			}
			if h.User == "" {
				return nil, fmt.Errorf("host %q is missing 'user'", h.Name)
			}
			if h.SSHKey == "" && h.Password == "" {
				return nil, fmt.Errorf("host %q needs either 'ssh_key'/'sshkey' or 'password'", h.Name)
			}
			if h.Port == 0 {
				h.Port = 22
			}
			h.SSHKey = expandHome(h.SSHKey)
			// Key is always populated by decodeGroupHosts (the map
			// key under `hosts:`), so the `k8s_<key>_ip` env var
			// has a stable identifier by construction — no
			// fallback to Name is needed.
			if _, dup := inv.hosts[h.Name]; dup {
				return nil, fmt.Errorf("host %q is declared more than once", h.Name)
			}
			inv.hosts[h.Name] = &h
		}
	}

	// Validate groups up-front. A member may be either a host name or
	// another group name; mixed-mode is allowed (e.g. allnode: [masters, workers]).
	for g, members := range groups {
		for _, m := range members {
			_, isHost := inv.hosts[m]
			_, isGroup := inv.groups[m]
			if !isHost && !isGroup {
				return nil, fmt.Errorf("group %q references unknown member %q", g, m)
			}
		}
	}

	// Pre-validate that no group cycle exists, by attempting to expand
	// every group with an empty visiting set. This catches cycles before
	// a play asks for them.
	for g := range inv.groups {
		if _, err := inv.expandGroup(g, map[string]bool{}); err != nil {
			return nil, err
		}
	}

	// Enforce the spec's master-count rule literally: steel only
	// supports inventories whose `masters` group has exactly 1, 3,
	// or 5 hosts. Any other value — including the absence of a
	// `masters` group, a count of 0, or any non-{1,3,5} positive
	// number — is rejected. The shell prelude exports the size as
	// `master_node_number`, and the bootstrapping playbooks only
	// know how to handle the three supported configurations; a
	// fourth valid etcd quorum (7, 9, ...) isn't bootstrappable by
	// the bundled playbooks, and a worker-only inventory has no
	// master count to export. Fail fast here so the operator gets
	// a clear error message at load time rather than a half-
	// bootstrapped cluster later.
	names, ok := inv.groups["masters"]
	if !ok {
		return nil, fmt.Errorf("inventory has no 'masters' group; steel requires a masters group with exactly 1, 3, or 5 hosts (to export $master_node_number)")
	}
	n := len(names)
	if !isValidMasterCount(n) {
		return nil, fmt.Errorf("masters group has %d hosts; steel supports only 1, 3, or 5 master nodes", n)
	}
	inv.masterCount = n

	return inv, nil
}

// isValidMasterCount reports whether n is one of the master-count
// values the spec accepts (1 = single-node, 3 = HA small, 5 = HA
// large). All other values — 0, 2, 4, 6, 7, and beyond — are
// rejected by New so a misconfigured inventory surfaces at load
// time rather than as a bootstrapping failure later.
func isValidMasterCount(n int) bool {
	switch n {
	case 1, 3, 5:
		return true
	}
	return false
}
