package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
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
