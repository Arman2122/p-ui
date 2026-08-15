package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
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
