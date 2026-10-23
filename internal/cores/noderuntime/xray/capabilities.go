package xrayruntime

import (
	"context"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func (r *Runtime) Capability(ctx context.Context) (*nodev1.CoreCapability, error) {
	if err := r.Validate([]byte(`{}`)); err != nil {
		return nil, err
	}
	version, err := inventory.RunningVersion(ctx)
	if err != nil {
		return nil, err
	}
	return &nodev1.CoreCapability{
		CoreId: "xray", Version: version, Protocols: []string{"http", "hysteria", "mixed", "shadowsocks", "trojan", "vless", "vmess"},
		ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true,
	}, nil
}
