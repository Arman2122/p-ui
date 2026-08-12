package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/json_util"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestEgressIngressDevice(t *testing.T) {
	cases := []struct {
		name     string
		inbound  *model.Inbound
		want     string
		resolves bool
	}{
		{"kernel wireguard is the one L3 ingress today", &model.Inbound{Id: 7, Protocol: model.WGKernel}, "pwg7", true},
		{"xray's userspace wireguard has no device of its own", &model.Inbound{Id: 7, Protocol: model.WireGuard}, "", false},
		{"a stream protocol has no ingress device", &model.Inbound{Id: 7, Protocol: model.VLESS}, "", false},
		{"no inbound, no device", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := egressIngressDevice(context.Background(), tc.inbound)
			if ok != tc.resolves || got != tc.want {
				t.Fatalf("egressIngressDevice = %q,%v; want %q,%v", got, ok, tc.want, tc.resolves)
			}
		})
	}
}

func egressRows(rows ...*model.Egress) []*model.Egress { return rows }

func enabledEgress(id int, target string) *model.Egress {
	return &model.Egress{Id: id, Type: "xray-tun", Enable: true, Target: target}
}

// frontRules is the routing section as the front injection leaves it.
type frontRules struct {
	DomainStrategy string `json:"domainStrategy"`
	Rules          []struct {
		Type        string   `json:"type"`
		InboundTag  []string `json:"inboundTag"`
		OutboundTag string   `json:"outboundTag"`
		BalancerTag string   `json:"balancerTag"`
	} `json:"rules"`
}

func parseFrontRules(t *testing.T, cfg *xray.Config) frontRules {
	t.Helper()
	var parsed frontRules
	if err := json.Unmarshal(cfg.RouterConfig, &parsed); err != nil {
		t.Fatalf("routing section is unparsable after injection: %v", err)
	}
	return parsed
}

func TestInjectEgressFronts(t *testing.T) {
	cfg := egressTestConfig()
	injectEgressFronts(cfg, egressRows(enabledEgress(1, "warp")), egressDriverRegistry)

	if len(cfg.InboundConfigs) != 2 {
		t.Fatalf("expected the front to be appended, got %d inbounds", len(cfg.InboundConfigs))
	}
	front := cfg.InboundConfigs[1]
	if front.Tag != "peg1" || front.Protocol != "tun" || front.Port != 0 {
		t.Fatalf("unexpected front inbound: %+v", front)
	}
	if string(front.Listen) != `"127.0.0.1"` {
		t.Fatalf("the front must not listen off-box, got %s", front.Listen)
	}
	// The device name, its own /32 and the MTU gVisor reads once, exactly as the
	// shape proven on the box; autoSystemRoutingTable would bind every outbound.
	want := `{"name":"peg1","mtu":1420,"gateway":["100.127.0.1/32"]}`
	if string(front.Settings) != want {
		t.Fatalf("front settings = %s, want %s", front.Settings, want)
	}

	routing := parseFrontRules(t, cfg)
	if routing.DomainStrategy != "AsIs" {
		t.Fatalf("routing keys outside rules must survive, got %+v", routing)
	}
	if len(routing.Rules) != 2 {
		t.Fatalf("expected the front rule prepended to the existing one, got %+v", routing.Rules)
	}
	first := routing.Rules[0]
	if first.Type != "field" || first.OutboundTag != "warp" ||
		len(first.InboundTag) != 1 || first.InboundTag[0] != "peg1" {
		t.Fatalf("the front rule must select the front's tag, got %+v", first)
	}
}

func TestInjectEgressFrontsSkips(t *testing.T) {
	cases := []struct {
		name string
		rows []*model.Egress
		set  func(cfg *xray.Config)
	}{
		{
			name: "an unresolvable target leaves the egress dark rather than direct",
			rows: egressRows(enabledEgress(1, "gone")),
		},
		{
			name: "a disabled row has no front at all",
			rows: egressRows(&model.Egress{Id: 1, Type: "xray-tun", Target: "warp"}),
		},
		{
			name: "a type this build does not serve is skipped, not guessed at",
			rows: egressRows(&model.Egress{Id: 1, Type: "ikev2", Enable: true, Target: "warp"}),
		},
		{
			name: "an inbound already owning the front's tag wins",
			rows: egressRows(enabledEgress(1, "warp")),
			set: func(cfg *xray.Config) {
				cfg.InboundConfigs = append(cfg.InboundConfigs,
					xray.InboundConfig{Port: 1234, Protocol: "vless", Tag: "peg1"})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := egressTestConfig()
			if tc.set != nil {
				tc.set(cfg)
			}
			inbounds := len(cfg.InboundConfigs)
			routing := string(cfg.RouterConfig)

			injectEgressFronts(cfg, tc.rows, egressDriverRegistry)

			if len(cfg.InboundConfigs) != inbounds {
				t.Fatalf("no front may be injected, got %d inbounds (was %d)", len(cfg.InboundConfigs), inbounds)
			}
			if string(cfg.RouterConfig) != routing {
				t.Fatalf("the routing section must be left byte-identical, got %s", cfg.RouterConfig)
			}
		})
	}
}

