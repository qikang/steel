package inventory

import "fmt"

// Hosts returns the resolved host list for a target spec.
// A target may be a single host name (e.g. "master1") or a group name
// (e.g. "masters", or "allnode" which transitively expands to its members).
// Returns an error if the target is unknown or part of a group cycle.
func (i *Inventory) Hosts(target string) ([]*Host, error) {
	if h, ok := i.hosts[target]; ok {
		return []*Host{h}, nil
	}
	return i.expandGroup(target, map[string]bool{})
}

// IsGroup reports whether name resolves as a host group rather than
// a single host. Kept for callers that want to distinguish the two
// shapes (e.g. inventory inspection, UI hints); the runner's actual
// dispatch-mode decision now lives on playbook.Play.ExecutionMode
// (see runner/apply.go), so IsGroup is no longer a runner-side
// dispatch hint.
//
// Returns false for unknown names. Callers that need to surface a
// "unknown target" error must still call Hosts and check its
// error — IsGroup is a predicate, not a validator.
func (i *Inventory) IsGroup(name string) bool {
	if _, ok := i.hosts[name]; ok {
		return false
	}
	_, ok := i.groups[name]
	return ok
}

// expandGroup walks a group recursively, collecting hosts while
// detecting cycles via the visiting set.
func (i *Inventory) expandGroup(name string, visiting map[string]bool) ([]*Host, error) {
	if visiting[name] {
		return nil, fmt.Errorf("group cycle involving %q", name)
	}
	members, ok := i.groups[name]
	if !ok {
		return nil, fmt.Errorf("unknown host or group %q", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	seen := map[string]bool{}
	out := []*Host{}
	for _, m := range members {
		if h, ok := i.hosts[m]; ok {
			if !seen[m] {
				seen[m] = true
				out = append(out, h)
			}
			continue
		}
		if _, isGroup := i.groups[m]; isGroup {
			sub, err := i.expandGroup(m, visiting)
			if err != nil {
				return nil, err
			}
			for _, h := range sub {
				if !seen[h.Name] {
					seen[h.Name] = true
					out = append(out, h)
				}
			}
			continue
		}
		return nil, fmt.Errorf("group %q references unknown member %q", name, m)
	}
	return out, nil
}

// All returns every host in the inventory, in a deterministic order:
// host groups are walked in alphabetical order (so a conventional
// `hosts: { masters: [...], workers: [...] }` yields masters before
// workers), and hosts within a group are in declaration order. This
// matches the spec for inventory-wide builtins like
// `all_node_hostname`, which want a stable, predictable list spanning
// both masters and workers. Iterating `i.hosts` directly would give
// Go's randomized map order, which is unsuitable for that contract.
//
// `addworkers` is included in All() — it lives in `i.hosts` like
// every other group, and callers that want the "original cluster
// members" list (excluding nodes joined post-install) should use
// PrimaryHosts() instead.
func (i *Inventory) All() []*Host {
	seen := map[string]bool{}
	out := make([]*Host, 0, len(i.hosts))
	for _, g := range i.groupOrder {
		for _, m := range i.groups[g] {
			h, ok := i.hosts[m]
			if !ok {
				continue
			}
			if seen[h.Name] {
				continue
			}
			seen[h.Name] = true
			out = append(out, h)
		}
	}
	return out
}

// PrimaryHosts returns every host in the inventory except those in
// the `addworkers` group — i.e. the "original cluster members" the
// initial bootstrap was performed against. Used by the runner to
// compute the `k8s_all_ip` / `k8s_all_hostname` aggregates, which
// must reflect only the nodes the bootstrap scripts wrote to (etcd
// `--initial-cluster`, kubelet peer lists, /etc/hosts generators):
// pulling in `addworkers` hosts would silently change every
// cluster-id hash downstream and mis-list peers.
//
// Order matches All() — alphabetical group, declaration order
// within each group — so a script can zip PrimaryHosts() by IP
// against PrimaryHosts() by hostname to reconstruct the same
// `ip hostname` pairs `k8s_hosts_alias` carries for the primary
// cluster. Returns an empty slice when the inventory has no
// non-addworkers hosts (e.g. an addworkers-only inventory that
// would never load anyway because the masters-count validation
// runs before this is consulted).
func (i *Inventory) PrimaryHosts() []*Host {
	seen := map[string]bool{}
	out := make([]*Host, 0, len(i.hosts))
	for _, g := range i.groupOrder {
		if g == addWorkersGroup {
			continue
		}
		for _, m := range i.groups[g] {
			h, ok := i.hosts[m]
			if !ok {
				continue
			}
			if seen[h.Name] {
				continue
			}
			seen[h.Name] = true
			out = append(out, h)
		}
	}
	return out
}

// GroupNames returns the names of every declared group (both the
// group names that appear under `hosts:` and the sibling group keys).
// Order is unspecified.
func (i *Inventory) GroupNames() []string {
	out := make([]string, 0, len(i.groups))
	for g := range i.groups {
		out = append(out, g)
	}
	return out
}
