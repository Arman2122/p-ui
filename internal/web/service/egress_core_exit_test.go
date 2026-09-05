package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// socksExitCore terminates a route behind a loopback SOCKS port and runs its
// own exits: the shape the SSH core has, with no egress driver anywhere.
type socksExitCore struct {
	stubCore
	refuse   error
	received []core.Exit
}

func (c *socksExitCore) ExitKinds() []core.Kind { return c.kinds }
func (c *socksExitCore) ExitHandleKind(kind core.Kind) core.ExitHandleKind {
	if kind == c.kinds[0] {
		return core.ExitSocksPort
	}
	return core.ExitNone
}

func (c *socksExitCore) ExitHandle(_ context.Context, exit core.Exit) (core.ExitHandle, error) {
	return core.ExitHandle{SocksPort: core.ExitSocksPortFor(exit.ID), TCPOnly: true}, nil
}
func (c *socksExitCore) ValidateExit(core.Exit) error { return c.refuse }
func (c *socksExitCore) ReconcileExits(_ context.Context, desired []core.Exit) error {
	c.received = desired
	return nil
}

func useSocksExitCore(t *testing.T) *socksExitCore {
	t.Helper()
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := &socksExitCore{stubCore: stubCore{descriptor: core.Descriptor{ID: "socksy", TitleKey: "cores.socksy.title"}, kinds: []core.Kind{"socksy"}}}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cores.Use(reg)
	// reconcileCoreExits reads the registry through the runtime manager, the way
	// the policy passes do, so the stub has to be reachable there too.
	previous := runtime.GetManager()
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: reg}))
	t.Cleanup(func() { runtime.SetManager(previous) })
	return stub
}

/*
An exit that is a SOCKS port has no egress driver: nothing to fill, no device,
no table. The service used to refuse any type without a driver, feed every row
to the plane, and map types to kinds by a switch. A core that answers
ExitSocksPort is now accepted under its kind, validated by the core, kept away
from the plane, and handed its rows to run. The SSH exit is the first.
*/
func TestACoreOwnedSocksExitBypassesThePlane(t *testing.T) {
	stub := useSocksExitCore(t)
	row := &model.Egress{Id: 7, Type: "socksy", Enable: true, Owner: model.EgressOwnerOperator, Settings: `{"host":"x"}`}

	if got := exitKindFor(row.Type); got != "socksy" {
		t.Fatalf("exitKindFor(%q) = %q, want the kind the registry claims", row.Type, got)
	}
	svc := &EgressService{}
	if err := svc.validate(row); err != nil {
		t.Fatalf("validate accepted nothing: %v", err)
	}
	stub.refuse = errors.New("a fingerprint is required")
	if err := svc.validate(row); err == nil || !strings.Contains(err.Error(), "fingerprint is required") {
		t.Fatalf("validate did not hand the core's refusal back: %v", err)
	}
	stub.refuse = nil
	if err := svc.validate(&model.Egress{Type: "ftp", Enable: true}); !errors.Is(err, ErrEgressUnknownType) {
		t.Fatalf("an unknown type is still refused: got %v", err)
	}

	desired, err := svc.desired(context.Background(), []*model.Egress{row})
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(desired) != 0 {
		t.Fatalf("desired handed the plane %+v; a SOCKS exit has nothing for it", desired)
	}

	disabled := &model.Egress{Id: 8, Type: "socksy", Enable: false, Owner: model.EgressOwnerOperator}
	front := &model.Egress{Id: 9, Type: "socksy", Enable: true, Owner: model.EgressOwnerPanel}
	if err := reconcileCoreExits(context.Background(), []*model.Egress{row, disabled, front}); err != nil {
		t.Fatalf("reconcileCoreExits: %v", err)
	}
	if len(stub.received) != 1 || stub.received[0].ID != 7 || stub.received[0].Kind != "socksy" || stub.received[0].Settings != row.Settings {
		t.Fatalf("the core received %+v, want only the enabled operator row 7", stub.received)
	}
}

// The band the exits listen in is loopback TCP on the local node; an inbound
// there would fight the exit for the port, or be shadowed by it.
func TestInboundPortConflictRefusesTheExitSocksBand(t *testing.T) {
	initTestDB(t)
	svc := &InboundService{}
	hit, err := svc.checkPortConflict(&model.Inbound{Protocol: model.VLESS, Port: core.ExitSocksPortFor(7), Listen: "0.0.0.0", Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp"}`}, 0)
	if err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit == nil || hit.Tag != "exit-socks" {
		t.Fatalf("an inbound on port %d was accepted; hit = %+v", core.ExitSocksPortFor(7), hit)
	}
	clear, err := svc.checkPortConflict(&model.Inbound{Protocol: model.VLESS, Port: 42999, Listen: "0.0.0.0", Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp"}`}, 0)
	if err != nil || clear != nil {
		t.Fatalf("a port outside the band was refused: %+v, %v", clear, err)
	}
}

/*
An exit whose core runs a client daemon that makes its own tun (the OpenVPN
exit is the first) is the opposite of the SOCKS one: it has no port to dial,
so the plane must route into the device -- which takes a driver the service
registers for it -- while the core still validates and runs the row. A
device-kind exit with no driver would be accepted, run, and never routed into.
*/
func TestACoreOwnedDeviceExitIsDrivenByThePlane(t *testing.T) {
	driver, ok := egressDriverRegistry.For("openvpn")
	if !ok {
		t.Fatal("no plane driver serves the openvpn exit: nothing would ever route into its device")
	}
	fill, err := driver.Fill(egress.Egress{ID: 3, Type: "openvpn", Enable: true})
	if err != nil || fill.Device != "pvx3" || !fill.Marked {
		t.Fatalf("fill = %+v, %v; want the core's own device, marked", fill, err)
	}
	svc := &EgressService{}
	serverProfile := &model.Egress{
		Id: 3, Type: "openvpn", Enable: true, Owner: model.EgressOwnerOperator,
		Settings: `{"profile":"server 10.8.0.0 255.255.255.0\nremote x 1\n"}`,
	}
	if err := svc.validate(serverProfile); err == nil || !strings.Contains(err.Error(), "server profile") {
		t.Fatalf("validate did not hand the core's refusal back: %v", err)
	}
	if err := svc.validate(&model.Egress{Id: 3, Type: "openvpn", Enable: false, Owner: model.EgressOwnerOperator}); err != nil {
		t.Fatalf("a disabled row was checked against the host: %v", err)
	}
	desired, err := svc.desired(context.Background(), []*model.Egress{serverProfile})
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(desired) != 1 || desired[0].Type != "openvpn" {
		t.Fatalf("desired handed the plane %+v; a device exit is the plane's to route into", desired)
	}
}
