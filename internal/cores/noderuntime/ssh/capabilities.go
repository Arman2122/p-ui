package sshruntime

import (
	"context"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func (*Runtime) Capability(ctx context.Context) (*nodev1.CoreCapability, error) {
	version, err := inventory.RunningVersion(ctx)
	if err != nil {
		return nil, err
	}
	return &nodev1.CoreCapability{
		CoreId: "ssh", Version: version, Protocols: []string{"ssh"},
		ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true,
	}, nil
}