func TestInjectEgressFrontsBalancerTarget(t *testing.T) {
	cfg := egressTestConfig()
	cfg.RouterConfig = json_util.RawMessage(`{"rules":[],"balancers":[{"tag":"lb","selector":["warp"]}]}`)

	injectEgressFronts(cfg, egressRows(enabledEgress(2, "lb")), egressDriverRegistry)

	routing := parseFrontRules(t, cfg)
	if len(routing.Rules) != 1 {
		t.Fatalf("expected the front rule, got %+v", routing.Rules)
	}
	if routing.Rules[0].BalancerTag != "lb" || routing.Rules[0].OutboundTag != "" {
		t.Fatalf("a balancer target must be reached through balancerTag, got %+v", routing.Rules[0])
	}
}

// Two egresses in one process is the case the whole schema assumes works: each
// front has its own device, its own /32 and its own rule.
func TestInjectEgressFrontsAreDistinctAndOrdered(t *testing.T) {
	cfg := egressTestConfig()
	injectEgressFronts(cfg, egressRows(enabledEgress(1, "warp"), enabledEgress(2, "direct")), egressDriverRegistry)

	if len(cfg.InboundConfigs) != 3 {
		t.Fatalf("expected both fronts, got %d inbounds", len(cfg.InboundConfigs))
	}
	if cfg.InboundConfigs[1].Tag != "peg1" || cfg.InboundConfigs[2].Tag != "peg2" {
		t.Fatalf("fronts must be emitted in id order, got %q then %q",
			cfg.InboundConfigs[1].Tag, cfg.InboundConfigs[2].Tag)
	}
	if string(cfg.InboundConfigs[1].Settings) == string(cfg.InboundConfigs[2].Settings) {
		t.Fatalf("two fronts must not share a gateway /32, both are %s", cfg.InboundConfigs[1].Settings)
	}

	routing := parseFrontRules(t, cfg)
	if len(routing.Rules) != 3 {
		t.Fatalf("expected one rule per front plus the existing one, got %+v", routing.Rules)
	}
	if routing.Rules[0].InboundTag[0] != "peg1" || routing.Rules[1].InboundTag[0] != "peg2" {
		t.Fatalf("front rules must be prepended in id order, got %+v", routing.Rules)
	}
}

// diffRouting replaces the section wholesale and Config.Equals is byte-exact, so
// an injection that reordered anything would restart the core on every build.
func TestInjectEgressFrontsIsByteStable(t *testing.T) {
	rows := egressRows(enabledEgress(1, "warp"), enabledEgress(2, "direct"))

	first := egressTestConfig()
	injectEgressFronts(first, rows, egressDriverRegistry)
	second := egressTestConfig()
	injectEgressFronts(second, rows, egressDriverRegistry)

	if !first.Equals(second) {
		t.Fatalf("two builds from the same rows differ:\n%s\n%s", first.RouterConfig, second.RouterConfig)
	}
}

func realityInbound() xray.InboundConfig {
	return xray.InboundConfig{
		Port: 443, Protocol: "vless", Tag: "in-443",
		Listen:         json_util.RawMessage(`"0.0.0.0"`),
		Settings:       json_util.RawMessage(`{"clients":[{"email":"a@b","id":"11111111-1111-1111-1111-111111111111"}],"decryption":"none"}`),
		StreamSettings: json_util.RawMessage(`{"network":"tcp","security":"reality","realitySettings":{"dest":"example.com:443"}}`),
	}
}

