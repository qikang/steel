package inventory

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads, expands ~ in ssh_key paths, and validates an inventory
// file. The file's `hosts:` block is a mapping of group name to host
// entries; each group's value MUST be a YAML mapping keyed by an
// internal host id (e.g. `k8s_master1`), with each value a single
// host object:
//
//	hosts:
//	  masters:
//	    k8s_master1:
//	      hostname: master1
//	      ip: 192.168.170.48
//	      user: root
//	      password: ...
//
// The map key populates Host.Key (used to build `k8s_<key>_ip` env
// vars); the `hostname:` field populates Host.Name (used for SSH
// and the `{{ hostname }}` template builtin). The keyed-map form is
// the only supported shape — a list-of-objects form is rejected at
// decode time so the per-host env-var naming has a stable identifier
// to draw from.
//
// `addworkers:` is a top-level sibling of `hosts:` that uses the
// same keyed-map shape — it represents nodes joined to the cluster
// AFTER the initial install. Hosts there are decoded into
// Hosts["addworkers"] and then excluded from the auto-built
// `allnode` / `k8s_all_ip` aggregates (see New and runner.computeK8sAllIP).
// An empty `addworkers: {}` is the canonical default.
//
// Sibling group keys (e.g. `allnode: [masters, workers]`) are
// captured into File.Groups.
func Load(path string) (*Inventory, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inventory %q: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse inventory %q: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("inventory %q is empty", path)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("inventory %q: top-level must be a mapping", path)
	}

	f := &File{
		Hosts:  map[string][]Host{},
		Groups: map[string][]string{},
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode, valNode := root.Content[i], root.Content[i+1]
		switch keyNode.Value {
		case "hosts":
			if valNode.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("inventory %q: 'hosts' must be a mapping of group name to host entries, got %v", path, valNode.Kind)
			}
			for j := 0; j+1 < len(valNode.Content); j += 2 {
				groupName := valNode.Content[j].Value
				hs, err := decodeGroupHosts(path, groupName, valNode.Content[j+1])
				if err != nil {
					return nil, err
				}
				f.Hosts[groupName] = hs
			}
		case addWorkersGroup:
			// Same keyed-map shape as the groups under `hosts:` —
			// see playbook.decodeTopLevelHosts for the matching
			// entry-file shape and the rationale. Reusing the
			// decodeGroupHosts helper keeps the per-host env-var
			// key (Host.Key) populated consistently across both
			// code paths.
			hs, err := decodeGroupHosts(path, addWorkersGroup, valNode)
			if err != nil {
				return nil, err
			}
			f.Hosts[addWorkersGroup] = hs
		default:
			var members []string
			if err := valNode.Decode(&members); err != nil {
				return nil, fmt.Errorf("inventory %q: group %q must be a list of names: %w", path, keyNode.Value, err)
			}
			f.Groups[keyNode.Value] = members
		}
	}

	inv, err := New(f)
	if err != nil {
		return nil, fmt.Errorf("inventory %q: %w", path, err)
	}
	return inv, nil
}

// decodeGroupHosts decodes a single group's host entries from a
// YAML mapping node keyed by an internal host id. The map key
// populates Host.Key; the value is decoded as a single host object.
// A non-mapping node (e.g. a YAML list) is rejected here with a
// clear message rather than failing later at a type assertion.
//
// The mapping is walked by hand rather than via map[string]Host
// so the per-host `key` populates Host.Key in declaration order —
// Go's map iteration is randomized, but the `k8s_<key>_ip`
// ordering in the shell prelude is declaration-stable, so losing
// the order would silently change env-var values.
func decodeGroupHosts(path, groupName string, node *yaml.Node) ([]Host, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("inventory %q: hosts[%q] must be a mapping of <host-key>: <host>, got %v", path, groupName, node.Kind)
	}
	hs := make([]Host, 0, len(node.Content)/2)
	for j := 0; j+1 < len(node.Content); j += 2 {
		hostKey := node.Content[j].Value
		var h Host
		if err := node.Content[j+1].Decode(&h); err != nil {
			return nil, fmt.Errorf("inventory %q: decode hosts[%q][%q]: %w", path, groupName, hostKey, err)
		}
		h.Key = hostKey
		hs = append(hs, h)
	}
	return hs, nil
}
