package egress

import (
	"context"
	"errors"
)

// The one probe object. Its priority is the band base, which no id derives, and
// its device is a name no core creates, so it collides with nothing and is
// detached for the microseconds it exists.
const (
	probePriority = prioBase
	probeDevice   = "pui-probe0"
)

// ProbeRule is the reversible write probeHost issues. Exported so an e2e test can
// prove the host is left exactly as it was found.
func ProbeRule() RuleSpec {
	return RuleSpec{Family: FamilyV4, Priority: probePriority, Iif: probeDevice, Table: tableBase}
}

/*
probeHost asks the plane for its read probe and then proves the write path.

Listing rules is an unprivileged read, so it says nothing about writing: a
capability-restricted unit passes it and then answers EPERM to every object the
manager installs. Only a write the probe can undo separates the two, which is
what lets Preflight name CAP_NET_ADMIN instead of leaving the operator with an
unrouted attach and no remedy.
*/
func (m *Manager) probeHost(ctx context.Context) error {
	if err := m.plane.Probe(ctx); err != nil {
		return err
	}
	spec := ProbeRule()
	if err := m.plane.AddRule(ctx, spec); err != nil {
		// EEXIST is the kernel answering after its own capability check, so the
		// write path works; a rule the panel did not add is not its to delete.
		if errors.Is(err, ErrAlreadyInstalled) {
			return nil
		}
		return err
	}
	return m.plane.DelRule(ctx, spec)
}
