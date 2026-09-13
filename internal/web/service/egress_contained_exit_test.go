package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
Exits answered from the row, never from the kernel: an uplink whose ip rule
failed to land, or whose device was gone, was still handed to the planner as a
marked exit, and the marked sockets left via main. The service now asks the
host; a change in that answer after a reconcile arms the Xray restart, so the
config never stays compiled from a stale one.
*/
func TestAnExitTheHostDoesNotRouteIsContainedUntilItComesBack(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	// The kernel is the subject; the wireguard core's own exit driving is not,
	// and on a root box it would put a real device on the host.
	previous := runtime.GetManager()
	runtime.SetManager(nil)
	t.Cleanup(func() { runtime.SetManager(previous) })
	service := &EgressService{}
	xray := &XrayService{}

	// A core-owned device exit: the plane routes into a device the daemon
	// brings up, so the kernel alone decides whether the mark is caught.
	uplink := seedEgress(t, &model.Egress{
		Type: "openvpn", Enable: true, Owner: "operator",
		Settings: "{\"profile\":\"client\nremote 198.51.100.7 1194\n\"}",
	})
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	xray.IsNeedRestartAndSetFalse()
	exits, err := (&RoutingService{}).Exits(context.Background())
	if err != nil || len(exits) != 1 {
		t.Fatalf("Exits = %+v, %v; want the uplink", exits, err)
	}
	if !exits[0].Contained {
		t.Fatal("a device the kernel does not hold was handed to the planner as routed")
	}

	driver, _ := egressDriverRegistry.For("openvpn")
	fill, err := driver.Fill(egress.Egress{ID: uplink.Id, Type: "openvpn", Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	device := fill.Device
	kernel.AddLink(device, "net.ipv4.conf."+device+".rp_filter")
	kernel.AddAddr(device, mustEgressGateway(t, uplink.Id))
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile with the device up: %v", err)
	}
	exits, _ = (&RoutingService{}).Exits(context.Background())
	if len(exits) != 1 || exits[0].Contained {
		t.Fatalf("Exits = %+v; the routed uplink must be offered as routed", exits)
	}
	if !xray.IsNeedRestartAndSetFalse() {
		t.Fatal("the exit became routed and nothing asked Xray to rebuild the config that contained it")
	}
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile again: %v", err)
	}
	if xray.IsNeedRestartAndSetFalse() {
		t.Fatal("an unchanged host asked for an Xray restart on every pass")
	}

	kernel.DelLink(device)
	_ = service.Reconcile(context.Background())
	exits, _ = (&RoutingService{}).Exits(context.Background())
	if len(exits) != 1 || !exits[0].Contained {
		t.Fatalf("Exits = %+v; a device that vanished must contain its exit", exits)
	}
	if !xray.IsNeedRestartAndSetFalse() {
		t.Fatal("the exit was lost and the config still carries its marked outbound")
	}
}
