package inventory

import (
	"gopkg.in/yaml.v3"
)

// Host represents a single remote target.
//
// Key (the entry-map key the host was declared under, e.g.
// `k8s_master1`) and Name (the `hostname:` field value, e.g.
// `master1`) are tracked separately. Key drives the per-host env
// var names (`k8s_<key>_ip`, `k8s_<key>_hostname`) used by shell
// scripts; Name drives the SSH hostname the orchestrator actually
// connects to and the per-host `{{ hostname }}` template builtin.
// They are equal for the common case where the entry-map key
// matches `hostname:`, but the spec lets them differ.
//
// Plugin is the host's per-host role assignments (e.g.
// `traefikServer: true`, `registryServer: { alias: ... }`). The
// runner walks this map and injects shell-visible env vars
// derived from it (see runner.derivePluginVars): boolean values
// expand to `<key>=<hostName>` plus an auto-derived `<key>_ip`,
// and map values get a `hostName` and `ip` sibling injected so
// the existing nested-walk in vars.ExpandHostNameIPs and the
// shell prelude's map-flattening pass can pick them up
// unchanged. nil when the host declares no `plugin:` field.
type Host struct {
	Key      string
	Name     string
	IP       string
	User     string
	SSHKey   string
	Password string
	Port     int
	Plugin   map[string]any
}

// hostYAML is the wire shape of a single host entry. It carries the
// canonical field names (name/ip/user/ssh_key/password/port) and the
// shorthand aliases (hostname/hostip/username/sshkey) that match the
// built-in env-var shortcuts exposed in templates. Both forms are
// accepted; the alias names are translated to their canonical
// counterpart before decode. The two-name model keeps the on-disk
// syntax flexible without forcing the rest of the code to know about
// the aliases (the rest only ever sees Host's canonical fields).
//
// `plugin:` is a free-form map[string]any — values are either a
// bool (`<key>: true`, meaning "this host is the <key> server") or
// a map (`<key>: { alias: ... }`, meaning the same plus an
// arbitrary set of supporting fields the runner exposes to shell
// scripts verbatim). Other shapes are passed through untouched
// and surfaced to the shell prelude's map-flattening pass
// unchanged, which lets the spec grow new plugin kinds without a
// Go-side decode bump.
type hostYAML struct {
	Name     string         `yaml:"name"`
	Hostname string         `yaml:"hostname"`
	IP       string         `yaml:"ip"`
	HostIP   string         `yaml:"hostip"`
	User     string         `yaml:"user"`
	Username string         `yaml:"username"`
	SSHKey   string         `yaml:"ssh_key"`
	SSHKeyS  string         `yaml:"sshkey"`
	Password string         `yaml:"password"`
	Port     int            `yaml:"port"`
	Plugin   map[string]any `yaml:"plugin"`
}

// UnmarshalYAML lets a host entry use either the canonical field
// names (name/ip/user/ssh_key/password/port) or the shorthand aliases
// (hostname/hostip/username/sshkey) that match the built-in env-var
// shortcuts exposed in templates. Shorthand values fall back to the
// canonical field when the canonical form is missing. If both forms
// are set, the value that appears later in the source wins (consistent
// with YAML's usual "last assignment wins" rule for repeated keys,
// which yaml.v3 does not actually re-implement per-key — the order of
// the map walk determines it; in practice users won't mix the two).
//
// The `plugin:` field is decoded as a generic map[string]any so
// the runner can walk it without committing to a fixed shape —
// see the type doc on Host.Plugin for the supported values.
func (h *Host) UnmarshalYAML(node *yaml.Node) error {
	var raw hostYAML
	if err := node.Decode(&raw); err != nil {
		return err
	}
	h.Name = pick(raw.Name, raw.Hostname)
	h.IP = pick(raw.IP, raw.HostIP)
	h.User = pick(raw.User, raw.Username)
	h.SSHKey = pick(raw.SSHKey, raw.SSHKeyS)
	h.Password = raw.Password
	h.Port = raw.Port
	h.Plugin = raw.Plugin
	return nil
}

// pick returns the first non-empty value. Used by Host.UnmarshalYAML
// to fall back from canonical to shorthand field.
func pick(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
