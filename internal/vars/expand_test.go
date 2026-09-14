package vars

import (
	"reflect"
	"testing"
)

// TestExpandHostNameIPs_HappyPath pins the spec rule:
// `registryServer.hostName = master2` produces a sibling
// `registryServer.ip = master2's IP` so both the template
// `{{ registryServer.ip }}` and the shell env var
// `$registryServer_ip` resolve to that IP.
func TestExpandHostNameIPs_HappyPath(t *testing.T) {
	vars := map[string]any{
		"registryServer": map[string]any{
			"hostName": "master2",
			"alias":    "registry.local",
		},
		"apiserverLB": map[string]any{
			"hostName": "master3",
			"alias":    "apiserver.cluster.local",
		},
	}
	hostIPs := map[string]string{
		"master1": "10.0.0.1",
		"master2": "10.0.0.2",
		"master3": "10.0.0.3",
	}

	got := ExpandHostNameIPs(vars, hostIPs)

	rs, ok := got["registryServer"].(map[string]any)
	if !ok {
		t.Fatalf("registryServer missing or not a map: %T", got["registryServer"])
	}
	if rs["ip"] != "10.0.0.2" {
		t.Errorf("registryServer.ip = %v, want 10.0.0.2", rs["ip"])
	}
	if rs["hostName"] != "master2" {
		t.Errorf("registryServer.hostName = %v, want master2 (preserved)", rs["hostName"])
	}
	if rs["alias"] != "registry.local" {
		t.Errorf("registryServer.alias = %v, want registry.local (preserved)", rs["alias"])
	}

	lb, ok := got["apiserverLB"].(map[string]any)
	if !ok {
		t.Fatalf("apiserverLB missing or not a map: %T", got["apiserverLB"])
	}
	if lb["ip"] != "10.0.0.3" {
		t.Errorf("apiserverLB.ip = %v, want 10.0.0.3", lb["ip"])
	}
}

// TestExpandHostNameIPs_UnknownHostnameUntouched pins that
// `hostName` values that don't resolve in hostIPs leave the
// map untouched. Silently inserting `ip: ""` for an unresolved
// host would mask a typo.
func TestExpandHostNameIPs_UnknownHostnameUntouched(t *testing.T) {
	vars := map[string]any{
		"registryServer": map[string]any{
			"hostName": "ghost",
			"alias":    "registry.local",
		},
	}
	hostIPs := map[string]string{"master1": "10.0.0.1"}

	got := ExpandHostNameIPs(vars, hostIPs)
	rs := got["registryServer"].(map[string]any)
	if _, hasIP := rs["ip"]; hasIP {
		t.Errorf("unknown hostName should not inject ip; got %v", rs["ip"])
	}
	if rs["hostName"] != "ghost" {
		t.Errorf("hostName should be preserved; got %v", rs["hostName"])
	}
}

// TestExpandHostNameIPs_ExplicitIPWins pins that a
// user-declared `ip` field is never overwritten by the derived
// value. This guards against surprising users whose intent is
// already encoded in the yaml.
func TestExpandHostNameIPs_ExplicitIPWins(t *testing.T) {
	vars := map[string]any{
		"registryServer": map[string]any{
			"hostName": "master2",
			"ip":       "10.99.99.99", // explicit override
		},
	}
	hostIPs := map[string]string{"master2": "10.0.0.2"}

	got := ExpandHostNameIPs(vars, hostIPs)
	rs := got["registryServer"].(map[string]any)
	if rs["ip"] != "10.99.99.99" {
		t.Errorf("explicit ip must win; got %v, want 10.99.99.99", rs["ip"])
	}
}

// TestExpandHostNameIPs_NonStringHostNameIgnored pins that a
// `hostName` field with a non-string value (e.g. nil, int) is
// not used to derive an ip. Only string values get looked up.
func TestExpandHostNameIPs_NonStringHostNameIgnored(t *testing.T) {
	vars := map[string]any{
		"weird": map[string]any{
			"hostName": 42,
		},
	}
	hostIPs := map[string]string{"42": "10.0.0.42"}

	got := ExpandHostNameIPs(vars, hostIPs)
	w := got["weird"].(map[string]any)
	if _, hasIP := w["ip"]; hasIP {
		t.Errorf("non-string hostName should not inject ip; got %v", w["ip"])
	}
}

