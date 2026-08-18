package awg

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

/*
Device is what the module reports back: the WireGuard half in the shape wgctrl
uses, so the existing reconcile logic can diff it unchanged, plus the
obfuscation the module adds.

Params come back too because a device configured by somebody else -- an operator
running awg by hand, a panel that crashed mid-apply -- is exactly the drift a
reconcile exists to correct, and a reconcile that cannot see a field can never
correct it.
*/
type Device struct {
	wgtypes.Device
	Params Params
}

// decodeDevice parses one GET_DEVICE reply. Peers arrive nested, and a device
// with many of them arrives across several replies the caller coalesces.
func decodeDevice(data []byte) (*Device, error) {
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return nil, fmt.Errorf("awg: decoding the device: %w", err)
	}
	device := &Device{}
	for decoder.Next() {
		switch decoder.Type() {
		case devIfName:
			device.Name = decoder.String()
		case devPrivateKey:
			device.PrivateKey = keyOf(decoder.Bytes())
		case devPublicKey:
			device.PublicKey = keyOf(decoder.Bytes())
		case devListenPort:
			device.ListenPort = int(decoder.Uint16())
		case devFwmark:
			device.FirewallMark = int(decoder.Uint32())
		case devPeers:
			peers, err := decodePeers(decoder.Bytes())
			if err != nil {
				return nil, err
			}
			device.Peers = append(device.Peers, peers...)
		default:
			decodeParamAttr(decoder, &device.Params)
		}
	}
	if err := decoder.Err(); err != nil {
		return nil, fmt.Errorf("awg: decoding the device: %w", err)
	}
	device.Type = wgtypes.LinuxKernel
	return device, nil
}

// decodeParamAttr reads the obfuscation attributes, which are the ones wgctrl
// would silently skip.
func decodeParamAttr(decoder *netlink.AttributeDecoder, p *Params) {
	switch decoder.Type() {
	case devJc:
		p.Jc = decoder.Uint16()
	case devJmin:
		p.Jmin = decoder.Uint16()
	case devJmax:
		p.Jmax = decoder.Uint16()
	case devS1:
		p.S1 = decoder.Uint16()
	case devS2:
		p.S2 = decoder.Uint16()
	case devS3:
		p.S3 = decoder.Uint16()
	case devS4:
		p.S4 = decoder.Uint16()
	case devH1:
		p.H1 = decoder.Uint64()
	case devH2:
		p.H2 = decoder.Uint64()
	case devH3:
		p.H3 = decoder.Uint64()
	case devH4:
		p.H4 = decoder.Uint64()
	case devI1:
		p.I1 = decoder.String()
	case devI2:
		p.I2 = decoder.String()
	case devI3:
		p.I3 = decoder.String()
	case devI4:
		p.I4 = decoder.String()
	case devI5:
		p.I5 = decoder.String()
	case devHeaderProtectionKey:
		p.HeaderProtectionKey = base64.StdEncoding.EncodeToString(decoder.Bytes())
	case devContentPaddingAddition:
		p.ContentPaddingAddition = decoder.Uint32()
	case devRekeyAfterTime:
		p.RekeyAfterTime = decoder.Uint32()
	case devRekeyTimeout:
		p.RekeyTimeout = decoder.Uint32()
	case devRejectAfterTime:
		p.RejectAfterTime = decoder.Uint32()
	case devKeepaliveTimeout:
		p.KeepaliveTimeout = decoder.Uint32()
	case devMaxHandshakeAttempts:
		p.MaxHandshakeAttempts = decoder.Uint32()
	case devRandomTrailers:
		p.RandomTrailers = decoder.Uint8() != 0
	case devDisableCookies:
		p.DisableCookies = decoder.Uint8() != 0
	}
}

func decodePeers(data []byte) ([]wgtypes.Peer, error) {
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return nil, fmt.Errorf("awg: decoding peers: %w", err)
	}
	var peers []wgtypes.Peer
	for decoder.Next() {
		peer, err := decodePeer(decoder.Bytes())
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, decoder.Err()
}

