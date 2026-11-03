// Package vars renders {{ xxx }} and {{ host.xxx }} placeholders
// against a context built from the playbook's vars and the current
// host's facts.
//
// The vars scope is the entry file's top-level `vars:` block. A bare
// name (no dot) resolves from vars first, then from a small set of
// per-host built-in shortcuts (hostname, hostip, username, password,
// sshkey, os_arch) that match the env-var shortcuts shell scripts
// rely on, then from a small set of inventory-wide builtins
// (`all_<group>_hostname` — space-separated lists of every host's
// name within a given group) that don't depend on a single host. A
// dotted expression's first segment picks the scope:
//   - `host.<field>` walks the per-host facts map.
//   - any other leading segment (e.g. `registryServer.hostName`)
//     walks the vars map, starting at the user-declared key.
//
// Vars always take precedence over host builtins so a user-declared
// `hostname` var shadows the per-host builtin of the same name.
package vars

import (
	"fmt"
	"strconv"
	"strings"
)

// Context is the lookup table for {{ ... }} substitutions.
// Vars comes from the playbook entry file's top-level `vars:` block;
// Host is per-target facts (name/ip/user/port/ssh_key/password plus
// the auto-injected os_arch); AllHostNames is the inventory-wide list
// of host names in stable order; GroupHostNames is per-group host
// lists keyed by group name (e.g. "masters" → ["master1","master2"]),
// used to resolve `all_<group>_hostname` inventory builtins.
type Context struct {
	Vars          map[string]any
	Host          map[string]any
	AllHostNames  []string
	GroupHostNames map[string][]string
}

// hostBuiltins maps a bare top-level placeholder name to the host
// fact it resolves to. These are the per-host convenience shorthands
// that mirror the fields the inventory exposes — `{{ hostname }}`
// reads cleaner than `{{ host.name }}` in a shell module that wants
// to pass the current host's name as an argument, and the docs
// already advertised these names in the playbook entry-file header.
//
// `master_node_number` is the one non-per-host fact in this table:
// it's the size of the inventory's `masters` group (validated by
// inventory.New to be 1, 3, or 5). It lives on the host map rather
// than a separate context field because every host sees the same
// value and putting it next to the other inventory-wide facts keeps
// the hostBuiltin lookup table the single source of truth for what
// bare names resolve.
//
// Bare names that do NOT match a builtin fall through to a lookup in
// ctx.Vars, so `{{ data_root }}` resolves the same way as
// `{{ vars.data_root }}` would have in the old format. The vars
// scope is searched first; only when a name is missing from both
// vars and the builtin table does the renderer error.
var hostBuiltins = map[string]string{
	"hostname":           "name",
	"hostip":             "ip",
	"username":           "user",
	"password":           "password",
	"sshkey":             "ssh_key",
	"os_arch":            "os_arch",
	"master_node_number": "master_node_number",
}

// inventoryBuiltin resolves an inventory-wide bare name. Returns
// (value, true) when the name is a recognized inventory builtin, or
// ("", false) when it isn't. The supported pattern today is
// `all_<group>_hostname` — a space-separated list of every host's
// `hostname` (i.e. its Name field, not its Key) within the named
// group, in declaration order. Examples:
//
//   - `{{ all_master_hostname }}` → "master1 master2 master3"
//   - `{{ all_worker_hostname }}` → "node1 node2 ip-192-168-1-1"
//
// The env-var prefix is in singular form even when the inventory
// group name is plural (`masters`, `workers`), matching the
// install-k8s.yaml spec — `{{ all_master_hostname }}` resolves
// against the group named `masters` after trimming the trailing
// `s`. A group name that doesn't end in `s` is used as-is.
//
// This is convenient for shell scripts that want to iterate one
// role without re-parsing the inventory. Empty when the group is
// missing or empty.
//
// This scope is consulted AFTER vars and host builtins so a
// user-declared `all_master_hostname` var shadows the builtin, just
// like `{{ hostname }}` can be shadowed by a var of the same name.
func inventoryBuiltin(name string, ctx *Context) (string, bool) {
	const prefix = "all_"
	const suffix = "_hostname"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return "", false
	}
	group := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if group == "" {
		return "", false
	}
	// Synthesized composition groups (currently only `allnode`)
	// don't get a per-group builtin — the spec only documents
	// per-role exports, so `{{ all_allnode_hostname }}` is a
	// regular "not found" error rather than a silent fallthrough
	// that would mask a typo for one of the role names.
	if group == "allnode" {
		return "", false
	}
	if len(ctx.GroupHostNames) == 0 {
		// Spec behavior: when no group lookup is possible at all,
		// `{{ all_<group>_hostname }}` resolves to "" rather than
		// erroring — an empty inventory is a legitimate state
		// (e.g. tests, partial inventories) and shell scripts
		// iterating the variable expect to see an empty value.
		return "", true
	}
	// The env-var prefix is in singular form even when the
	// inventory group name is plural. Try the literal name first,
	// then fall back to the plural form (singular + 's') so that
	// `{{ all_master_hostname }}` resolves against a group named
	// `masters`, while a hypothetical group literally named
	// `master` (no trailing s) still matches its own entry.
	candidates := []string{group}
	if !strings.HasSuffix(group, "s") {
		candidates = append(candidates, group+"s")
	}
	for _, g := range candidates {
		if names, ok := ctx.GroupHostNames[g]; ok {
			return strings.Join(names, " "), true
		}
	}
	// Group key wasn't found — fall through to the regular
	// "not found" error so typos in the placeholder name surface
	// at parse time.
	return "", false
}

