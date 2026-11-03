// Package inventory parses Steel's hosts section and resolves targets
// (individual hosts or group names) into a flat list of hosts.
//
// The on-disk format groups hosts by role:
//
//	hosts:
//	  masters:
//	    - hostname: master1
//	      ip: 192.168.170.48
//	      user: root
//	      password: ...
//	  workers: []
//
// Every top-level key under `hosts:` is a group name; the value is the
// list of host entries that belong to it. A group's members are host
// names; the resolver expands nested groups depth-first and rejects
// cycles. Top-level group keys (siblings of `hosts:`, e.g. `allnode:`)
// are also supported for cross-group composition.
//
// Variables do NOT live here — they belong on the playbook entry
// file. Keeping the inventory host-only lets the same definition be
// reused across playbooks with different parameter sets.
package inventory

// File is the on-disk shape of the inventory / entry hosts block.
//
// Hosts is keyed by group name; each value is the list of hosts that
// belong to that group. Groups is an optional sibling map for
// cross-group composition (e.g. `allnode: [masters, workers]`) that
// lives at the same level as `hosts:` in the entry file. The
// inventory group table is the union of Hosts's keys and Groups's
// keys; a group's members may be host names (looked up in Hosts) or
// other group names (looked up in Groups).
//
// The `addworkers` group is a special case: it lives at the top
// level of the entry file (sibling of `hosts:`, with the same
// keyed-map shape) and is meant for nodes joined to the cluster
// AFTER the initial install. Hosts in this group are still in
// Hosts["addworkers"] here — inventory.New is what excludes them
// from the auto-built `allnode` aggregate, and the runner's
// computeK8sAllIP is what excludes them from the inventory-wide
// IP / hostname lists. Plays target them explicitly with
// `hosts: addworkers`.
type File struct {
	Hosts  map[string][]Host
	Groups map[string][]string
}

// Inventory is a parsed and validated inventory, ready for lookup.
type Inventory struct {
	hosts      map[string]*Host
	groups     map[string][]string
	groupOrder []string // sorted host-group names (the keys of f.Hosts), for deterministic All() iteration
	// masterCount is the size of the `masters` group at build time,
	// validated by New against the spec's {1, 3, 5} rule. Cached so
	// the shell prelude and the {{ master_node_number }} builtin
	// don't have to walk the group table on every call.
	masterCount int
}

// MasterCount returns the number of hosts in the `masters` group.
// The value is guaranteed to be 1, 3, or 5 — New rejects any
// inventory that doesn't have a `masters` group of exactly one
// of those sizes, so the inventory returned to callers is always
// in a state where MasterCount() reports a supported value.
func (i *Inventory) MasterCount() int {
	return i.masterCount
}
