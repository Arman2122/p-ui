package mtproto

import (
	"context"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func (m *Managed) Capability(ctx context.Context) (*nodev1.CoreCapability, error) {
	if err := m.Validate([]byte(`{}`)); err != nil {
		return nil, err
	}
	version, err := inventory.RunningVersion(ctx)
	if err != nil {
		return nil, err
	}
	return &nodev1.CoreCapability{
		CoreId: "mtproto", Version: version, Protocols: []string{"mtproto"},
		ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true,
	}, nil
}
