// Package playbook parses Steel's playbook YAML into a list of plays
// the runner can dispatch.
//
// Two entry shapes are accepted by LoadEntry:
//
//  1. A plain playbook — a YAML sequence of plays, e.g.
//     `- name: ...  hosts: ...  copy: ...`
//
//  2. A runlist — a YAML mapping with a top-level `runlist:` key
//     listing paths to other playbook files. Files in the runlist
//     are loaded in strict order, and each play's name is prefixed
//     with its source file's basename to keep names unique and
//     traceable across the concatenation.
package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"steel/internal/inventory"
)

// bareTplRE matches a YAML mapping value (the text after `key:`) that
// is a bare `{{ ... }}` placeholder, e.g. `hosts: {{ registryServer.hostName }}`.
// YAML 1.2 would otherwise interpret the leading `{` as the start of a
// flow mapping and parse the value as a nested map, which then fails
// to unmarshal into the typed string fields. We pre-process the raw
// document to wrap such values in double quotes so YAML treats them
// as plain scalars; the renderer later substitutes the placeholders
// per host. Already-quoted values (those starting with `"`) and
// mixed-content values (e.g. `cmd: bash foo {{ x }}`) are left alone
// because they don't start with `{{`.
var bareTplRE = regexp.MustCompile(`(?m)^([ \t]*[\w-]+:[ \t]*)(\{\{[^}\n]*\}\}[^\n]*)$`)

// autoQuoteBareTemplates wraps every YAML mapping value that starts
// with a bare `{{ ... }}` placeholder in double quotes. See bareTplRE
// for the matching rule. Operates on the raw text — the YAML parser
// is then free to decode the now-plain-string values into the typed
// structs, and the existing renderer handles the placeholder
// substitution at execution time.
func autoQuoteBareTemplates(raw string) string {
	return bareTplRE.ReplaceAllString(raw, `$1"$2"`)
}

// CopySpec is the parameter block for the `copy` module.
type CopySpec struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
	Mode string `yaml:"mode"`
}

// ShellSpec is the parameter block for the `shell` module.
type ShellSpec struct {
	Cmd   string `yaml:"cmd"`
	Bash  string `yaml:"bash"` // path to the shell interpreter; defaults to /bin/bash
	Chdir string `yaml:"chdir"`
	// directory on the remote host in which the command runs. Empty
	// means run from the login shell's default CWD (typically $HOME).
	// Supports {{ ... }} template rendering, same as Cmd, so values
	// like `{{ data_root }}/common/foo` resolve per host.
}

// Play is one entry in a playbook file. Exactly one of {Copy, Shell}
// is set.
//
// ExecutionMode controls how the play's targets fan out across hosts:
// when set to "serial", the targets are dispatched one after the other
// in declaration order; when empty (or any other value), every target
// runs concurrently. The default-empty semantics are "parallel" — the
// typical case for a one-host `hosts:` is unaffected (fan-out is a
// no-op for one target), and group plays (e.g. `hosts: allnode`)
// default to concurrent fan-out unless this field says otherwise.
// Only the literal "serial" string is recognized; unknown values
// fall through to the default (parallel) so a typo doesn't silently
// turn a play into a serial one.
type Play struct {
	Name          string     `yaml:"name"`
	Hosts         string     `yaml:"hosts"`
	ExecutionMode string     `yaml:"execution_mode"`
	Copy          *CopySpec  `yaml:"copy"`
	Shell         *ShellSpec `yaml:"shell"`
}

// Entry is what LoadEntry returns: the plays in execution order, plus
// the top-level `vars:` block (if any) and the embedded inventory
// declared in the entry file.
//
// Vars is the user-facing way to pass values into {{ xxx }}
// placeholders. It lives on the playbook entry file so that the same
// hosts definition can be reused across playbooks with different
// parameter sets. A plain (non-runlist) playbook has no place to
// declare vars; for those, Vars is nil.
//
// Hosts and Groups are the embedded inventory shape — the entry file
// can declare its hosts inline as a `hosts: { masters: [...], workers: [...] }`
// mapping and (optionally) sibling group keys for cross-group
// composition. The top-level `addworkers:` field (when present) is
// also decoded as a host group and stored under Hosts["addworkers"]
// — see decodeTopLevelHosts for the rationale. cmd/apply.go converts
// this into an *inventory.Inventory via inventory.New. Both fields
// are empty when the operator relies on the -i flag.
type Entry struct {
	Plays  []Play
	Vars   map[string]any
	Hosts  map[string][]inventory.Host
	Groups map[string][]string
}

