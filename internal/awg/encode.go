package awg

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/mdlayher/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

/*
Config is one device as the panel wants it, obfuscation included.

Separate from wgtypes.Config rather than wrapping it: that type is the shape of
a WireGuard device, and half of what makes an AmneziaWG device is not in it.
Keeping them apart means a field can never be silently dropped on the way to the
kernel because the wrapper forgot to copy it.
*/
type Config struct {
	PrivateKey   *wgtypes.Key
	ListenPort   *int
	FirewallMark *int
	// ReplacePeers makes the message authoritative: peers absent from it are
	// removed. Without it a SET only adds, and a revoked client keeps its tunnel.
	ReplacePeers bool
	Peers        []Peer
	Params       Params
}

// Peer is one client on the device.
type Peer struct {
	PublicKey    wgtypes.Key
	PresharedKey *wgtypes.Key
	Endpoint     string
	// AllowedIPs are the prefixes routed to this peer, in CIDR form.
	AllowedIPs                  []string
	PersistentKeepaliveInterval *int
	Remove                      bool
	ReplaceAllowedIPs           bool
	// AdvancedSecurity turns AmneziaWG's obfuscation on for this peer. Absent
	// means "leave it alone"; the flag below is what makes the value meaningful.
	AdvancedSecurity *bool
}

/*
encodeConfig turns a Config into the attributes a SET_DEVICE message carries.

Attribute ORDER does not matter to the kernel, but presence does: an attribute
left out means "leave it alone", which is why every optional field here is a
pointer or a zero-means-unset value rather than a plain one. Sending a zero for
an unset listen port would move the device to a random one on every reconcile.
*/
func encodeConfig(name string, cfg Config) ([]netlink.Attribute, error) {
	if err := cfg.Params.Validate(); err != nil {
		return nil, err
	}

	attrs := []netlink.Attribute{{Type: devIfName, Data: nlNulString(name)}}
	if cfg.ReplacePeers {
		attrs = append(attrs, netlink.Attribute{Type: devFlags, Data: nlU32(deviceReplacePeers)})
	}
	if cfg.PrivateKey != nil {
		attrs = append(attrs, netlink.Attribute{Type: devPrivateKey, Data: cfg.PrivateKey[:]})
	}
	if cfg.ListenPort != nil {
		attrs = append(attrs, netlink.Attribute{Type: devListenPort, Data: nlU16(uint16(*cfg.ListenPort))})
	}
	if cfg.FirewallMark != nil {
		attrs = append(attrs, netlink.Attribute{Type: devFwmark, Data: nlU32(uint32(*cfg.FirewallMark))})
	}
	attrs = append(attrs, encodeParams(cfg.Params)...)

	if len(cfg.Peers) > 0 {
		nested, err := encodePeers(cfg.Peers)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, netlink.Attribute{Type: devPeers | netlink.Nested, Data: nested})
	}
	return attrs, nil
}

/*
encodeParams emits only the parameters that were set.

Zero means "leave it alone" here, matching an absent attribute, so a device
carrying no obfuscation emits nothing and is plain WireGuard. The alternative --
emitting every field -- would make every reconcile rewrite parameters the
operator never touched, and on a live device that is a rekey for every client.
*/
func encodeParams(p Params) []netlink.Attribute {
	var attrs []netlink.Attribute
	u16 := func(kind uint16, value uint16) {
		if value != 0 {
			attrs = append(attrs, netlink.Attribute{Type: kind, Data: nlU16(value)})
		}
	}
	u64 := func(kind uint16, value uint64) {
		if value != 0 {
			attrs = append(attrs, netlink.Attribute{Type: kind, Data: nlU64(value)})
		}
	}
	str := func(kind uint16, value string) {
		if value != "" {
			attrs = append(attrs, netlink.Attribute{Type: kind, Data: nlNulString(value)})
		}
	}
	boolean := func(kind uint16, value bool) {
		if value {
			attrs = append(attrs, netlink.Attribute{Type: kind, Data: []byte{1}})
		}
	}

	u16(devJc, p.Jc)
	u16(devJmin, p.Jmin)
	u16(devJmax, p.Jmax)
	u16(devS1, p.S1)
	u16(devS2, p.S2)
	u16(devS3, p.S3)
	u16(devS4, p.S4)
	u64(devH1, p.H1)
	u64(devH2, p.H2)
	u64(devH3, p.H3)
	u64(devH4, p.H4)
	str(devI1, p.I1)
	str(devI2, p.I2)
	str(devI3, p.I3)
	str(devI4, p.I4)
	str(devI5, p.I5)
	str(devHeaderProtectionKey, p.HeaderProtectionKey)
	u16(devContentPaddingAddition, p.ContentPaddingAddition)
	u16(devRekeyAfterTime, p.RekeyAfterTime)
	u16(devRekeyTimeout, p.RekeyTimeout)
	u16(devRejectAfterTime, p.RejectAfterTime)
	u16(devKeepaliveTimeout, p.KeepaliveTimeout)
	u16(devMaxHandshakeAttempts, p.MaxHandshakeAttempts)
	boolean(devRandomTrailers, p.RandomTrailers)
	boolean(devDisableCookies, p.DisableCookies)
	return attrs
}