func decodePeer(data []byte) (wgtypes.Peer, error) {
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return wgtypes.Peer{}, fmt.Errorf("awg: decoding a peer: %w", err)
	}
	peer := wgtypes.Peer{}
	for decoder.Next() {
		switch decoder.Type() {
		case peerPublicKey:
			peer.PublicKey = keyOf(decoder.Bytes())
		case peerPresharedKey:
			peer.PresharedKey = keyOf(decoder.Bytes())
		case peerEndpoint:
			peer.Endpoint = decodeSockaddr(decoder.Bytes())
		case peerPersistentKeepaliveInterval:
			peer.PersistentKeepaliveInterval = time.Duration(decoder.Uint32()) * time.Second
		case peerLastHandshakeTime:
			peer.LastHandshakeTime = decodeTimespec(decoder.Bytes())
		case peerRxBytes:
			peer.ReceiveBytes = int64(decoder.Uint64())
		case peerTxBytes:
			peer.TransmitBytes = int64(decoder.Uint64())
		case peerAllowedIPs:
			nets, err := decodeAllowedIPs(decoder.Bytes())
			if err != nil {
				return wgtypes.Peer{}, err
			}
			peer.AllowedIPs = nets
		case peerProtocolVersion:
			peer.ProtocolVersion = int(decoder.Uint32())
		}
	}
	return peer, decoder.Err()
}

func decodeAllowedIPs(data []byte) ([]net.IPNet, error) {
	decoder, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return nil, fmt.Errorf("awg: decoding allowed ips: %w", err)
	}
	var out []net.IPNet
	for decoder.Next() {
		one, err := netlink.NewAttributeDecoder(decoder.Bytes())
		if err != nil {
			return nil, fmt.Errorf("awg: decoding an allowed ip: %w", err)
		}
		var entry net.IPNet
		var family uint16
		var mask int
		for one.Next() {
			switch one.Type() {
			case allowedIPFamily:
				family = one.Uint16()
			case allowedIPAddr:
				entry.IP = net.IP(one.Bytes())
			case allowedIPCidrMask:
				mask = int(one.Uint8())
			}
		}
		bits := 32
		if family == afInet6 {
			bits = 128
		}
		entry.Mask = net.CIDRMask(mask, bits)
		out = append(out, entry)
	}
	return out, decoder.Err()
}

// decodeSockaddr reads a sockaddr_in or sockaddr_in6. A short or unknown one is
// no endpoint rather than an error: a peer that has never been contacted has
// none, which is normal rather than a fault.
func decodeSockaddr(data []byte) *net.UDPAddr {
	if len(data) < 8 {
		return nil
	}
	switch binary.NativeEndian.Uint16(data[0:2]) {
	case afInet:
		return &net.UDPAddr{
			IP:   net.IP(data[4:8]).To4(),
			Port: int(binary.BigEndian.Uint16(data[2:4])),
		}
	case afInet6:
		if len(data) < 24 {
			return nil
		}
		return &net.UDPAddr{
			IP:   net.IP(data[8:24]),
			Port: int(binary.BigEndian.Uint16(data[2:4])),
		}
	}
	return nil
}

// decodeTimespec reads the kernel's struct timespec. A zero one means the peer
// has never completed a handshake, which must stay the zero Time rather than
// becoming the epoch -- the difference is "never connected" versus "connected in
// 1970", and the panel shows one of those to an operator.
func decodeTimespec(data []byte) time.Time {
	if len(data) < 16 {
		return time.Time{}
	}
	sec := int64(binary.NativeEndian.Uint64(data[0:8]))
	nsec := int64(binary.NativeEndian.Uint64(data[8:16]))
	if sec == 0 && nsec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, nsec)
}

func keyOf(data []byte) wgtypes.Key {
	var key wgtypes.Key
	copy(key[:], data)
	return key
}
