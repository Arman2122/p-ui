//go:build linux

package awg

import (
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/wireguard"
)

/*
Driver lets internal/wireguard's kernel plane drive AmneziaWG devices.

Addresses, routes, link state and the whole reconcile above them are identical
for both modules -- what differs is the link type and which netlink family
answers. So this implements only that difference, and the existing manager
drives an AmneziaWG device with no idea it is one.

Params are deliberately NOT part of this: wgtypes.Config has no room for them
and widening it would put AmneziaWG's vocabulary into WireGuard's package. The
core applies them through ConfigureParams instead, which the module merges into
the same device.
*/
type Driver struct{}

// NewPlane returns a Plane that creates amneziawg links and configures them
// through the module's own family.
func NewPlane() wireguard.Plane { return wireguard.NewKernelPlane(LinkType, Driver{}) }

// Probe answers the question an operator actually has -- is this module usable
// here -- by resolving its family, which fails precisely when it is not loaded.
func (Driver) Probe() error {
	client, err := New()
	if err != nil {
		return err
	}
	return client.Close()
}

func (Driver) Device(name string) (*wgtypes.Device, error) {
	client, err := New()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	device, err := client.Device(name)
	if err != nil {
		return nil, err
	}
	return &device.Device, nil
}

func (Driver) ConfigureDevice(name string, cfg wgtypes.Config) error {
	client, err := New()
	if err != nil {
		return err
	}
	defer client.Close()
	return client.ConfigureDevice(name, fromWgtypes(cfg))
}

/*
ConfigureParams applies the obfuscation alone, leaving peers untouched.

Separate from the device's peers on purpose: a parameter change must not be an
excuse to rewrite the peer list, and the module merges a SET carrying only
device attributes into the device it already has.
*/
func ConfigureParams(name string, params Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	client, err := New()
	if err != nil {
		return err
	}
	defer client.Close()
	return client.ConfigureDevice(name, Config{Params: params})
}

// fromWgtypes translates the WireGuard half. Peers are carried across whole,
// since a dropped one is a client that silently stops connecting.
func fromWgtypes(cfg wgtypes.Config) Config {
	out := Config{
		PrivateKey:   cfg.PrivateKey,
		ListenPort:   cfg.ListenPort,
		FirewallMark: cfg.FirewallMark,
		ReplacePeers: cfg.ReplacePeers,
	}
	for _, peer := range cfg.Peers {
		converted := Peer{
			PublicKey:         peer.PublicKey,
			PresharedKey:      peer.PresharedKey,
			Remove:            peer.Remove,
			ReplaceAllowedIPs: peer.ReplaceAllowedIPs,
		}
		if peer.Endpoint != nil {
			converted.Endpoint = peer.Endpoint.String()
		}
		if peer.PersistentKeepaliveInterval != nil {
			seconds := int(peer.PersistentKeepaliveInterval.Seconds())
			converted.PersistentKeepaliveInterval = &seconds
		}
		for _, allowed := range peer.AllowedIPs {
			converted.AllowedIPs = append(converted.AllowedIPs, allowed.String())
		}
		out.Peers = append(out.Peers, converted)
	}
	return out
}

// ensure the driver keeps satisfying the seam it was written for.
var _ wireguard.DeviceDriver = Driver{}