// LoadEntry reads an entry file, auto-detecting its shape:
//   - top-level mapping containing `runlist:` → load and concatenate
//     each listed file in order (relative paths are resolved against
//     the entry file's directory). Any top-level `vars:` block is
//     captured and returned alongside the plays.
//   - top-level sequence of plays → return them after validation.
//
// The returned plays are in the order they will be executed.
func LoadEntry(path string) (Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("read entry %q: %w", path, err)
	}

	// Pre-process: wrap bare `{{ ... }}` placeholder values in double
	// quotes so YAML doesn't parse them as flow mappings. Done BEFORE
	// yaml.Unmarshal so the values are seen as plain strings. The
	// renderer handles placeholder substitution at execution time.
	processed := autoQuoteBareTemplates(string(raw))

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(processed), &doc); err != nil {
		return Entry{}, fmt.Errorf("parse entry %q: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return Entry{}, fmt.Errorf("entry %q is empty", path)
	}
	root := doc.Content[0]

	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "runlist" {
				return loadRunlist(path, root.Content[i+1], root)
			}
		}
		return Entry{}, fmt.Errorf("entry %q: top-level mapping has no 'runlist' key", path)
	}

	plays, err := loadPlainPlaybook(path, root)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Plays: plays}, nil
}

func loadRunlist(entryPath string, list *yaml.Node, root *yaml.Node) (Entry, error) {
	var entries []string
	if err := list.Decode(&entries); err != nil {
		return Entry{}, fmt.Errorf("runlist %q: must be a list of file paths: %w", entryPath, err)
	}
	baseDir := filepath.Dir(entryPath)

	vars, err := decodeTopLevelVars(root, entryPath)
	if err != nil {
		return Entry{}, err
	}

	hosts, groups, err := decodeTopLevelHosts(root, entryPath)
	if err != nil {
		return Entry{}, err
	}

	var all []Play
	for _, e := range entries {
		if e == "" {
			return Entry{}, fmt.Errorf("runlist %q: empty entry", entryPath)
		}
		sub := e
		if !filepath.IsAbs(sub) {
			sub = filepath.Join(baseDir, sub)
		}
		child, err := LoadEntry(sub)
		if err != nil {
			return Entry{}, fmt.Errorf("runlist entry %q (resolved %q): %w", e, sub, err)
		}
		// Prefix each play's name with the basename of the source file
		// (as written in the runlist, not the resolved path) so users
		// can trace a play back to the file that declared it.
		tag := filepath.Base(e)
		for i := range child.Plays {
			switch {
			case child.Plays[i].Name == "":
				child.Plays[i].Name = tag + ":play-" + strconv.Itoa(i+1)
			default:
				child.Plays[i].Name = tag + ":" + child.Plays[i].Name
			}
		}
		all = append(all, child.Plays...)
	}
	return Entry{
		Plays:  all,
		Vars:   vars,
		Hosts:  hosts,
		Groups: groups,
	}, nil
}

// decodeTopLevelVars extracts the entry file's top-level `vars:` map
// (sibling of `runlist:`). It returns (nil, nil) when the key is
// absent — vars are optional. A present-but-malformed value (e.g.
// `vars: "not a map"`) returns an error so a typo surfaces
// immediately rather than silently dropping the block.
//
// `path` is the entry file's path on disk; it's only used to format
// the error message. Pass whatever the caller has.
func decodeTopLevelVars(root *yaml.Node, path string) (map[string]any, error) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "vars" {
			continue
		}
		valNode := root.Content[i+1]
		if valNode.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("playbook %q: top-level 'vars' must be a mapping, got %v", path, valNode.Kind)
		}
		var out map[string]any
		if err := valNode.Decode(&out); err != nil {
			return nil, fmt.Errorf("playbook %q: decode top-level 'vars': %w", path, err)
		}
		return out, nil
	}
	return nil, nil
}

