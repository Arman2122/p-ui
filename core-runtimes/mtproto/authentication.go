package mtproto

import (
	"context"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

func (m *Managed) InvalidateAuthentication(ctx context.Context, subjects []admission.Subject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	byInbound := make(map[string][]string)
	for _, subject := range subjects {
		byInbound[subject.InboundTag] = append(byInbound[subject.InboundTag], subject.ClientID)
	}
	for inbound, clients := range byInbound {
		if process := m.instances[inbound]; process != nil {
			process.proxy.InvalidateAuthentication(clients)
		}
	}
	return nil
}
