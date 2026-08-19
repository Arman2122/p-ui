/*
Package wgclient dials a WireGuard uplink and offers it as an egress.

The other end of internal/wireguard: the same engine that serves clients on an
inbound dials a provider here, and the only difference is that the peer carries
an endpoint. That is why this exists as a driver rather than a second
implementation -- a device, a key and one peer, pointed the other way.

Provisioner rather than Injector: an xray-tun front belongs to Xray and appears
when Xray does, while this device is the panel's to make. An openvpn or ikev2
uplink picks whichever of those two shapes matches who owns its device.
*/
package wgclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/Arman2122/p-ui/internal/egress"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

// Type is the value of the egress row's type column this driver serves.
const Type = "wg-client"

/*
Settings is the row's settings column: one provider's configuration, in the
shape every WireGuard provider hands out.

Deliberately the same field names a .conf file uses, so an operator pasting from
Surfshark or Mullvad recognises every one of them.
*/
type Settings struct {
	// PrivateKey is this side's key. Address is what the provider assigned us,
	// and it decides which families the uplink can carry.
	PrivateKey string   `json:"privateKey"`
	Address    []string `json:"address"`
	MTU        int      `json:"mtu"`

	// PublicKey, Endpoint and PresharedKey describe the provider's side.
	PublicKey    string `json:"publicKey"`
	Endpoint     string `json:"endpoint"`
	PresharedKey string `json:"presharedKey"`
	KeepAlive    int    `json:"keepAlive"`
}

// Driver is the wg-client egress type.
/*
Driver dials one WireGuard-family uplink, parameterised by the type it serves.

AmneziaWG dials with the same fields -- a key, an address and one peer -- so it
is this driver over a manager pointed at the other module rather than a copy.
The TYPE has to differ even though the shape does not: it is what decides which
module the device is created with, and an AmneziaWG exit dialled by the plain
WireGuard driver would connect with no obfuscation at all and say nothing.
*/
type Driver struct {
	mgr        *engine.Manager
	driverType string
	// obfuscate applies a module's own extra device parameters, if it has any.
	// Nil for plain WireGuard, which has none.
	obfuscate func(device, settings string) error
}

// New returns a driver that dials through the uplink engine. That manager owns
// its own device namespace, so an uplink id and an inbound id of the same number
// name two devices rather than fighting over one.
func New(mgr *engine.Manager) Driver { return NewTyped(Type, mgr) }

// NewTyped returns a driver serving one uplink type through its own manager.
func NewTyped(driverType string, mgr *engine.Manager) Driver {
	return Driver{mgr: mgr, driverType: driverType}
}

// WithObfuscation returns the driver with a module-specific parameter step, run
// once the device exists.
func (d Driver) WithObfuscation(apply func(device, settings string) error) Driver {
	d.obfuscate = apply
	return d
}

func (d Driver) Type() string {
	if d.driverType == "" {
		return Type
	}
	return d.driverType
}

/*
Fill names the device and, crucially, the families it may carry.

An uplink is where the packet really leaves, so a family it holds no address in
must not be routed into it: the kernel would borrow another interface's source
and the egress would silently leave from eth0 with the host's own identity.
*/
func (d Driver) Fill(e egress.Egress) (egress.Fill, error) {
	settings, err := parse(e)
	if err != nil {
		return egress.Fill{}, err
	}
	families, err := familiesOf(settings.Address)
	if err != nil {
		return egress.Fill{}, err
	}
	// Named by the engine that creates it, not by a second derivation of the same
	// rule: a device routed to under one name and created under another is inert.
	device := d.mgr.Name(e.ID)
	return egress.Fill{
		Device:   device,
		Families: families,
		// Marked, because what reaches an uplink is a socket this host originated:
		// a core's own outbound, which carries a mark and has no ingress device.
		Marked: true,
		// The return packet arrives on the uplink from an address the main table
		// routes elsewhere, which strict reverse-path filtering drops.
		//
		// 2 (loose), not 0: the kernel applies max(conf.all, conf.<dev>), so on a
		// host hardened to all=1 a 0 here is overridden and the drops persist.
		Sysctls: map[string]string{
			"net.ipv4.conf." + device + ".rp_filter": "2",
		},
	}, nil
}

