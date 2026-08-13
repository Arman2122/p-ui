package egress_test

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

/*
fakeUplink stands in for a driver that MAKES its device -- wg-client today,
openvpn or ikev2 later. It records what it was asked to do, because the failure
this file is about is a call that never happens.
*/
type fakeUplink struct {
	kernel        *egtest.Kernel
	provisioned   []int
	deprovisioned []int
}

func (f *fakeUplink) Type() string { return "fake-uplink" }

func (f *fakeUplink) Fill(e egress.Egress) (egress.Fill, error) {
	return egress.Fill{Device: egress.Uplink(e.ID), Marked: true}, nil
}

func (f *fakeUplink) Provision(_ context.Context, e egress.Egress) error {
	f.provisioned = append(f.provisioned, e.ID)
	f.kernel.AddLink(egress.Uplink(e.ID))
	return nil
}

func (f *fakeUplink) Deprovision(_ context.Context, id int) error {
	f.deprovisioned = append(f.deprovisioned, id)
	f.kernel.DelLink(egress.Uplink(id))
	return nil
}

func uplinkRow(id int) egress.Egress {
	return egress.Egress{ID: id, Type: "fake-uplink", Enable: true}
}

/*
Deleting the row must take the device down.

A deleted row is synthesised from what the kernel still holds, so it carries no
type and the manager cannot look its driver up. Left unhandled, the rule and the
table are reaped while the device keeps dialling the provider: the operator sees
the egress gone and the host is still connected to it.
*/
func TestDeletedUplinkStopsDialling(t *testing.T) {
	kernel := egtest.New()
	driver := &fakeUplink{kernel: kernel}
	manager := newManager(t, kernel, driver)
	ctx := context.Background()

	if err := manager.Reconcile(ctx, []egress.Egress{uplinkRow(4)}); err != nil {
		t.Fatalf("reconcile with the row: %v", err)
	}
	if len(driver.provisioned) == 0 {
		t.Fatal("the row was never provisioned, so this proves nothing about deleting it")
	}

	if err := manager.Reconcile(ctx, nil); err != nil {
		t.Fatalf("reconcile without the row: %v", err)
	}

	if len(driver.deprovisioned) == 0 {
		t.Fatal("the row is gone and the device was never taken down: it is still dialling the provider")
	}
	snap, err := kernel.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, link := range snap.Links {
		if link == egress.Uplink(4) {
			t.Fatalf("%s survived its row", egress.Uplink(4))
		}
	}
}

/*
A device whose rule and table were already reaped is still the panel's to clean
up. The reserved band cannot find it -- nothing is left in the band -- so the
device namespace is what identifies it.
*/
func TestOrphanedUplinkDeviceIsReaped(t *testing.T) {
	kernel := egtest.New()
	driver := &fakeUplink{kernel: kernel}
	manager := newManager(t, kernel, driver)

	// The state left by a crash between taking the route out and the device.
	kernel.AddLink(egress.Uplink(7))

	if err := manager.Reconcile(context.Background(), nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(driver.deprovisioned) == 0 {
		t.Fatalf("%s is in the panel's own namespace and no row wants it, so it must be taken down",
			egress.Uplink(7))
	}
}

// A row merely disabled is taken down too, and by its own driver: the operator
// switched it off, and a device that keeps dialling is not off.
func TestDisabledUplinkStopsDialling(t *testing.T) {
	kernel := egtest.New()
	driver := &fakeUplink{kernel: kernel}
	manager := newManager(t, kernel, driver)
	ctx := context.Background()

	if err := manager.Reconcile(ctx, []egress.Egress{uplinkRow(4)}); err != nil {
		t.Fatalf("reconcile enabled: %v", err)
	}
	off := uplinkRow(4)
	off.Enable = false
	if err := manager.Reconcile(ctx, []egress.Egress{off}); err != nil {
		t.Fatalf("reconcile disabled: %v", err)
	}

	if len(driver.deprovisioned) == 0 {
		t.Fatal("a disabled uplink must stop dialling")
	}
}
