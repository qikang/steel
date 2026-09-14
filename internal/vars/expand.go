package vars

// ExpandHostNameIPs returns a copy of vars with an `ip` field
// auto-injected next to every nested map's `hostName` entry whose
// string value is a key of hostIPs. The walk is non-destructive —
// the input map tree is not mutated.
//
// This implements the install-k8s.yaml spec's
// "依据hostName生成内置变量" rule. A playbook that declares
//
//	registryServer:
//	  hostName: master2
//	  alias: registry.local
//
// gets the additional field `registryServer.ip: <master2's IP>`
// injected, so:
//
//   - the dotted template `{{ registryServer.ip }}` resolves
//     (via the existing nested-walk in lookupPath);
//   - the shell prelude's existing map-flattening pass
//     (writeEnvFromAny) emits `export registryServer_ip=<IP>`
//     without any change, because `ip` is now just another field
//     on the same nested map.
//
// User-declared `ip` wins — if a playbook already has `ip:` on the
// same map we leave it alone, never overwrite an explicit value
// with a derived one. Unknown hostnames (anything not in hostIPs)
// are also left alone: silently inserting `ip: ""` for an
// unresolved host would mask a typo.
//
// hostIPs keys are the host Name field values (the `hostname:`
// yaml field, e.g. "master2"), not the hosts-map Key
// (e.g. "k8s_master2") — the spec writes `hostName` as the
// cross-reference, so the lookup follows that.
func ExpandHostNameIPs(vars map[string]any, hostIPs map[string]string) map[string]any {
	return expandHostNameIPsValue(vars, hostIPs).(map[string]any)
}

// ExpandTopLevelHostnameIPs returns a copy of vars with a
// `<key>_ip` field auto-injected for every top-level entry whose
// value is a non-empty string that resolves to a host Name in
// hostIPs. The walk is non-destructive — the input map tree is
// not mutated. Implements the install-k8s.yaml spec rule:
//
//	# 依据 nfsServer 的主机名，添加nfsServer_ip，比如nfsServer_ip="192.168.170.49"
//
// Unlike ExpandHostNameIPs (which operates on nested maps with a
// `hostName` field), this handles the flat-shape var where the
// hostname IS the value, e.g.:
//
//	nfsServer: master2
//
// gets the additional field `nfsServer_ip: 192.168.170.49`
// injected, so:
//
//   - the dotted template `{{ nfsServer_ip }}` resolves
//     (via lookupPath starting at the vars scope);
//   - the shell prelude's existing map-flattening pass
//     (writeEnvFromAny) emits `export nfsServer_ip=<IP>`
//     without any change, because `_ip` is now just another
//     top-level scalar.
//
// User-declared `<key>_ip` wins — if a playbook already has the
// `_ip` field we leave it alone, never overwrite an explicit
// value with a derived one. Unknown hostnames (anything not in
// hostIPs) are also left alone: silently inserting `_ip=""` for
// an unresolved host would mask a typo.
//
// hostIPs keys are the host Name field values (the `hostname:`
// yaml field, e.g. "master2"), not the hosts-map Key
// (e.g. "k8s_master2") — same convention as
// ExpandHostNameIPs, matching what the spec writes as the
// cross-reference.
//
// Note: this rule is intentionally restricted to top-level
// scalar values. Non-string top-level values (ints, bools,
// nested maps, slices) are passed through untouched —
// recursively walking nested maps would either duplicate the
// ExpandHostNameIPs work (for the map shape) or mis-derive
// from a list (which would be nonsense). Restricting to
// top-level strings also avoids false positives from the many
// non-hostname string vars in install-k8s.yaml (CIDRs, paths,
// etc.) — those values simply don't match a key in hostIPs.
func ExpandTopLevelHostnameIPs(vars map[string]any, hostIPs map[string]string) map[string]any {
	out := make(map[string]any, len(vars)+1)
	for k, v := range vars {
		out[k] = v
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		ip, found := hostIPs[s]
		if !found {
			continue
		}
		// User-declared `<key>_ip` wins — same override
		// policy as ExpandHostNameIPs' `ip`-field rule.
		if _, already := out[k+"_ip"]; already {
			continue
		}
		out[k+"_ip"] = ip
	}
	return out
}

// expandHostNameIPsValue is the recursive worker for
// ExpandHostNameIPs. At each map it copies every entry
// (recursing into sub-maps), then — if the map has a
// `hostName: <string>` field whose value resolves in hostIPs and
// the map has no existing `ip` — inserts an `ip` entry. Non-map
// values are returned by reference (strings, ints, etc. are
// immutable in Go).
func expandHostNameIPsValue(v any, hostIPs map[string]string) any {
	m, ok := v.(map[string]any)
	if !ok {
		// Slice / scalar — pass through untouched.
		return v
	}
	out := make(map[string]any, len(m)+1)
	for k, val := range m {
		out[k] = expandHostNameIPsValue(val, hostIPs)
	}
	if hn, ok := m["hostName"].(string); ok && hn != "" {
		if ip, found := hostIPs[hn]; found {
			if _, already := m["ip"]; !already {
				out["ip"] = ip
			}
		}
	}
	return out
}