// Provision brings the device up and points it at the provider. Idempotent: the
// engine reconciles, so a device already correct is left alone.
func (d Driver) Provision(ctx context.Context, e egress.Egress) error {
	settings, err := parse(e)
	if err != nil {
		return err
	}
	if settings.Endpoint == "" {
		return fmt.Errorf("wgclient: egress %d names no endpoint to dial", e.ID)
	}
	inst := engine.Instance{
		ID:         e.ID,
		Tag:        d.mgr.Name(e.ID),
		PrivateKey: strings.TrimSpace(settings.PrivateKey),
		Address:    settings.Address,
		MTU:        settings.MTU,
		Peers: []engine.Peer{{
			Email:        "uplink",
			PublicKey:    strings.TrimSpace(settings.PublicKey),
			PreSharedKey: strings.TrimSpace(settings.PresharedKey),
			// Everything, because the table this device sits in decides what is sent
			// here; narrowing it a second time would only lose packets.
			AllowedIPs: []string{"0.0.0.0/0", "::/0"},
			KeepAlive:  keepAliveOr(settings.KeepAlive),
			Endpoint:   strings.TrimSpace(settings.Endpoint),
		}},
	}
	if err := d.mgr.Ensure(ctx, inst); err != nil {
		return err
	}
	// The obfuscation, for a driver whose module has any. A provider tunnel is
	// exactly where it is needed -- an uplink dialled without the parameters the
	// far end expects completes no handshake at all -- and applying it after the
	// device exists leaves the peer untouched.
	if d.obfuscate != nil {
		return d.obfuscate(d.mgr.Name(e.ID), string(e.Settings))
	}
	return nil
}

// Deprovision takes the device down. The row may already be gone, so this works
// from the id alone.
func (d Driver) Deprovision(ctx context.Context, id int) error {
	return d.mgr.Remove(ctx, id)
}

// keepAliveOr keeps the tunnel open through a NAT the panel does not control.
// Every provider asks for one; 25s is the value they all suggest.
func keepAliveOr(value int) int {
	if value <= 0 {
		return 25
	}
	return value
}

func parse(e egress.Egress) (Settings, error) {
	var settings Settings
	if len(e.Settings) == 0 {
		return Settings{}, fmt.Errorf("wgclient: egress %d has no settings", e.ID)
	}
	if err := json.Unmarshal(e.Settings, &settings); err != nil {
		return Settings{}, fmt.Errorf("wgclient: egress %d settings are unreadable: %w", e.ID, err)
	}
	return settings, nil
}

// familiesOf reads the families out of the addresses the provider assigned. An
// uplink with no address at all carries nothing, which is a refusal rather than
// a device that silently sources from somewhere else.
func familiesOf(addresses []string) ([]egress.Family, error) {
	var families []egress.Family
	for _, raw := range addresses {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("wgclient: %q is not an address this uplink can hold: %w", raw, err)
		}
		family := egress.FamilyV4
		if prefix.Addr().Is6() {
			family = egress.FamilyV6
		}
		if !containsFamily(families, family) {
			families = append(families, family)
		}
	}
	if len(families) == 0 {
		return nil, fmt.Errorf("wgclient: an uplink with no address carries no family")
	}
	return families, nil
}

func containsFamily(families []egress.Family, want egress.Family) bool {
	for _, family := range families {
		if family == want {
			return true
		}
	}
	return false
}

// Compile-time proof that this driver answers the two halves it claims.
var (
	_ egress.Driver      = Driver{}
	_ egress.Provisioner = Driver{}
)