/*
Adding or dropping a front must stay hot-appliable.

The REALITY variant is the one that matters: hot_diff turns any change to a
REALITY inbound into a full process restart, so a selection rendered into an
inbound's settings instead of its own row would drop every connection on the box
each time an egress was edited.
*/
func TestInjectEgressFrontsStayHotAppliable(t *testing.T) {
	cases := []struct {
		name  string
		build func() *xray.Config
	}{
		{"a plain config", egressTestConfig},
		{"a config carrying a REALITY inbound", func() *xray.Config {
			cfg := egressTestConfig()
			cfg.InboundConfigs = append(cfg.InboundConfigs, realityInbound())
			return cfg
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			without := tc.build()
			with := tc.build()
			injectEgressFronts(with, egressRows(enabledEgress(1, "warp")), egressDriverRegistry)

			diff, ok := xray.ComputeHotDiff(without, with)
			if !ok {
				t.Fatal("adding a front must be applicable through the core API, not by restarting it")
			}
			if len(diff.AddedInbounds) != 1 || len(diff.RemovedInboundTags) != 0 {
				t.Fatalf("adding a front must add exactly the front: %+v", diff)
			}
			if !strings.Contains(string(diff.AddedInbounds[0]), `"tag":"peg1"`) {
				t.Fatalf("the added inbound must be the front, got %s", diff.AddedInbounds[0])
			}
			if diff.RoutingConfig == nil {
				t.Fatal("the front's routing rule must travel with the diff")
			}

			back, ok := xray.ComputeHotDiff(with, without)
			if !ok {
				t.Fatal("dropping a front must be applicable through the core API too")
			}
			if len(back.RemovedInboundTags) != 1 || back.RemovedInboundTags[0] != "peg1" {
				t.Fatalf("dropping a front must remove exactly the front: %+v", back)
			}
		})
	}
}

// shippedTemplate is the default template the panel actually installs, not a
// fixture: the ordering this pins only matters against the rules it really ships.
func shippedTemplate(t *testing.T) *xray.Config {
	t.Helper()
	cfg := &xray.Config{}
	if err := json.Unmarshal([]byte(xrayTemplateConfig), cfg); err != nil {
		t.Fatalf("the shipped template is unparsable: %v", err)
	}
	return cfg
}

// blockRules is the routing section with the fields the template's own block
// rules carry, so a rule's position relative to them can be asserted.
type blockRules struct {
	Rules []struct {
		InboundTag  []string `json:"inboundTag"`
		IP          []string `json:"ip"`
		Protocol    []string `json:"protocol"`
		OutboundTag string   `json:"outboundTag"`
	} `json:"rules"`
}

/*
Xray takes the FIRST matching rule and the front's rule is prepended, so without
a companion rule ahead of it an egress-attached L3 inbound is the one class of
traffic Xray forwards that is exempt from the template's own geoip:private block
— and it is the class whose destination is a raw client-chosen IP the panel's
outbound then dials from the host: 169.254.169.254, the provider's management
LAN, RFC1918.
*/
func TestAnEgressFrontDoesNotOutrankTheTemplatesPrivateBlock(t *testing.T) {
	cfg := shippedTemplate(t)
	injectEgressFronts(cfg, egressRows(enabledEgress(1, "direct")), egressDriverRegistry)

	var routing blockRules
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatalf("routing section is unparsable after injection: %v", err)
	}
	if len(routing.Rules) < 2 {
		t.Fatalf("rules = %+v, want the front's pair prepended to the template's own", routing.Rules)
	}
	guard, front := routing.Rules[0], routing.Rules[1]
	if len(guard.InboundTag) != 1 || guard.InboundTag[0] != "peg1" ||
		len(guard.IP) != 1 || guard.IP[0] != "geoip:private" || guard.OutboundTag != "blocked" {
		t.Fatalf("rule 0 = %+v, want peg1's private destinations sent to the blackhole", guard)
	}
	if len(front.InboundTag) != 1 || front.InboundTag[0] != "peg1" || front.OutboundTag != "direct" {
		t.Fatalf("rule 1 = %+v, want the front's own rule immediately behind its guard", front)
	}
	// And the template's own rules still follow, in their original order.
	if got := routing.Rules[2].InboundTag; len(got) != 1 || got[0] != "api" {
		t.Fatalf("rule 2 = %+v, want the template's api rule", routing.Rules[2])
	}
}

// A template with nothing to send blocked traffic to gets no guard rule rather
// than a rule naming an outbound that does not exist, which Xray refuses to load.
func TestNoPrivateGuardWithoutABlackholeOutbound(t *testing.T) {
	cfg := egressTestConfig()
	injectEgressFronts(cfg, egressRows(enabledEgress(1, "warp")), egressDriverRegistry)

	routing := parseFrontRules(t, cfg)
	if len(routing.Rules) != 2 {
		t.Fatalf("rules = %+v, want the front rule and the template's own alone", routing.Rules)
	}
	if routing.Rules[0].OutboundTag != "warp" {
		t.Fatalf("rule 0 = %+v, want the front's own rule", routing.Rules[0])
	}
}