// Render returns s with every {{ ... }} occurrence replaced by its
// stringified lookup result. Unknown keys are an error so typos
// surface at parse time, not at runtime on a remote host.
func Render(s string, ctx *Context) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}

	var b strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				return "", fmt.Errorf("unterminated placeholder at offset %d: %q", i, s[i:])
			}
			expr := strings.TrimSpace(s[i+2 : i+2+end])
			val, err := resolve(expr, ctx)
			if err != nil {
				return "", fmt.Errorf("render %q: %w", expr, err)
			}
			b.WriteString(val)
			i += 2 + end + 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), nil
}

// resolve walks a path against ctx. A bare name with no dot is first
// checked against ctx.Vars (so `{{ data_root }}` works directly),
// then against hostBuiltins so `{{ hostname }}` resolves to the
// current host's name. A dotted expression whose first segment is
// `host` is routed to the host-facts scope; any other leading
// segment walks the vars map.
func resolve(expr string, ctx *Context) (string, error) {
	if !strings.Contains(expr, ".") {
		// 1. vars scope first — user-declared vars shadow host and
		//    inventory builtins so a var named e.g. `hostname` or
		//    `all_node_hostname` wins.
		if ctx.Vars != nil {
			if v, ok := lookupPath(ctx.Vars, []string{expr}); ok {
				return stringify(v), nil
			}
		}
		// 2. host-facts builtin shortcut.
		if field, ok := hostBuiltins[expr]; ok {
			if ctx.Host == nil {
				return "", fmt.Errorf("placeholder %q: host has no %q (no host context)", expr, field)
			}
			val, ok := ctx.Host[field]
			if !ok {
				return "", fmt.Errorf("placeholder %q: host has no %q", expr, field)
			}
			return stringify(val), nil
		}
		// 3. inventory-wide builtins (e.g. all_node_hostname).
		if v, ok := inventoryBuiltin(expr, ctx); ok {
			return v, nil
		}
		return "", fmt.Errorf("placeholder %q not found (vars scope and built-in host names: %s)", expr, builtinList())
	}

	parts := strings.Split(expr, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("placeholder %q must be of form <scope>.<key>", expr)
	}

	var root any
	if parts[0] == "host" {
		if ctx.Host == nil {
			return "", fmt.Errorf("placeholder %q: no host context", expr)
		}
		root = ctx.Host
		parts = parts[1:]
	} else {
		if ctx.Vars == nil {
			return "", fmt.Errorf("placeholder %q not found (no vars block in entry file)", expr)
		}
		root = ctx.Vars
	}

	v, ok := lookupPath(root, parts)
	if !ok {
		return "", fmt.Errorf("placeholder %q not found", expr)
	}
	return stringify(v), nil
}

// lookupPath walks a nested map[string]any by path. The boolean
// distinguishes "key found with nil value" from "key not present at
// all" — both stringify to "" but only the first is a successful
// lookup. The path must have at least one segment.
func lookupPath(root any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	cur := root
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, present := m[p]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// builtinList returns a stable, sorted list of the bare-name
// shortcuts so the error message can name them.
func builtinList() string {
	names := make([]string, 0, len(hostBuiltins))
	for k := range hostBuiltins {
		names = append(names, k)
	}
	// Tiny fixed list, sort by hand to avoid importing sort for one
	// call site.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return strings.Join(names, ", ")
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}