func encodePeers(peers []Peer) ([]byte, error) {
	encoder := netlink.NewAttributeEncoder()
	for i, peer := range peers {
		one, err := encodePeer(peer)
		if err != nil {
			return nil, fmt.Errorf("peer %d: %w", i, err)
		}
		// The index is the nested array's own key, which is how the kernel tells
		// one peer from the next inside a single WGDEVICE_A_PEERS attribute.
		encoder.Bytes(uint16(i)|netlink.Nested, one)
	}
	return encoder.Encode()
}

func encodePeer(peer Peer) ([]byte, error) {
	encoder := netlink.NewAttributeEncoder()
	encoder.Bytes(peerPublicKey, peer.PublicKey[:])

	var flags uint32
	if peer.Remove {
		flags |= peerRemoveMe
	}
	if peer.ReplaceAllowedIPs {
		flags |= peerReplaceAllowed
	}
	if peer.AdvancedSecurity != nil {
		// The flag is what makes the value readable at all: without it the kernel
		// ignores the attribute, so "advanced security off" and "unset" would be
		// the same message and a peer could never be switched back.
		flags |= peerHasAdvancedSec
		var on uint8
		if *peer.AdvancedSecurity {
			on = 1
		}
		encoder.Uint8(peerAdvancedSecurity, on)
	}
	if flags != 0 {
		encoder.Uint32(peerFlags, flags)
	}
	if peer.PresharedKey != nil {
		encoder.Bytes(peerPresharedKey, peer.PresharedKey[:])
	}
	if peer.PersistentKeepaliveInterval != nil {
		encoder.Uint16(peerPersistentKeepaliveInterval, uint16(*peer.PersistentKeepaliveInterval))
	}
	if len(peer.AllowedIPs) > 0 {
		allowed, err := encodeAllowedIPs(peer.AllowedIPs)
		if err != nil {
			return nil, err
		}
		encoder.Bytes(peerAllowedIPs|netlink.Nested, allowed)
	}
	return encoder.Encode()
}

func nlU16(v uint16) []byte {
	b := make([]byte, 2)
	binary.NativeEndian.PutUint16(b, v)
	return b
}

func nlU32(v uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)
	return b
}

func nlU64(v uint64) []byte {
	b := make([]byte, 8)
	binary.NativeEndian.PutUint64(b, v)
	return b
}

// nlNulString is NLA_NUL_STRING: the kernel's policy expects the terminator, and
// a string sent without one is rejected rather than truncated.
func nlNulString(s string) []byte { return append([]byte(s), 0) }

/*
encodeAllowedIPs is what decides which packets reach a peer at all.

Omitted, the peer exists and routes nothing: it completes a handshake and then
silently drops everything, which reads to an operator as a working tunnel that
carries no traffic.
*/
func encodeAllowedIPs(prefixes []string) ([]byte, error) {
	encoder := netlink.NewAttributeEncoder()
	for i, raw := range prefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("allowed ip %q: %w", raw, err)
		}
		one := netlink.NewAttributeEncoder()
		family := afInet
		if prefix.Addr().Is6() {
			family = afInet6
		}
		one.Uint16(allowedIPFamily, family)
		// Masked, because the kernel stores the prefix and a host bit left set
		// here makes the stored route differ from the one the operator wrote.
		one.Bytes(allowedIPAddr, prefix.Masked().Addr().AsSlice())
		one.Uint8(allowedIPCidrMask, uint8(prefix.Bits()))
		encoded, err := one.Encode()
		if err != nil {
			return nil, err
		}
		encoder.Bytes(uint16(i)|netlink.Nested, encoded)
	}
	return encoder.Encode()
}

// The address families, as numbers rather than through golang.org/x/sys/unix.
// That package is Linux-only, and importing it here would make this encoder --
// pure logic, and the part most worth testing -- impossible to test anywhere but
// Linux. These two values are stable kernel ABI.
const (
	afInet  uint16 = 2
	afInet6 uint16 = 10
)
