package profile

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func terminalMergedConfig(t *testing.T, base, incoming, overlay string) map[string]any {
	t.Helper()
	content, err := MergeSubscription(base, incoming, overlay)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := yaml.Unmarshal([]byte(content), &result); err != nil {
		t.Fatalf("merged config is invalid YAML: %v\n%s", err, content)
	}
	return result
}

func TestTerminalSubscriptionPreservesLocalSettings(t *testing.T) {
	base := `mixed-port: 17890
external-controller: 127.0.0.1:19090
secret: local-secret
dns:
  enable: true
  nameserver: [1.1.1.1]
proxies: [{name: old, type: direct}]
proxy-groups: [{name: keep, type: select, proxies: [old]}]
rules: ["MATCH,DIRECT"]
`
	incoming := `mixed-port: 7890
external-controller: 0.0.0.0:9090
secret: provider-secret
dns: {enable: false}
proxies: [{name: new, type: direct}]
`
	got := terminalMergedConfig(t, base, incoming, "")
	if got["mixed-port"] != 17890 || got["external-controller"] != "127.0.0.1:19090" || got["secret"] != "local-secret" {
		t.Fatalf("local listener/credentials overwritten: %#v", got)
	}
	if dns := got["dns"].(map[string]any); dns["enable"] != true || !reflect.DeepEqual(dns["nameserver"], []any{"1.1.1.1"}) {
		t.Fatalf("local DNS overwritten: %#v", dns)
	}
	if got["proxies"].([]any)[0].(map[string]any)["name"] != "new" || got["proxy-groups"].([]any)[0].(map[string]any)["name"] != "keep" {
		t.Fatalf("node-only subscription replaced unrelated routing fields: %#v", got)
	}
	if !reflect.DeepEqual(got["rules"], []any{"MATCH,DIRECT"}) {
		t.Fatalf("node-only subscription changed rules: %#v", got["rules"])
	}
}

func TestTerminalSubscriptionCompleteRoutingAndGeodata(t *testing.T) {
	got := terminalMergedConfig(t, `mixed-port: 17890
proxy-providers: {old: {type: file, path: old.yaml}}
proxy-groups: [{name: old}]
rule-providers: {old: {type: file}}
sub-rules: {old: ["MATCH,DIRECT"]}
rules: ["MATCH,DIRECT"]
`, `proxies: [{name: new, type: direct}]
rules: ["DOMAIN,example.com,DIRECT", "MATCH,new"]
geodata-mode: true
geox-url: {geoip: https://example.com/geoip.dat}
`, "")
	for _, absent := range []string{"proxy-providers", "proxy-groups", "rule-providers"} {
		if _, ok := got[absent]; ok {
			t.Errorf("full subscription retained stale %s", absent)
		}
	}
	if got["mixed-port"] != 17890 || got["geodata-mode"] != true {
		t.Fatalf("local or geodata fields wrong: %#v", got)
	}
	if !reflect.DeepEqual(got["rules"], []any{"SUB-RULE,(OR,((NETWORK,TCP),(NETWORK,UDP))),订阅规则"}) {
		t.Fatalf("rules were not grouped: %#v", got["rules"])
	}
	groups := got["sub-rules"].(map[string]any)
	if len(groups) != 1 || !reflect.DeepEqual(groups["订阅规则"], []any{"DOMAIN,example.com,DIRECT", "MATCH,new"}) {
		t.Fatalf("wrong sub-rule group: %#v", groups)
	}
}

func TestTerminalSubscriptionUniqueRuleGroupAndOverride(t *testing.T) {
	got := terminalMergedConfig(t, `dns: {enable: true, nameserver: [1.1.1.1], fallback: [8.8.8.8]}
`, `proxies: [{name: new, type: direct}]
sub-rules:
  订阅规则: ["MATCH,DIRECT"]
  订阅规则 2: ["MATCH,REJECT"]
rules: ["MATCH,new"]
`, `dns:
  nameserver: [9.9.9.9]
prepend-rules:
  - DOMAIN,blocked.example,REJECT
  - DOMAIN,blocked.example,REJECT
  - SUB-RULE,(OR,((NETWORK,TCP),(NETWORK,UDP))),订阅规则 3
`)
	if _, ok := got["prepend-rules"]; ok {
		t.Fatal("pseudo field prepend-rules leaked into Mihomo config")
	}
	dns := got["dns"].(map[string]any)
	if dns["enable"] != true || !reflect.DeepEqual(dns["nameserver"], []any{"9.9.9.9"}) || !reflect.DeepEqual(dns["fallback"], []any{"8.8.8.8"}) {
		t.Fatalf("override did not recursively merge maps/replace lists: %#v", dns)
	}
	expected := []any{"DOMAIN,blocked.example,REJECT", "SUB-RULE,(OR,((NETWORK,TCP),(NETWORK,UDP))),订阅规则 3"}
	if !reflect.DeepEqual(got["rules"], expected) {
		t.Fatalf("prepend order/dedup wrong: %#v", got["rules"])
	}
	if !reflect.DeepEqual(got["sub-rules"].(map[string]any)["订阅规则 3"], []any{"MATCH,new"}) {
		t.Fatalf("rule group collision not handled: %#v", got["sub-rules"])
	}
}

func TestTerminalSubscriptionResolvesAnchorsAcrossReplacement(t *testing.T) {
	got := terminalMergedConfig(t, `proxies: &nodes [{name: old, type: direct}]
custom-local: *nodes
dns:
  <<: &dns {enable: true, nameserver: [1.1.1.1]}
  enable: false
`, `proxies: &new [{name: new, type: direct}]
proxy-groups:
  - name: select
    type: select
    proxies: [new]
`, "")
	if got["custom-local"].([]any)[0].(map[string]any)["name"] != "old" {
		t.Fatalf("replacing anchored field changed its former aliases: %#v", got["custom-local"])
	}
	dns := got["dns"].(map[string]any)
	if dns["enable"] != false || !reflect.DeepEqual(dns["nameserver"], []any{"1.1.1.1"}) {
		t.Fatalf("YAML merge precedence wrong: %#v", dns)
	}
}

func TestTerminalSubscriptionRejectsUnsafeInputs(t *testing.T) {
	valid := "proxies: [{name: node, type: direct}]\n"
	tests := []struct {
		name, base, incoming, overlay string
	}{
		{"duplicate local", "dns: {enable: true, enable: false}", valid, ""},
		{"duplicate subscription", "{}", "proxies: []\nproxies: []", ""},
		{"multiple documents", "{}", valid + "---\nproxies: []", ""},
		{"nonmapping", "{}", "[one, two]", ""},
		{"numeric key", "{}", "1: value", ""},
		{"nonstring rules", "{}", valid + "rules: [123]", ""},
		{"null rules", "{}", valid + "rules: null", ""},
		{"invalid groups", "{}", valid + "sub-rules: []\nrules: [MATCH,DIRECT]", ""},
		{"invalid prepend", "{}", valid, "prepend-rules: [invalid]"},
		{"nonstring prepend", "{}", valid, "prepend-rules: [123]"},
		{"circular anchor", "local: &loop [*loop]", valid, ""},
		{"oversized", "{}", strings.Repeat(" ", MaxSize+1), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if content, err := MergeSubscription(tt.base, tt.incoming, tt.overlay); err == nil {
				t.Fatalf("accepted invalid input: %s", content)
			}
		})
	}
}