// TestExpandHostNameIPs_Recurses pins that the expansion walks
// nested maps. A vars tree with a sub-group under a top-level
// var still gets the ip-injection for any matching hostName
// deep down.
func TestExpandHostNameIPs_Recurses(t *testing.T) {
	vars := map[string]any{
		"topology": map[string]any{
			"registry": map[string]any{
				"hostName": "master1",
			},
		},
	}
	hostIPs := map[string]string{"master1": "10.0.0.1"}

	got := ExpandHostNameIPs(vars, hostIPs)
	topo := got["topology"].(map[string]any)
	reg := topo["registry"].(map[string]any)
	if reg["ip"] != "10.0.0.1" {
		t.Errorf("nested hostName should still derive ip; got %v", reg["ip"])
	}
}

// TestExpandHostNameIPs_NonMutating pins that the input map
// tree is not mutated. A regression here would surprise users
// who share the vars map between Apply() and downstream code.
func TestExpandHostNameIPs_NonMutating(t *testing.T) {
	vars := map[string]any{
		"registryServer": map[string]any{
			"hostName": "master2",
		},
	}
	hostIPs := map[string]string{"master2": "10.0.0.2"}

	// Snapshot the original tree so we can assert no mutation.
	original := map[string]any{
		"registryServer": map[string]any{
			"hostName": "master2",
		},
	}

	_ = ExpandHostNameIPs(vars, hostIPs)

	if !reflect.DeepEqual(vars, original) {
		t.Errorf("ExpandHostNameIPs mutated input\n  got:  %v\n  want: %v", vars, original)
	}
	rs := vars["registryServer"].(map[string]any)
	if _, hasIP := rs["ip"]; hasIP {
		t.Errorf("input map should not gain ip field; got %v", rs["ip"])
	}
}

// TestExpandHostNameIPs_PassesThroughNonMaps pins that scalars
// (string / int / etc.) and slices in the vars tree are
// returned by reference rather than being copied. The function
// only walks maps; everything else is opaque to it.
func TestExpandHostNameIPs_PassesThroughNonMaps(t *testing.T) {
	vars := map[string]any{
		"data_root":         "/data/install-k8s",
		"pod_network_cidr":  "10.244.0.0/16",
		"registryServer":    map[string]any{"hostName": "master2"},
		"unrelated_list":    []any{"a", "b", "c"},
	}
	hostIPs := map[string]string{"master2": "10.0.0.2"}

	got := ExpandHostNameIPs(vars, hostIPs)
	if got["data_root"] != "/data/install-k8s" {
		t.Errorf("scalar var lost: %v", got["data_root"])
	}
	if got["pod_network_cidr"] != "10.244.0.0/16" {
		t.Errorf("scalar var lost: %v", got["pod_network_cidr"])
	}
	if !reflect.DeepEqual(got["unrelated_list"], []any{"a", "b", "c"}) {
		t.Errorf("list var lost or mutated: %v", got["unrelated_list"])
	}
}

// TestExpandHostNameIPs_NilInputs pins that nil / empty inputs
// don't panic. A playbook with no `hostName` references and an
// empty inventory both produce empty results.
func TestExpandHostNameIPs_NilInputs(t *testing.T) {
	if got := ExpandHostNameIPs(nil, nil); len(got) != 0 {
		t.Errorf("nil vars -> %v, want empty", got)
	}
	if got := ExpandHostNameIPs(map[string]any{}, map[string]string{}); len(got) != 0 {
		t.Errorf("empty inputs -> %v, want empty", got)
	}
}

