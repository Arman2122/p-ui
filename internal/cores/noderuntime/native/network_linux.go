//go:build linux

package nativeruntime

import (
	"context"

	"github.com/Arman2122/p-ui/internal/node/native"
)

func (r *Runtime) validateNetwork(target *native.NetworkBinding) error {
	if target == nil {
		return nil
	}
	state, err := r.journal.Network()
	if err != nil {
		return err
	}
	if state.Pending != nil && *state.Pending != *target {
		return native.ErrBusy
	}
	protected, err := r.plane.managementAddresses()
	if err != nil {
		return err
	}
	if protected[target.PeerIPv4] || protected[target.GatewayIPv4] {
		return native.ErrOwnership
	}
	return r.plane.gate.CheckNetwork(*target)
}

func (r *Runtime) applyNetwork(ctx context.Context, target *native.NetworkBinding) error {
	if target == nil {
		return nil
	}
	state, err := r.journal.Network()
	if err != nil {
		return err
	}
	if r.initialized && state.Pending == nil && state.Current != nil && *state.Current == *target {
		return nil
	}
	state, err = r.journal.StageNetwork(*target)
	if err != nil {
		return err
	}
	r.initialized = false
	if err := r.plane.gate.ConfigureNetwork(state); err != nil {
		return err
	}
	if err := r.plane.gate.Initialize(ctx); err != nil {
		return err
	}
	if err := r.control.Recover(ctx); err != nil {
		return err
	}
	if err := r.journal.CommitNetwork(*target); err != nil {
		return err
	}
	if err := r.plane.gate.ConfigureNetwork(native.NetworkState{Current: target}); err != nil {
		return err
	}
	r.initialized = true
	return nil
}
