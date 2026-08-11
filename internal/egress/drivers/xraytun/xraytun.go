/*
Package xraytun fronts an egress with a TUN device Xray itself creates.

The panel owns the ip rule, the blackhole and the one route into the device;
Xray owns the device. That split is what makes failure closed: a TUN fd carries
no TUNSETPERSIST, so the device dies with the process and the kernel purges the
only route out of the table, leaving the blackhole and nothing on the wire.
*/
package xraytun

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/Arman2122/p-ui/internal/egress"
)

// Type is the value of the egress row's type column this driver serves.
const Type = "xray-tun"

/*
MTU is a constant for v1 and deliberately not the attached inbound's.

gVisor reads the MTU once, when the device is made, and the front's lifecycle
follows the egress row rather than any attachment — so at creation time "the
inbound's MTU" has no referent, and a later LinkSetMTU would not update the MSS
the front advertises anyway. A per-egress override belongs in settings later.
*/
const MTU = 1420

// Driver is the xray-tun egress type. GatewayBase carves the front's own /32,
// which the return path needs: an addressless tun fails reverse-path filtering.
type Driver struct {
	GatewayBase netip.Prefix
}

// New returns the driver on the default RFC 6598 base.
func New() Driver { return Driver{GatewayBase: egress.DefaultGatewayBase} }

func (Driver) Type() string { return Type }

// Fill names the device Xray will create and the one knob its return path needs.
// Reverse-path filtering is v4-only — /proc has no ipv6 conf.<dev>.rp_filter.
func (d Driver) Fill(e egress.Egress) (egress.Fill, error) {
	device, err := device(e.ID)
	if err != nil {
		return egress.Fill{}, err
	}
	return egress.Fill{
		Device:  device,
		Sysctls: map[string]string{"net.ipv4.conf." + device + ".rp_filter": "0"},
	}, nil
}

// tunInbound is the generated inbound, field for field as it was proven on the
// box. The struct is the schema: it cannot grow a key the driver never meant.
type tunInbound struct {
	Listen   string      `json:"listen"`
	Port     int         `json:"port"`
	Protocol string      `json:"protocol"`
	Tag      string      `json:"tag"`
	Settings tunSettings `json:"settings"`
}

/*
tunSettings deliberately has no autoSystemRoutingTable and no
autoOutboundsInterface, and must never gain them.

TunConfig.Build defaults autoOutboundsInterface to "auto" whenever
autoSystemRoutingTable is non-empty, which installs a process-global dialer
controller binding EVERY Xray outbound to the tun — a total outage plus a
routing loop. The panel installs the one route it needs itself.
*/
type tunSettings struct {
	Name    string   `json:"name"`
	MTU     int      `json:"mtu"`
	Gateway []string `json:"gateway"`
}

// Inject builds the front Xray adds to its generated config. The tag matches the
// device and no inbound row, so the core's counters for it are discarded.
func (d Driver) Inject(e egress.Egress) (egress.Injection, error) {
	device, err := device(e.ID)
	if err != nil {
		return egress.Injection{}, err
	}
	base := d.GatewayBase
	if !base.IsValid() {
		base = egress.DefaultGatewayBase
	}
	gateway, err := egress.Gateway(base, e.ID)
	if err != nil {
		return egress.Injection{}, err
	}
	body, err := json.Marshal(tunInbound{
		Listen:   "127.0.0.1",
		Port:     0,
		Protocol: "tun",
		Tag:      device,
		Settings: tunSettings{Name: device, MTU: MTU, Gateway: []string{gateway.String()}},
	})
	if err != nil {
		return egress.Injection{}, fmt.Errorf("egress: build the xray-tun front for egress %d: %w", e.ID, err)
	}
	return egress.Injection{Tag: device, Inbound: body}, nil
}

func device(id int) (string, error) {
	if !egress.ValidID(id) {
		return "", fmt.Errorf("%w: %d", egress.ErrIDOutOfRange, id)
	}
	return egress.Device(id), nil
}

// Compile-time proof that this driver answers both halves of the contract.
var (
	_ egress.Driver   = Driver{}
	_ egress.Injector = Driver{}
)