// TestExpandTopLevelHostnameIPs_HappyPath pins the spec rule:
// `nfsServer: master2` produces a sibling `nfsServer_ip: 192.168.170.49`
// so both the template `{{ nfsServer_ip }}` and the shell env
// var `$nfsServer_ip` resolve to that IP.
func TestExpandTopLevelHostnameIPs_HappyPath(t *testing.T) {
	vars := map[string]any{
		"nfsServer": "master2",
		"timeServer": "master3",
	}
	hostIPs := map[string]string{
		"master1": "192.168.170.48",
		"master2": "192.168.170.49",
		"master3": "192.168.170.47",
	}

	got := ExpandTopLevelHostnameIPs(vars, hostIPs)

	if got["nfsServer"] != "master2" {
		t.Errorf("nfsServer = %v, want master2 (preserved)", got["nfsServer"])
	}
	if got["nfsServer_ip"] != "192.168.170.49" {
		t.Errorf("nfsServer_ip = %v, want 192.168.170.49", got["nfsServer_ip"])
	}
	if got["timeServer"] != "master3" {
		t.Errorf("timeServer = %v, want master3 (preserved)", got["timeServer"])
	}
	if got["timeServer_ip"] != "192.168.170.47" {
		t.Errorf("timeServer_ip = %v, want 192.168.170.47", got["timeServer_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_UnknownHostnameUntouched pins
// that top-level string values not present in hostIPs leave
// the map untouched. Silently inserting `_ip=""` for an
// unresolved hostname would mask a typo (same rationale as
// ExpandHostNameIPs' unknown-hostname guard).
func TestExpandTopLevelHostnameIPs_UnknownHostnameUntouched(t *testing.T) {
	vars := map[string]any{
		"nfsServer": "ghost",
	}
	hostIPs := map[string]string{"master1": "10.0.0.1"}

	got := ExpandTopLevelHostnameIPs(vars, hostIPs)
	if got["nfsServer"] != "ghost" {
		t.Errorf("nfsServer = %v, want ghost (preserved)", got["nfsServer"])
	}
	if _, hasIP := got["nfsServer_ip"]; hasIP {
		t.Errorf("unknown hostname should not inject _ip; got %v", got["nfsServer_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_ExplicitIPWins pins that a
// user-declared `<key>_ip` field is never overwritten by the
// derived value. Same rationale as ExpandHostNameIPs'
// `ip`-field override policy.
func TestExpandTopLevelHostnameIPs_ExplicitIPWins(t *testing.T) {
	vars := map[string]any{
		"nfsServer":    "master2",
		"nfsServer_ip": "10.99.99.99", // explicit override
	}
	hostIPs := map[string]string{"master2": "192.168.170.49"}

	got := ExpandTopLevelHostnameIPs(vars, hostIPs)
	if got["nfsServer_ip"] != "10.99.99.99" {
		t.Errorf("explicit _ip must win; got %v, want 10.99.99.99", got["nfsServer_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_SkipsNonHostnameStrings pins
// that the rule ONLY triggers on string values that resolve in
// hostIPs. Non-hostname strings (CIDRs, paths, network stack
// names, etc.) MUST pass through untouched — otherwise every
// non-hostname string in install-k8s.yaml (pod_network_cidr,
// data_root, k8s_network_stack, dns_domain, ...) would get a
// bogus `<key>_ip=""` injection that masks typos and clutters
// the shell prelude.
func TestExpandTopLevelHostnameIPs_SkipsNonHostnameStrings(t *testing.T) {
	vars := map[string]any{
		"pod_network_cidr": "10.244.0.0/16",
		"data_root":        "/data/install-k8s",
		"k8s_network_stack": "ipv4",
		"dns_domain":       "cluster.local",
		"nfsServer":        "master2",
	}
	hostIPs := map[string]string{"master2": "192.168.170.49"}

	got := ExpandTopLevelHostnameIPs(vars, hostIPs)
	if _, hasIP := got["pod_network_cidr_ip"]; hasIP {
		t.Errorf("CIDR string should not inject _ip; got %v", got["pod_network_cidr_ip"])
	}
	if _, hasIP := got["data_root_ip"]; hasIP {
		t.Errorf("path string should not inject _ip; got %v", got["data_root_ip"])
	}
	if _, hasIP := got["k8s_network_stack_ip"]; hasIP {
		t.Errorf("network stack string should not inject _ip; got %v", got["k8s_network_stack_ip"])
	}
	if got["nfsServer_ip"] != "192.168.170.49" {
		t.Errorf("nfsServer_ip = %v, want 192.168.170.49", got["nfsServer_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_SkipsNonStrings pins that
// non-string top-level values (ints, bools, nested maps,
// slices) are NOT recursed into — the rule is intentionally
// restricted to top-level strings. Maps are the responsibility
// of ExpandHostNameIPs (which walks them recursively); ints and
// bools are never hostnames.
func TestExpandTopLevelHostnameIPs_SkipsNonStrings(t *testing.T) {
	vars := map[string]any{
		"registryServer": map[string]any{"hostName": "master2"},
		"intVar":         42,
		"boolVar":        true,
		"listVar":        []any{"a", "b"},
	}
	hostIPs := map[string]string{"master2": "192.168.170.49"}

	got := ExpandTopLevelHostnameIPs(vars, hostIPs)
	if _, hasIP := got["registryServer_ip"]; hasIP {
		t.Errorf("nested map should not be expanded at top level (use ExpandHostNameIPs); got %v", got["registryServer_ip"])
	}
	if _, hasIP := got["intVar_ip"]; hasIP {
		t.Errorf("int should not inject _ip; got %v", got["intVar_ip"])
	}
	if _, hasIP := got["boolVar_ip"]; hasIP {
		t.Errorf("bool should not inject _ip; got %v", got["boolVar_ip"])
	}
	if _, hasIP := got["listVar_ip"]; hasIP {
		t.Errorf("list should not inject _ip; got %v", got["listVar_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_EmptyStringUntouched pins that
// an empty string value (e.g. a misconfigured `nfsServer: ""`)
// is left alone rather than getting an empty `_ip=""` injection.
func TestExpandTopLevelHostnameIPs_EmptyStringUntouched(t *testing.T) {
	vars := map[string]any{
		"nfsServer": "",
	}
	hostIPs := map[string]string{"master2": "192.168.170.49"}

	got := ExpandTopLevelHostnameIPs(vars, hostIPs)
	if _, hasIP := got["nfsServer_ip"]; hasIP {
		t.Errorf("empty hostname string should not inject _ip; got %v", got["nfsServer_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_NonMutating pins that the
// input map is not mutated. Same contract as
// ExpandHostNameIPs_NonMutating — a regression here would
// surprise users who share the vars map between Apply() and
// downstream code.
func TestExpandTopLevelHostnameIPs_NonMutating(t *testing.T) {
	vars := map[string]any{
		"nfsServer": "master2",
	}
	hostIPs := map[string]string{"master2": "192.168.170.49"}

	original := map[string]any{
		"nfsServer": "master2",
	}

	_ = ExpandTopLevelHostnameIPs(vars, hostIPs)

	if !reflect.DeepEqual(vars, original) {
		t.Errorf("ExpandTopLevelHostnameIPs mutated input\n  got:  %v\n  want: %v", vars, original)
	}
	if _, hasIP := vars["nfsServer_ip"]; hasIP {
		t.Errorf("input map should not gain _ip field; got %v", vars["nfsServer_ip"])
	}
}

// TestExpandTopLevelHostnameIPs_NilInputs pins that nil / empty
// inputs don't panic. A playbook with no top-level hostname
// vars and an empty inventory both produce empty results.
func TestExpandTopLevelHostnameIPs_NilInputs(t *testing.T) {
	if got := ExpandTopLevelHostnameIPs(nil, nil); len(got) != 0 {
		t.Errorf("nil vars -> %v, want empty", got)
	}
	if got := ExpandTopLevelHostnameIPs(map[string]any{}, map[string]string{}); len(got) != 0 {
		t.Errorf("empty inputs -> %v, want empty", got)
	}
}