// decodeTopLevelHosts extracts the entry file's embedded inventory so
// a single file (e.g. install-k8s.yaml) is self-contained and the
// operator does not need a separate hosts.yaml. The shape mirrors
// the inventory package's File exactly:
//
//	hosts:
//	  masters:
//	    - hostname: master1
//	      ip: 192.168.170.48
//	      ...
//	  workers: []
//	addworkers: {}
//	allnode: [masters, workers]
//
// The `hosts:` value is decoded as a map of group name → []Host.
// `addworkers:` is a sibling-of-hosts top-level key that uses the
// same keyed-map shape as the groups under `hosts:` — it represents
// nodes that will be joined to the cluster AFTER the initial install,
// so the runner exposes it as its own group (a play can target it
// with `hosts: addworkers`) but excludes it from the auto-built
// `allnode` / `k8s_all_ip` / `k8s_all_hostname` aggregates (see
// inventory.New and computeK8sAllIP). An empty `addworkers: {}` is
// the canonical default — the install-k8s.yaml ships with it that
// way so the same entry file can be re-run later to grow the cluster
// by filling it in.
// Every other top-level key whose value is a list of strings is
// captured as a group (e.g. `allnode:` for cross-group composition).
// Reserved keys (`runlist`, `vars`) are skipped here — they're owned
// by their respective decoders.
//
// Returns (nil, nil, nil) when no hosts/groups are declared at all.
// Validation (required fields, group cycle detection) is the
// inventory package's job — this function only parses the YAML.
func decodeTopLevelHosts(root *yaml.Node, path string) (map[string][]inventory.Host, map[string][]string, error) {
	hosts := map[string][]inventory.Host{}
	groups := map[string][]string{}
	found := false

	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode, valNode := root.Content[i], root.Content[i+1]
		switch keyNode.Value {
		case "runlist", "vars":
			// Owned by sibling decoders; skip here.
			continue
		case "hosts":
			if valNode.Kind != yaml.MappingNode {
				return nil, nil, fmt.Errorf("playbook %q: top-level 'hosts' must be a mapping of group name to host entries, got %v", path, valNode.Kind)
			}
			for j := 0; j+1 < len(valNode.Content); j += 2 {
				groupName := valNode.Content[j].Value
				hs, err := decodePlaybookGroupHosts(path, groupName, valNode.Content[j+1])
				if err != nil {
					return nil, nil, err
				}
				hosts[groupName] = hs
			}
			found = true
		case "addworkers":
			// `addworkers:` lives at the top level (sibling of
			// `hosts:`), but its value uses the same keyed-map
			// shape as the groups under `hosts:` — it's a real
			// host group, not a reference list. Decode it as
			// such so a play can later target it with
			// `hosts: addworkers`. The inventory layer is
			// responsible for keeping it OUT of the auto-built
			// `allnode` / `k8s_all_ip` aggregates; this decoder
			// just populates Hosts["addworkers"] like any other
			// group. Empty `addworkers: {}` decodes to a zero-
			// length slice — valid, the install-k8s.yaml ships
			// that way and the apply loop simply sees no targets
			// for any `hosts: addworkers` play.
			hs, err := decodePlaybookGroupHosts(path, "addworkers", valNode)
			if err != nil {
				return nil, nil, err
			}
			hosts["addworkers"] = hs
			found = true
		default:
			// Anything else is treated as a group: a top-level key
			// whose value is a list of host/group names. A
			// non-list value here is a user error — surface it
			// rather than silently dropping.
			if valNode.Kind != yaml.SequenceNode {
				return nil, nil, fmt.Errorf("playbook %q: top-level %q must be a list of host/group names, got %v", path, keyNode.Value, valNode.Kind)
			}
			var members []string
			if err := valNode.Decode(&members); err != nil {
				return nil, nil, fmt.Errorf("playbook %q: decode group %q: %w", path, keyNode.Value, err)
			}
			groups[keyNode.Value] = members
			found = true
		}
	}

	if !found {
		return nil, nil, nil
	}
	return hosts, groups, nil
}

// decodePlaybookGroupHosts mirrors inventory.decodeGroupHosts but
// is duplicated here to avoid an import cycle (playbook already
// imports inventory; reusing the inventory helper would force
// inventory to import playbook to satisfy the helper's signature).
// The two implementations MUST stay in sync — both accept the
// same keyed-map input and produce []Host values with Host.Key
// populated from the map key. The keyed-map form is the only
// supported shape; a list-of-objects value is rejected here.
func decodePlaybookGroupHosts(path, groupName string, node *yaml.Node) ([]inventory.Host, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("playbook %q: hosts[%q] must be a mapping of <host-key>: <host>, got %v", path, groupName, node.Kind)
	}
	hs := make([]inventory.Host, 0, len(node.Content)/2)
	for j := 0; j+1 < len(node.Content); j += 2 {
		hostKey := node.Content[j].Value
		var h inventory.Host
		if err := node.Content[j+1].Decode(&h); err != nil {
			return nil, fmt.Errorf("playbook %q: decode hosts[%q][%q]: %w", path, groupName, hostKey, err)
		}
		h.Key = hostKey
		hs = append(hs, h)
	}
	return hs, nil
}

func loadPlainPlaybook(path string, root *yaml.Node) ([]Play, error) {
	var plays []Play
	if err := root.Decode(&plays); err != nil {
		return nil, fmt.Errorf("parse playbook %q: %w", path, err)
	}

	for i := range plays {
		p := &plays[i]
		if p.Name == "" {
			p.Name = fmt.Sprintf("play-%d", i+1)
		}
		if p.Hosts == "" {
			return nil, fmt.Errorf("play %q is missing 'hosts'", p.Name)
		}
		// Exactly one of the two module kinds must be set. Counting
		// rather than enumerating cases keeps the rule clear as new
		// modules are added.
		set := 0
		if p.Copy != nil {
			set++
		}
		if p.Shell != nil {
			set++
		}
		if set == 0 {
			return nil, fmt.Errorf("play %q has no module (copy/shell)", p.Name)
		}
		if set > 1 {
			return nil, fmt.Errorf("play %q defines multiple modules; only one is allowed", p.Name)
		}
		if p.Copy != nil {
			if p.Copy.Src == "" || p.Copy.Dest == "" {
				return nil, fmt.Errorf("play %q copy needs both 'src' and 'dest'", p.Name)
			}
		}
		if p.Shell != nil && p.Shell.Cmd == "" {
			return nil, fmt.Errorf("play %q shell needs 'cmd'", p.Name)
		}
	}

	return plays, nil
}
